//go:build integration

package entity

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/internal/auditctx"
	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/database"
)

type refusingSink struct{ calls int }

func (s *refusingSink) Record(context.Context, pgx.Tx, audit.Event) error {
	s.calls++
	return errors.New("refused")
}

type auditTxFixture struct {
	t        *testing.T
	db       *database.DB
	svc      *Service
	schemaID uuid.UUID
	ids      []uuid.UUID
	ctx      context.Context
}

func newAuditTxFixture(t *testing.T) *auditTxFixture {
	t.Helper()
	_, schemaID, _ := localeFixture(t, `{}`)
	db := newIntegrationDB(t)
	repo := NewPostgresEntityRepository(db)
	repo.SetAuditTx(auditlog.TxHook(slog.Default()))
	f := &auditTxFixture{
		t: t, db: db, schemaID: schemaID,
		svc: NewService(NewPostgresSchemaRepository(db), repo, testLogger()),
		ctx: auditctx.With(context.Background(), auditctx.Actor{Name: "api-key:it", Kind: "api_key", KeyID: uuid.NewString(), Via: "api"}),
	}
	auditstate.Reset()
	t.Cleanup(auditstate.Reset)
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_admin_audit WHERE target = ANY($1)`, uuidStrings(f.ids))
	})
	return f
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

func (f *auditTxFixture) create(ctx context.Context, slug string) (*Entity, error) {
	e, err := f.svc.CreateEntity(ctx, nil, &CreateEntityInput{
		SchemaID: f.schemaID, Name: slug, Slug: slug, Locale: "en", Attributes: jsontext.Value(`{}`),
	})
	if e != nil {
		f.ids = append(f.ids, e.ID)
	}
	return e, err
}

func (f *auditTxFixture) mustCreate(slug string) *Entity {
	f.t.Helper()
	e, err := f.create(context.Background(), slug) // no actor: unaudited setup
	if err != nil {
		f.t.Fatal(err)
	}
	return e
}

func (f *auditTxFixture) count(q string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.db.Pool().QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *auditTxFixture) rows(id uuid.UUID) int {
	return f.count(`SELECT count(*) FROM hatamoto_admin_audit WHERE target = $1`, id.String())
}

func TestEntityWriteAuditedInTx(t *testing.T) {
	f := newAuditTxFixture(t)
	slug := "aud-" + uuid.NewString()[:8]
	e, err := f.create(f.ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	name := "renamed"
	if _, err := f.svc.UpdateEntity(f.ctx, e.ID, nil, &UpdateEntityInput{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.UpdateEntityStatus(f.ctx, e.ID, nil, StatusPublished); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ScheduleEntity(f.ctx, e.ID, nil, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.DeleteEntity(f.ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"entity.create", "entity.update", "entity.publish", "entity.schedule", "entity.delete"} {
		n := f.count(`SELECT count(*) FROM hatamoto_admin_audit
			WHERE target = $1 AND action = $2 AND actor_kind = 'api_key' AND actor_key_id IS NOT NULL AND result = 'ok'`, e.ID.String(), action)
		if n != 1 {
			t.Errorf("%s rows = %d, want 1", action, n)
		}
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_admin_audit WHERE target = $1 AND action = 'entity.delete' AND detail->>'slug' = $2 AND detail->>'via' = 'api'`, e.ID.String(), slug); n != 1 {
		t.Errorf("delete row lacks slug/via detail")
	}
}

func TestEntityWriteActorDetailMerged(t *testing.T) {
	f := newAuditTxFixture(t)
	ctx := auditctx.With(context.Background(), auditctx.Actor{
		Name: "admin@example.com", Kind: "user", Detail: map[string]any{"schema": "page", "bulk": true},
	})
	e, err := f.create(ctx, "aud-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_admin_audit WHERE target = $1 AND detail->>'schema' = 'page'
		AND detail->>'bulk' = 'true' AND NOT (detail ? 'via')`, e.ID.String()); n != 1 {
		t.Fatalf("merged detail rows = %d, want 1", n)
	}
}

func TestEntityWriteRefusedBySinkRollsBack(t *testing.T) {
	f := newAuditTxFixture(t)
	pre := f.mustCreate("aud-" + uuid.NewString()[:8])
	auditstate.Register(&refusingSink{})

	slug := "aud-" + uuid.NewString()[:8]
	if _, err := f.create(f.ctx, slug); !errors.Is(err, auditstate.ErrSink) {
		t.Fatalf("create err = %v, want ErrSink", err)
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_entities WHERE slug = $1`, slug); n != 0 {
		t.Fatalf("entities with slug = %d, want 0", n)
	}

	name := "changed"
	steps := map[string]func() error{
		"update": func() error {
			_, err := f.svc.UpdateEntity(f.ctx, pre.ID, nil, &UpdateEntityInput{Name: &name})
			return err
		},
		"status": func() error { _, err := f.svc.UpdateEntityStatus(f.ctx, pre.ID, nil, StatusPublished); return err },
		"schedule": func() error {
			_, err := f.svc.ScheduleEntity(f.ctx, pre.ID, nil, time.Now().Add(time.Hour))
			return err
		},
		"delete": func() error { return f.svc.DeleteEntity(f.ctx, pre.ID) },
	}
	for step, run := range steps {
		if err := run(); !errors.Is(err, auditstate.ErrSink) {
			t.Fatalf("%s err = %v, want ErrSink", step, err)
		}
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_entities WHERE id = $1 AND name = $2 AND status = 'draft' AND published_at IS NULL`, pre.ID, pre.Name); n != 1 {
		t.Fatalf("entity changed or gone after refused writes")
	}
	if n := f.rows(pre.ID); n != 0 {
		t.Fatalf("audit rows = %d, want 0", n)
	}
}

func TestEntityAutosaveNotAudited(t *testing.T) {
	f := newAuditTxFixture(t)
	pre := f.mustCreate("aud-" + uuid.NewString()[:8])
	sink := &refusingSink{}
	auditstate.Register(sink)

	name := "typed"
	if _, err := f.svc.UpdateEntity(f.ctx, pre.ID, nil, &UpdateEntityInput{Name: &name, Autosave: true}); err != nil {
		t.Fatalf("autosave: %v", err)
	}
	if n := f.rows(pre.ID); n != 0 {
		t.Fatalf("audit rows = %d, want 0", n)
	}
	if sink.calls != 0 {
		t.Fatalf("sink called %d times", sink.calls)
	}
}

func TestEntityWriteWithoutActorNotAudited(t *testing.T) {
	f := newAuditTxFixture(t)
	sink := &refusingSink{}
	auditstate.Register(sink)

	e, err := f.create(context.Background(), "aud-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.UpdateEntityStatus(context.Background(), e.ID, nil, StatusPublished); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.DeleteEntity(context.Background(), e.ID); err != nil {
		t.Fatal(err)
	}
	if n := f.rows(e.ID); n != 0 || sink.calls != 0 {
		t.Fatalf("rows = %d, sink calls = %d, want 0", n, sink.calls)
	}
}

func TestEntityDefaultRowFailureDoesNotBlock(t *testing.T) {
	f := newAuditTxFixture(t)
	ctx := auditctx.With(context.Background(), auditctx.Actor{Name: "x\x00", Kind: "api_key", Via: "api"})
	e, err := f.create(ctx, "aud-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatalf("create with a failing default row: %v", err)
	}
	if err := f.svc.DeleteEntity(ctx, e.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestEntityWriteDeleteErrorsOnAuditedPath(t *testing.T) {
	f := newAuditTxFixture(t)
	parent := f.mustCreate("aud-" + uuid.NewString()[:8])
	child, err := f.svc.CreateEntity(context.Background(), nil, &CreateEntityInput{
		SchemaID: f.schemaID, Name: "kid", Slug: "kid-" + uuid.NewString()[:8], Locale: "en", ParentID: &parent.ID, Attributes: jsontext.Value(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	f.ids = append(f.ids, child.ID)
	if err := f.svc.DeleteEntity(f.ctx, parent.ID); !errors.Is(err, ErrHasChildren) {
		t.Fatalf("delete parent = %v, want ErrHasChildren", err)
	}
	if err := f.svc.DeleteEntity(f.ctx, uuid.New()); !errors.Is(err, ErrEntityNotFound) {
		t.Fatalf("delete unknown = %v, want ErrEntityNotFound", err)
	}
}
