//go:build integration

package comment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

const ownSchemaID = "22222222-2222-2222-2222-222222222222"

var storyScope = Scope{Plugin: "example", Schema: "story"}

// ownStory makes the fixture's content type a "story" created by plugin "example".
func (f *fixture) ownStory() {
	f.t.Helper()
	f.exec(`UPDATE hatamoto_entity_schemas SET slug = 'story', origin_plugin = 'example' WHERE id = $1`, ownSchemaID)
}

// entityOn creates an entity on a new content type.
func (f *fixture) entityOn(slug string, origin *string) uuid.UUID {
	f.t.Helper()
	sid := uuid.New()
	f.exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema, origin_plugin) VALUES ($1, $2, $2, '{"type":"object"}', $3)`,
		sid, slug, origin)
	id := uuid.New()
	f.exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status) VALUES ($1, $2, $3, $3, 'published')`, id, sid, slug+"-e")
	return id
}

func (f *fixture) statusOf(id uuid.UUID) Status {
	f.t.Helper()
	var s string
	f.scalar(&s, `SELECT status FROM hatamoto_comments WHERE id = $1`, id)
	return Status(s)
}

type batchRecorder struct {
	NopObserver
	updated []Comment
	inBatch []bool
	batches [][]uuid.UUID
}

func (r *batchRecorder) OnUpdated(ctx context.Context, _, after Comment) {
	r.updated = append(r.updated, after)
	r.inBatch = append(r.inBatch, InBatch(ctx))
}
func (r *batchRecorder) OnBatch(_ context.Context, ids []uuid.UUID) {
	r.batches = append(r.batches, ids)
}

func TestMarkDeadRestoreOwnSchema(t *testing.T) {
	f := newFixture(t)
	f.enable()
	f.ownStory()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := f.seedComment(nil, at, StatusApproved)
	p := f.seedComment(nil, at.Add(time.Minute), StatusPending)
	sp := f.seedComment(nil, at.Add(2*time.Minute), StatusSpam)
	f.exec(`INSERT INTO hatamoto_comment_entities (entity_id, comment_count, pending_count) VALUES ($1, 1, 1)`, f.entity)
	f.wantCounts(1, 1, "before")
	rec := &batchRecorder{}
	f.svc.Register(rec)

	n, err := f.svc.MarkDead(f.ctx, storyScope, []uuid.UUID{a.id, p.id, sp.id, a.id})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || f.statusOf(a.id) != StatusDead || f.statusOf(p.id) != StatusDead || f.statusOf(sp.id) != StatusSpam {
		t.Fatalf("changed = %d, statuses %s %s %s", n, f.statusOf(a.id), f.statusOf(p.id), f.statusOf(sp.id))
	}
	f.wantCounts(0, 0, "after mark dead")
	var by string
	f.scalar(&by, `SELECT dead_by FROM hatamoto_comments WHERE id = $1`, a.id)
	if by != "example" {
		t.Errorf("dead_by = %q", by)
	}
	if len(rec.updated) != 2 || len(rec.batches) != 1 || len(rec.batches[0]) != 1 || rec.batches[0][0] != f.entity {
		t.Errorf("events: %d updated, batches %v", len(rec.updated), rec.batches)
	}

	rec.updated, rec.inBatch, rec.batches = nil, nil, nil
	n, err = f.svc.Restore(f.ctx, storyScope, []uuid.UUID{a.id, p.id, sp.id})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || f.statusOf(a.id) != StatusApproved || f.statusOf(p.id) != StatusApproved || f.statusOf(sp.id) != StatusSpam {
		t.Fatalf("restore changed = %d", n)
	}
	f.wantCounts(2, 0, "after restore")
	if len(rec.updated) != 2 || !rec.inBatch[0] || !rec.inBatch[1] || len(rec.batches) != 1 {
		t.Errorf("restore events: %d updated %v, %d batches", len(rec.updated), rec.inBatch, len(rec.batches))
	}
	f.scalar(&by, `SELECT COALESCE(dead_by, '') FROM hatamoto_comments WHERE id = $1`, a.id)
	if by != "" {
		t.Errorf("dead_by after restore = %q", by)
	}
	if n, _ := f.svc.Restore(f.ctx, storyScope, []uuid.UUID{a.id}); n != 0 {
		t.Errorf("restoring an approved comment changed %d", n)
	}
}

func TestMarkDeadForeignIDChangesNothing(t *testing.T) {
	f := newFixture(t)
	f.enable()
	f.ownStory()
	other := "other"
	foreignEntity := f.entityOn("note", &other)
	own := f.seedComment(nil, time.Now().UTC(), StatusApproved)
	var foreign uuid.UUID
	f.scalar(&foreign, `WITH s AS (SELECT nextval('hatamoto_comments_seq') AS v)
		INSERT INTO hatamoto_comments (seq, entity_id, path, depth, status, body_raw, body_html)
		SELECT v, $1, lpad(to_hex(v), 12, '0'), 1, 'approved', 'b', '<p>b</p>' FROM s RETURNING id`, foreignEntity)

	for name, ids := range map[string][]uuid.UUID{"foreign": {own.id, foreign}, "unknown": {own.id, uuid.New()}} {
		if _, err := f.svc.MarkDead(f.ctx, storyScope, ids); !errors.Is(err, ErrNotOwned) {
			t.Errorf("%s: MarkDead = %v, want ErrNotOwned", name, err)
		}
		if _, err := f.svc.SetRank(f.ctx, storyScope, []RankItem{{own.id, 5}, {ids[1], 5}}); !errors.Is(err, ErrNotOwned) {
			t.Errorf("%s: SetRank = %v, want ErrNotOwned", name, err)
		}
	}
	if f.statusOf(own.id) != StatusApproved || f.statusOf(foreign) != StatusApproved {
		t.Error("a refused batch changed a status")
	}
	var rank float64
	f.scalar(&rank, `SELECT rank FROM hatamoto_comments WHERE id = $1`, own.id)
	if rank != 0 {
		t.Errorf("a refused batch changed a rank: %v", rank)
	}
}

func TestMarkDeadOwnerSchemaNotOwned(t *testing.T) {
	f := newFixture(t)
	f.enable()
	sc := Scope{Plugin: "example", Schema: "article"}
	other := "other"
	for name, origin := range map[string]*string{"owner type": nil, "other plugin": &other} {
		e := f.entityOn("article", origin)
		var id uuid.UUID
		f.scalar(&id, `WITH s AS (SELECT nextval('hatamoto_comments_seq') AS v)
			INSERT INTO hatamoto_comments (seq, entity_id, path, depth, status, body_raw, body_html)
			SELECT v, $1, lpad(to_hex(v), 12, '0'), 1, 'approved', 'b', '<p>b</p>' FROM s RETURNING id`, e)
		if _, err := f.svc.MarkDead(f.ctx, sc, []uuid.UUID{id}); !errors.Is(err, ErrNotOwned) {
			t.Errorf("%s: MarkDead = %v", name, err)
		}
		if _, err := f.svc.Restore(f.ctx, sc, []uuid.UUID{id}); !errors.Is(err, ErrNotOwned) {
			t.Errorf("%s: Restore = %v", name, err)
		}
		if _, err := f.svc.SetRank(f.ctx, sc, []RankItem{{id, 1}}); !errors.Is(err, ErrNotOwned) {
			t.Errorf("%s: SetRank = %v", name, err)
		}
		if f.statusOf(id) != StatusApproved {
			t.Errorf("%s: status changed", name)
		}
		f.exec(`UPDATE hatamoto_entity_schemas SET slug = 'gone-' || id::text WHERE slug = 'article'`)
	}
}

func TestMarkDeadOwnershipReadAtCallTime(t *testing.T) {
	f := newFixture(t)
	f.enable()
	f.ownStory()
	c := f.seedComment(nil, time.Now().UTC(), StatusApproved)
	if _, err := f.svc.MarkDead(f.ctx, storyScope, []uuid.UUID{c.id}); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE hatamoto_entity_schemas SET origin_plugin = 'other' WHERE id = $1`, ownSchemaID)
	if _, err := f.svc.Restore(f.ctx, storyScope, []uuid.UUID{c.id}); !errors.Is(err, ErrNotOwned) {
		t.Fatalf("Restore after the owner changed = %v, want ErrNotOwned", err)
	}
	if f.statusOf(c.id) != StatusDead {
		t.Error("status changed after ownership moved")
	}
}

func TestSetRank(t *testing.T) {
	f := newFixture(t)
	f.enable()
	f.ownStory()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := f.seedComment(nil, at, StatusApproved)
	b := f.seedComment(nil, at.Add(time.Minute), StatusApproved)
	rec := &batchRecorder{}
	f.svc.Register(rec)

	n, err := f.svc.SetRank(f.ctx, storyScope, []RankItem{{a.id, 2.5}, {b.id, 0}, {a.id, 3.5}})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("changed = %d, want 1 (b already 0, a repeated keeps the last)", n)
	}
	var rank float64
	f.scalar(&rank, `SELECT rank FROM hatamoto_comments WHERE id = $1`, a.id)
	if rank != 3.5 {
		t.Errorf("rank = %v, want 3.5", rank)
	}
	if len(rec.updated) != 0 || len(rec.batches) != 1 {
		t.Errorf("events: %d updated, %d batches; want 0 and 1", len(rec.updated), len(rec.batches))
	}
	var touched time.Time
	f.scalar(&touched, `SELECT updated_at FROM hatamoto_comments WHERE id = $1`, a.id)
	if !touched.Equal(at) {
		t.Errorf("updated_at moved to %v", touched)
	}
	rec.batches = nil
	if n, err := f.svc.SetRank(f.ctx, storyScope, []RankItem{{a.id, 3.5}}); err != nil || n != 0 || len(rec.batches) != 0 {
		t.Errorf("unchanged rank: n=%d err=%v batches=%d", n, err, len(rec.batches))
	}
}

func TestViewerFlag(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.SetViewerFlag(f.ctx, "example", "k1", true); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetViewerFlag(f.ctx, "example", "k1", true); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetViewerFlag(f.ctx, "other", "k2", true); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.ShowDeadPlugins(f.ctx, map[string]string{"example": "k1", "other": "wrong"})
	if err != nil || len(got) != 1 || got[0] != "example" {
		t.Fatalf("ShowDeadPlugins = %v, %v", got, err)
	}
	if all, _ := f.svc.FlaggedPlugins(f.ctx); len(all) != 2 {
		t.Errorf("FlaggedPlugins = %v", all)
	}
	if err := f.svc.SetViewerFlag(f.ctx, "example", "k1", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.ShowDeadPlugins(f.ctx, map[string]string{"example": "k1"}); len(got) != 0 {
		t.Errorf("after clearing: %v", got)
	}
}
