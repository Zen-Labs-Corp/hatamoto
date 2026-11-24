//go:build integration

package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/migrations"
)

const fixtureSchemaID = "22222222-2222-2222-2222-222222222222"

type fixture struct {
	t      *testing.T
	ctx    context.Context
	db     *database.DB
	svc    *comment.Service
	store  *pgStore
	entity uuid.UUID
	n      int
}

// newFixture migrates a scratch database, builds the comment service and the
// store on it and seeds one entity.
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
	name := "hm_identity_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
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
	pcfg.MaxConns, pcfg.MinConns = 12, 1
	db, err := database.NewWithConfig(ctx, pcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})

	f := &fixture{t: t, ctx: ctx, db: db, store: &pgStore{db: db},
		svc: comment.New(db, "integration-test-secret", slog.New(slog.NewTextHandler(io.Discard, nil)))}
	f.exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ($1, 'Note', 'cmm-note', '{"type":"object"}')`, fixtureSchemaID)
	f.entity = uuid.New()
	f.exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status) VALUES ($1, $2, 'Hello', 'hello', 'published')`, f.entity, fixtureSchemaID)
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

// draft stores a draft through the real model, with a fresh address unless
// email is given, and returns it as stored.
func (f *fixture) draft(email, ip string) comment.Comment {
	f.t.Helper()
	f.n++
	if email == "" {
		email = fmt.Sprintf("c%d@example.test", f.n)
	}
	c, err := f.svc.Store(f.ctx, comment.Draft{EntityID: f.entity, Email: email, AuthorName: "A",
		BodyRaw: "hi", BodyHTML: "<p>hi</p>", IP: ip, Status: comment.StatusDraft})
	if err != nil {
		f.t.Fatal(err)
	}
	if c.CommenterID == nil {
		f.t.Fatal("draft has no commenter")
	}
	return c
}

// issue records one confirmation for c under the default limits.
func (f *fixture) issue(c comment.Comment, now time.Time) (raw string, limited bool) {
	f.t.Helper()
	return f.issueWith(c, now, DefaultLimits)
}

func (f *fixture) issueWith(c comment.Comment, now time.Time, l Limits) (raw string, limited bool) {
	f.t.Helper()
	raw, hash, err := NewToken()
	if err != nil {
		f.t.Fatal(err)
	}
	limited, err = f.store.Issue(f.ctx, issueRow{Hash: hash, CommentID: &c.ID, CommenterID: *c.CommenterID,
		IPHash: c.IPHash, NetHash: c.NetHash, Now: now}, l)
	if err != nil {
		f.t.Fatal(err)
	}
	return raw, limited
}

func (f *fixture) confirmations() (n int) {
	f.t.Helper()
	f.scalar(&n, `SELECT count(*) FROM hatamoto_comment_confirmations`)
	return n
}

func mustHash(t *testing.T, raw string) []byte {
	t.Helper()
	h, ok := HashToken(raw)
	if !ok {
		t.Fatalf("token %q not well formed", raw)
	}
	return h
}

func TestStore_ConsumeIsSingleUse(t *testing.T) {
	f := newFixture(t)
	c := f.draft("", "192.0.2.1")
	now := time.Now().UTC()
	raw, _ := f.issue(c, now)
	h := mustHash(t, raw)

	cid, eid, mid, ok, err := f.store.Peek(f.ctx, h, now)
	if err != nil || !ok || cid != c.ID || eid != f.entity || mid != *c.CommenterID {
		t.Fatalf("Peek = %v %v %v %v %v", cid, eid, mid, ok, err)
	}
	var used *time.Time
	f.scalar(&used, `SELECT used_at FROM hatamoto_comment_confirmations WHERE token_hash = $1`, h)
	if used != nil {
		t.Fatal("Peek consumed the token")
	}
	cid, mid, ok, err = f.store.Consume(f.ctx, h, now, false)
	if err != nil || !ok || cid != c.ID || mid != *c.CommenterID {
		t.Fatalf("Consume = %v %v %v %v", cid, mid, ok, err)
	}
	if _, _, ok, err := f.store.Consume(f.ctx, h, now, false); err != nil || ok {
		t.Errorf("second Consume ok = %v err = %v, want refused", ok, err)
	}
	if _, _, _, ok, err := f.store.Peek(f.ctx, h, now); err != nil || ok {
		t.Errorf("Peek after Consume ok = %v err = %v, want refused", ok, err)
	}
}

func TestStore_ReleaseRestoresAConsumedToken(t *testing.T) {
	f := newFixture(t)
	c := f.draft("", "192.0.2.1")
	now := time.Now().UTC()
	raw, _ := f.issue(c, now)
	h := mustHash(t, raw)
	if _, _, ok, err := f.store.Consume(f.ctx, h, now, false); err != nil || !ok {
		t.Fatalf("Consume ok = %v err = %v", ok, err)
	}
	if err := f.store.Release(f.ctx, h); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := f.store.Consume(f.ctx, h, now, false); err != nil || !ok {
		t.Errorf("Consume after Release ok = %v err = %v, want accepted", ok, err)
	}
}

func TestStore_PeekNeedsDraft(t *testing.T) {
	f := newFixture(t)
	c := f.draft("", "192.0.2.1")
	now := time.Now().UTC()
	raw, _ := f.issue(c, now)
	f.exec(`UPDATE hatamoto_comments SET status = 'pending' WHERE id = $1`, c.ID)
	if _, _, _, ok, err := f.store.Peek(f.ctx, mustHash(t, raw), now); err != nil || ok {
		t.Errorf("Peek of a moderated comment ok = %v err = %v, want refused", ok, err)
	}
}

func TestStore_ExpiredTokenRejected(t *testing.T) {
	f := newFixture(t)
	c := f.draft("", "192.0.2.1")
	now := time.Now().UTC()
	raw, _ := f.issue(c, now)
	h := mustHash(t, raw)
	late := now.Add(TokenTTL + time.Second)
	if _, _, _, ok, err := f.store.Peek(f.ctx, h, late); err != nil || ok {
		t.Errorf("Peek after expiry ok = %v err = %v", ok, err)
	}
	if _, _, ok, err := f.store.Consume(f.ctx, h, late, false); err != nil || ok {
		t.Errorf("Consume after expiry ok = %v err = %v", ok, err)
	}
	if _, _, ok, err := f.store.Consume(f.ctx, h, now, false); err != nil || !ok {
		t.Errorf("Consume before expiry ok = %v err = %v, want accepted", ok, err)
	}
}

func TestStore_StoresOnlyHash(t *testing.T) {
	f := newFixture(t)
	c := f.draft("", "192.0.2.1")
	raw, _ := f.issue(c, time.Now().UTC())
	var stored []byte
	f.scalar(&stored, `SELECT token_hash FROM hatamoto_comment_confirmations`)
	sum := sha256.Sum256([]byte(raw))
	if !bytes.Equal(stored, sum[:]) {
		t.Error("token_hash is not sha256(raw)")
	}
	var leaked bool
	f.scalar(&leaked, `SELECT bool_or(k::text LIKE '%' || $1 || '%') FROM hatamoto_comment_confirmations k`, raw)
	if leaked {
		t.Error("the raw token appears in a row")
	}
}

func TestStore_IssueLimits(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	c := f.draft("", "192.0.2.1")
	for i := 0; i < 3; i++ {
		if _, limited := f.issue(c, now); limited {
			t.Fatalf("send %d limited", i+1)
		}
	}
	if _, limited := f.issue(c, now); !limited {
		t.Error("4th send for one commenter not limited")
	}
	if n := f.confirmations(); n != 3 {
		t.Errorf("rows = %d, want 3 (a limited request inserts nothing)", n)
	}
	// Rows older than 24 hours do not count.
	f.exec(`UPDATE hatamoto_comment_confirmations SET created_at = now() - interval '25 hours'`)
	if _, limited := f.issue(c, now); limited {
		t.Error("aged rows still count")
	}
}

func TestStore_IssueIPAndNetLimits(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	// ip: 10 distinct commenters from one address, the 11th is limited.
	var last comment.Comment
	for i := 0; i < 11; i++ {
		last = f.draft("", "192.0.2.1")
		_, limited := f.issue(last, now)
		if want := i == 10; limited != want {
			t.Fatalf("commenter %d limited = %v, want %v", i+1, limited, want)
		}
	}
	// net: the same /24 but another address; the per-net limit is the only one in play.
	l := Limits{PerCommenter: 100, PerIP: 100, PerNet: 10}
	other := f.draft("", "192.0.2.77")
	if other.NetHash != last.NetHash || other.IPHash == last.IPHash {
		t.Fatalf("test setup: want same net, other ip")
	}
	if _, limited := f.issueWith(other, now, l); !limited {
		t.Error("per-net limit not applied across addresses")
	}
	// Empty hashes are never counted.
	noIP := f.draft("", "not an ip")
	if noIP.IPHash != "" || noIP.NetHash != "" {
		t.Fatalf("test setup: hashes = %q %q, want empty", noIP.IPHash, noIP.NetHash)
	}
	f.exec(`UPDATE hatamoto_comment_confirmations SET ip_hash = '', net_hash = ''`)
	for i := 0; i < 3; i++ {
		if _, limited := f.issueWith(noIP, now, Limits{PerCommenter: 100, PerIP: 1, PerNet: 1}); limited {
			t.Fatalf("send %d with empty hashes limited by ip/net", i+1)
		}
	}
}

func TestStore_IssueConcurrent(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	c := f.draft("", "192.0.2.1")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, hash, err := NewToken()
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := f.store.Issue(f.ctx, issueRow{Hash: hash, CommentID: &c.ID, CommenterID: *c.CommenterID,
				IPHash: c.IPHash, NetHash: c.NetHash, Now: now}, DefaultLimits); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := f.confirmations(); n != 3 {
		t.Errorf("rows = %d, want exactly 3", n)
	}
}

// Many addresses from one IP (or one /24) must not overshoot the shared caps.
func TestStore_IssueConcurrentAcrossCommenters(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ip     func(i int) string
		limits Limits
	}{
		{"ip_hash", func(int) string { return "192.0.2.1" }, Limits{PerCommenter: 3, PerIP: 3, PerNet: 100}},
		{"net_hash", func(i int) string { return fmt.Sprintf("192.0.2.%d", i+1) }, Limits{PerCommenter: 3, PerIP: 100, PerNet: 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			now := time.Now().UTC()
			const n = 12
			drafts := make([]comment.Comment, n)
			for i := range drafts {
				drafts[i] = f.draft("", tc.ip(i))
			}
			var wg sync.WaitGroup
			for _, c := range drafts {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, hash, err := NewToken()
					if err != nil {
						t.Error(err)
						return
					}
					if _, err := f.store.Issue(f.ctx, issueRow{Hash: hash, CommentID: &c.ID, CommenterID: *c.CommenterID,
						IPHash: c.IPHash, NetHash: c.NetHash, Now: now}, tc.limits); err != nil {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			if got := f.confirmations(); got != 3 {
				t.Errorf("rows = %d, want exactly 3", got)
			}
		})
	}
}

func TestStore_Purge(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	exists := func(table string, id uuid.UUID) bool {
		f.t.Helper()
		var n int
		f.scalar(&n, `SELECT count(*) FROM `+table+` WHERE id = $1`, id)
		return n == 1
	}
	age := func(id uuid.UUID, hours int) {
		f.t.Helper()
		f.exec(`UPDATE hatamoto_comments SET created_at = now() - make_interval(hours => $2) WHERE id = $1`, id, hours)
	}
	ageCommenter := func(id uuid.UUID, hours int) {
		f.t.Helper()
		f.exec(`UPDATE hatamoto_commenters SET last_seen_at = now() - make_interval(hours => $2) WHERE id = $1`, id, hours)
	}

	old := f.draft("", "192.0.2.1")
	fresh := f.draft("", "192.0.2.2")
	pending := f.draft("", "192.0.2.3")
	parent := f.draft("", "192.0.2.4")
	known := f.draft("", "192.0.2.5")
	recent := f.draft("", "192.0.2.6")
	reply, err := f.svc.Store(f.ctx, comment.Draft{EntityID: f.entity, ParentID: &parent.ID, Email: "reply@example.test",
		AuthorName: "A", BodyRaw: "re", BodyHTML: "<p>re</p>", IP: "192.0.2.7", Status: comment.StatusDraft})
	if err != nil {
		t.Fatal(err)
	}

	f.exec(`UPDATE hatamoto_comments SET status = 'pending' WHERE id = $1`, pending.ID)
	age(old.ID, 25)
	age(fresh.ID, 23)
	age(pending.ID, 25)
	age(parent.ID, 25)
	age(known.ID, 25)
	age(recent.ID, 25)
	f.exec(`UPDATE hatamoto_commenters SET trust_level = 'known' WHERE id = $1`, *known.CommenterID)
	ageCommenter(*old.CommenterID, 25)
	ageCommenter(*known.CommenterID, 25)
	ageCommenter(*recent.CommenterID, 1)
	ageCommenter(*reply.CommenterID, 25)

	// An old confirmation and a fresh one.
	f.issue(old, now.Add(-25*time.Hour))
	f.issue(fresh, now)

	drafts, tokens, commenters, err := f.store.Purge(f.ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	// The 25 h old leaf drafts go (old, recent, known: a draft is a draft whoever
	// wrote it); the parent has a reply and stays.
	if drafts != 3 || tokens != 1 {
		t.Errorf("purged drafts = %d tokens = %d, want 3 and 1", drafts, tokens)
	}
	if exists("hatamoto_comments", old.ID) || exists("hatamoto_comments", recent.ID) || exists("hatamoto_comments", known.ID) {
		t.Error("an expired leaf draft survived")
	}
	for name, c := range map[string]comment.Comment{"fresh draft": fresh, "pending": pending, "draft with a reply": parent, "reply": reply} {
		if !exists("hatamoto_comments", c.ID) {
			t.Errorf("%s was purged", name)
		}
	}
	if exists("hatamoto_commenters", *old.CommenterID) {
		t.Error("idle draft-only commenter survived")
	}
	if !exists("hatamoto_commenters", *known.CommenterID) {
		t.Error("known commenter was purged")
	}
	if !exists("hatamoto_commenters", *recent.CommenterID) {
		t.Error("recently seen commenter was purged")
	}
	if !exists("hatamoto_commenters", *fresh.CommenterID) || !exists("hatamoto_commenters", *pending.CommenterID) {
		t.Error("a commenter with a comment was purged")
	}
	if commenters != 1 {
		t.Errorf("purged commenters = %d, want 1 (only the idle draft-only one)", commenters)
	}
}
