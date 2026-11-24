//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/commentfmt"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

// moderationRig is a server with a comment service, the event and cache
// observers, and a pluginhost API over the real moderator adapter.
type moderationRig struct {
	t       *testing.T
	s       *Server
	api     *pluginhost.API
	res     commentfmt.Result
	mu      sync.Mutex
	updated []sdk.CommentEvent
	drops   []uuid.UUID
}

func newModerationRig(t *testing.T, manifests ...sdk.Manifest) *moderationRig {
	t.Helper()
	ctx := context.Background()
	env := newWireEnv(t, nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := &moderationRig{t: t}
	if _, err := env.db.Pool().Exec(ctx, `UPDATE hatamoto_site_config SET language='en', prefix_all=false`); err != nil {
		t.Fatal(err)
	}
	r.s = New(env.cfg, env.db, logger)
	r.s.hooks = hooks.New(logger)
	r.s.comments = comment.New(env.db, env.cfg.Secret, logger)
	set, err := r.s.comments.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	set.Enabled = true
	if err := r.s.comments.SaveSettings(ctx, set, nil); err != nil {
		t.Fatal(err)
	}
	r.s.hooks.AddAction(sdk.CommentUpdated.Name, 0, func(_ context.Context, p any) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.updated = append(r.updated, p.(sdk.CommentEvent))
	})
	drop := func(_ context.Context, id uuid.UUID) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.drops = append(r.drops, id)
	}
	r.s.comments.Register(commentCacheObserver{drop: drop, all: func(context.Context) {}, coalesce: newDropCoalescer(drop, commentDropWindow)})
	r.s.comments.Register(newCommentEventObserver(r.s.hooks))

	grants := pluginhost.NewGrantStore(nil)
	byName := map[string]sdk.Manifest{}
	for _, m := range manifests {
		byName[m.Name] = m
		if err := grants.Set(ctx, m.Name, m.Permissions); err != nil {
			t.Fatal(err)
		}
	}
	r.api = pluginhost.NewAPI(pluginhost.Services{
		Comments: pluginCommentReader{s: r.s}, CommentModerator: pluginCommentModerator{r.s}, Audit: r.s.pluginModerationAudit,
	}, grants, func(n string) (sdk.Manifest, bool) { m, ok := byName[n]; return m, ok }, logger)
	if r.res, err = commentfmt.Render("hello"); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *moderationRig) exec(q string, args ...any) {
	r.t.Helper()
	if _, err := r.s.db.Pool().Exec(context.Background(), q, args...); err != nil {
		r.t.Fatalf("%s: %v", q, err)
	}
}

// entityOn creates a published entity on a content type owned by origin (nil: the owner's own type).
func (r *moderationRig) entityOn(slug string, origin *string) uuid.UUID {
	r.t.Helper()
	sid, id := uuid.New(), uuid.New()
	r.exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings,origin_plugin) VALUES ($1,$2,$2,'{"type":"object"}','{"url_pattern":"/{slug}"}',$3)`, sid, slug, origin)
	r.exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,published_at,attributes)
		VALUES ($1,$2,$3,$3,'en','published',now(),'{}'::jsonb)`, id, sid, slug+"-e")
	return id
}

func (r *moderationRig) approved(entity uuid.UUID) uuid.UUID {
	r.t.Helper()
	ctx := context.Background()
	c, err := r.s.comments.Store(ctx, comment.Draft{
		EntityID: entity, Email: "a@example.test", AuthorName: "Ann", BodyRaw: "hello", BodyHTML: r.res.HTML,
		BodyHash: r.res.BodyHash, SpamVerdict: "allow", Status: comment.StatusDraft, Locale: "en",
	})
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.s.comments.Confirm(ctx, c.ID); err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.s.comments.Approve(ctx, c.ID, nil); err != nil {
		r.t.Fatal(err)
	}
	return c.ID
}

func (r *moderationRig) status(id uuid.UUID) string {
	r.t.Helper()
	var st string
	if err := r.s.db.Pool().QueryRow(context.Background(), `SELECT status FROM hatamoto_comments WHERE id=$1`, id).Scan(&st); err != nil {
		r.t.Fatal(err)
	}
	return st
}

func (r *moderationRig) audits() int {
	r.t.Helper()
	var n int
	if err := r.s.db.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM hatamoto_admin_audit WHERE action='plugin.comment.moderate'`).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *moderationRig) call(plugin, fn string, args any) sdk.CallResponse {
	r.t.Helper()
	raw, _ := jsonx.MarshalPinned(args)
	in, _ := jsonx.MarshalPinned(sdk.CallRequest{Fn: fn, Args: raw})
	var resp sdk.CallResponse
	if err := jsonx.UnmarshalPinned(r.api.Call(context.Background(), plugin, in), &resp); err != nil {
		r.t.Fatal(err)
	}
	return resp
}

func moderatorManifest(name, schema string) sdk.Manifest {
	return sdk.Manifest{
		Name: name, EntitySchema: schema,
		Permissions: []string{sdk.PermEntitiesWriteOwn, sdk.PermCommentsModerate, sdk.PermCommentsRead},
	}
}

func TestPluginCommentModerate(t *testing.T) {
	r := newModerationRig(t, moderatorManifest("example", "story"))
	owner := "example"
	own := r.entityOn("story", &owner)
	foreign := r.entityOn("notes", nil)
	a, b := r.approved(own), r.approved(own)
	other := r.approved(foreign)

	resp := r.call("example", sdk.FnCommentsMarkDead, sdk.CommentIDsArgs{IDs: []string{a.String()}})
	if !resp.OK || string(resp.Result) != `{"changed":1}` {
		t.Fatalf("mark_dead own = %+v", resp)
	}
	r.s.hooks.Wait()
	if got := r.status(a); got != "dead" {
		t.Fatalf("status = %s, want dead", got)
	}
	if r.audits() != 1 {
		t.Fatalf("audit rows = %d, want 1", r.audits())
	}
	r.mu.Lock()
	if len(r.updated) != 1 || r.updated[0].ID != a.String() || r.updated[0].Status != "dead" {
		t.Fatalf("comment.updated = %+v", r.updated)
	}
	r.mu.Unlock()

	// Another content type, even mixed with an own id: refused, nothing changes, no audit row.
	resp = r.call("example", sdk.FnCommentsMarkDead, sdk.CommentIDsArgs{IDs: []string{b.String(), other.String()}})
	if resp.OK || resp.Error == nil || resp.Error.Code != sdk.ErrNotOwned {
		t.Fatalf("foreign mark_dead = %+v", resp)
	}
	if r.status(b) != "approved" || r.status(other) != "approved" || r.audits() != 1 {
		t.Fatalf("a refused call changed state: %s %s audits %d", r.status(b), r.status(other), r.audits())
	}

	if resp = r.call("example", sdk.FnCommentsRestore, sdk.CommentIDsArgs{IDs: []string{a.String()}}); !resp.OK || r.status(a) != "approved" {
		t.Fatalf("restore = %+v status %s", resp, r.status(a))
	}

	// One rank batch over three comments of one entity (fresh: no drop of its own yet): one page drop.
	own2 := uuid.New()
	r.exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,published_at,attributes)
		SELECT $1, schema_id, 'story-e2', 'story-e2', 'en', 'published', now(), '{}'::jsonb FROM hatamoto_entities WHERE id = $2`, own2, own)
	a, b, c := r.approved(own2), r.approved(own2), r.approved(own2)
	r.mu.Lock()
	r.drops = nil
	r.mu.Unlock()
	items := []sdk.CommentRankItem{{ID: a.String(), Rank: 3}, {ID: b.String(), Rank: 2}, {ID: c.String(), Rank: 1}}
	if resp = r.call("example", sdk.FnCommentsSetRank, sdk.CommentRankArgs{Items: items}); !resp.OK || string(resp.Result) != `{"changed":3}` {
		t.Fatalf("set_rank = %+v", resp)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.drops) != 1 || r.drops[0] != own2 {
		t.Fatalf("cache drops = %v, want one for the entity", r.drops)
	}
}

// A plugin loaded from a development directory names any entity_schema it likes
// (the install-time shipped-schema check never runs); ownership still comes from
// the schema's origin_plugin, so an owner-created type is out of reach.
func TestPluginCommentModerateDevDirSchema(t *testing.T) {
	r := newModerationRig(t, moderatorManifest("devdir", "article"))
	article := r.entityOn("article", nil) // created by the site owner: origin_plugin NULL
	a := r.approved(article)
	r.exec(`UPDATE hatamoto_comments SET status = 'dead' WHERE id = $1`, a)
	ids := sdk.CommentIDsArgs{IDs: []string{a.String()}}

	for _, fn := range []string{sdk.FnCommentsMarkDead, sdk.FnCommentsRestore} {
		if resp := r.call("devdir", fn, ids); resp.OK || resp.Error == nil || resp.Error.Code != sdk.ErrNotOwned {
			t.Fatalf("%s on an owner-created type = %+v", fn, resp)
		}
	}
	rank := sdk.CommentRankArgs{Items: []sdk.CommentRankItem{{ID: a.String(), Rank: 9}}}
	if resp := r.call("devdir", sdk.FnCommentsSetRank, rank); resp.OK || resp.Error == nil || resp.Error.Code != sdk.ErrNotOwned {
		t.Fatalf("set_rank on an owner-created type = %+v", resp)
	}
	var st string
	var rk float64
	if err := r.s.db.Pool().QueryRow(context.Background(), `SELECT status, rank FROM hatamoto_comments WHERE id = $1`, a).Scan(&st, &rk); err != nil {
		t.Fatal(err)
	}
	if st != "dead" || rk != 0 {
		t.Fatalf("a refused call changed the comment: status %s rank %v", st, rk)
	}
	if r.audits() != 0 {
		t.Fatalf("audit rows = %d, want 0", r.audits())
	}

	resp := r.call("devdir", sdk.FnCommentsThread, sdk.CommentsThreadArgs{EntityID: article.String(), IncludeDead: true})
	if !resp.OK {
		t.Fatalf("thread = %+v", resp)
	}
	var th sdk.CommentThread
	if err := jsonx.UnmarshalPinned(resp.Result, &th); err != nil {
		t.Fatal(err)
	}
	if len(th.Items) != 1 || th.Items[0].Status != "dead" || th.Items[0].AuthorName != "" || th.Items[0].BodyHTML != "" {
		t.Fatalf("dead comment on an owner-created type is not masked: %+v", th.Items)
	}
}
