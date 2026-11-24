//go:build integration

package entity

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/jsonx"
)

const pluginStorySchema = `{"type":"object","properties":{"url":{"type":"string"},"text":{"type":"string"}}}`

type pluginWriteRig struct {
	db   *database.DB
	svc  *Service
	sink *recordingSink
}

func newPluginWriteRig(t *testing.T) *pluginWriteRig {
	t.Helper()
	db := newIntegrationDB(t)
	svc := NewService(NewPostgresSchemaRepository(db), NewPostgresEntityRepository(db), slog.New(slog.NewTextHandler(io.Discard, nil)))
	sink := &recordingSink{}
	svc.SetEvents(sink)
	return &pluginWriteRig{db: db, svc: svc, sink: sink}
}

// schema inserts a content type with the given origin_plugin ("" = NULL) and removes it with its rows.
func (r *pluginWriteRig) schema(t *testing.T, prefix, owner string) (id uuid.UUID, slug string) {
	t.Helper()
	id, slug = uuid.New(), prefix+"-"+uuid.NewString()[:8]
	var origin *string
	if owner != "" {
		origin = &owner
	}
	if _, err := r.db.Pool().Exec(context.Background(),
		`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema, settings, origin_plugin) VALUES ($1, $2, $2, $3, '{}', $4)`,
		id, slug, pluginStorySchema, origin); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = r.db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, id)
		_, _ = r.db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, id)
	})
	return id, slug
}

func (r *pluginWriteRig) count(t *testing.T, schemaIDs ...uuid.UUID) (n int) {
	t.Helper()
	if err := r.db.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM hatamoto_entities WHERE schema_id = ANY($1)`, schemaIDs).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPluginWrite_Integration(t *testing.T) {
	r := newPluginWriteRig(t)
	ctx := context.Background()
	fields := []string{"url", "text"}
	schemaID, slug := r.schema(t, "story", "fixture")
	_, otherSlug := r.schema(t, "story", "other")

	e, err := r.svc.PluginCreate(ctx, "fixture", PluginWrite{
		Schema: slug, Fields: fields, Name: "First story", Slug: "1001",
		Attributes: jsontext.Value(`{"url":"https://example.test/a","text":"hello"}`),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if e.Status != StatusPublished || e.Slug != "1001" || e.Schema == nil || e.Schema.Slug != slug {
		t.Fatalf("created %+v", e)
	}
	var origin string
	var published bool
	if err := r.db.Pool().QueryRow(ctx, `SELECT origin, published_at IS NOT NULL FROM hatamoto_entities WHERE id = $1`, e.ID).Scan(&origin, &published); err != nil {
		t.Fatal(err)
	}
	if origin != "plugin:fixture" || !published {
		t.Fatalf("origin = %q, published_at set = %v", origin, published)
	}
	if got := r.sink.kinds(); !slices.Equal(got, []EventKind{EventCreated, EventPublished}) {
		t.Fatalf("create events = %v", got)
	}

	// Update: merge, null removes, status draft, origin kept.
	up, err := r.svc.PluginUpdate(ctx, "fixture", e.ID, PluginWrite{
		Schema: slug, Fields: fields, Attributes: jsontext.Value(`{"text":"changed"}`), Status: StatusDraft,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	var attrs map[string]string
	if err := jsonx.Unmarshal(up.Attributes, &attrs); err != nil {
		t.Fatal(err)
	}
	if attrs["text"] != "changed" || attrs["url"] != "https://example.test/a" || up.Status != StatusDraft || up.Name != "First story" || up.Slug != "1001" {
		t.Fatalf("updated %+v attrs=%v", up, attrs)
	}
	if err := r.db.Pool().QueryRow(ctx, `SELECT origin FROM hatamoto_entities WHERE id = $1`, e.ID).Scan(&origin); err != nil || origin != "plugin:fixture" {
		t.Fatalf("origin after update = %q, %v", origin, err)
	}
	if got := r.sink.kinds(); !slices.Equal(got, []EventKind{EventCreated, EventPublished, EventUpdated}) {
		t.Fatalf("update events = %v", got)
	}
	up, err = r.svc.PluginUpdate(ctx, "fixture", e.ID, PluginWrite{Schema: slug, Fields: fields, Attributes: jsontext.Value(`{"url":null}`)})
	if err != nil {
		t.Fatalf("update null: %v", err)
	}
	attrs = nil
	if err := jsonx.Unmarshal(up.Attributes, &attrs); err != nil {
		t.Fatal(err)
	}
	if len(attrs) != 1 || attrs["text"] != "changed" || up.Status != StatusDraft {
		t.Fatalf("after null: %s %s", up.Attributes, up.Status)
	}
	// The plugin unpublished it itself above; publishing again is the owner's.
	if _, err := r.svc.PluginUpdate(ctx, "fixture", e.ID, PluginWrite{Schema: slug, Fields: fields, Status: StatusPublished}); !errors.Is(err, ErrPluginRepublish) {
		t.Fatalf("republish: %v", err)
	}

	// Refused: key outside the whitelist, status archived.
	if _, err := r.svc.PluginUpdate(ctx, "fixture", e.ID, PluginWrite{Schema: slug, Fields: fields, Attributes: jsontext.Value(`{"body":"x"}`)}); !errors.Is(err, ErrFieldNotWritable) || !errors.Is(err, ErrValidationFailed) {
		t.Fatalf("body key: %v", err)
	}
	if _, err := r.svc.PluginCreate(ctx, "fixture", PluginWrite{Schema: slug, Fields: fields, Name: "x", Attributes: jsontext.Value(`{"body":"x"}`)}); !errors.Is(err, ErrFieldNotWritable) {
		t.Fatalf("create body key: %v", err)
	}
	if _, err := r.svc.PluginUpdate(ctx, "fixture", e.ID, PluginWrite{Schema: slug, Fields: fields, Status: StatusArchived}); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("archived: %v", err)
	}
	if _, err := r.svc.PluginUpdate(ctx, "fixture", uuid.New(), PluginWrite{Schema: slug, Fields: fields}); !errors.Is(err, ErrEntityNotFound) {
		t.Fatalf("unknown id: %v", err)
	}

	// An owner row in the same schema and a row of another plugin's schema are not the plugin's.
	ownerRow, err := r.svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: "Owner story", Attributes: jsontext.Value(`{"text":"mine"}`)})
	if err != nil {
		t.Fatal(err)
	}
	otherRow, err := r.svc.PluginCreate(ctx, "other", PluginWrite{Schema: otherSlug, Fields: fields, Name: "Other story"})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		id     uuid.UUID
		schema string
	}{"owner row": {ownerRow.ID, slug}, "other plugin's schema": {otherRow.ID, otherSlug}, "other plugin's row via own schema": {otherRow.ID, slug}} {
		if _, err := r.svc.PluginUpdate(ctx, "fixture", c.id, PluginWrite{Schema: c.schema, Fields: fields, Name: "hijack"}); !errors.Is(err, ErrNotPluginOwned) {
			t.Fatalf("%s update: %v", name, err)
		}
		if _, err := r.svc.PluginDelete(ctx, "fixture", c.schema, c.id); !errors.Is(err, ErrNotPluginOwned) {
			t.Fatalf("%s delete: %v", name, err)
		}
	}
	for id, want := range map[uuid.UUID]string{ownerRow.ID: "Owner story", otherRow.ID: "Other story"} {
		var name string
		if err := r.db.Pool().QueryRow(ctx, `SELECT name FROM hatamoto_entities WHERE id = $1`, id).Scan(&name); err != nil || name != want {
			t.Fatalf("row changed: %q %v, want %q", name, err, want)
		}
	}
	var ownerOrigin string
	if err := r.db.Pool().QueryRow(ctx, `SELECT origin FROM hatamoto_entities WHERE id = $1`, ownerRow.ID).Scan(&ownerOrigin); err != nil || ownerOrigin != "owner" {
		t.Fatalf("owner row origin = %q %v", ownerOrigin, err)
	}

	// Delete.
	pre, err := r.svc.PluginDelete(ctx, "fixture", slug, e.ID)
	if err != nil || pre.Name != "First story" {
		t.Fatalf("delete: %v %+v", err, pre)
	}
	if got := r.sink.kinds(); got[len(got)-1] != EventDeleted {
		t.Fatalf("delete events = %v", got)
	}
	if _, err := r.svc.entityRepo.GetByID(ctx, e.ID); !errors.Is(err, ErrEntityNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if _, err := r.svc.PluginDelete(ctx, "fixture", slug, e.ID); !errors.Is(err, ErrEntityNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

// A plugin never installed (a dev directory) names a type it does not own: the
// check is made on the schema row at call time.
func TestPluginWriteNotOwnedSchema_Integration(t *testing.T) {
	r := newPluginWriteRig(t)
	ctx := context.Background()
	ownedID, owned := r.schema(t, "owned", "other")
	freeID, free := r.schema(t, "free", "")
	for _, slug := range []string{owned, free, "page", "no-such-" + uuid.NewString()[:8]} {
		_, err := r.svc.PluginCreate(ctx, "fixture", PluginWrite{Schema: slug, Fields: []string{"url"}, Name: "x"})
		if !errors.Is(err, ErrNotPluginOwned) {
			t.Fatalf("create in %q: %v, want ErrNotPluginOwned", slug, err)
		}
	}
	if n := r.count(t, ownedID, freeID); n != 0 {
		t.Fatalf("%d rows written", n)
	}
	if got := r.sink.kinds(); len(got) != 0 {
		t.Fatalf("events for refused writes: %v", got)
	}
}

// The owner moderates a plugin's entry by unpublishing or archiving it. The
// plugin may publish only an entry that was never published, and may not
// touch an archived one; the entry stays the plugin's otherwise.
func TestPluginWriteOwnerModeration_Integration(t *testing.T) {
	r := newPluginWriteRig(t)
	ctx := context.Background()
	fields := []string{"url", "text"}
	_, slug := r.schema(t, "story", "fixture")
	create := func(name string, st EntityStatus) *Entity {
		t.Helper()
		e, err := r.svc.PluginCreate(ctx, "fixture", PluginWrite{Schema: slug, Fields: fields, Name: name, Status: st})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	update := func(id uuid.UUID, st EntityStatus) error {
		_, err := r.svc.PluginUpdate(ctx, "fixture", id, PluginWrite{Schema: slug, Fields: fields, Status: st, Name: "again"})
		return err
	}

	// Owner unpublishes: published_at stays set and the plugin cannot republish.
	e := create("taken down", "")
	if _, err := r.svc.UpdateEntityStatus(ctx, e.ID, nil, StatusDraft); err != nil {
		t.Fatal(err)
	}
	if got, err := r.svc.entityRepo.GetByID(ctx, e.ID); err != nil || got.PublishedAt == nil {
		t.Fatalf("owner unpublish must keep published_at: %v %v", got, err)
	}
	if err := update(e.ID, StatusPublished); !errors.Is(err, ErrPluginRepublish) {
		t.Fatalf("republish after owner unpublish: %v, want ErrPluginRepublish", err)
	}
	if err := update(e.ID, ""); err != nil {
		t.Fatalf("plugin may still edit a draft: %v", err)
	}

	// The plugin's own unpublish is final too.
	e = create("self", "")
	if err := update(e.ID, StatusDraft); err != nil {
		t.Fatal(err)
	}
	if err := update(e.ID, StatusPublished); !errors.Is(err, ErrPluginRepublish) {
		t.Fatalf("republish after plugin unpublish: %v, want ErrPluginRepublish", err)
	}

	// A draft the plugin created may be published once.
	e = create("draft", StatusDraft)
	if err := update(e.ID, StatusPublished); err != nil {
		t.Fatalf("first publish of a draft: %v", err)
	}
	if got := r.sink.kinds(); got[len(got)-1] != EventPublished {
		t.Fatalf("draft -> published events = %v", got)
	}

	// Archived is the owner's.
	e = create("archived", "")
	if _, err := r.svc.UpdateEntityStatus(ctx, e.ID, nil, StatusArchived); err != nil {
		t.Fatal(err)
	}
	for _, st := range []EntityStatus{"", StatusDraft, StatusPublished} {
		if err := update(e.ID, st); !errors.Is(err, ErrPluginEntryClosed) {
			t.Fatalf("update of an archived entry (status %q): %v, want ErrPluginEntryClosed", st, err)
		}
	}
}

func TestPluginWriteAuditFailsClosed_Integration(t *testing.T) {
	r := newPluginWriteRig(t)
	ctx := context.Background()
	fields := []string{"url", "text"}
	schemaID, slug := r.schema(t, "story", "fixture")

	existing, err := r.svc.PluginCreate(ctx, "fixture", PluginWrite{Schema: slug, Fields: fields, Name: "Kept"})
	if err != nil {
		t.Fatal(err)
	}
	events := len(r.sink.kinds())

	var actions []string
	refuse := true
	r.svc.SetPluginAudit(func(_ context.Context, _ pgx.Tx, ev auditstate.Event) error {
		actions = append(actions, ev.Action+" "+ev.Actor)
		if refuse {
			return errors.New("sink down")
		}
		return nil
	})
	if _, err := r.svc.PluginCreate(ctx, "fixture", PluginWrite{Schema: slug, Fields: fields, Name: "New"}); err == nil {
		t.Fatal("create succeeded with a failing audit sink")
	}
	if n := r.count(t, schemaID); n != 1 {
		t.Fatalf("%d rows after a refused create, want 1", n)
	}
	if _, err := r.svc.PluginUpdate(ctx, "fixture", existing.ID, PluginWrite{Schema: slug, Fields: fields, Name: "Renamed"}); err == nil {
		t.Fatal("update succeeded with a failing audit sink")
	}
	if _, err := r.svc.PluginDelete(ctx, "fixture", slug, existing.ID); err == nil {
		t.Fatal("delete succeeded with a failing audit sink")
	}
	got, err := r.svc.entityRepo.GetByID(ctx, existing.ID)
	if err != nil || got.Name != "Kept" {
		t.Fatalf("row after refused update and delete: %+v, %v", got, err)
	}
	if n := len(r.sink.kinds()); n != events {
		t.Fatalf("%d events for refused writes", n-events)
	}
	want := []string{"plugin.entity.create plugin:fixture", "plugin.entity.update plugin:fixture", "plugin.entity.delete plugin:fixture"}
	if !slices.Equal(actions, want) {
		t.Fatalf("audit calls = %v, want %v", actions, want)
	}
}

// A plugin call cancelled before the commit stops and rolls back: nothing is
// stored, no audit row survives and no event fires. Once the commit has
// happened the call's cancellation changes nothing: the reload and every event
// still run.
func TestPluginWriteSurvivesCancel_Integration(t *testing.T) {
	r := newPluginWriteRig(t)
	fields := []string{"url", "text"}
	schemaID, slug := r.schema(t, "story", "fixture")

	// Before the commit: the cancel arrives while the transaction is open.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.svc.SetPluginAudit(func(hctx context.Context, tx pgx.Tx, ev auditstate.Event) error {
		cancel()
		if _, err := tx.Exec(hctx, `SELECT 1`); err == nil {
			t.Error("a statement on a cancelled call's transaction succeeded")
		}
		return hctx.Err()
	})
	if _, err := r.svc.PluginCreate(ctx, "fixture", PluginWrite{Schema: slug, Fields: fields, Name: "Late"}); err == nil {
		t.Fatal("create on a call cancelled before the commit succeeded")
	}
	if n := r.count(t, schemaID); n != 0 {
		t.Fatalf("entries after a cancelled create = %d, want 0", n)
	}
	if got := r.sink.kinds(); len(got) != 0 {
		t.Fatalf("events for a cancelled create: %v", got)
	}
	r.svc.SetPluginAudit(nil)

	// Cancelled before the call begins: it never starts.
	dead, stop := context.WithCancel(context.Background())
	stop()
	if _, err := r.svc.PluginCreate(dead, "fixture", PluginWrite{Schema: slug, Fields: fields, Name: "Never"}); err == nil || r.count(t, schemaID) != 0 {
		t.Fatalf("create on a cancelled ctx: err=%v, entries=%d", err, r.count(t, schemaID))
	}

	// After the commit: the first event cancels the call; the rest still arrive.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	cs := &cancellingSink{recordingSink: r.sink, cancel: cancel2}
	r.svc.SetEvents(cs)
	e, err := r.svc.PluginCreate(ctx2, "fixture", PluginWrite{Schema: slug, Fields: fields, Name: "On time"})
	if err != nil {
		t.Fatalf("create cancelled after the commit: %v", err)
	}
	if _, err := r.svc.entityRepo.GetByID(context.Background(), e.ID); err != nil {
		t.Fatalf("entry missing: %v", err)
	}
	if got := r.sink.kinds(); !slices.Equal(got, []EventKind{EventCreated, EventPublished}) {
		t.Fatalf("events = %v", got)
	}
	if cs.lateErr != nil {
		t.Fatalf("an event after the cancel got a dead context: %v", cs.lateErr)
	}
}

// cancellingSink cancels the plugin call at its first event and records whether
// a later event still had a live context.
type cancellingSink struct {
	*recordingSink
	cancel  context.CancelFunc
	n       int
	lateErr error
}

func (c *cancellingSink) Emit(ctx context.Context, ev Event) {
	c.n++
	if c.n == 1 {
		c.cancel()
	} else if err := ctx.Err(); err != nil {
		c.lateErr = err
	}
	c.recordingSink.Emit(ctx, ev)
}
