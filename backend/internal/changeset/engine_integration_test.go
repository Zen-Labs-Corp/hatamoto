//go:build integration

package changeset

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/taxonomy"
)

type eventLog struct {
	mu sync.Mutex
	n  int
}

func (l *eventLog) Emit(context.Context, entity.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n++
}

func (l *eventLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

type fixture struct {
	t      *testing.T
	ctx    context.Context
	db     *database.DB
	eng    *Engine
	ent    *entity.Service
	media  *media.Service
	events *eventLog
	schema *entity.EntitySchema
	dir    string
	now    time.Time
	tag    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 6, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	schemas := entity.NewPostgresSchemaRepository(db)
	tag := uuid.NewString()[:8]
	sc, err := schemas.Create(ctx, &entity.CreateSchemaInput{Name: "cs " + tag, Slug: "cs-" + tag,
		Schema: json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"}},"required":["content"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	events := &eventLog{}
	entSvc := entity.NewService(schemas, entity.NewPostgresEntityRepository(db), logger)
	entSvc.SetEvents(events)
	dir := t.TempDir()
	mediaSvc := media.NewService(media.NewPostgresRepository(db), media.Config{UploadDir: dir, BaseURL: "/media", MaxSizeMB: 10}, logger)

	f := &fixture{t: t, ctx: ctx, db: db, ent: entSvc, media: mediaSvc, events: events, schema: sc, dir: dir, tag: tag,
		now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
	f.eng = &Engine{
		DB: db, Blobs: mediaSvc, Logger: logger, Now: func() time.Time { return f.now },
		Writers: map[Kind]KindWriter{
			KindEntity:   EntityWriter{Svc: entSvc},
			KindTerm:     TermWriter{Svc: taxonomy.NewService(taxonomy.NewPostgresRepository(db), logger)},
			KindMenu:     MenuWriter{Svc: menu.NewService(menu.NewPostgresRepository(db), logger)},
			KindRedirect: RedirectWriter{Svc: redirect.NewService(db, logger)},
			KindMedia:    MediaWriter{Svc: mediaSvc},
		},
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = db.Pool().Exec(bg, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, sc.ID)
		_ = schemas.Delete(bg, sc.ID)
		for _, q := range []string{
			`DELETE FROM hatamoto_terms WHERE slug LIKE $1`,
			`DELETE FROM hatamoto_menus WHERE slug LIKE $1`,
			`DELETE FROM hatamoto_redirects WHERE source_path LIKE '/' || $1 || '%'`,
			`DELETE FROM hatamoto_changeset_source_keys WHERE source_key LIKE $1`,
		} {
			arg := tag + "%"
			if strings.Contains(q, "source_path") {
				arg = tag
			}
			_, _ = db.Pool().Exec(bg, q, arg)
		}
	})
	return f
}

func raw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func (f *fixture) begin(plugin string) int64 {
	f.t.Helper()
	id, err := f.eng.Begin(f.ctx, uuid.New(), plugin)
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *fixture) stage(cs int64, it Item) {
	f.t.Helper()
	if err := f.eng.Stage(f.ctx, cs, it); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) entityItem(key, slug, content string) Item {
	return Item{Kind: KindEntity, Op: OpUpsert, SourceKey: key, Payload: raw(map[string]any{
		"schema": f.schema.Slug, "name": "N " + slug, "slug": slug, "attributes": map[string]any{"content": content}})}
}

func (f *fixture) count(q string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.db.Pool().QueryRow(f.ctx, q, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *fixture) entities() int {
	return f.count(`SELECT count(*) FROM hatamoto_entities WHERE schema_id = $1`, f.schema.ID)
}

func (f *fixture) state(cs int64) string {
	f.t.Helper()
	var s string
	if err := f.db.Pool().QueryRow(f.ctx, `SELECT state FROM hatamoto_changesets WHERE id = $1`, cs).Scan(&s); err != nil {
		f.t.Fatal(err)
	}
	return s
}

func (f *fixture) publish(cs int64) Result {
	f.t.Helper()
	res, err := f.eng.Publish(f.ctx, cs)
	if err != nil {
		f.t.Fatal(err)
	}
	return res
}

func (f *fixture) entityToken(id string) string {
	f.t.Helper()
	var v int
	var at time.Time
	if err := f.db.Pool().QueryRow(f.ctx, `SELECT version, updated_at FROM hatamoto_entities WHERE id = $1`, id).Scan(&v, &at); err != nil {
		f.t.Fatal(err)
	}
	return EntityToken(v, at)
}

func (f *fixture) stageFile(run uuid.UUID, name string) string {
	f.t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{200, 10, 10, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		f.t.Fatal(err)
	}
	key, err := f.media.StageBlob(f.ctx, run, &buf, name)
	if err != nil {
		f.t.Fatal(err)
	}
	return key
}

func (f *fixture) fileExists(key string) bool {
	_, err := os.Stat(filepath.Join(f.dir, key))
	return err == nil
}

// liveFiles counts files outside the staged directory.
func (f *fixture) liveFiles() int {
	n := 0
	_ = filepath.WalkDir(f.dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && !strings.Contains(filepath.ToSlash(p), "/staged/") {
			n++
		}
		return nil
	})
	return n
}

func (f *fixture) runOf(cs int64) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	if err := f.db.Pool().QueryRow(f.ctx, `SELECT run_id FROM hatamoto_changesets WHERE id = $1`, cs).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func TestChangesetPublishAtomic(t *testing.T) {
	f := newFixture(t)
	cs := f.begin("")
	run := f.runOf(cs)
	f.stage(cs, f.entityItem("a-"+f.tag, "atomic-"+f.tag, "ok"))
	f.stage(cs, Item{Kind: KindTerm, Op: OpUpsert, Payload: raw(map[string]any{"kind": "tag", "name": "T", "slug": f.tag + "-term"})})
	f.stage(cs, Item{Kind: KindRedirect, Op: OpUpsert, Payload: raw(map[string]any{"source_path": "/" + f.tag + "-old", "target": "/new"})})
	f.stage(cs, Item{Kind: KindMenu, Op: OpUpsert, Payload: raw(map[string]any{"name": "M", "slug": f.tag + "-menu"})})
	f.stage(cs, Item{Kind: KindMedia, Op: OpUpsert, BlobKeys: []string{f.stageFile(run, "p.png")}, Payload: raw(map[string]any{"filename": "p.png"})})
	// The last item breaks the schema (content is required).
	bad := f.entityItem("b-"+f.tag, "atomic-bad-"+f.tag, "x")
	bad.Payload = raw(map[string]any{"schema": f.schema.Slug, "name": "Bad", "attributes": map[string]any{}})
	f.stage(cs, bad)

	if _, err := f.eng.Publish(f.ctx, cs); err == nil {
		t.Fatal("publish succeeded with an invalid last item")
	}
	if n := f.entities(); n != 0 {
		t.Errorf("entities = %d, want 0", n)
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_terms WHERE slug = $1`, f.tag+"-term"); n != 0 {
		t.Errorf("terms = %d", n)
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_redirects WHERE source_path = $1`, "/"+f.tag+"-old"); n != 0 {
		t.Errorf("redirects = %d", n)
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_menus WHERE slug = $1`, f.tag+"-menu"); n != 0 {
		t.Errorf("menus = %d", n)
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_media WHERE origin = 'owner' AND original_filename = 'p.png' AND created_at > $1`, time.Now().Add(-time.Minute)); n != 0 {
		t.Errorf("media rows = %d", n)
	}
	if n := f.liveFiles(); n != 0 {
		t.Errorf("%d published media files left behind", n)
	}
	if got := f.state(cs); got != StateStaged {
		t.Errorf("state = %s, want staged", got)
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_changeset_source_keys WHERE source_key LIKE $1`, "%-"+f.tag); n != 0 {
		t.Errorf("source keys = %d", n)
	}
}

func TestChangesetNoEventsOnRollback(t *testing.T) {
	f := newFixture(t)
	cs := f.begin("")
	f.stage(cs, f.entityItem("a-"+f.tag, "ev-"+f.tag, "ok"))
	bad := f.entityItem("b-"+f.tag, "", "x")
	bad.Payload = raw(map[string]any{"schema": f.schema.Slug, "name": "Bad", "attributes": map[string]any{}})
	f.stage(cs, bad)
	if _, err := f.eng.Publish(f.ctx, cs); err == nil {
		t.Fatal("want an error")
	}
	if n := f.events.count(); n != 0 {
		t.Fatalf("%d events after a rolled-back publish", n)
	}

	ok := f.begin("")
	f.stage(ok, f.entityItem("c-"+f.tag, "ev2-"+f.tag, "ok"))
	f.publish(ok)
	if n := f.events.count(); n != 1 {
		t.Fatalf("events after commit = %d, want 1 batch event", n)
	}
}

func TestChangesetConflictSkipped(t *testing.T) {
	f := newFixture(t)
	first := f.begin("")
	f.stage(first, f.entityItem("e-"+f.tag, "conf-"+f.tag, "v1"))
	f.stage(first, Item{Kind: KindRedirect, Op: OpUpsert, SourceKey: "r-" + f.tag,
		Payload: raw(map[string]any{"source_path": "/" + f.tag + "-r", "target": "/a"})})
	f.publish(first)

	var entID, redID string
	_ = f.db.Pool().QueryRow(f.ctx, `SELECT id FROM hatamoto_entities WHERE schema_id = $1`, f.schema.ID).Scan(&entID)
	_ = f.db.Pool().QueryRow(f.ctx, `SELECT id FROM hatamoto_redirects WHERE source_path = $1`, "/"+f.tag+"-r").Scan(&redID)
	staleEnt := f.entityToken(entID)
	var at time.Time
	_ = f.db.Pool().QueryRow(f.ctx, `SELECT updated_at FROM hatamoto_redirects WHERE id = $1`, redID).Scan(&at)

	second := f.begin("")
	upd := f.entityItem("", "conf-"+f.tag, "v2")
	upd.TargetID, upd.BaseToken = entID, staleEnt
	f.stage(second, upd)
	f.stage(second, Item{Kind: KindRedirect, Op: OpUpsert, TargetID: redID, BaseToken: TimeToken(at),
		Payload: raw(map[string]any{"source_path": "/" + f.tag + "-r", "target": "/b"})})
	f.stage(second, f.entityItem("e2-"+f.tag, "conf2-"+f.tag, "other"))

	// The owner edits the entity meanwhile.
	if _, err := f.db.Pool().Exec(f.ctx, `UPDATE hatamoto_entities SET name = 'edited', updated_at = updated_at + interval '1 second' WHERE id = $1`, entID); err != nil {
		t.Fatal(err)
	}
	res := f.publish(second)
	if res.Applied != 2 || res.Skipped != 1 || len(res.Conflicts) != 1 || res.Conflicts[0].Kind != KindEntity || res.Conflicts[0].Reason != "moved" {
		t.Fatalf("result = %+v", res)
	}
	if n := f.entities(); n != 2 {
		t.Errorf("entities = %d, want 2 (the conflict skipped, the new one published)", n)
	}
	var content, target string
	_ = f.db.Pool().QueryRow(f.ctx, `SELECT attributes->>'content' FROM hatamoto_entities WHERE id = $1`, entID).Scan(&content)
	_ = f.db.Pool().QueryRow(f.ctx, `SELECT target_url FROM hatamoto_redirects WHERE id = $1`, redID).Scan(&target)
	if content != "v1" || target != "/b" {
		t.Errorf("content = %q (want v1), redirect target = %q (want /b)", content, target)
	}
}

func TestChangesetSlugSuffix(t *testing.T) {
	f := newFixture(t)
	// A draft already holds the slugs; a changeset must not collide with it.
	seed := f.begin("")
	f.stage(seed, f.entityItem("", "same-"+f.tag, "draft"))
	f.stage(seed, Item{Kind: KindTerm, Op: OpUpsert, Payload: raw(map[string]any{"kind": "tag", "name": "T", "slug": f.tag + "-same"})})
	f.stage(seed, Item{Kind: KindMenu, Op: OpUpsert, Payload: raw(map[string]any{"name": "M", "slug": f.tag + "-same"})})
	f.publish(seed)

	cs := f.begin("")
	f.stage(cs, f.entityItem("", "same-"+f.tag, "new"))
	f.stage(cs, Item{Kind: KindTerm, Op: OpUpsert, Payload: raw(map[string]any{"kind": "tag", "name": "T", "slug": f.tag + "-same"})})
	f.stage(cs, Item{Kind: KindMenu, Op: OpUpsert, Payload: raw(map[string]any{"name": "M", "slug": f.tag + "-same"})})
	f.publish(cs)

	if n := f.count(`SELECT count(*) FROM hatamoto_entities WHERE schema_id = $1 AND slug = $2`, f.schema.ID, "same-"+f.tag+"-2"); n != 1 {
		t.Errorf("entity -2 count = %d", n)
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_terms WHERE slug = $1`, f.tag+"-same-2"); n != 1 {
		t.Errorf("term -2 count = %d", n)
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_menus WHERE slug = $1`, f.tag+"-same-2"); n != 1 {
		t.Errorf("menu -2 count = %d", n)
	}
}

func TestChangesetRevert(t *testing.T) {
	f := newFixture(t)
	run := uuid.New()
	create, err := f.eng.Begin(f.ctx, run, "")
	if err != nil {
		t.Fatal(err)
	}
	f.stage(create, f.entityItem("e-"+f.tag, "rev-"+f.tag, "original"))
	f.stage(create, Item{Kind: KindTerm, Op: OpUpsert, SourceKey: "t-" + f.tag, Payload: raw(map[string]any{"kind": "tag", "name": "Orig", "slug": f.tag + "-rev"})})
	f.stage(create, Item{Kind: KindRedirect, Op: OpUpsert, SourceKey: "r-" + f.tag, Payload: raw(map[string]any{"source_path": "/" + f.tag + "-rev", "target": "/x"})})
	f.stage(create, Item{Kind: KindMenu, Op: OpUpsert, SourceKey: "m-" + f.tag, Payload: raw(map[string]any{"name": "Orig", "slug": f.tag + "-rev"})})
	f.stage(create, Item{Kind: KindMedia, Op: OpUpsert, SourceKey: "i-" + f.tag, BlobKeys: []string{f.stageFile(run, "r.png")},
		Payload: raw(map[string]any{"filename": "r.png"})})
	f.publish(create)
	if f.liveFiles() == 0 {
		t.Fatal("published media left no file")
	}

	// Second changeset: update the entity, term and menu, delete the redirect.
	second := f.begin("")
	ids := map[string]string{}
	for q, dst := range map[string]string{
		`SELECT id FROM hatamoto_entities WHERE schema_id = $1`: "e", `SELECT id FROM hatamoto_terms WHERE slug = $1`: "t",
		`SELECT id FROM hatamoto_redirects WHERE source_path = $1`: "r", `SELECT id FROM hatamoto_menus WHERE slug = $1`: "m",
	} {
		var arg any = f.tag + "-rev"
		switch dst {
		case "e":
			arg = f.schema.ID
		case "r":
			arg = "/" + f.tag + "-rev"
		}
		var id string
		if err := f.db.Pool().QueryRow(f.ctx, q, arg).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[dst] = id
	}
	upd := f.entityItem("", "", "changed")
	upd.TargetID = ids["e"]
	upd.Payload = raw(map[string]any{"attributes": map[string]any{"content": "changed"}})
	f.stage(second, upd)
	f.stage(second, Item{Kind: KindTerm, Op: OpUpsert, TargetID: ids["t"], Payload: raw(map[string]any{"name": "Changed", "slug": f.tag + "-rev"})})
	f.stage(second, Item{Kind: KindMenu, Op: OpUpsert, TargetID: ids["m"], Payload: raw(map[string]any{"name": "Changed"})})
	f.stage(second, Item{Kind: KindRedirect, Op: OpDelete, TargetID: ids["r"]})
	f.publish(second)

	if _, err := f.eng.Revert(f.ctx, second); err != nil {
		t.Fatal(err)
	}
	var content, tname, mname, rtarget string
	_ = f.db.Pool().QueryRow(f.ctx, `SELECT attributes->>'content' FROM hatamoto_entities WHERE id = $1`, ids["e"]).Scan(&content)
	_ = f.db.Pool().QueryRow(f.ctx, `SELECT name FROM hatamoto_terms WHERE id = $1`, ids["t"]).Scan(&tname)
	_ = f.db.Pool().QueryRow(f.ctx, `SELECT name FROM hatamoto_menus WHERE id = $1`, ids["m"]).Scan(&mname)
	_ = f.db.Pool().QueryRow(f.ctx, `SELECT target_url FROM hatamoto_redirects WHERE id = $1`, ids["r"]).Scan(&rtarget)
	if content != "original" || tname != "Orig" || mname != "Orig" || rtarget != "/x" {
		t.Fatalf("after revert: content=%q term=%q menu=%q redirect=%q", content, tname, mname, rtarget)
	}
	if got := f.state(second); got != StateReverted {
		t.Errorf("state = %s", got)
	}

	// Reverting the second changeset handed its restored tokens to the first
	// one, so the first reverts whole: nothing but the engine wrote them.
	res, err := f.eng.Revert(f.ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 5 || res.Skipped != 0 {
		t.Fatalf("create revert result = %+v, want all five items removed", res)
	}
	if f.entities() != 0 ||
		f.count(`SELECT count(*) FROM hatamoto_terms WHERE slug = $1`, f.tag+"-rev") != 0 ||
		f.count(`SELECT count(*) FROM hatamoto_menus WHERE slug = $1`, f.tag+"-rev") != 0 ||
		f.count(`SELECT count(*) FROM hatamoto_redirects WHERE source_path = $1`, "/"+f.tag+"-rev") != 0 {
		t.Fatal("rows left after the first changeset's revert")
	}
	if n := f.liveFiles(); n != 0 {
		t.Errorf("%d media files survived the revert", n)
	}
	if _, err := f.eng.Revert(f.ctx, create); !errors.Is(err, ErrNotPublished) {
		t.Errorf("second revert = %v, want ErrNotPublished", err)
	}
}

func TestChangesetDiscardDeletesBlobs(t *testing.T) {
	f := newFixture(t)
	run := uuid.New()
	cs, err := f.eng.Begin(f.ctx, run, "")
	if err != nil {
		t.Fatal(err)
	}
	key := f.stageFile(run, "d.png")
	f.stage(cs, Item{Kind: KindMedia, Op: OpUpsert, BlobKeys: []string{key}, Payload: raw(map[string]any{"filename": "d.png"})})
	f.stage(cs, f.entityItem("", "disc-"+f.tag, "x"))
	if _, err := f.db.Pool().Exec(f.ctx, `INSERT INTO hatamoto_run_buffer (run_id, key, value) VALUES ($1, 'k', '\x01')`, run); err != nil {
		t.Fatal(err)
	}
	if !f.fileExists(key) {
		t.Fatal("staged file missing")
	}
	if err := f.eng.Discard(f.ctx, cs); err != nil {
		t.Fatal(err)
	}
	if f.fileExists(key) {
		t.Error("staged file survived discard")
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_changeset_items WHERE changeset_id = $1`, cs); n != 0 {
		t.Errorf("items = %d", n)
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_run_buffer WHERE run_id = $1`, run); n != 0 {
		t.Errorf("buffer rows = %d", n)
	}
	if got := f.state(cs); got != StateDiscarded {
		t.Errorf("state = %s", got)
	}
	if f.entities() != 0 {
		t.Error("discard published something")
	}
	if err := f.eng.Discard(f.ctx, cs); !errors.Is(err, ErrNotStaged) {
		t.Errorf("second discard = %v", err)
	}
}

func TestChangesetExpiry72h(t *testing.T) {
	f := newFixture(t)
	t0 := f.now
	run := uuid.New()
	ok := f.begin("")
	stale, err := f.eng.Begin(f.ctx, run, "")
	if err != nil {
		t.Fatal(err)
	}
	key := f.stageFile(run, "e.png")
	f.stage(stale, Item{Kind: KindMedia, Op: OpUpsert, BlobKeys: []string{key}, Payload: raw(map[string]any{"filename": "e.png"})})
	f.stage(ok, f.entityItem("", "exp-"+f.tag, "x"))
	lazy := f.begin("")
	f.stage(lazy, f.entityItem("", "exp2-"+f.tag, "x"))

	f.now = t0.Add(71 * time.Hour)
	if n, err := f.eng.Expire(f.ctx); err != nil || f.state(stale) != StateStaged {
		t.Fatalf("expired at 71h: n=%d err=%v state=%s", n, err, f.state(stale))
	}
	f.publish(ok)

	f.now = t0.Add(73 * time.Hour)
	if _, err := f.eng.Publish(f.ctx, lazy); !errors.Is(err, ErrExpired) {
		t.Fatalf("publish at 73h = %v, want ErrExpired", err)
	}
	if got := f.state(lazy); got != StateExpired {
		t.Errorf("lazy expiry state = %s", got)
	}
	if n, err := f.eng.Expire(f.ctx); err != nil || n < 1 {
		t.Fatalf("Expire = %d, %v", n, err)
	}
	if got := f.state(stale); got != StateExpired {
		t.Errorf("state = %s, want expired", got)
	}
	if f.fileExists(key) {
		t.Error("staged file of an expired changeset survived")
	}
	if err := f.eng.Stage(f.ctx, stale, f.entityItem("", "late", "x")); err == nil {
		t.Error("staged into an expired changeset")
	}
	if n := f.entities(); n != 1 {
		t.Errorf("entities = %d, want 1", n)
	}
}

func TestChangesetRerunNoDuplicate(t *testing.T) {
	f := newFixture(t)
	for _, content := range []string{"one", "two"} {
		cs := f.begin("")
		f.stage(cs, f.entityItem("same-key-"+f.tag, "rerun-"+f.tag, content))
		f.stage(cs, Item{Kind: KindTerm, Op: OpUpsert, SourceKey: "t-" + f.tag, Payload: raw(map[string]any{"kind": "tag", "name": content, "slug": f.tag + "-rerun"})})
		f.publish(cs)
	}
	if n := f.entities(); n != 1 {
		t.Errorf("entities = %d, want 1", n)
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_terms WHERE slug LIKE $1`, f.tag+"-rerun%"); n != 1 {
		t.Errorf("terms = %d, want 1", n)
	}
	var content string
	_ = f.db.Pool().QueryRow(f.ctx, `SELECT attributes->>'content' FROM hatamoto_entities WHERE schema_id = $1`, f.schema.ID).Scan(&content)
	if content != "two" {
		t.Errorf("content = %q, want the second run's", content)
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_changeset_source_keys WHERE source_key LIKE $1`, "%"+f.tag); n != 2 {
		t.Errorf("source keys = %d, want 2", n)
	}
}

// AttachStagedTx inserts a library row with the op's origin and no run, and
// the library listing leaves rows that still belong to a run out.
func TestMediaAttachStagedAndLibraryFilter(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	run := uuid.New()
	key := f.stageFile(run, "lib.png")

	tx, err := f.db.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m, err := f.media.AttachStagedTx(ctx, tx, media.MediaOp{StagedKey: key, Filename: "lib.png", Origin: "plugin:" + f.tag})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var origin string
	var runID *uuid.UUID
	if err := f.db.Pool().QueryRow(ctx, `SELECT origin, run_id FROM hatamoto_media WHERE id = $1`, m.ID).Scan(&origin, &runID); err != nil {
		t.Fatal(err)
	}
	if origin != "plugin:"+f.tag || runID != nil {
		t.Fatalf("origin = %q, run_id = %v", origin, runID)
	}
	if !f.fileExists(m.StoragePath) || !f.fileExists(key) {
		t.Fatal("attach must copy the file and leave the staged one for DeleteStaged")
	}

	// A row that still belongs to a run (a copy of the attached one).
	if _, err := f.db.Pool().Exec(ctx, `
		INSERT INTO hatamoto_media (id, filename, original_filename, mime_type, file_size, storage_provider, storage_path, folder, run_id)
		SELECT gen_random_uuid(), 'staged-' || filename, original_filename, mime_type, file_size, storage_provider, 'staged-' || storage_path, folder, $2
		FROM hatamoto_media WHERE id = $1`, m.ID, run); err != nil {
		t.Fatal(err)
	}
	list, err := f.media.List(ctx, nil, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	var sawAttached bool
	for _, it := range list.Items {
		if strings.HasPrefix(it.Filename, "staged-") {
			t.Errorf("library lists the run's row %s", it.Filename)
		}
		sawAttached = sawAttached || it.ID == m.ID
	}
	if !sawAttached {
		t.Error("library does not list the attached media")
	}
	var total int
	if err := f.db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_media WHERE run_id IS NULL`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if list.Total != total {
		t.Errorf("library total = %d, want %d rows with no run", list.Total, total)
	}

	if err := f.media.DeleteStaged(ctx, []string{key}); err != nil {
		t.Fatal(err)
	}
	if f.fileExists(key) {
		t.Error("DeleteStaged left the file")
	}
	_, _ = f.db.Pool().Exec(ctx, `DELETE FROM hatamoto_media WHERE original_filename = 'lib.png' AND (id = $1 OR run_id = $2)`, m.ID, run)
}

func TestChangesetRevertCreateWithChildrenIsConflict(t *testing.T) {
	f := newFixture(t)
	cs := f.begin("")
	f.stage(cs, f.entityItem("p-"+f.tag, "par-"+f.tag, "x"))
	f.publish(cs)
	var parent uuid.UUID
	if err := f.db.Pool().QueryRow(f.ctx, `SELECT id FROM hatamoto_entities WHERE schema_id = $1`, f.schema.ID).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	var kid uuid.UUID
	if err := f.db.Pool().QueryRow(f.ctx, `
		INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, parent_id)
		VALUES ($1, 'K', 'kid-' || $3::text, 'draft', '{"content":"x"}', $2) RETURNING id`, f.schema.ID, parent, f.tag).Scan(&kid); err != nil {
		t.Fatal(err)
	}
	res, err := f.eng.Revert(f.ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || len(res.Conflicts) != 1 || res.Conflicts[0].TargetID != parent.String() {
		t.Fatalf("result = %+v", res)
	}
	var kidParent *uuid.UUID
	f.db.Pool().QueryRow(f.ctx, `SELECT parent_id FROM hatamoto_entities WHERE id = $1`, kid).Scan(&kidParent)
	if f.entities() != 2 || kidParent == nil || *kidParent != parent {
		t.Errorf("parent removed or child re-rooted (entities=%d, parent=%v)", f.entities(), kidParent)
	}
}

func TestChangesetSlugChangeRunsPathObserver(t *testing.T) {
	f := newFixture(t)
	type move struct{ from, to string }
	var moves []move
	f.ent.SetPathObserver(func(_ context.Context, before, after *entity.Entity, _ *uuid.UUID) {
		moves = append(moves, move{before.Slug, after.Slug})
	})
	first := f.begin("")
	pub := f.entityItem("e-"+f.tag, "old-"+f.tag, "x")
	pub.Payload = raw(map[string]any{"schema": f.schema.Slug, "name": "N", "slug": "old-" + f.tag, "status": "published", "attributes": map[string]any{"content": "x"}})
	f.stage(first, pub)
	f.publish(first)
	if len(moves) != 0 {
		t.Fatalf("a create moved something: %v", moves)
	}
	var id string
	if err := f.db.Pool().QueryRow(f.ctx, `SELECT id FROM hatamoto_entities WHERE schema_id = $1`, f.schema.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	second := f.begin("")
	f.stage(second, Item{Kind: KindEntity, Op: OpUpsert, TargetID: id, Payload: raw(map[string]any{"slug": "new-" + f.tag})})
	f.publish(second)
	if len(moves) != 1 || moves[0] != (move{"old-" + f.tag, "new-" + f.tag}) {
		t.Fatalf("after publish: moves = %v", moves)
	}
	if _, err := f.eng.Revert(f.ctx, second); err != nil {
		t.Fatal(err)
	}
	if len(moves) != 2 || moves[1] != (move{"new-" + f.tag, "old-" + f.tag}) {
		t.Fatalf("after revert: moves = %v", moves)
	}
}

func TestChangesetRevertSkipsObjectsEditedSincePublish(t *testing.T) {
	f := newFixture(t)
	cs := f.begin("")
	f.stage(cs, f.entityItem("e-"+f.tag, "mv-"+f.tag, "x"))
	f.stage(cs, Item{Kind: KindTerm, Op: OpUpsert, SourceKey: "t-" + f.tag, Payload: raw(map[string]any{"kind": "tag", "name": "T", "slug": f.tag + "-mv"})})
	f.stage(cs, Item{Kind: KindMenu, Op: OpUpsert, SourceKey: "m-" + f.tag, Payload: raw(map[string]any{"name": "M", "slug": f.tag + "-mv"})})
	f.stage(cs, Item{Kind: KindRedirect, Op: OpUpsert, SourceKey: "r-" + f.tag, Payload: raw(map[string]any{"source_path": "/" + f.tag + "-mv", "target": "/x"})})
	f.stage(cs, Item{Kind: KindRedirect, Op: OpUpsert, SourceKey: "r2-" + f.tag, Payload: raw(map[string]any{"source_path": "/" + f.tag + "-keep", "target": "/x"})})
	f.publish(cs)
	// The owner edits four of the five objects after the publish.
	for _, q := range []string{
		`UPDATE hatamoto_entities SET name = 'Owner edit' WHERE schema_id = $1`,
	} {
		if _, err := f.db.Pool().Exec(f.ctx, q, f.schema.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`UPDATE hatamoto_terms SET name = 'Owner edit', updated_at = now() WHERE slug = $1`,
		`UPDATE hatamoto_menus SET name = 'Owner edit' WHERE slug = $1`,
		`UPDATE hatamoto_redirects SET target_url = '/owner' WHERE source_path = '/' || $1`,
	} {
		if _, err := f.db.Pool().Exec(f.ctx, q, f.tag+"-mv"); err != nil {
			t.Fatal(err)
		}
	}
	res, err := f.eng.Revert(f.ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 4 || res.Applied != 1 || len(res.Conflicts) != 4 {
		t.Fatalf("result = %+v", res)
	}
	if f.entities() != 1 ||
		f.count(`SELECT count(*) FROM hatamoto_terms WHERE slug = $1 AND name = 'Owner edit'`, f.tag+"-mv") != 1 ||
		f.count(`SELECT count(*) FROM hatamoto_menus WHERE slug = $1 AND name = 'Owner edit'`, f.tag+"-mv") != 1 ||
		f.count(`SELECT count(*) FROM hatamoto_redirects WHERE source_path = '/' || $1 AND target_url = '/owner'`, f.tag+"-mv") != 1 {
		t.Error("an edited object was overwritten or deleted by the revert")
	}
	if f.count(`SELECT count(*) FROM hatamoto_redirects WHERE source_path = '/' || $1`, f.tag+"-keep") != 0 {
		t.Error("the untouched redirect survived the revert")
	}
}

func TestChangesetStageRefusesForeignBlobKeys(t *testing.T) {
	f := newFixture(t)
	runA := uuid.New()
	a, err := f.eng.Begin(f.ctx, runA, "")
	if err != nil {
		t.Fatal(err)
	}
	foreign := f.stageFile(uuid.New(), "a.png")
	media := func(keys ...string) Item {
		return Item{Kind: KindMedia, Op: OpUpsert, BlobKeys: keys, Payload: raw(map[string]any{"filename": "a.png"})}
	}
	if err := f.eng.Stage(f.ctx, a, media(foreign)); !errors.Is(err, ErrInvalidItem) {
		t.Errorf("another run's file: %v", err)
	}
	own := f.stageFile(runA, "own.png")
	if err := f.eng.Stage(f.ctx, a, media(strings.Replace(own, runA.String()+"/", runA.String()+"/../"+uuid.NewString()+"/", 1))); !errors.Is(err, ErrInvalidItem) {
		t.Errorf("traversal key: %v", err)
	}
	ent := f.entityItem("", "blob-"+f.tag, "x")
	ent.BlobKeys = []string{own}
	if err := f.eng.Stage(f.ctx, a, ent); !errors.Is(err, ErrInvalidItem) {
		t.Errorf("files on an entity item: %v", err)
	}
	if err := f.eng.Stage(f.ctx, a, media(own)); err != nil {
		t.Errorf("the run's own file: %v", err)
	}
	if !f.fileExists(foreign) {
		t.Error("the foreign file is gone")
	}
}

// Two changesets that touched the same objects revert newest first with no
// false conflict, and leave the state from before both.
func TestChangesetRevertReverseOrder(t *testing.T) {
	f := newFixture(t)
	base := f.begin("")
	f.stage(base, f.entityItem("e-"+f.tag, "ro-"+f.tag, "v0"))
	f.stage(base, Item{Kind: KindTerm, Op: OpUpsert, SourceKey: "t-" + f.tag, Payload: raw(map[string]any{"kind": "tag", "name": "N0", "slug": f.tag + "-ro"})})
	f.stage(base, Item{Kind: KindRedirect, Op: OpUpsert, SourceKey: "r-" + f.tag, Payload: raw(map[string]any{"source_path": "/" + f.tag + "-ro", "target": "/v0"})})
	f.publish(base)
	// Stable ids; the base changeset is the owner's from here on.
	var eid, tid, rid string
	for q, dst := range map[string]*string{
		`SELECT id FROM hatamoto_entities WHERE schema_id = $1`: &eid, `SELECT id FROM hatamoto_terms WHERE slug = $1`: &tid,
		`SELECT id FROM hatamoto_redirects WHERE source_path = $1`: &rid,
	} {
		var arg any = f.tag + "-ro"
		if dst == &eid {
			arg = f.schema.ID
		} else if dst == &rid {
			arg = "/" + f.tag + "-ro"
		}
		if err := f.db.Pool().QueryRow(f.ctx, q, arg).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	round := func(n string) int64 {
		cs := f.begin("")
		e := f.entityItem("", "", n)
		e.TargetID = eid
		e.Payload = raw(map[string]any{"attributes": map[string]any{"content": n}})
		f.stage(cs, e)
		f.stage(cs, Item{Kind: KindTerm, Op: OpUpsert, TargetID: tid, Payload: raw(map[string]any{"name": "N" + n, "slug": f.tag + "-ro"})})
		f.stage(cs, Item{Kind: KindRedirect, Op: OpUpsert, TargetID: rid, Payload: raw(map[string]any{"source_path": "/" + f.tag + "-ro", "target": "/" + n})})
		if res := f.publish(cs); res.Applied != 3 {
			t.Fatalf("publish %s: %+v", n, res)
		}
		return cs
	}
	a, b := round("a"), round("b")
	for _, cs := range []int64{b, a} {
		res, err := f.eng.Revert(f.ctx, cs)
		if err != nil {
			t.Fatal(err)
		}
		if res.Skipped != 0 || res.Applied != 3 || len(res.Conflicts) != 0 {
			t.Fatalf("revert of %d = %+v", cs, res)
		}
		if got := f.state(cs); got != StateReverted {
			t.Errorf("state of %d = %s", cs, got)
		}
	}
	var content, tname, target string
	_ = f.db.Pool().QueryRow(f.ctx, `SELECT attributes->>'content' FROM hatamoto_entities WHERE id = $1`, eid).Scan(&content)
	_ = f.db.Pool().QueryRow(f.ctx, `SELECT name FROM hatamoto_terms WHERE id = $1`, tid).Scan(&tname)
	_ = f.db.Pool().QueryRow(f.ctx, `SELECT target_url FROM hatamoto_redirects WHERE id = $1`, rid).Scan(&target)
	if content != "v0" || tname != "N0" || target != "/v0" {
		t.Fatalf("original state not back: content=%q term=%q redirect=%q", content, tname, target)
	}
}

// A revert with a conflict keeps the changeset published; once the conflict is
// gone a second revert finishes it and does not touch what the first restored.
func TestChangesetRevertWithConflictCanRetry(t *testing.T) {
	f := newFixture(t)
	cs := f.begin("")
	f.stage(cs, f.entityItem("e-"+f.tag, "rt-"+f.tag, "x"))
	f.stage(cs, Item{Kind: KindTerm, Op: OpUpsert, SourceKey: "t-" + f.tag, Payload: raw(map[string]any{"kind": "tag", "name": "T", "slug": f.tag + "-rt"})})
	f.publish(cs)
	var afterTok string
	if err := f.db.Pool().QueryRow(f.ctx, `SELECT after_token FROM hatamoto_changeset_items WHERE changeset_id = $1 AND kind = 'term'`, cs).Scan(&afterTok); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool().Exec(f.ctx, `UPDATE hatamoto_terms SET name = 'Owner', updated_at = now() WHERE slug = $1`, f.tag+"-rt"); err != nil {
		t.Fatal(err)
	}
	res, err := f.eng.Revert(f.ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 || res.Skipped != 1 {
		t.Fatalf("first revert = %+v", res)
	}
	if got := f.state(cs); got != StatePublished {
		t.Fatalf("state after a conflicted revert = %s, want published", got)
	}
	if f.entities() != 0 {
		t.Fatal("the entity was not restored")
	}
	// The owner's edit is undone (the token is back); the entity item is not retried.
	at, err := time.Parse(time.RFC3339Nano, afterTok)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool().Exec(f.ctx, `UPDATE hatamoto_terms SET updated_at = $2 WHERE slug = $1`, f.tag+"-rt", at); err != nil {
		t.Fatal(err)
	}
	res, err = f.eng.Revert(f.ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 || res.Skipped != 0 {
		t.Fatalf("second revert = %+v", res)
	}
	if got := f.state(cs); got != StateReverted {
		t.Errorf("state = %s, want reverted", got)
	}
	if f.count(`SELECT count(*) FROM hatamoto_terms WHERE slug = $1`, f.tag+"-rt") != 0 {
		t.Error("term survived the second revert")
	}
}

// An edit of a menu's items moves the menu: a revert after it leaves the menu
// alone instead of replacing the owner's items.
func TestChangesetRevertSkipsMenuAfterItemEdit(t *testing.T) {
	url := "/a"
	edits := map[string]func(f *fixture, svc *menu.Service, menuID uuid.UUID, itemID uuid.UUID) error{
		"create": func(f *fixture, svc *menu.Service, m, _ uuid.UUID) error {
			u := "/c"
			_, err := svc.CreateMenuItem(f.ctx, m, &menu.CreateMenuItemInput{Label: "Contact", LinkType: menu.LinkTypeCustom, URL: &u})
			return err
		},
		"update": func(f *fixture, svc *menu.Service, _, i uuid.UUID) error {
			l := "Renamed"
			_, err := svc.UpdateMenuItem(f.ctx, i, &menu.UpdateMenuItemInput{Label: &l})
			return err
		},
		"delete": func(f *fixture, svc *menu.Service, _, i uuid.UUID) error { return svc.DeleteMenuItem(f.ctx, i) },
		"reorder": func(f *fixture, svc *menu.Service, m, i uuid.UUID) error {
			return svc.ReorderItems(f.ctx, m, []menu.ReorderInput{{ID: i, Position: 7}})
		},
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			svc := menu.NewService(menu.NewPostgresRepository(f.db), slog.Default())
			cs := f.begin("")
			f.stage(cs, Item{Kind: KindMenu, Op: OpUpsert, SourceKey: "m-" + f.tag, Payload: raw(map[string]any{
				"name": "M", "slug": f.tag + "-mi", "items": []menu.ReplaceItem{{Label: "A", LinkType: menu.LinkTypeCustom, URL: &url, Parent: -1}}})})
			f.publish(cs)
			var mid, iid uuid.UUID
			if err := f.db.Pool().QueryRow(f.ctx, `SELECT id FROM hatamoto_menus WHERE slug = $1`, f.tag+"-mi").Scan(&mid); err != nil {
				t.Fatal(err)
			}
			if err := f.db.Pool().QueryRow(f.ctx, `SELECT id FROM hatamoto_menu_items WHERE menu_id = $1`, mid).Scan(&iid); err != nil {
				t.Fatal(err)
			}
			time.Sleep(10 * time.Millisecond)
			if err := edit(f, svc, mid, iid); err != nil {
				t.Fatal(err)
			}
			res, err := f.eng.Revert(f.ctx, cs)
			if err != nil {
				t.Fatal(err)
			}
			if res.Skipped != 1 || res.Applied != 0 || len(res.Conflicts) != 1 || res.Conflicts[0].Reason != reasonRevertMoved {
				t.Fatalf("result = %+v", res)
			}
			if f.count(`SELECT count(*) FROM hatamoto_menus WHERE id = $1`, mid) != 1 {
				t.Error("the edited menu was deleted by the revert")
			}
		})
	}
}

type reindexLog struct {
	mu  sync.Mutex
	ids map[uuid.UUID]int
}

func (l *reindexLog) Reindex(_ context.Context, id uuid.UUID) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ids[id]++
	return nil
}
func (l *reindexLog) RewriteForEntity(context.Context, uuid.UUID, string, string, *uuid.UUID) error {
	return nil
}
func (l *reindexLog) ResolveCanonical(*entity.Entity) (string, error) { return "/x", nil }
func (l *reindexLog) n(id string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ids[uuid.MustParse(id)]
}

// Only an entity whose slug or parent changed gets the path step, and every
// entity is reindexed once per publish or revert, not once per hook.
func TestChangesetPathStepOnlyForMovedEntities(t *testing.T) {
	f := newFixture(t)
	idx := &reindexLog{ids: map[uuid.UUID]int{}}
	f.ent.SetLinkSync(idx)
	var moved []string
	f.ent.SetPathObserver(func(_ context.Context, before, _ *entity.Entity, _ *uuid.UUID) { moved = append(moved, before.Slug) })
	first := f.begin("")
	for _, s := range []string{"a", "b", "c"} {
		it := f.entityItem("e"+s+"-"+f.tag, s+"-"+f.tag, "x")
		it.Payload = raw(map[string]any{"schema": f.schema.Slug, "name": s, "slug": s + "-" + f.tag, "status": "published", "attributes": map[string]any{"content": "x"}})
		f.stage(first, it)
	}
	f.publish(first)
	ids := map[string]string{}
	for _, s := range []string{"a", "b", "c"} {
		var id string
		if err := f.db.Pool().QueryRow(f.ctx, `SELECT id FROM hatamoto_entities WHERE slug = $1`, s+"-"+f.tag).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[s] = id
	}
	waitFor := func(id string, want int) {
		t.Helper()
		for i := 0; i < 300 && idx.n(id) < want; i++ {
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(100 * time.Millisecond) // room for an unwanted extra reindex
		if got := idx.n(id); got != want {
			t.Fatalf("entity %s reindexed %d times, want %d", id, got, want)
		}
	}
	second := f.begin("")
	for _, s := range []string{"a", "b"} {
		f.stage(second, Item{Kind: KindEntity, Op: OpUpsert, TargetID: ids[s], Payload: raw(map[string]any{"attributes": map[string]any{"content": "y"}})})
	}
	f.stage(second, Item{Kind: KindEntity, Op: OpUpsert, TargetID: ids["c"], Payload: raw(map[string]any{"slug": "moved-" + f.tag})})
	f.publish(second)
	if len(moved) != 1 || moved[0] != "c-"+f.tag {
		t.Fatalf("path step ran for %v, want only c", moved)
	}
	waitFor(ids["a"], 2)
	waitFor(ids["c"], 2)
	if _, err := f.eng.Revert(f.ctx, second); err != nil {
		t.Fatal(err)
	}
	if len(moved) != 2 || moved[1] != "moved-"+f.tag {
		t.Fatalf("path step after revert ran for %v", moved)
	}
	waitFor(ids["a"], 3)
	waitFor(ids["c"], 3) // not 4: the revert's path step does not reindex again
}

// A plugin redirect may not take the home page or a wildcard source, and a
// revert does not bring back a deleted one over a page that has taken its path.
func TestChangesetRedirectSourceRules(t *testing.T) {
	f := newFixture(t)
	live := map[string]bool{}
	f.eng.Writers[KindRedirect] = RedirectWriter{Svc: redirect.NewService(f.db, slog.Default()),
		SourceLive: func(_ context.Context, p string) (bool, error) { return live[p], nil }}

	cs := f.begin("acme")
	for i, src := range []string{"/", "/" + f.tag + "-wild/*"} {
		f.stage(cs, Item{Kind: KindRedirect, Op: OpUpsert, SourceKey: "r" + string(rune('0'+i)) + f.tag, Payload: raw(map[string]any{"source_path": src, "target": "/x"})})
	}
	res := f.publish(cs)
	if res.Applied != 0 || res.Skipped != 2 {
		t.Fatalf("home page and wildcard sources: %+v", res)
	}

	src := "/" + f.tag + "-gone"
	create := f.begin("acme")
	f.stage(create, Item{Kind: KindRedirect, Op: OpUpsert, SourceKey: "g-" + f.tag, Payload: raw(map[string]any{"source_path": src, "target": "/x"})})
	f.publish(create)
	var id string
	if err := f.db.Pool().QueryRow(f.ctx, `SELECT id FROM hatamoto_redirects WHERE source_path = $1`, src).Scan(&id); err != nil {
		t.Fatal(err)
	}
	del := f.begin("acme")
	f.stage(del, Item{Kind: KindRedirect, Op: OpDelete, TargetID: id})
	f.publish(del)
	live[src] = true // the owner publishes a page there
	res, err := f.eng.Revert(f.ctx, del)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 0 || res.Skipped != 1 || f.count(`SELECT count(*) FROM hatamoto_redirects WHERE source_path = $1`, src) != 0 {
		t.Fatalf("revert put a redirect over a live page: %+v", res)
	}
	if got := f.state(del); got != StatePublished {
		t.Errorf("state = %s", got)
	}
}
