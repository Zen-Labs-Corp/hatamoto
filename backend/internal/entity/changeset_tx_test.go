//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/database"
)

type csFixture struct {
	t      *testing.T
	ctx    context.Context
	db     *database.DB
	svc    *Service
	repo   *PostgresEntityRepository
	schema *EntitySchema
	rec    *csRecorder
}

type csRecorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *csRecorder) Emit(_ context.Context, ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *csRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func newCSFixture(t *testing.T) *csFixture { return newCSFixtureSettings(t, nil) }

// newCSFixtureSettings is newCSFixture with the schema's settings JSON set.
func newCSFixtureSettings(t *testing.T, settings json.RawMessage) *csFixture {
	t.Helper()
	ctx := context.Background()
	db := newIntegrationDB(t)
	schemas := NewPostgresSchemaRepository(db)
	repo := NewPostgresEntityRepository(db)
	svc := NewService(schemas, repo, testLogger())
	rec := &csRecorder{}
	svc.SetEvents(rec)
	sc, err := schemas.Create(ctx, &CreateSchemaInput{
		Name: "cs " + uuid.NewString()[:8], Slug: "cs-" + uuid.NewString()[:8],
		Schema:   json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"}},"required":["content"]}`),
		Settings: settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	f := &csFixture{t: t, ctx: ctx, db: db, svc: svc, repo: repo, schema: sc, rec: rec}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, sc.ID)
		_ = schemas.Delete(ctx, sc.ID)
	})
	return f
}

// inTx runs fn in a transaction that commits when fn returns nil.
func (f *csFixture) inTx(fn func(tx pgx.Tx) error) error {
	f.t.Helper()
	tx, err := f.db.Pool().Begin(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(f.ctx)
		return err
	}
	return tx.Commit(f.ctx)
}

func (f *csFixture) apply(op TxOp) (TxResult, error) {
	f.t.Helper()
	if op.Schema == "" {
		op.Schema = f.schema.Slug
	}
	var res TxResult
	err := f.inTx(func(tx pgx.Tx) (err error) {
		res, err = f.svc.ApplyChangesetTx(f.ctx, tx, op)
		return err
	})
	return res, err
}

const dirty = `{"content":"<p>hi</p><script>alert(1)</script>"}`

func TestChangesetTxParityWithCreateEntity(t *testing.T) {
	f := newCSFixture(t)
	normal, err := f.svc.CreateEntity(f.ctx, nil, &CreateEntityInput{SchemaID: f.schema.ID, Name: "A", Slug: "parity-a", Attributes: json.RawMessage(dirty)})
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.apply(TxOp{Name: "A", Slug: "parity-b", Attributes: json.RawMessage(dirty)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.repo.GetByID(f.ctx, res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonRawEqual(got.Attributes, normal.Attributes) || strings.Contains(string(got.Attributes), "script") {
		t.Errorf("attrs = %s, CreateEntity stored %s", got.Attributes, normal.Attributes)
	}
	if got.Status != normal.Status || got.Version != normal.Version || got.Locale != normal.Locale {
		t.Errorf("row = %s/%d/%s, CreateEntity = %s/%d/%s", got.Status, got.Version, got.Locale, normal.Status, normal.Version, normal.Locale)
	}
	if !res.Created {
		t.Error("Created not set")
	}

	// The same invalid input is refused by both with the same error class.
	bad := json.RawMessage(`{"nope":1}`)
	_, e1 := f.svc.CreateEntity(f.ctx, nil, &CreateEntityInput{SchemaID: f.schema.ID, Name: "B", Slug: "parity-c", Attributes: bad})
	_, e2 := f.apply(TxOp{Name: "B", Slug: "parity-d", Attributes: bad})
	if e1 == nil || e2 == nil || !errors.Is(e1, ErrValidationFailed) || !errors.Is(e2, ErrValidationFailed) {
		t.Errorf("validation errors differ: normal %v, changeset %v", e1, e2)
	}
}

func TestChangesetTxLocaleNeutral(t *testing.T) {
	for _, c := range []struct {
		name, settings, locale, want string // want "" = site language; "!" = error
		single                       bool   // the site has one language
	}{
		{"flagged omitted", `{"locale_neutral":true}`, "", "und", false},
		{"flagged omitted single-locale site", `{"locale_neutral":true}`, "", "", true},
		{"flagged explicit", `{"locale_neutral":true}`, "en-AU", "en-AU", false},
		{"unflagged omitted", `{}`, "", "", false},
		{"flagged explicit und refused", `{"locale_neutral":true}`, "und", "!", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newCSFixtureSettings(t, json.RawMessage(c.settings))
			if !c.single {
				useLocales(t, f.svc, "en", "fr")
			}
			res, err := f.apply(TxOp{Name: "L", Slug: "loc", Locale: c.locale, Attributes: json.RawMessage(`{"content":"x"}`)})
			if c.want == "!" {
				if err == nil {
					t.Fatal("explicit und accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := c.want
			if want == "" {
				if err := f.db.Pool().QueryRow(f.ctx, `SELECT public.hatamoto_site_language()`).Scan(&want); err != nil {
					t.Fatal(err)
				}
			}
			got, err := f.repo.GetByID(f.ctx, res.ID)
			if err != nil || got.Locale != want {
				t.Fatalf("locale = %q, %v; want %q", got.Locale, err, want)
			}
		})
	}
}

func TestChangesetTxParityWithCreateEntityLocaleNeutral(t *testing.T) {
	f := newCSFixtureSettings(t, json.RawMessage(`{"locale_neutral":true}`))
	useLocales(t, f.svc, "en", "fr")
	attrs := json.RawMessage(`{"content":"x"}`)
	normal, err := f.svc.CreateEntity(f.ctx, nil, &CreateEntityInput{SchemaID: f.schema.ID, Name: "A", Slug: "parity-a", Attributes: attrs})
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.apply(TxOp{Name: "A", Slug: "parity-b", Attributes: attrs})
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.repo.GetByID(f.ctx, res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Locale != normal.Locale || got.Locale != LocaleShared {
		t.Errorf("changeset locale %q, CreateEntity %q; want both %q", got.Locale, normal.Locale, LocaleShared)
	}
}

func TestChangesetTxOriginAndSlugSuffix(t *testing.T) {
	f := newCSFixture(t)
	draft, err := f.svc.CreateEntity(f.ctx, nil, &CreateEntityInput{SchemaID: f.schema.ID, Name: "T", Slug: "taken", Attributes: json.RawMessage(`{"content":"x"}`)})
	if err != nil || draft.Status != StatusDraft {
		t.Fatalf("draft: %v %v", draft, err)
	}
	r1, err := f.apply(TxOp{Name: "T", Slug: "taken", Origin: "import-x", Attributes: json.RawMessage(`{"content":"y"}`)})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := f.apply(TxOp{Name: "T", Slug: "taken", Attributes: json.RawMessage(`{"content":"z"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Slug != "taken-2" || r2.Slug != "taken-3" {
		t.Errorf("slugs = %q, %q; want taken-2, taken-3", r1.Slug, r2.Slug)
	}
	var origin, origin2 string
	f.db.Pool().QueryRow(f.ctx, `SELECT origin FROM hatamoto_entities WHERE id = $1`, r1.ID).Scan(&origin)
	f.db.Pool().QueryRow(f.ctx, `SELECT origin FROM hatamoto_entities WHERE id = $1`, r2.ID).Scan(&origin2)
	if origin != "import-x" || origin2 != "owner" {
		t.Errorf("origins = %q, %q", origin, origin2)
	}
}

func TestChangesetTxSlugSuffixStaysWithinColumn(t *testing.T) {
	f := newCSFixture(t)
	long := strings.Repeat("a", 255)
	attrs := json.RawMessage(`{"content":"x"}`)
	r1, err := f.apply(TxOp{Name: "L", Slug: long, Attributes: attrs})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := f.apply(TxOp{Name: "L", Slug: long, Attributes: attrs})
	if err != nil {
		t.Fatal(err)
	}
	if len(r1.Slug) != 255 || len(r2.Slug) != 255 || r1.Slug == r2.Slug || !strings.HasSuffix(r2.Slug, "-2") {
		t.Errorf("slug lengths %d, %d; second %q", len(r1.Slug), len(r2.Slug), r2.Slug[len(r2.Slug)-5:])
	}
}

func TestChangesetTxConflict(t *testing.T) {
	f := newCSFixture(t)
	e, err := f.svc.CreateEntity(f.ctx, nil, &CreateEntityInput{SchemaID: f.schema.ID, Name: "C", Slug: "conf", Attributes: json.RawMessage(`{"content":"x"}`)})
	if err != nil {
		t.Fatal(err)
	}
	attrs := json.RawMessage(`{"content":"changeset"}`)

	// A matching token applies.
	v, ts := e.Version, e.UpdatedAt
	if _, err := f.apply(TxOp{ID: &e.ID, Attributes: attrs, BaseVersion: &v, BaseUpdatedAt: &ts}); err != nil {
		t.Fatalf("matching token: %v", err)
	}
	// The row moved (version 2 now): the stale token is a conflict and writes nothing.
	res, err := f.apply(TxOp{ID: &e.ID, Attributes: json.RawMessage(`{"content":"stale"}`), BaseVersion: &v})
	if !errors.Is(err, ErrConflict) || !res.Conflict {
		t.Fatalf("stale version: res %+v err %v", res, err)
	}
	// A rename moves updated_at without a version bump: that conflicts too.
	cur, _ := f.repo.GetByID(f.ctx, e.ID)
	name := "renamed"
	if _, err := f.svc.UpdateEntity(f.ctx, e.ID, nil, &UpdateEntityInput{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.apply(TxOp{ID: &e.ID, Name: "X", BaseUpdatedAt: &cur.UpdatedAt}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale updated_at: %v", err)
	}
	got, _ := f.repo.GetByID(f.ctx, e.ID)
	if got.Name != "renamed" || !strings.Contains(string(got.Attributes), "changeset") {
		t.Errorf("conflicting ops wrote: %s / %s", got.Name, got.Attributes)
	}
}

func TestChangesetTxDeleteWithChildrenFailsAndTxSurvives(t *testing.T) {
	f := newCSFixture(t)
	attrs := json.RawMessage(`{"content":"x"}`)
	parent, err := f.apply(TxOp{Name: "P", Slug: "par", Attributes: attrs})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.apply(TxOp{Name: "K", Slug: "kid", Attributes: attrs, ParentID: &parent.ID}); err != nil {
		t.Fatal(err)
	}
	// One tx: the failed delete rolls back only its savepoint; the next item still applies.
	err = f.inTx(func(tx pgx.Tx) error {
		if _, err := f.svc.ApplyChangesetTx(f.ctx, tx, TxOp{ID: &parent.ID, Delete: true}); !errors.Is(err, ErrHasChildren) {
			t.Errorf("delete with children: %v", err)
		}
		_, err := f.svc.ApplyChangesetTx(f.ctx, tx, TxOp{Schema: f.schema.Slug, Name: "N", Slug: "next", Attributes: attrs})
		return err
	})
	if err != nil {
		t.Fatalf("tx unusable after a failed item: %v", err)
	}
}

func TestChangesetTxRestore(t *testing.T) {
	f := newCSFixture(t)
	e, err := f.svc.CreateEntity(f.ctx, nil, &CreateEntityInput{SchemaID: f.schema.ID, Name: "R", Slug: "rest", Attributes: json.RawMessage(`{"content":"one"}`)})
	if err != nil {
		t.Fatal(err)
	}
	// Update, then restore.
	up, err := f.apply(TxOp{ID: &e.ID, Name: "R2", Attributes: json.RawMessage(`{"content":"two"}`), Origin: "import-x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.inTx(func(tx pgx.Tx) error { return f.svc.RestoreChangesetTx(f.ctx, tx, e.ID, up.Before) }); err != nil {
		t.Fatal(err)
	}
	got, _ := f.repo.GetByID(f.ctx, e.ID)
	if got.Name != "R" || !jsonRawEqual(got.Attributes, e.Attributes) || got.Version <= e.Version+1 {
		t.Errorf("after restore: %s %s v%d", got.Name, got.Attributes, got.Version)
	}
	// The version never rewinds: a normal save after the revert must work.
	three, four := json.RawMessage(`{"content":"three"}`), json.RawMessage(`{"content":"four"}`)
	if _, err := f.svc.UpdateEntity(f.ctx, e.ID, nil, &UpdateEntityInput{Attributes: &three}); err != nil {
		t.Fatalf("save after restore: %v", err)
	}
	if _, err := f.apply(TxOp{ID: &e.ID, Attributes: four}); err != nil {
		t.Fatalf("changeset update after restore: %v", err)
	}
	if _, err := f.svc.UpdateEntity(f.ctx, e.ID, nil, &UpdateEntityInput{Attributes: &three}); err != nil {
		t.Fatal(err)
	}
	var origin string
	f.db.Pool().QueryRow(f.ctx, `SELECT origin FROM hatamoto_entities WHERE id = $1`, e.ID).Scan(&origin)
	if origin != "owner" {
		t.Errorf("origin = %q after restore", origin)
	}

	// Delete, then restore re-creates the same id.
	del, err := f.apply(TxOp{ID: &e.ID, Delete: true})
	if err != nil || !del.Deleted {
		t.Fatalf("delete: %+v %v", del, err)
	}
	if _, err := f.repo.GetByID(f.ctx, e.ID); !errors.Is(err, ErrEntityNotFound) {
		t.Fatalf("still there: %v", err)
	}
	if err := f.inTx(func(tx pgx.Tx) error { return f.svc.RestoreChangesetTx(f.ctx, tx, e.ID, del.Before) }); err != nil {
		t.Fatal(err)
	}
	if got, err = f.repo.GetByID(f.ctx, e.ID); err != nil || got.Slug != "rest" || got.Name != "R" {
		t.Fatalf("re-created: %+v %v", got, err)
	}

	// A created row is reverted by an empty pre-image.
	cr, err := f.apply(TxOp{Name: "C", Slug: "created", Attributes: json.RawMessage(`{"content":"x"}`)})
	if err != nil || cr.Before != nil {
		t.Fatalf("create: %+v %v", cr, err)
	}
	if err := f.inTx(func(tx pgx.Tx) error { return f.svc.RestoreChangesetTx(f.ctx, tx, cr.ID, cr.Before) }); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.GetByID(f.ctx, cr.ID); !errors.Is(err, ErrEntityNotFound) {
		t.Errorf("created row survived revert: %v", err)
	}
}

func TestChangesetTxRollbackLeavesNothing(t *testing.T) {
	f := newCSFixture(t)
	attrs := json.RawMessage(`{"content":"x"}`)
	boom := errors.New("boom")
	err := f.inTx(func(tx pgx.Tx) error {
		if _, err := f.svc.ApplyChangesetTx(f.ctx, tx, TxOp{Schema: f.schema.Slug, Name: "A", Slug: "gone", Attributes: attrs}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	var n int
	f.db.Pool().QueryRow(f.ctx, `SELECT count(*) FROM hatamoto_entities WHERE schema_id = $1`, f.schema.ID).Scan(&n)
	if n != 0 {
		t.Errorf("%d rows survived the rollback", n)
	}
}

func TestChangesetTxNoEventBeforeAfterChangeset(t *testing.T) {
	f := newCSFixture(t)
	var lived []uuid.UUID
	f.svc.SetLiveObserver(func(_ context.Context, ids []uuid.UUID) { lived = append(lived, ids...) })
	created, err := f.apply(TxOp{Name: "E", Slug: "ev", Attributes: json.RawMessage(`{"content":"x"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if f.rec.count() != 0 || len(lived) != 0 {
		t.Fatalf("side effects before AfterChangeset: %d events, %d live", f.rec.count(), len(lived))
	}
	f.svc.AfterChangeset(f.ctx, []uuid.UUID{created.ID}, nil, nil, nil)
	if f.rec.count() != 1 || f.rec.events[0].Kind != EventImportCompleted || f.rec.events[0].Import.Source != ChangesetSource {
		t.Errorf("events = %+v", f.rec.events)
	}
	if len(lived) != 1 || lived[0] != created.ID {
		t.Errorf("live = %v", lived)
	}
}

func TestChangesetTxRestoreDeleteKeepsLinks(t *testing.T) {
	f := newCSFixture(t)
	e, err := f.svc.CreateEntity(f.ctx, nil, &CreateEntityInput{SchemaID: f.schema.ID, Name: "L", Slug: "links", Attributes: json.RawMessage(`{"content":"x"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var termID, group uuid.UUID
	slug := "t-" + uuid.NewString()[:8]
	if err := f.db.Pool().QueryRow(f.ctx, `INSERT INTO hatamoto_terms (kind, slug, name) VALUES ('tag', $1, 'T') RETURNING id`, slug).Scan(&termID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.db.Pool().Exec(f.ctx, `DELETE FROM hatamoto_terms WHERE id = $1`, termID) })
	group = uuid.New()
	if _, err := f.db.Pool().Exec(f.ctx, `INSERT INTO hatamoto_entity_terms (entity_id, term_id) VALUES ($1, $2)`, e.ID, termID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool().Exec(f.ctx, `UPDATE hatamoto_entities SET translation_group = $2, translation_original = true WHERE id = $1`, e.ID, group); err != nil {
		t.Fatal(err)
	}
	del, err := f.apply(TxOp{ID: &e.ID, Delete: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.inTx(func(tx pgx.Tx) error { return f.svc.RestoreChangesetTx(f.ctx, tx, e.ID, del.Before) }); err != nil {
		t.Fatal(err)
	}
	var linked int
	var gotGroup *uuid.UUID
	var original bool
	f.db.Pool().QueryRow(f.ctx, `SELECT count(*) FROM hatamoto_entity_terms WHERE entity_id = $1 AND term_id = $2`, e.ID, termID).Scan(&linked)
	f.db.Pool().QueryRow(f.ctx, `SELECT translation_group, translation_original FROM hatamoto_entities WHERE id = $1`, e.ID).Scan(&gotGroup, &original)
	if linked != 1 || gotGroup == nil || *gotGroup != group || !original {
		t.Errorf("after restore: term links %d, translation group %v original %v", linked, gotGroup, original)
	}
}

func TestChangesetTxRestoreCreateRefusesWithChildren(t *testing.T) {
	f := newCSFixture(t)
	cr, err := f.apply(TxOp{Schema: f.schema.Slug, Name: "P", Slug: "parent", Attributes: json.RawMessage(`{"content":"x"}`)})
	if err != nil {
		t.Fatal(err)
	}
	child, err := f.svc.CreateEntity(f.ctx, nil, &CreateEntityInput{SchemaID: f.schema.ID, Name: "K", Slug: "kid", ParentID: &cr.ID, Attributes: json.RawMessage(`{"content":"x"}`)})
	if err != nil {
		t.Fatal(err)
	}
	err = f.inTx(func(tx pgx.Tx) error { return f.svc.RestoreChangesetTx(f.ctx, tx, cr.ID, nil) })
	if !errors.Is(err, ErrHasChildren) {
		t.Fatalf("restore = %v, want ErrHasChildren", err)
	}
	got, err := f.repo.GetByID(f.ctx, child.ID)
	if err != nil || got.ParentID == nil || *got.ParentID != cr.ID {
		t.Errorf("child re-rooted: %+v %v", got, err)
	}
}

func TestChangesetTxRestoreDeleteLeavesOriginalMarkWithTheOtherMember(t *testing.T) {
	f := newCSFixture(t)
	mk := func(slug, locale string) *Entity {
		e, err := f.svc.CreateEntity(f.ctx, nil, &CreateEntityInput{SchemaID: f.schema.ID, Name: slug, Slug: slug, Locale: locale, Attributes: json.RawMessage(`{"content":"x"}`)})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	orig, other := mk("tr-orig", "en"), mk("tr-other", "fr")
	group := uuid.New()
	if _, err := f.db.Pool().Exec(f.ctx, `UPDATE hatamoto_entities SET translation_group = $1, translation_original = (id = $2) WHERE id IN ($2, $3)`, group, orig.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	del, err := f.apply(TxOp{ID: &orig.ID, Delete: true})
	if err != nil {
		t.Fatal(err)
	}
	// The owner names the other member the original, then the delete is reverted.
	if _, err := f.db.Pool().Exec(f.ctx, `UPDATE hatamoto_entities SET translation_original = true WHERE id = $1`, other.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.inTx(func(tx pgx.Tx) error { return f.svc.RestoreChangesetTx(f.ctx, tx, orig.ID, del.Before) }); err != nil {
		t.Fatalf("restore: %v", err)
	}
	var n int
	f.db.Pool().QueryRow(f.ctx, `SELECT count(*) FROM hatamoto_entities WHERE translation_group = $1 AND translation_original`, group).Scan(&n)
	if n != 1 {
		t.Errorf("%d originals in the group, want the owner's one", n)
	}
}
