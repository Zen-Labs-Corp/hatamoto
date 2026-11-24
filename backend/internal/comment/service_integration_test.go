//go:build integration

package comment

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/testutil"
	"hatamoto.dev/core/migrations"
)

type fixture struct {
	t       *testing.T
	ctx     context.Context
	svc     *Service
	db      *database.DB
	counter *testutil.QueryCounter
	entity  uuid.UUID
}

// newFixture migrates a scratch database, builds a Service on it (statements
// counted) and seeds one entity.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_comment_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	prov, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prov.Up(ctx); err != nil {
		t.Fatal(err)
	}
	sqlDB.Close()
	pool.Close()

	pcfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		t.Fatal(err)
	}
	counter := &testutil.QueryCounter{}
	pcfg.ConnConfig.Tracer = counter
	pcfg.MaxConns, pcfg.MinConns = 6, 1
	db, err := database.NewWithConfig(ctx, pcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})

	f := &fixture{t: t, ctx: ctx, db: db, counter: counter,
		svc: New(db, "integration-test-secret", slog.New(slog.NewTextHandler(io.Discard, nil)))}
	f.exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ('22222222-2222-2222-2222-222222222222', 'Note', 'cmm-note', '{"type":"object"}')`)
	f.entity = f.newEntity("hello")
	return f
}

func (f *fixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Pool().Exec(f.ctx, q, args...); err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
}

func (f *fixture) scalar(dst any, q string, args ...any) {
	f.t.Helper()
	if err := f.db.Pool().QueryRow(f.ctx, q, args...).Scan(dst); err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
}

func (f *fixture) newEntity(slug string) uuid.UUID {
	f.t.Helper()
	id := uuid.New()
	f.exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status) VALUES ($1, '22222222-2222-2222-2222-222222222222', $2, $2, 'published')`, id, slug)
	return id
}

func (f *fixture) enable() {
	f.t.Helper()
	set := Defaults()
	set.Enabled = true
	if err := f.svc.SaveSettings(f.ctx, set, nil); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) draft(email, body string) Draft {
	return Draft{EntityID: f.entity, Email: email, AuthorName: "Visitor", BodyRaw: body, BodyHTML: "<p>" + body + "</p>",
		SpamVerdict: "allow", Status: StatusDraft}
}

func (f *fixture) store(d Draft) Comment {
	f.t.Helper()
	c, err := f.svc.Store(f.ctx, d)
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}

func (f *fixture) counts(entity uuid.UUID) EntityCounts {
	f.t.Helper()
	m, err := f.svc.Counts(f.ctx, []uuid.UUID{entity})
	if err != nil {
		f.t.Fatal(err)
	}
	return m[entity]
}

func (f *fixture) wantCounts(approved, pending int, step string) {
	f.t.Helper()
	if got := f.counts(f.entity); got != (EntityCounts{approved, pending}) {
		f.t.Errorf("%s: counts = %+v, want approved %d pending %d", step, got, approved, pending)
	}
}

type seeded struct {
	id   uuid.UUID
	path string
}

// seedComment inserts an approved (or other status) comment straight into the
// table, with an explicit created_at, to build big threads quickly.
func (f *fixture) seedComment(parent *seeded, created time.Time, status Status) seeded {
	f.t.Helper()
	parentPath, depth := "", 1
	var parentID *uuid.UUID
	if parent != nil {
		parentPath, depth, parentID = parent.path, Depth(parent.path)+1, &parent.id
	}
	var s seeded
	f.scalar(&s.id, `WITH s AS (SELECT nextval('hatamoto_comments_seq') AS v)
		INSERT INTO hatamoto_comments (seq, entity_id, parent_id, path, depth, status, body_raw, body_html, created_at, updated_at)
		SELECT v, $1, $2, CASE WHEN $3::text = '' THEN '' ELSE $3::text || '.' END || lpad(to_hex(v), 12, '0'), $4, $5, 'b', '<p>b</p>', $6, $6 FROM s
		RETURNING id`, f.entity, parentID, parentPath, depth, string(status), created)
	f.scalar(&s.path, `SELECT path FROM hatamoto_comments WHERE id = $1`, s.id)
	return s
}

func TestThreadPageOneQuery(t *testing.T) {
	f := newFixture(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(i int) time.Time { return base.Add(time.Duration(i) * time.Minute) }
	tick := 1000
	next := func() time.Time { tick++; return at(tick) }

	roots := make([]seeded, 51)
	for i := range roots {
		roots[i] = f.seedComment(nil, at(i), StatusApproved) // root i+1 is the newest at i = 50
	}
	for i := 0; i < 50; i++ {
		cur := roots[i]
		for d := 2; d <= 5; d++ {
			cur = f.seedComment(&cur, next(), StatusApproved)
		}
	}
	top := roots[49]
	for i := 0; i < 17; i++ { // more than 16 siblings: the hex width of a segment matters
		f.seedComment(&top, next(), StatusApproved)
	}
	pending := f.seedComment(&top, next(), StatusPending)
	orphan := f.seedComment(&pending, next(), StatusApproved)

	if _, err := f.svc.Settings(f.ctx); err != nil { // warm the cache
		t.Fatal(err)
	}
	n := f.counter.Count()
	page, err := f.svc.Thread(f.ctx, f.entity, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.counter.Count() - n; got != 1 {
		t.Fatalf("Thread ran %d statements, want 1: %v", got, f.counter.Since(n))
	}
	if !page.HasMore || page.Page != 1 || page.Pages != 2 {
		t.Errorf("HasMore = %v Page = %d Pages = %d, want pages 2", page.HasMore, page.Page, page.Pages)
	}

	var rootOrder []uuid.UUID
	rootOf := map[uuid.UUID]uuid.UUID{}
	var current uuid.UUID
	seen := map[uuid.UUID]bool{}
	lastSibling := map[uuid.UUID]time.Time{}
	for _, c := range page.Comments {
		if c.ID == orphan.id {
			t.Error("a reply under a non-approved parent was returned")
		}
		if c.Depth > 5 {
			t.Errorf("depth %d > 5", c.Depth)
		}
		if c.ParentID == nil {
			rootOrder = append(rootOrder, c.ID)
			current = c.ID
			rootOf[c.ID] = c.ID
		} else {
			if !seen[*c.ParentID] {
				t.Fatalf("child %s before its parent", c.ID)
			}
			if rootOf[*c.ParentID] != current {
				t.Fatalf("subtree of root %s is not contiguous", rootOf[*c.ParentID])
			}
			rootOf[c.ID] = current
			if prev, ok := lastSibling[*c.ParentID]; ok && c.CreatedAt.Before(prev) {
				t.Errorf("replies under %s are not oldest first", *c.ParentID)
			}
			lastSibling[*c.ParentID] = c.CreatedAt
		}
		seen[c.ID] = true
	}
	if len(rootOrder) != 50 {
		t.Fatalf("roots on page 1 = %d, want 50", len(rootOrder))
	}
	for i, id := range rootOrder {
		if want := roots[50-i].id; id != want {
			t.Fatalf("root %d = %s, want newest first (%s)", i, id, want)
		}
	}

	page2, err := f.svc.Thread(f.ctx, f.entity, 2)
	if err != nil {
		t.Fatal(err)
	}
	if page2.HasMore || page2.Pages != 2 || len(page2.Comments) == 0 || page2.Comments[0].ID != roots[0].id {
		t.Errorf("page 2 = %d comments, HasMore %v, want the oldest root and its chain", len(page2.Comments), page2.HasMore)
	}
	for _, c := range page2.Comments {
		if c.ParentID == nil && c.ID != roots[0].id {
			t.Errorf("page 2 holds another root %s", c.ID)
		}
	}
}

func TestStatusTransitionsAndCounts(t *testing.T) {
	f := newFixture(t)
	f.enable()
	var entityUpdated string
	var changes int
	snapshot := func() {
		f.scalar(&entityUpdated, `SELECT updated_at::text FROM hatamoto_entities WHERE id = $1`, f.entity)
		f.scalar(&changes, `SELECT count(*) FROM hatamoto_content_changes`)
	}
	snapshot()

	a := f.store(f.draft("a@example.test", "first"))
	f.wantCounts(0, 0, "draft stored")
	a, err := f.svc.Confirm(f.ctx, a.ID)
	if err != nil || a.Status != StatusPending {
		t.Fatalf("new commenter confirm = %s, %v; want pending", a.Status, err)
	}
	f.wantCounts(0, 1, "confirm to pending")
	if _, err := f.svc.Confirm(f.ctx, a.ID); !errors.Is(err, ErrBadTransition) {
		t.Errorf("confirm of a pending comment = %v, want ErrBadTransition", err)
	}
	if a, err = f.svc.Approve(f.ctx, a.ID, nil); err != nil || a.Status != StatusApproved || a.ApprovedAt == nil {
		t.Fatalf("approve = %+v, %v", a, err)
	}
	f.wantCounts(1, 0, "approve")
	var after string
	var changesAfter int
	f.scalar(&after, `SELECT updated_at::text FROM hatamoto_entities WHERE id = $1`, f.entity)
	f.scalar(&changesAfter, `SELECT count(*) FROM hatamoto_content_changes`)
	if after != entityUpdated || changesAfter != changes {
		t.Errorf("approve touched the entity: updated_at %s -> %s, content_changes %d -> %d", entityUpdated, after, changes, changesAfter)
	}
	if _, err := f.svc.Approve(f.ctx, a.ID, nil); !errors.Is(err, ErrBadTransition) {
		t.Errorf("approve of an approved comment = %v, want ErrBadTransition", err)
	}

	// A vouched commenter's clean draft goes straight to approved.
	cm, err := f.svc.Commenter(f.ctx, f.svc.Keys().HashEmail("a@example.test"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Vouch(f.ctx, cm.ID, TrustKnown); err != nil {
		t.Fatal(err)
	}
	b := f.store(f.draft("a@example.test", "second"))
	if b, err = f.svc.Confirm(f.ctx, b.ID); err != nil || b.Status != StatusApproved {
		t.Fatalf("known commenter confirm = %s, %v; want approved", b.Status, err)
	}
	f.wantCounts(2, 0, "confirm to approved")

	if _, err := f.svc.Trash(f.ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	f.wantCounts(1, 0, "trash")

	// Edit that adds a link sends an approved comment back to moderation.
	c := f.store(f.draft("a@example.test", "third"))
	if c, err = f.svc.Confirm(f.ctx, c.ID); err != nil || c.Status != StatusApproved {
		t.Fatalf("confirm = %s, %v", c.Status, err)
	}
	f.wantCounts(2, 0, "third approved")
	if c, err = f.svc.Edit(f.ctx, c.ID, Edited{BodyRaw: "see example.test", BodyHTML: "<p>see example.test</p>", HasLinks: true}); err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusPending || c.EditedAt == nil {
		t.Errorf("edit adding a link = %s edited %v, want pending", c.Status, c.EditedAt)
	}
	f.wantCounts(1, 1, "edit adds link")
	if c2, err := f.svc.Edit(f.ctx, c.ID, Edited{BodyRaw: "fine", BodyHTML: "<p>fine</p>"}); err != nil || c2.Status != StatusPending {
		t.Errorf("edit of a pending comment = %s, %v", c2.Status, err)
	}

	if _, err := f.svc.MarkSpam(f.ctx, a.ID, nil, Learn{}); err != nil {
		t.Fatal(err)
	}
	f.wantCounts(0, 1, "spam")
	if err := f.svc.Delete(f.ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	f.wantCounts(0, 0, "delete")
	if err := f.svc.Delete(f.ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
	snapshot()
}

func TestCascadeOnEntityDelete(t *testing.T) {
	f := newFixture(t)
	root := f.store(Draft{EntityID: f.entity, BodyRaw: "r", BodyHTML: "<p>r</p>", Status: StatusApproved})
	f.store(Draft{EntityID: f.entity, ParentID: &root.ID, BodyRaw: "c", BodyHTML: "<p>c</p>", Status: StatusApproved})
	f.wantCounts(2, 0, "seed")
	f.exec(`DELETE FROM hatamoto_entities WHERE id = $1`, f.entity)
	var n int
	f.scalar(&n, `SELECT (SELECT count(*) FROM hatamoto_comments) + (SELECT count(*) FROM hatamoto_comment_entities)`)
	if n != 0 {
		t.Errorf("%d comment rows left after the entity was deleted", n)
	}
}

func TestReplyDepthIsCapped(t *testing.T) {
	f := newFixture(t)
	set := Defaults()
	set.MaxDepth = 2
	if err := f.svc.SaveSettings(f.ctx, set, nil); err != nil {
		t.Fatal(err)
	}
	mk := func(parent *uuid.UUID) Comment {
		return f.store(Draft{EntityID: f.entity, ParentID: parent, BodyRaw: "x", BodyHTML: "<p>x</p>", Status: StatusApproved})
	}
	r := mk(nil)
	c1 := mk(&r.ID)
	c2 := mk(&c1.ID)
	if r.Depth != 1 || c1.Depth != 2 || c2.Depth != 2 || c2.ParentID == nil || *c2.ParentID != r.ID {
		t.Errorf("depths %d %d %d, c2 parent %v; want 1 2 2 under the root", r.Depth, c1.Depth, c2.Depth, c2.ParentID)
	}
	other := f.newEntity("other")
	if _, err := f.svc.Store(f.ctx, Draft{EntityID: other, ParentID: &r.ID, BodyRaw: "x", BodyHTML: "x", Status: StatusApproved}); !errors.Is(err, ErrNotFound) {
		t.Errorf("reply across entities = %v, want ErrNotFound", err)
	}
}

func TestTrustThroughService(t *testing.T) {
	f := newFixture(t)
	f.enable()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	f.svc.now = func() time.Time { return now }
	h := f.svc.Keys().HashEmail("t@example.test")

	var ids []uuid.UUID
	for _, body := range []string{"one", "two", "three"} {
		c := f.store(f.draft("t@example.test", body))
		if c, err := f.svc.Confirm(f.ctx, c.ID); err != nil || c.Status != StatusPending {
			t.Fatalf("confirm = %s, %v", c.Status, err)
		}
		ids = append(ids, c.ID)
	}
	if _, err := f.svc.Approve(f.ctx, ids[0], nil); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE hatamoto_commenters SET first_approved_at = $1 WHERE email_hash = $2`, now.Add(-7*24*time.Hour), h)
	if _, err := f.svc.Approve(f.ctx, ids[1], nil); err != nil {
		t.Fatal(err)
	}
	if cm, _ := f.svc.Commenter(f.ctx, h); cm.Trust != TrustNew || cm.ApprovedCount != 2 {
		t.Fatalf("after 2 approvals: %s / %d", cm.Trust, cm.ApprovedCount)
	}
	if _, err := f.svc.Approve(f.ctx, ids[2], nil); err != nil {
		t.Fatal(err)
	}
	cm, err := f.svc.Commenter(f.ctx, h)
	if err != nil || cm.Trust != TrustKnown || cm.ApprovedCount != 3 {
		t.Fatalf("after 3 approvals at 7 days: %s / %d, %v; want known / 3", cm.Trust, cm.ApprovedCount, err)
	}

	if _, err := f.svc.MarkSpam(f.ctx, ids[2], nil, Learn{}); err != nil {
		t.Fatal(err)
	}
	spammed, _ := f.svc.Commenter(f.ctx, h)
	if spammed.Trust != TrustBlocked || spammed.TokenVersion != cm.TokenVersion+1 || spammed.LastSpamAt == nil || !spammed.LastSpamAt.Equal(now) {
		t.Errorf("after spam: %+v", spammed)
	}
	if spammed.ApprovedCount != 2 {
		t.Errorf("approved count after spam = %d, want 2", spammed.ApprovedCount)
	}
	// A blocked commenter's next draft is spam.
	d := f.store(f.draft("t@example.test", "more"))
	if d, err = f.svc.Confirm(f.ctx, d.ID); err != nil || d.Status != StatusSpam {
		t.Errorf("blocked commenter confirm = %s, %v; want spam", d.Status, err)
	}
	if err := f.svc.Vouch(f.ctx, cm.ID, TrustKnown); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.svc.Commenter(f.ctx, h); v.Trust != TrustKnown {
		t.Errorf("after vouch: %s", v.Trust)
	}
	if v, err := f.svc.RevokeDevices(f.ctx, cm.ID); err != nil || v != spammed.TokenVersion+1 {
		t.Errorf("RevokeDevices = %d, %v", v, err)
	}
	if err := f.svc.Vouch(f.ctx, cm.ID, TrustBlocked); err == nil {
		t.Error("Vouch(blocked) should fail")
	}
}

func TestBlocklistLearning(t *testing.T) {
	f := newFixture(t)
	keys := f.svc.Keys()
	d := f.draft("bot@throwaway.example.test", "buy now")
	d.LinkDomains, d.BodyHash, d.IP = []string{"spam.example.com"}, "hash-of-body", "192.0.2.10"
	c, err := f.svc.Confirm(f.ctx, f.store(d).ID) // only a confirmed email teaches the domain
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.MarkSpam(f.ctx, c.ID, nil, Learn{EmailDomain: true, LinkDomains: true, Hashes: true}); err != nil {
		t.Fatal(err)
	}
	ip, net := keys.HashIP("192.0.2.77") // same /24, other address
	hits, err := f.svc.MatchBlocks(f.ctx, Probe{
		EmailDomain: "mail.throwaway.example.test", LinkDomains: []string{"x.spam.example.com"},
		IPHash: ip, NetHash: net, BodyHash: "hash-of-body",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[BlockKind]string{}
	for _, h := range hits {
		got[h.Kind] = h.Source
	}
	for _, k := range []BlockKind{BlockEmailDomain, BlockLinkDomain, BlockNetHash, BlockBodyHash} {
		if got[k] != "spam" {
			t.Errorf("kind %s not learned: %v", k, got)
		}
	}
	if _, ok := got[BlockIPHash]; ok {
		t.Error("another address of the same network matched the ip hash")
	}
	if own, _ := keys.HashIP("192.0.2.10"); own != "" {
		if hits, _ := f.svc.MatchBlocks(f.ctx, Probe{IPHash: own}); len(hits) != 1 {
			t.Errorf("the spamming address itself: %d hits, want 1", len(hits))
		}
	}

	seed, err := f.svc.MatchBlocks(f.ctx, Probe{EmailDomain: "mailinator.com"})
	if err != nil || len(seed) != 1 || seed[0].Source != "seed" {
		t.Errorf("seeded disposable domain: %+v, %v", seed, err)
	}
	if hits, _ := f.svc.MatchBlocks(f.ctx, Probe{EmailDomain: "example.test", LinkDomains: []string{"example.com"}}); len(hits) != 0 {
		t.Errorf("unrelated probe matched: %+v", hits)
	}

	if err := f.svc.AddBlock(f.ctx, BlockLinkDomain, " Bad.Example.Org. ", "by hand", nil); err != nil {
		t.Fatal(err)
	}
	list, _ := f.svc.Blocks(f.ctx, BlockLinkDomain)
	var manual Block
	for _, b := range list {
		if b.Value == "bad.example.org" {
			manual = b
		}
	}
	if manual.Source != "manual" {
		t.Fatalf("manual block missing: %+v", list)
	}
	if err := f.svc.RemoveBlock(f.ctx, manual.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RemoveBlock(f.ctx, manual.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second remove = %v", err)
	}
	if err := f.svc.AddBlock(f.ctx, "nope", "x", "", nil); err == nil {
		t.Error("unknown kind accepted")
	}
}

func TestFormTokenSingleUse(t *testing.T) {
	f := newFixture(t)
	exp := time.Now().Add(time.Hour)
	for i, want := range []bool{true, false} {
		if got, err := f.svc.ConsumeFormToken(f.ctx, "tok-1", exp); err != nil || got != want {
			t.Errorf("use %d: fresh = %v, %v; want %v", i+1, got, err, want)
		}
	}
	if got, _ := f.svc.ConsumeFormToken(f.ctx, "tok-2", exp); !got {
		t.Error("another token should be fresh")
	}
}

func TestNoPlainEmailOrIPStored(t *testing.T) {
	f := newFixture(t)
	d := f.draft("visitor@example.test", "hello")
	d.IP = "192.0.2.10"
	d.UserAgent = strings.Repeat("u", 400)
	c := f.store(d)
	if len([]rune(c.UserAgent)) != 256 {
		t.Errorf("user agent length = %d, want 256", len([]rune(c.UserAgent)))
	}
	if c.IPHash == "" || c.NetHash == "" || c.IPHash == c.NetHash {
		t.Errorf("hashes: %q %q", c.IPHash, c.NetHash)
	}
	for _, table := range []string{"hatamoto_comments", "hatamoto_commenters"} {
		rows, err := f.db.Pool().Query(f.ctx, `SELECT t::text FROM `+table+` t`)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for rows.Next() {
			var row string
			if err := rows.Scan(&row); err != nil {
				t.Fatal(err)
			}
			n++
			for _, secret := range []string{"visitor@example.test", "192.0.2.10"} {
				if strings.Contains(row, secret) {
					t.Errorf("%s stores %q in plain text", table, secret)
				}
			}
		}
		rows.Close()
		if n == 0 {
			t.Errorf("%s has no rows to check", table)
		}
	}
	// The sealed address still opens for the export.
	ex, err := f.svc.ExportByEmail(f.ctx, "Visitor@Example.test")
	if err != nil || ex.Email != "visitor@example.test" || len(ex.Comments) != 1 {
		t.Errorf("export = %+v, %v", ex, err)
	}
	if _, err := f.svc.ExportByEmail(f.ctx, "nobody@example.test"); !errors.Is(err, ErrNotFound) {
		t.Errorf("export of an unknown address = %v", err)
	}
}

func TestStoreNotifyReplies(t *testing.T) {
	f := newFixture(t)
	d := f.draft("notify@example.test", "ask me")
	d.NotifyReplies = true
	stored := f.store(d)
	got, err := f.svc.Get(f.ctx, stored.ID)
	if err != nil || !got.NotifyReplies {
		t.Errorf("opted-in comment: NotifyReplies = %v, err %v, want true", got.NotifyReplies, err)
	}
	admin := f.draft("", "from staff")
	admin.NotifyReplies = true
	got, err = f.svc.Get(f.ctx, f.store(admin).ID)
	if err != nil || got.NotifyReplies {
		t.Errorf("admin draft: NotifyReplies = %v, err %v, want false", got.NotifyReplies, err)
	}
	plain, err := f.svc.Get(f.ctx, f.store(f.draft("quiet@example.test", "no mail")).ID)
	if err != nil || plain.NotifyReplies {
		t.Errorf("default draft: NotifyReplies = %v, err %v, want false", plain.NotifyReplies, err)
	}
}

func TestEraseByEmail(t *testing.T) {
	f := newFixture(t)
	mine := func(body string, parent *uuid.UUID) Comment {
		d := f.draft("erase@example.test", body)
		d.ParentID, d.Status, d.NotifyReplies = parent, StatusApproved, true
		return f.store(d)
	}
	withReply := mine("keep the shape", nil)
	leaf := mine("no replies", nil)
	other := f.draft("other@example.test", "a reply")
	other.ParentID, other.Status = &withReply.ID, StatusApproved
	reply := f.store(other)
	f.wantCounts(3, 0, "seed")

	var deleted []uuid.UUID
	var updated []Comment
	f.svc.Register(&deleteRecorder{ids: &deleted, updated: &updated})
	res, err := f.svc.EraseByEmail(f.ctx, "erase@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if res != (EraseResult{Deleted: 1, Anonymised: 1}) {
		t.Errorf("result = %+v", res)
	}
	if len(deleted) != 1 || deleted[0] != leaf.ID {
		t.Errorf("OnDeleted ids = %v, want the leaf", deleted)
	}
	if len(updated) != 1 || updated[0].ID != withReply.ID || updated[0].Status != StatusApproved || updated[0].BodyHTML != deletedBody {
		t.Errorf("OnUpdated = %+v, want the anonymised comment, approved, body %q", updated, deletedBody)
	}
	var name, body, ip string
	var commenter *uuid.UUID
	f.scalar(&name, `SELECT author_name FROM hatamoto_comments WHERE id = $1`, withReply.ID)
	f.scalar(&body, `SELECT body_html FROM hatamoto_comments WHERE id = $1`, withReply.ID)
	f.scalar(&ip, `SELECT ip_hash || net_hash || user_agent FROM hatamoto_comments WHERE id = $1`, withReply.ID)
	f.scalar(&commenter, `SELECT commenter_id FROM hatamoto_comments WHERE id = $1`, withReply.ID)
	if name != "" || body != deletedBody || ip != "" || commenter != nil {
		t.Errorf("anonymised row: name %q body %q hashes %q commenter %v", name, body, ip, commenter)
	}
	var notify bool
	f.scalar(&notify, `SELECT notify_replies FROM hatamoto_comments WHERE id = $1`, withReply.ID)
	if notify {
		t.Error("anonymised row still has notify_replies")
	}
	var n int
	f.scalar(&n, `SELECT count(*) FROM hatamoto_comments WHERE id = $1`, reply.ID)
	if n != 1 {
		t.Error("someone else's reply was deleted")
	}
	if _, err := f.svc.Commenter(f.ctx, f.svc.Keys().HashEmail("erase@example.test")); !errors.Is(err, ErrNotFound) {
		t.Errorf("commenter still there: %v", err)
	}
	f.wantCounts(2, 0, "after erase")
	if _, err := f.svc.EraseByEmail(f.ctx, "erase@example.test"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second erase = %v", err)
	}
}

type commenterIDRecorder struct {
	NopObserver
	seen []*uuid.UUID
}

func (r *commenterIDRecorder) OnUpdated(_ context.Context, _, after Comment) {
	r.seen = append(r.seen, after.CommenterID)
}
func (r *commenterIDRecorder) OnDeleted(_ context.Context, c Comment) {
	r.seen = append(r.seen, c.CommenterID)
}

// The rows an erase announces must not carry the erased commenter's id.
func TestEraseAnnouncesWithoutCommenterID(t *testing.T) {
	f := newFixture(t)
	for _, body := range []string{"has a reply", "leaf"} {
		d := f.draft("erase-ids@example.test", body)
		d.Status = StatusApproved
		c := f.store(d)
		if body == "has a reply" {
			r := f.draft("someone@example.test", "reply")
			r.ParentID, r.Status = &c.ID, StatusApproved
			f.store(r)
		}
	}
	rec := &commenterIDRecorder{}
	f.svc.Register(rec)
	if _, err := f.svc.EraseByEmail(f.ctx, "erase-ids@example.test"); err != nil {
		t.Fatal(err)
	}
	if len(rec.seen) != 2 {
		t.Fatalf("announced %d rows, want 2 (one updated, one deleted)", len(rec.seen))
	}
	for i, id := range rec.seen {
		if id != nil {
			t.Errorf("announced row %d carries commenter id %v", i, id)
		}
	}
}

type deleteRecorder struct {
	NopObserver
	ids     *[]uuid.UUID
	updated *[]Comment
}

func (d *deleteRecorder) OnUpdated(_ context.Context, _, after Comment) {
	if d.updated != nil {
		*d.updated = append(*d.updated, after)
	}
}

func (d *deleteRecorder) OnDeleted(_ context.Context, c Comment) { *d.ids = append(*d.ids, c.ID) }

func TestEntityModeAndObservers(t *testing.T) {
	f := newFixture(t)
	if m, err := f.svc.EntityMode(f.ctx, f.entity); err != nil || m != ModeOff {
		t.Fatalf("comments off: %s, %v", m, err)
	}
	var settingsEvents int
	var modeEvents []uuid.UUID
	f.svc.Register(&settingsRecorder{n: &settingsEvents, modes: &modeEvents})
	f.enable()
	if settingsEvents != 1 {
		t.Errorf("OnSettingsChanged fired %d times", settingsEvents)
	}
	if m, _ := f.svc.EntityMode(f.ctx, f.entity); m != ModeOpen {
		t.Errorf("default = %s, want open", m)
	}
	f.exec(`UPDATE hatamoto_entity_schemas SET settings = jsonb_set(settings, '{comments}', '{"mode":"closed"}') WHERE slug = 'cmm-note'`)
	if m, _ := f.svc.EntityMode(f.ctx, f.entity); m != ModeClosed {
		t.Errorf("schema default = %s, want closed", m)
	}
	if err := f.svc.SetEntityMode(f.ctx, f.entity, ModeHidden); err != nil {
		t.Fatal(err)
	}
	if m, _ := f.svc.EntityMode(f.ctx, f.entity); m != ModeHidden {
		t.Errorf("entity override = %s, want hidden", m)
	}
	if err := f.svc.SetEntityMode(f.ctx, f.entity, ""); err != nil {
		t.Fatal(err)
	}
	if m, _ := f.svc.EntityMode(f.ctx, f.entity); m != ModeClosed {
		t.Errorf("inherit again = %s, want closed", m)
	}
	if err := f.svc.SetEntityMode(f.ctx, f.entity, "bogus"); err == nil {
		t.Error("bogus mode accepted")
	}
	f.svc.TypeModeChanged(f.ctx)
	if len(modeEvents) != 3 || modeEvents[0] != f.entity || modeEvents[1] != f.entity || modeEvents[2] != uuid.Nil {
		t.Errorf("OnModeChanged events = %v", modeEvents)
	}
	if _, err := f.svc.EntityMode(f.ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown entity = %v", err)
	}
}

type settingsRecorder struct {
	NopObserver
	n     *int
	modes *[]uuid.UUID
}

func (s *settingsRecorder) OnSettingsChanged(context.Context, Settings) { *s.n++ }

func (s *settingsRecorder) OnModeChanged(_ context.Context, id uuid.UUID) {
	if s.modes != nil {
		*s.modes = append(*s.modes, id)
	}
}

func TestQueueAndStatusCounts(t *testing.T) {
	f := newFixture(t)
	a := f.store(f.draft("q@example.test", "needle 50% off"))
	f.store(f.draft("q@example.test", "hay"))
	if _, err := f.svc.Confirm(f.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	items, err := f.svc.Queue(f.ctx, QueueFilter{Status: StatusPending})
	if err != nil || len(items) != 1 || items[0].Comment.ID != a.ID || items[0].Trust != TrustNew {
		t.Fatalf("queue = %+v, %v", items, err)
	}
	if len(items[0].Comment.SpamReasons) == 0 {
		t.Error("queue item lost its hold reasons")
	}
	if items, _ = f.svc.Queue(f.ctx, QueueFilter{Search: "50%"}); len(items) != 1 {
		t.Errorf("literal %% search matched %d", len(items))
	}
	if items, _ = f.svc.Queue(f.ctx, QueueFilter{Search: "%"}); len(items) != 1 {
		t.Errorf("a bare %% must match literally, got %d", len(items))
	}
	if items, _ = f.svc.Queue(f.ctx, QueueFilter{}); len(items) != 2 {
		t.Errorf("unfiltered queue = %d", len(items))
	}
	sc, err := f.svc.StatusCounts(f.ctx)
	if err != nil || sc[StatusPending] != 1 || sc[StatusDraft] != 1 {
		t.Errorf("status counts = %v, %v", sc, err)
	}
}

type updateRecorder struct {
	NopObserver
	got *[][2]Status
}

func (u *updateRecorder) OnUpdated(_ context.Context, before, after Comment) {
	*u.got = append(*u.got, [2]Status{before.Status, after.Status})
}

func TestObserverSeesEditAndUnapproval(t *testing.T) {
	f := newFixture(t)
	var got [][2]Status
	f.svc.Register(&updateRecorder{got: &got})
	d := f.draft("upd@example.test", "hello")
	d.Status = StatusApproved
	c := f.store(d)
	if _, err := f.svc.Edit(f.ctx, c.ID, Edited{BodyRaw: "hi", BodyHTML: "<p>hi</p>"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.transition(f.ctx, c.ID, StatusPending, nil, nil); err != nil {
		t.Fatal(err)
	}
	want := [][2]Status{{StatusApproved, StatusApproved}, {StatusApproved, StatusPending}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("OnUpdated events = %v, want %v", got, want)
	}
}

// waitForLockWait blocks until want sessions in the scratch database waits on a lock.
func (f *fixture) waitForLockWait(want int) {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		f.scalar(&n, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND datname = current_database()`)
		if n >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatal("no session ever waited on a lock")
}

// A path that waits for the entity counts row must not hold the commenter row
// meanwhile: transition and Confirm take counts first, so the reverse order
// would deadlock.
func TestLockOrderCountsBeforeCommenter(t *testing.T) {
	f := newFixture(t)
	f.enable()
	seed := f.draft("lock@example.test", "seed")
	seed.Status = StatusApproved
	f.store(seed) // creates the commenter and the counts row
	var commenter uuid.UUID
	f.scalar(&commenter, `SELECT id FROM hatamoto_commenters WHERE email_hash = $1`, f.svc.Keys().HashEmail("lock@example.test"))

	cases := map[string]func() error{
		"store": func() error {
			d := f.draft("lock@example.test", "again")
			d.Status = StatusPending
			_, err := f.svc.Store(f.ctx, d)
			return err
		},
		"erase": func() error {
			_, err := f.svc.EraseByEmail(f.ctx, "lock@example.test")
			return err
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			holder, err := f.db.Pool().Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback(f.ctx)
			if _, err := holder.Exec(f.ctx, `SELECT 1 FROM hatamoto_comment_entities WHERE entity_id = $1 FOR UPDATE`, f.entity); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- run() }()
			f.waitForLockWait(1)
			probe, err := f.db.Pool().Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := probe.Exec(f.ctx, `SELECT 1 FROM hatamoto_commenters WHERE id = $1 FOR UPDATE NOWAIT`, commenter); err != nil {
				t.Errorf("%s holds the commenter row while it waits for the counts row: %v", name, err)
			}
			probe.Rollback(f.ctx)
			holder.Rollback(f.ctx)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A reply must lock its parent comment before the entity counts row: a
// moderator transition holds the comment and then waits for the counts row.
func TestLockOrderParentBeforeCounts(t *testing.T) {
	f := newFixture(t)
	f.enable()
	seed := f.draft("parent@example.test", "parent")
	seed.Status = StatusApproved
	parent := f.store(seed)

	holder, err := f.db.Pool().Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(f.ctx)
	if _, err := holder.Exec(f.ctx, `SELECT 1 FROM hatamoto_comments WHERE id = $1 FOR UPDATE`, parent.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		d := f.draft("reply@example.test", "reply")
		d.Status, d.ParentID = StatusPending, &parent.ID
		_, err := f.svc.Store(f.ctx, d)
		done <- err
	}()
	f.waitForLockWait(1)
	probe, err := f.db.Pool().Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := probe.Exec(f.ctx, `SELECT 1 FROM hatamoto_comment_entities WHERE entity_id = $1 FOR UPDATE NOWAIT`, f.entity); err != nil {
		t.Errorf("reply holds the counts row while it waits for the parent comment: %v", err)
	}
	probe.Rollback(f.ctx)
	holder.Rollback(f.ctx)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestApprovedCountUnderConcurrentApprovals(t *testing.T) {
	f := newFixture(t)
	f.enable()
	other := f.newEntity("other")
	var ids []uuid.UUID
	for _, e := range []uuid.UUID{f.entity, other} {
		d := f.draft("race@example.test", "hi")
		d.EntityID, d.Status = e, StatusPending
		ids = append(ids, f.store(d).ID)
	}
	var commenter uuid.UUID
	h := f.svc.Keys().HashEmail("race@example.test")
	f.scalar(&commenter, `SELECT id FROM hatamoto_commenters WHERE email_hash = $1`, h)

	holder, err := f.db.Pool().Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(f.ctx)
	if _, err := holder.Exec(f.ctx, `SELECT 1 FROM hatamoto_commenters WHERE id = $1 FOR UPDATE`, commenter); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for _, id := range ids {
		go func() { _, err := f.svc.Approve(f.ctx, id, nil); done <- err }()
	}
	f.waitForLockWait(2)
	holder.Rollback(f.ctx)
	for range ids {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if cm, err := f.svc.Commenter(f.ctx, h); err != nil || cm.ApprovedCount != 2 {
		t.Errorf("approved_count = %d, %v; want 2", cm.ApprovedCount, err)
	}
}

func TestMarkSpamOnDraftLeavesEmailAlone(t *testing.T) {
	f := newFixture(t)
	f.enable()
	d := f.draft("ceo@victim-company.example", "buy pills")
	d.BodyHash = "draft-spam-hash"
	draft := f.store(d)
	got, err := f.svc.MarkSpam(f.ctx, draft.ID, nil, Learn{EmailDomain: true, Hashes: true})
	if err != nil || got.Status != StatusSpam {
		t.Fatalf("mark spam on draft = %s, %v", got.Status, err)
	}
	cm, err := f.svc.Commenter(f.ctx, f.svc.Keys().HashEmail("ceo@victim-company.example"))
	if err != nil {
		t.Fatal(err)
	}
	if cm.Trust == TrustBlocked || cm.LastSpamAt != nil || cm.TokenVersion != 1 {
		t.Errorf("unconfirmed email was punished: %+v", cm)
	}
	hits, err := f.svc.MatchBlocks(f.ctx, Probe{EmailDomain: "victim-company.example", BodyHash: "draft-spam-hash"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Kind != BlockBodyHash {
		t.Errorf("blocklist hits = %+v, want only the body hash", hits)
	}
}

func TestEraseRemovesOnlyItsOwnLearnedIPBlock(t *testing.T) {
	f := newFixture(t)
	spam := func(email, ip string) {
		d := f.draft(email, "spam "+email)
		d.IP = ip
		c, err := f.svc.Confirm(f.ctx, f.store(d).ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.MarkSpam(f.ctx, c.ID, nil, Learn{Hashes: true}); err != nil {
			t.Fatal(err)
		}
	}
	spam("gone@example.test", "192.0.2.10")
	spam("other@example.test", "192.0.2.20") // same /24, another spammer
	// A bystander on the same network whose comment was never spam.
	d := f.draft("fine@example.test", "hello")
	d.IP = "192.0.2.30"
	if _, err := f.svc.Confirm(f.ctx, f.store(d).ID); err != nil {
		t.Fatal(err)
	}
	ip10, net := f.svc.Keys().HashIP("192.0.2.10")
	ip20, _ := f.svc.Keys().HashIP("192.0.2.20")
	ip30, _ := f.svc.Keys().HashIP("192.0.2.30")
	f.exec(`INSERT INTO hatamoto_comment_blocks (kind, value, source, note, created_at) VALUES ('ip_hash', $1, 'spam', '', now())`, ip30)

	if _, err := f.svc.EraseByEmail(f.ctx, "fine@example.test"); err != nil {
		t.Fatal(err)
	}
	if hits, _ := f.svc.MatchBlocks(f.ctx, Probe{IPHash: ip10, NetHash: net}); len(hits) != 2 {
		t.Errorf("erasing a commenter who was never marked spam lifted blocks: %+v", hits)
	}
	if hits, _ := f.svc.MatchBlocks(f.ctx, Probe{IPHash: ip30}); len(hits) != 1 {
		t.Errorf("a block no spam comment of the erased commenter taught was lifted: %+v", hits)
	}

	if _, err := f.svc.EraseByEmail(f.ctx, "gone@example.test"); err != nil {
		t.Fatal(err)
	}
	hits, _ := f.svc.MatchBlocks(f.ctx, Probe{IPHash: ip10, NetHash: net})
	if len(hits) != 1 || hits[0].Kind != BlockNetHash {
		t.Errorf("after erase want only the shared /24 block, got %+v", hits)
	}
	if hits, _ := f.svc.MatchBlocks(f.ctx, Probe{IPHash: ip20}); len(hits) != 1 {
		t.Errorf("the other spammer's own IP block was lifted: %+v", hits)
	}
}

func TestMarkSpamOnTrashedDraftLeavesEmailAlone(t *testing.T) {
	f := newFixture(t)
	f.enable()
	d := f.draft("ceo@victim-company.example", "buy pills")
	d.BodyHash = "trashed-draft-hash"
	draft := f.store(d)
	if _, err := f.svc.Trash(f.ctx, draft.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.MarkSpam(f.ctx, draft.ID, nil, Learn{EmailDomain: true}); err != nil {
		t.Fatal(err)
	}
	cm, err := f.svc.Commenter(f.ctx, f.svc.Keys().HashEmail("ceo@victim-company.example"))
	if err != nil {
		t.Fatal(err)
	}
	if cm.Trust == TrustBlocked || cm.LastSpamAt != nil || cm.TokenVersion != 1 {
		t.Errorf("unconfirmed email was punished after a trash: %+v", cm)
	}
	if hits, _ := f.svc.MatchBlocks(f.ctx, Probe{EmailDomain: "victim-company.example"}); len(hits) != 0 {
		t.Errorf("domain was blocked: %+v", hits)
	}
}

func TestEditHeldDemotesApprovedComment(t *testing.T) {
	f := newFixture(t)
	var got [][2]Status
	f.svc.Register(&updateRecorder{got: &got})
	d := f.draft("held@example.test", "hello")
	d.Status = StatusApproved
	c := f.store(d)
	c, err := f.svc.Edit(f.ctx, c.ID, Edited{BodyRaw: "hi", BodyHTML: "<p>hi</p>", Hold: true})
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusPending {
		t.Errorf("held edit = %s, want pending", c.Status)
	}
	if want := [][2]Status{{StatusApproved, StatusPending}}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("OnUpdated events = %v, want %v", got, want)
	}
}

func TestReplyParent(t *testing.T) {
	f := newFixture(t)
	d := f.draft("rp@example.test", "hello")
	d.Status = StatusApproved
	ok := f.store(d)
	d = f.draft("rp2@example.test", "waiting")
	d.Status = StatusPending
	pending := f.store(d)
	for name, tc := range map[string]struct {
		entity, parent uuid.UUID
		want           bool
	}{
		"approved":     {f.entity, ok.ID, true},
		"pending":      {f.entity, pending.ID, false},
		"other entity": {uuid.New(), ok.ID, false},
		"unknown":      {f.entity, uuid.New(), false},
	} {
		if got, err := f.svc.ReplyParent(f.ctx, tc.entity, tc.parent); err != nil || got != tc.want {
			t.Errorf("%s: ReplyParent = %v, %v; want %v", name, got, err, tc.want)
		}
	}
}

func TestThreadDeadKeepsSubtree(t *testing.T) {
	f := newFixture(t)
	f.enable()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	root := f.seedComment(nil, base, StatusApproved)
	dead := f.seedComment(&root, base.Add(time.Minute), StatusDead)
	reply := f.seedComment(&dead, base.Add(2*time.Minute), StatusApproved)
	f.seedComment(&root, base.Add(3*time.Minute), StatusSpam)

	page, err := f.svc.Thread(f.ctx, f.entity, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Comments) != 3 {
		t.Fatalf("comments = %d, want 3 (root, dead, reply)", len(page.Comments))
	}
	if page.Comments[1].ID != dead.id || page.Comments[1].Status != StatusDead {
		t.Errorf("second item = %s %q, want the dead comment", page.Comments[1].ID, page.Comments[1].Status)
	}
	if page.Comments[2].ID != reply.id || page.Comments[2].Status != StatusApproved {
		t.Errorf("third item = %s %q, want the approved reply", page.Comments[2].ID, page.Comments[2].Status)
	}
}

func TestThreadRankOrder(t *testing.T) {
	f := newFixture(t)
	f.enable()
	f.exec(`UPDATE hatamoto_entity_schemas SET settings = jsonb_build_object('comments', jsonb_build_object('order', 'rank'))
		WHERE slug = 'cmm-note'`)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	low := f.seedComment(nil, base.Add(3*time.Minute), StatusApproved)
	high := f.seedComment(nil, base, StatusApproved)
	tie := f.seedComment(nil, base.Add(time.Minute), StatusApproved)
	f.exec(`UPDATE hatamoto_comments SET rank = 10 WHERE id = $1`, high.id)
	f.exec(`UPDATE hatamoto_comments SET rank = 10 WHERE id = $1`, tie.id)
	oldReply := f.seedComment(&high, base.Add(time.Hour), StatusApproved)
	topReply := f.seedComment(&high, base.Add(2*time.Hour), StatusApproved)
	f.exec(`UPDATE hatamoto_comments SET rank = 4 WHERE id = $1`, oldReply.id)

	cfg, err := f.svc.EntityConfig(f.ctx, f.entity)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Order != OrderRank {
		t.Fatalf("EntityConfig order = %q, want rank", cfg.Order)
	}
	n := f.counter.Count()
	page, err := f.svc.Thread(f.ctx, f.entity, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.counter.Count() - n; got != 1 {
		t.Fatalf("Thread ran %d statements, want 1", got)
	}
	// roots: rank 10 newest first (tie), then rank 0; replies: rank 4 before rank 0.
	want := []uuid.UUID{tie.id, high.id, oldReply.id, topReply.id, low.id}
	if len(page.Comments) != len(want) {
		t.Fatalf("comments = %d, want %d", len(page.Comments), len(want))
	}
	for i, id := range want {
		if page.Comments[i].ID != id {
			t.Errorf("position %d = %s, want %s", i, page.Comments[i].ID, id)
		}
	}
}
