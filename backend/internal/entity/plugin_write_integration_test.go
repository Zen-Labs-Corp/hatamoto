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
	up, err = r.svc.PluginUpdate(ctx, "fixture", e.ID, PluginWrite{Schema: slug, Fields: fields, Attributes: jsontext.Value(`{"url":null}`), Status: StatusPublished})
	if err != nil {
		t.Fatalf("update null: %v", err)
	}
	attrs = nil
	if err := jsonx.Unmarshal(up.Attributes, &attrs); err != nil {
		t.Fatal(err)
	}
	if len(attrs) != 1 || attrs["text"] != "changed" || up.Status != StatusPublished {
		t.Fatalf("after null: %s %s", up.Attributes, up.Status)
	}
	if got := r.sink.kinds(); got[len(got)-1] != EventPublished {
		t.Fatalf("draft -> published events = %v", got)
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
