//go:build integration

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/commentfmt"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/pluginkey"
	"hatamoto.dev/sdk"
)

// The plugin read API must see published entities of every public locale of a
// multi-locale site, shared (und) content included, and still hide a draft and
// an entity of a configured but hidden locale.
func TestPluginCommentReaderLocales_Integration(t *testing.T) {
	ctx := context.Background()
	env := newWireEnv(t, nil)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := env.db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`UPDATE hatamoto_site_config SET language='en', prefix_all=false,
		locales='[{"tag":"en","segment":""},{"tag":"nl","segment":"nl"},{"tag":"fr","segment":"fr","hidden":true}]'::jsonb`)
	schemaID := uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'Rdr','rdr','{"type":"object"}','{"url_pattern":"/{slug}"}')`, schemaID)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(env.cfg, env.db, logger)
	s.comments = comment.New(env.db, env.cfg.Secret, logger)
	set, err := s.comments.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	set.Enabled = true
	if err := s.comments.SaveSettings(ctx, set, nil); err != nil {
		t.Fatal(err)
	}

	res, err := commentfmt.Render("hello")
	if err != nil {
		t.Fatal(err)
	}
	seed := func(locale, status string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,published_at,attributes)
			VALUES ($1,$2,$3,$3,$4,$5,now(),'{}'::jsonb)`, id, schemaID, "e-"+locale+"-"+status, locale, status)
		c, err := s.comments.Store(ctx, comment.Draft{
			EntityID: id, Email: "a@rdr.test", AuthorName: "A", BodyRaw: "hello", BodyHTML: res.HTML,
			BodyHash: res.BodyHash, SpamVerdict: "allow", Status: comment.StatusDraft, Locale: locale,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.comments.Confirm(ctx, c.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.comments.Approve(ctx, c.ID, nil); err != nil {
			t.Fatal(err)
		}
		return id
	}
	en, nl, und, draft, hidden := seed("en", "published"), seed("nl", "published"), seed("und", "published"), seed("nl", "draft"), seed("fr", "published")

	r := pluginCommentReader{s: s}
	for name, id := range map[string]uuid.UUID{"en": en, "nl": nl, "und": und} {
		th, err := r.Thread(ctx, id, 1, pluginhost.ThreadOpts{})
		if err != nil || len(th.Items) != 1 {
			t.Errorf("Thread(%s) = %d items, %v; want 1", name, len(th.Items), err)
		}
	}
	if th, err := r.Thread(ctx, draft, 1, pluginhost.ThreadOpts{}); err != nil || len(th.Items) != 0 {
		t.Errorf("Thread(draft) = %d items, %v; want 0", len(th.Items), err)
	}
	if th, err := r.Thread(ctx, hidden, 1, pluginhost.ThreadOpts{}); err != nil || len(th.Items) != 0 {
		t.Errorf("Thread(hidden locale) = %d items, %v; want 0", len(th.Items), err)
	}
	counts, err := r.Counts(ctx, []uuid.UUID{en, nl, und, draft, hidden})
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]uuid.UUID{"en": en, "nl": nl, "und": und} {
		if counts[id] != 1 {
			t.Errorf("Counts(%s) = %d, want 1", name, counts[id])
		}
	}
	if counts[draft] != 0 {
		t.Errorf("Counts(draft) = %d, want 0", counts[draft])
	}
	if counts[hidden] != 0 {
		t.Errorf("Counts(hidden locale) = %d, want 0", counts[hidden])
	}
}

// readRig is a server with comments on and one entity of a content type owned
// by plugin-a, plus helpers to seed commenters and comments straight into the table.
type readRig struct {
	t      *testing.T
	s      *Server
	keys   *pluginkey.Deriver
	reader pluginCommentReader
	owned  uuid.UUID // published, on plugin-a's type "item"
	seq    int
}

func newReadRig(t *testing.T) *readRig {
	t.Helper()
	ctx := context.Background()
	env := newWireEnv(t, nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(env.cfg, env.db, logger)
	s.comments = comment.New(env.db, env.cfg.Secret, logger)
	set, err := s.comments.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	set.Enabled = true
	if err := s.comments.SaveSettings(ctx, set, nil); err != nil {
		t.Fatal(err)
	}
	keys := pluginkey.New("test-secret")
	r := &readRig{t: t, s: s, keys: keys, reader: pluginCommentReader{s: s, keys: keys,
		authors: newCommenterIndex(s.comments.CommentersSince, keys.Commenter)}}
	r.owned = r.entity("item", "plugin-a", "published", "")
	return r
}

func (r *readRig) exec(q string, args ...any) {
	r.t.Helper()
	if _, err := r.s.db.Pool().Exec(context.Background(), q, args...); err != nil {
		r.t.Fatalf("%s: %v", q, err)
	}
}

// entity creates a content type (origin plugin may be "") and one entity on it.
func (r *readRig) entity(slug, origin, status, mode string) uuid.UUID {
	r.t.Helper()
	sid, id := uuid.New(), uuid.New()
	var o *string
	if origin != "" {
		o = &origin
	}
	r.exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings,origin_plugin) VALUES ($1,$2,$2,'{"type":"object"}','{"url_pattern":"/{slug}"}',$3)`, sid, slug, o)
	r.exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,published_at,attributes)
		VALUES ($1,$2,$3,$3,'en',$4,now(),'{}'::jsonb)`, id, sid, slug+"-e", status)
	if mode != "" {
		r.exec(`INSERT INTO hatamoto_comment_entities (entity_id, mode) VALUES ($1, $2)`, id, mode)
	}
	return id
}

func (r *readRig) commenter(tag string) uuid.UUID {
	r.t.Helper()
	var id uuid.UUID
	if err := r.s.db.Pool().QueryRow(context.Background(),
		`INSERT INTO hatamoto_commenters (email_hash, email_sealed) VALUES ('h-'||$1::text, '\x00') RETURNING id`, tag).Scan(&id); err != nil {
		r.t.Fatal(err)
	}
	return id
}

// comment inserts a comment (parent may be nil, who may be nil) and returns its id and path.
func (r *readRig) comment(ent uuid.UUID, parent *uuid.UUID, who *uuid.UUID, status, body string) uuid.UUID {
	r.t.Helper()
	r.seq++
	var id uuid.UUID
	err := r.s.db.Pool().QueryRow(context.Background(), `
		WITH s AS (SELECT nextval('hatamoto_comments_seq') AS v),
		     p AS (SELECT path, depth FROM hatamoto_comments WHERE id = $2)
		INSERT INTO hatamoto_comments (seq, entity_id, parent_id, path, depth, status, author_name, body_raw, body_html, created_at, updated_at, commenter_id)
		SELECT s.v, $1, $2, COALESCE((SELECT path || '.' FROM p), '') || lpad(to_hex(s.v), 12, '0'),
		       COALESCE((SELECT depth FROM p), 0) + 1, $3, 'Author', 'raw', $4, now() + $5 * interval '1 second', now(), $6
		FROM s RETURNING id`, ent, parent, status, "<p>"+body+"</p>", r.seq, who).Scan(&id)
	if err != nil {
		r.t.Fatal(err)
	}
	return id
}

func itemIDs(items []sdk.CommentItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func sameIDs(t *testing.T, step string, items []sdk.CommentItem, want ...uuid.UUID) {
	t.Helper()
	got := itemIDs(items)
	if len(got) != len(want) {
		t.Fatalf("%s: got %v, want %v", step, got, want)
	}
	for i := range want {
		if got[i] != want[i].String() {
			t.Fatalf("%s: got %v, want %v", step, got, want)
		}
	}
}

func TestPluginCommentReaderByAuthor_Integration(t *testing.T) {
	ctx := context.Background()
	r := newReadRig(t)
	cid := r.commenter("a")
	hidden := r.entity("hidden-type", "", "published", "hidden")
	draft := r.entity("draft-type", "", "draft", "")
	other := r.entity("other-type", "plugin-a", "published", "")

	approved1 := r.comment(r.owned, nil, &cid, "approved", "one")
	approved2 := r.comment(other, nil, &cid, "approved", "two")
	dead := r.comment(r.owned, nil, &cid, "dead", "gone")
	deadOther := r.comment(other, nil, &cid, "dead", "gone too")
	r.comment(r.owned, nil, &cid, "pending", "wait")
	r.comment(hidden, nil, &cid, "approved", "no")
	r.comment(draft, nil, &cid, "approved", "no")
	r.comment(r.owned, nil, nil, "approved", "staff")

	keyA := r.keys.Commenter("plugin-a", cid)
	opts := pluginhost.ThreadOpts{Plugin: "plugin-a", Schema: "item"}

	th, err := r.reader.ByAuthor(ctx, keyA, 1, opts)
	if err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "approved only", th.Items, approved2, approved1)
	for _, it := range th.Items {
		if it.CommenterKey != keyA || it.Status != "approved" {
			t.Errorf("item %+v", it)
		}
	}

	opts.IncludeDead = true
	th, err = r.reader.ByAuthor(ctx, keyA, 1, opts)
	if err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "include_dead on the owner type", th.Items, dead, approved2, approved1)
	if d := th.Items[0]; d.Status != "dead" || d.BodyHTML != "<p>gone</p>" || d.CommenterKey != keyA {
		t.Errorf("revealed dead item = %+v", d)
	}

	// The rule is per content type: naming the other type reveals its dead row only.
	th, err = r.reader.ByAuthor(ctx, keyA, 1, pluginhost.ThreadOpts{Plugin: "plugin-a", Schema: "other-type", IncludeDead: true})
	if err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "other type", th.Items, deadOther, approved2, approved1)
	// A plugin that is not the owner never gets a dead row, not even a placeholder.
	keyB := r.keys.Commenter("plugin-b", cid)
	th, err = r.reader.ByAuthor(ctx, keyB, 1, pluginhost.ThreadOpts{Plugin: "plugin-b", Schema: "item", IncludeDead: true})
	if err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "non-owner", th.Items, approved2, approved1)
	for _, it := range th.Items {
		if it.Status == "dead" || it.BodyHTML == "" {
			t.Errorf("non-owner item %+v", it)
		}
	}

	// A key of another plugin resolves nobody; so does a random key, no keys, no index.
	opts.IncludeDead = false
	for name, c := range map[string]struct {
		rd  pluginCommentReader
		key string
	}{
		"other plugin's key": {r.reader, keyB},
		"random key":         {r.reader, strings.Repeat("A", 43)},
		"no keys":            {pluginCommentReader{s: r.s}, keyA},
	} {
		got, err := c.rd.ByAuthor(ctx, c.key, 1, opts)
		if err != nil || got.Items == nil || len(got.Items) != 0 {
			t.Errorf("%s: %+v, %v; want an empty page", name, got, err)
		}
	}

	all, _ := jsonx.Marshal(th)
	for _, bad := range []string{"email", "hash", "\"ip", "dead_by"} {
		if strings.Contains(string(all), bad) {
			t.Errorf("result contains %q: %s", bad, all)
		}
	}
}

// The owner rule in the by_author SQL must match revealDead, which thread and
// subtree use: a dead comment comes back if and only if revealDead says so.
func TestPluginCommentReaderByAuthorRevealParity_Integration(t *testing.T) {
	ctx := context.Background()
	r := newReadRig(t)
	cases := []struct {
		name, origin, slug string
		opts               pluginhost.ThreadOpts
	}{
		{"owner asks", "plugin-a", "t1", pluginhost.ThreadOpts{Plugin: "plugin-a", Schema: "t1", IncludeDead: true}},
		{"not asked", "plugin-a", "t2", pluginhost.ThreadOpts{Plugin: "plugin-a", Schema: "t2"}},
		{"other schema named", "plugin-a", "t3", pluginhost.ThreadOpts{Plugin: "plugin-a", Schema: "item", IncludeDead: true}},
		{"other plugin", "plugin-b", "t4", pluginhost.ThreadOpts{Plugin: "plugin-a", Schema: "t4", IncludeDead: true}},
		{"type without owner", "", "t5", pluginhost.ThreadOpts{Plugin: "plugin-a", Schema: "t5", IncludeDead: true}},
		// ByAuthor resolves no key without a plugin, so this one goes to the SQL directly.
		{"no plugin", "plugin-a", "t6", pluginhost.ThreadOpts{Schema: "t6", IncludeDead: true}},
	}
	// Create every commenter first, so the one full scan sees them all and
	// each call reaches the SQL instead of the index's miss guard.
	cids := make([]uuid.UUID, len(cases))
	deads := make([]uuid.UUID, len(cases))
	for i, c := range cases {
		cids[i] = r.commenter(fmt.Sprint("parity", i))
		deads[i] = r.comment(r.entity(c.slug, c.origin, "published", ""), nil, &cids[i], "dead", "gone")
	}
	revealed := 0
	for i, c := range cases {
		want := revealDead(comment.EntityConfig{OwnerPlugin: c.origin, SchemaSlug: c.slug}, c.opts)
		if want {
			revealed++
		}
		var th sdk.CommentThread
		if c.opts.Plugin == "" {
			var dead comment.DeadScope
			if c.opts.IncludeDead {
				dead = comment.DeadScope{Plugin: c.opts.Plugin, Schema: c.opts.Schema}
			}
			tp, err := r.s.comments.ByAuthor(r.reader.scoped(ctx), cids[i], 1, dead)
			if err != nil {
				t.Fatal(err)
			}
			th = pluginThread(tp, true, r.reader.keyFor(c.opts.Plugin))
		} else {
			key := r.keys.Commenter(c.opts.Plugin, cids[i])
			if id, ok, err := r.reader.authors.resolve(ctx, c.opts.Plugin, key); err != nil || !ok || id != cids[i] {
				t.Fatalf("%s: key does not resolve (%v %v %v), the case would not reach the SQL", c.name, id, ok, err)
			}
			var err error
			if th, err = r.reader.ByAuthor(ctx, key, 1, c.opts); err != nil {
				t.Fatal(err)
			}
		}
		got := len(th.Items) == 1 && th.Items[0].ID == deads[i].String() && th.Items[0].BodyHTML != ""
		if got != want {
			t.Errorf("%s: by_author reveals dead = %v, revealDead = %v (items %+v)", c.name, got, want, th.Items)
		}
		if !want && len(th.Items) != 0 {
			t.Errorf("%s: unrevealed dead row listed: %+v", c.name, th.Items)
		}
	}
	if revealed != 1 {
		t.Fatalf("table reveals %d cases, want 1", revealed)
	}
}

func TestPluginCommentReaderSubtree_Integration(t *testing.T) {
	ctx := context.Background()
	r := newReadRig(t)
	cid := r.commenter("a")
	root := r.comment(r.owned, nil, &cid, "approved", "root")
	a := r.comment(r.owned, &root, &cid, "approved", "a")
	b := r.comment(r.owned, &a, &cid, "approved", "b")
	c := r.comment(r.owned, &b, &cid, "approved", "c")
	opts := pluginhost.ThreadOpts{Plugin: "plugin-a", Schema: "item"}
	key := r.keys.Commenter("plugin-a", cid)

	sub, err := r.reader.Subtree(ctx, b, 1, opts)
	if err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "ancestors", sub.Ancestors, root, a)
	sameIDs(t, "items", sub.Items, b, c)
	if sub.Items[0].CommenterKey != key || sub.Page != 1 || sub.HasMore {
		t.Errorf("subtree = %+v", sub)
	}

	r.exec(`UPDATE hatamoto_comments SET status = 'dead' WHERE id = $1`, a)
	sub, err = r.reader.Subtree(ctx, b, 1, opts)
	if err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "dead ancestor kept", sub.Ancestors, root, a)
	sameIDs(t, "replies under dead kept", sub.Items, b, c)
	if d := sub.Ancestors[1]; d.Status != "dead" || d.AuthorName != "" || d.BodyHTML != "" || d.CommenterKey != "" {
		t.Errorf("unrevealed dead ancestor = %+v", d)
	}
	opts.IncludeDead = true
	sub, err = r.reader.Subtree(ctx, b, 1, opts)
	if err != nil {
		t.Fatal(err)
	}
	if d := sub.Ancestors[1]; d.Status != "dead" || d.BodyHTML != "<p>a</p>" || d.CommenterKey != key {
		t.Errorf("revealed dead ancestor = %+v", d)
	}
	opts.IncludeDead = false
	r.exec(`UPDATE hatamoto_comments SET status = 'approved' WHERE id = $1`, a)

	hiddenEnt := r.entity("hidden-type", "", "published", "hidden")
	draftEnt := r.entity("draft-type", "", "draft", "")
	pending := r.comment(r.owned, nil, &cid, "pending", "p")
	underPending := r.comment(r.owned, &pending, &cid, "approved", "u")
	for name, id := range map[string]uuid.UUID{
		"pending target":     pending,
		"under pending":      underPending,
		"hidden-mode entity": r.comment(hiddenEnt, nil, &cid, "approved", "h"),
		"unpublished entity": r.comment(draftEnt, nil, &cid, "approved", "d"),
		"unknown":            uuid.New(),
	} {
		got, err := r.reader.Subtree(ctx, id, 1, opts)
		if !errors.Is(err, pluginhost.ErrCommentNotFound) || len(got.Items) != 0 || len(got.Ancestors) != 0 {
			t.Errorf("%s: %+v, %v; want ErrCommentNotFound", name, got, err)
		}
	}
}
