//go:build integration

package pluginhost

import (
	"context"
	"encoding/json/jsontext"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
)

// writeDBRig is the entity service over Postgres plus the API wired to it, the
// way the server wires it; plugins' manifests are built by the test.
type writeDBRig struct {
	t      *testing.T
	db     *database.DB
	svc    *entity.Service
	api    *API
	tag    string
	grants *GrantStore
	manif  map[string]sdk.Manifest
}

func newWriteDBRig(t *testing.T) *writeDBRig {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := database.New(context.Background(), config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	r := &writeDBRig{t: t, db: db, grants: NewGrantStore(nil), manif: map[string]sdk.Manifest{},
		tag: strings.ReplaceAll(uuid.NewString(), "-", "")[:8]}
	r.svc = entity.NewService(entity.NewPostgresSchemaRepository(db), entity.NewPostgresEntityRepository(db),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.api = NewAPI(Services{Entities: r.svc, EntityWrites: r.svc}, r.grants,
		func(p string) (sdk.Manifest, bool) { m, ok := r.manif[p]; return m, ok }, nil)
	return r
}

// schema inserts a content type owned by plugin ("" = owner-made, origin_plugin NULL).
func (r *writeDBRig) schema(prefix, plugin string) (id uuid.UUID, slug string) {
	r.t.Helper()
	id, slug = uuid.New(), prefix+"-"+r.tag
	var origin *string
	if plugin != "" {
		origin = &plugin
	}
	if _, err := r.db.Pool().Exec(context.Background(),
		`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema, settings, origin_plugin) VALUES ($1, $2, $2, $3, '{}', $4)`,
		id, slug, `{"type":"object","properties":{"url":{"type":"string"},"text":{"type":"string"}}}`, origin); err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() {
		c := context.Background()
		_, _ = r.db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, id)
		_, _ = r.db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, id)
	})
	return id, slug
}

// plugin grants entities.write.own to name, which owns the schema slug.
func (r *writeDBRig) plugin(name, slug string) {
	r.t.Helper()
	if err := r.grants.Set(context.Background(), name, []sdk.Permission{sdk.PermEntitiesWriteOwn}); err != nil {
		r.t.Fatal(err)
	}
	r.manif[name] = sdk.Manifest{Name: name, SDK: "^0.27", Permissions: []string{sdk.PermEntitiesWriteOwn},
		EntitySchema: slug, EntityFields: []string{"url", "text"}}
}

func (r *writeDBRig) call(plugin, fn string, args any) sdk.CallResponse {
	r.t.Helper()
	raw, _ := jsonx.MarshalPinned(args)
	in, _ := jsonx.MarshalPinned(sdk.CallRequest{Fn: fn, Args: raw})
	var out sdk.CallResponse
	if err := jsonx.UnmarshalPinned(r.api.Call(context.Background(), plugin, in), &out); err != nil {
		r.t.Fatal(err)
	}
	return out
}

func (r *writeDBRig) count(ids ...uuid.UUID) (n int) {
	r.t.Helper()
	if err := r.db.Pool().QueryRow(context.Background(), `SELECT count(*) FROM hatamoto_entities WHERE schema_id = ANY($1)`, ids).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *writeDBRig) row(id string) (name, status, origin string, found bool) {
	r.t.Helper()
	err := r.db.Pool().QueryRow(context.Background(), `SELECT name, status, origin FROM hatamoto_entities WHERE id = $1`, id).Scan(&name, &status, &origin)
	return name, status, origin, err == nil
}

func TestEntitiesWriteOwn_Integration(t *testing.T) {
	r := newWriteDBRig(t)
	schemaID, slug := r.schema("story", "capa")
	_, otherSlug := r.schema("story-b", "other")
	r.plugin("capa", slug)

	created := r.call("capa", sdk.FnEntitiesCreate, map[string]any{"name": "First", "slug": "1001", "attributes": map[string]any{"url": "https://example.test/a", "text": "hi"}})
	if !created.OK {
		t.Fatalf("create: %+v", created.Error)
	}
	var dto struct {
		ID, Status, Slug string
		SchemaSlug       string `json:"schema"`
	}
	if err := jsonx.UnmarshalPinned(created.Result, &dto); err != nil {
		t.Fatal(err)
	}
	if name, status, origin, ok := r.row(dto.ID); !ok || name != "First" || status != "published" || origin != "plugin:capa" {
		t.Fatalf("created row = %q %q %q %v", name, status, origin, ok)
	}

	up := r.call("capa", sdk.FnEntitiesUpdate, map[string]any{"id": dto.ID, "status": "draft", "attributes": map[string]any{"text": "changed"}})
	if !up.OK {
		t.Fatalf("update: %+v", up.Error)
	}
	var attrs map[string]string
	if err := r.db.Pool().QueryRow(context.Background(), `SELECT attributes FROM hatamoto_entities WHERE id = $1`, dto.ID).Scan(&attrs); err != nil {
		t.Fatal(err)
	}
	if name, status, origin, _ := r.row(dto.ID); name != "First" || status != "draft" || origin != "plugin:capa" || attrs["text"] != "changed" || attrs["url"] != "https://example.test/a" {
		t.Fatalf("updated row = %q %q %q %v", name, status, origin, attrs)
	}

	// The owner's own entry in the same schema, and another plugin's entry.
	ownerRow, err := r.svc.CreateEntity(context.Background(), nil, &entity.CreateEntityInput{SchemaID: schemaID, Name: "Owner", Attributes: jsontext.Value(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	r.plugin("other", otherSlug)
	otherRow := r.call("other", sdk.FnEntitiesCreate, map[string]any{"name": "Theirs"})
	if !otherRow.OK {
		t.Fatalf("other create: %+v", otherRow.Error)
	}
	var otherDTO struct{ ID string }
	_ = jsonx.UnmarshalPinned(otherRow.Result, &otherDTO)
	for name, id := range map[string]string{"owner's entry": ownerRow.ID.String(), "another plugin's entry": otherDTO.ID} {
		for _, c := range []struct {
			fn   string
			args map[string]any
		}{
			{sdk.FnEntitiesUpdate, map[string]any{"id": id, "name": "hijacked"}},
			{sdk.FnEntitiesDelete, map[string]any{"id": id}},
		} {
			if o := r.call("capa", c.fn, c.args); o.OK || o.Error.Code != sdk.ErrNotOwned {
				t.Fatalf("%s %s = %+v, want not_owned", c.fn, name, o)
			}
		}
		if n, _, _, ok := r.row(id); !ok || n == "hijacked" {
			t.Fatalf("%s changed (%q, found %v)", name, n, ok)
		}
	}
	if o := r.call("capa", sdk.FnEntitiesUpdate, map[string]any{"id": uuid.NewString(), "name": "x"}); o.OK || o.Error.Code != sdk.ErrNotFound {
		t.Fatalf("unknown id = %+v", o)
	}

	if o := r.call("capa", sdk.FnEntitiesDelete, map[string]any{"id": dto.ID}); !o.OK {
		t.Fatalf("delete: %+v", o.Error)
	}
	if _, _, _, ok := r.row(dto.ID); ok {
		t.Fatal("deleted row still there")
	}
}

// A plugin that was never installed (no hatamoto_packages row, loaded from the
// plugin directory) names a content type it does not own: nothing is written.
func TestEntitiesWriteOwnNeverInstalled_Integration(t *testing.T) {
	r := newWriteDBRig(t)
	ownedID, owned := r.schema("story", "capa")
	freeID, free := r.schema("free", "")
	for name, slug := range map[string]string{"owned by another plugin": owned, "owner-made (origin_plugin NULL)": free} {
		r.plugin("devp", slug)
		if o := r.call("devp", sdk.FnEntitiesCreate, map[string]any{"name": "x", "attributes": map[string]any{"url": "u"}}); o.OK || o.Error.Code != sdk.ErrNotOwned {
			t.Fatalf("create in a type %s = %+v, want not_owned", name, o)
		}
	}
	if n := r.count(ownedID, freeID); n != 0 {
		t.Fatalf("%d rows written", n)
	}
}
