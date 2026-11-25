//go:build integration

package packages

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

const productSchema = `{"title":"Product","description":"Things for sale","type":"object","properties":{"price":{"type":"number"}}}`

func schemaPack(name string) map[string][]byte {
	// A widget keeps the package a valid plugin without a wasm module.
	files := widgetPack(name, map[string][4]string{"banner": {wSchema, wHTML, ".hm-" + name + "_banner{color:red}", ""}})
	files["schemas/product.json"] = []byte(productSchema)
	// a schema with a section template is a builder section, not a type
	files["schemas/hero.json"] = []byte(`{"type":"object","properties":{}}`)
	files["sections/hero.html"] = []byte(`<div></div>`)
	return files
}

// schemaRig attaches a real entity service to the rig's manager.
func schemaRig(t *testing.T) (*rig, *entity.Service) {
	t.Helper()
	r := newRig(t)
	ctx := context.Background()
	db, err := database.NewWithConfig(ctx, r.pool.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	svc := entity.NewService(entity.NewPostgresSchemaRepository(db), entity.NewPostgresEntityRepository(db), slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.mgr.Schemas = svc
	return r, svc
}

func (r *rig) installPack(files map[string][]byte) error {
	r.t.Helper()
	raw, err := PackFiles(files, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	_, err = r.mgr.Install(context.Background(), raw, InstallRequest{Source: SourceDev})
	return err
}

func (r *rig) typeOwner(slug string) (owner string, found bool) {
	r.t.Helper()
	err := r.pool.QueryRow(context.Background(), `SELECT COALESCE(origin_plugin, '') FROM hatamoto_entity_schemas WHERE slug = $1`, slug).Scan(&owner)
	return owner, err == nil
}

func TestPluginContentTypesInstallOnEnable(t *testing.T) {
	r, _ := schemaRig(t)
	ctx := context.Background()
	if err := r.installPack(schemaPack("shop")); err != nil {
		t.Fatal(err)
	}
	if owner, ok := r.typeOwner("product"); !ok || owner != "shop" {
		t.Fatalf("product type: owner=%q found=%v", owner, ok)
	}
	if _, ok := r.typeOwner("hero"); ok {
		t.Fatal("a schema with a section template became a content type")
	}

	// Disabling keeps the type; enabling again does not overwrite an edited one.
	if err := r.mgr.SetEnabled(ctx, "shop", false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE hatamoto_entity_schemas SET name = 'Goods' WHERE slug = 'product'`); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.SetEnabled(ctx, "shop", true); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := r.pool.QueryRow(ctx, `SELECT name FROM hatamoto_entity_schemas WHERE slug = 'product'`).Scan(&name); err != nil || name != "Goods" {
		t.Fatalf("name = %q, %v (an existing type must not be overwritten)", name, err)
	}
	if n, _ := auditWidgets(t, r, "package.schemas.install", "plugin:shop"); n != 1 {
		t.Fatalf("package.schemas.install audited %d times, want 1", n)
	}
}

func TestPluginContentTypeNameTakenRefuses(t *testing.T) {
	r, svc := schemaRig(t)
	ctx := context.Background()
	if _, err := svc.CreateSchema(ctx, &entity.CreateSchemaInput{Name: "Mine", Slug: "product", Schema: []byte(productSchema)}); err != nil {
		t.Fatal(err)
	}
	err := r.installPack(schemaPack("shop"))
	if !errors.Is(err, ErrContentTypeTaken) {
		t.Fatalf("err = %v, want ErrContentTypeTaken", err)
	}
	if _, err := r.mgr.store.Get(ctx, KindPlugin, "shop"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a refused install was stored: %v", err)
	}
	if owner, _ := r.typeOwner("product"); owner != "" {
		t.Fatalf("the owner's type was claimed by %q", owner)
	}
}

type recordSink struct{ deleted []entity.EventKind }

func (r *recordSink) Emit(_ context.Context, ev entity.Event) { r.deleted = append(r.deleted, ev.Kind) }

func TestUninstallContentTypes(t *testing.T) {
	for _, drop := range []bool{false, true} {
		name := map[bool]string{false: "keep", true: "remove"}[drop]
		t.Run(name, func(t *testing.T) {
			r, svc := schemaRig(t)
			ctx := context.Background()
			if err := r.installPack(schemaPack("shop")); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.CreateSchema(ctx, &entity.CreateSchemaInput{Name: "Mine", Slug: "mine", Schema: []byte(productSchema)}); err != nil {
				t.Fatal(err)
			}
			for _, slug := range []string{"product", "mine"} {
				sc, err := svc.GetSchemaBySlug(ctx, slug)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := svc.CreateEntity(ctx, nil, &entity.CreateEntityInput{SchemaID: sc.ID, Name: "x " + slug, Attributes: []byte(`{}`)}); err != nil {
					t.Fatal(err)
				}
			}
			sink := &recordSink{}
			svc.SetEvents(sink)
			if err := r.mgr.Uninstall(ctx, KindPlugin, "shop", UninstallOpts{DropContentTypes: drop}); err != nil {
				t.Fatal(err)
			}
			if got := slices.Contains(sink.deleted, entity.EventDeleted); got != drop {
				t.Fatalf("drop=%v: entity.deleted emitted = %v (subscribers must hear about removed entries)", drop, got)
			}
			_, found := r.typeOwner("product")
			var entities int
			_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities WHERE name = 'x product'`).Scan(&entities)
			if found == drop || (entities == 0) != drop {
				t.Fatalf("drop=%v: type found=%v, entities=%d", drop, found, entities)
			}
			if _, ok := r.typeOwner("mine"); !ok {
				t.Fatal("an owner-created type was removed")
			}
			var dropped bool
			if err := r.pool.QueryRow(ctx, `SELECT (detail->>'content_types_dropped')::bool FROM hatamoto_admin_audit WHERE action = 'package.uninstall' AND target = 'plugin:shop'`).Scan(&dropped); err != nil || dropped != drop {
				t.Fatalf("audit content_types_dropped = %v, %v", dropped, err)
			}
		})
	}
}

// A content type the owner has taken since never keeps the plugin disabled.
func TestEnableSurvivesContentTypeConflict(t *testing.T) {
	r, svc := schemaRig(t)
	ctx := context.Background()
	if err := r.installPack(schemaPack("shop")); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.SetEnabled(ctx, "shop", false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE slug = 'product'`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateSchema(ctx, &entity.CreateSchemaInput{Name: "Mine", Slug: "product", Schema: []byte(productSchema)}); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.SetEnabled(ctx, "shop", true); err != nil {
		t.Fatalf("enable failed on a content type conflict: %v", err)
	}
	if owner, _ := r.typeOwner("product"); owner != "" {
		t.Fatalf("the owner's type was claimed by %q", owner)
	}
}

// Uninstalling and keeping the content hands the plugin's entries to the owner:
// a later package of the same name can neither edit nor delete them.
func TestUninstallKeepContentRevokesPluginEntries(t *testing.T) {
	r, svc := schemaRig(t)
	ctx := context.Background()
	if err := r.installPack(schemaPack("shop")); err != nil {
		t.Fatal(err)
	}
	w := entity.PluginWrite{Schema: "product", Fields: []string{"price"}, Name: "Kept", Attributes: []byte(`{"price":1}`)}
	e, err := svc.PluginCreate(ctx, "shop", w)
	if err != nil {
		t.Fatal(err)
	}
	// An entry a changeset put into an owner-made type is not the plugin's own
	// surface and stays as it is.
	var otherSchema, imported string
	if err := r.pool.QueryRow(ctx, `INSERT INTO hatamoto_entity_schemas (name, slug, schema, settings) VALUES ('Other', 'other-'||substr(gen_random_uuid()::text,1,8), '{"type":"object"}', '{}') RETURNING id, slug`).Scan(&otherSchema, new(string)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM hatamoto_entities WHERE schema_id = $1`, otherSchema)
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, otherSchema)
	})
	if err := r.pool.QueryRow(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, origin) VALUES ($1, 'Imported', 'imported', 'published', '{}', 'plugin:shop') RETURNING id`, otherSchema).Scan(&imported); err != nil {
		t.Fatal(err)
	}
	// Pin updated_at to the past, as an import does (hatamoto.import skips the trigger).
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('hatamoto.import', 'on', true)`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE hatamoto_entities SET updated_at = $1 WHERE id = ANY($2::uuid[])`, old, []string{e.ID.String(), imported})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.Uninstall(ctx, KindPlugin, "shop", UninstallOpts{}); err != nil {
		t.Fatal(err)
	}
	var updated time.Time
	if err := r.pool.QueryRow(ctx, `SELECT updated_at FROM hatamoto_entities WHERE id = $1`, e.ID).Scan(&updated); err != nil || !updated.Equal(old) {
		t.Fatalf("updated_at after uninstall = %v, %v, want %v untouched", updated, err, old)
	}
	var importedOrigin string
	if err := r.pool.QueryRow(ctx, `SELECT origin FROM hatamoto_entities WHERE id = $1`, imported).Scan(&importedOrigin); err != nil || importedOrigin != "plugin:shop" {
		t.Fatalf("changeset entry in another type: origin %q, %v, want plugin:shop", importedOrigin, err)
	}
	var origin string
	if err := r.pool.QueryRow(ctx, `SELECT origin FROM hatamoto_entities WHERE id = $1`, e.ID).Scan(&origin); err != nil || origin != "owner" {
		t.Fatalf("origin after uninstall = %q, %v, want owner", origin, err)
	}
	if err := r.installPack(schemaPack("shop")); err != nil {
		t.Fatalf("reinstall under the same name: %v", err)
	}
	w.Name = "Taken over"
	if _, err := svc.PluginUpdate(ctx, "shop", e.ID, w); !errors.Is(err, entity.ErrNotPluginOwned) {
		t.Fatalf("update by a package reusing the name: %v, want ErrNotPluginOwned", err)
	}
	if _, err := svc.PluginDelete(ctx, "shop", "product", e.ID); !errors.Is(err, entity.ErrNotPluginOwned) {
		t.Fatalf("delete by a package reusing the name: %v, want ErrNotPluginOwned", err)
	}
}

// packWith is schemaPack with the product schema replaced.
func packWith(name, schemaJSON string) map[string][]byte {
	files := schemaPack(name)
	files["schemas/product.json"] = []byte(schemaJSON)
	return files
}

const productWithSKU = `{"title":"Product","type":"object","properties":{"price":{"type":"number"},"sku":{"type":"string","title":"SKU"}},"required":["sku"]}`

func (r *rig) storedSchema(slug string) map[string]any {
	r.t.Helper()
	var raw []byte
	if err := r.pool.QueryRow(context.Background(), `SELECT schema FROM hatamoto_entity_schemas WHERE slug = $1`, slug).Scan(&raw); err != nil {
		r.t.Fatal(err)
	}
	var m map[string]any
	if err := jsonx.Unmarshal(raw, &m, jsonx.Stored); err != nil {
		r.t.Fatal(err)
	}
	return m
}

func (r *rig) storedProps(slug string) map[string]any {
	r.t.Helper()
	p, _ := r.storedSchema(slug)["properties"].(map[string]any)
	return p
}

func TestUpdateAddsFieldToOwnedType(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "ordered"}[ordered], func(t *testing.T) {
			r, svc := schemaRig(t)
			ctx := context.Background()
			if err := r.installPack(schemaPack("shop")); err != nil {
				t.Fatal(err)
			}
			if ordered {
				sc, err := svc.GetSchemaBySlug(ctx, "product")
				if err != nil {
					t.Fatal(err)
				}
				s := jsontext.Value(`{"title":"Product","type":"object","properties":{"price":{"type":"number"}},"x-hm-order":["price"]}`)
				if _, err := svc.UpdateSchema(ctx, sc.ID, &entity.UpdateSchemaInput{Schema: &s}); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.installPack(packWith("shop", productWithSKU)); err != nil {
				t.Fatal(err)
			}
			if _, ok := r.storedProps("product")["sku"]; !ok {
				t.Fatalf("sku not added: %v", r.storedProps("product"))
			}
			if req, _ := r.storedSchema("product")["required"].([]any); slices.Contains(req, any("sku")) {
				t.Fatalf("sku became required: %v", req)
			}
			if ordered {
				if got := r.storedSchema("product")["x-hm-order"]; !reflect.DeepEqual(got, []any{"price", "sku"}) {
					t.Fatalf("x-hm-order = %v", got)
				}
			}
			if n, _ := auditWidgets(t, r, "package.schemas.extend", "plugin:shop"); n != 1 {
				t.Fatalf("package.schemas.extend audited %d times, want 1", n)
			}
		})
	}
}

func TestUpdateKeepsRemovedAndRetypedFields(t *testing.T) {
	r, _ := schemaRig(t)
	if err := r.installPack(schemaPack("shop")); err != nil {
		t.Fatal(err)
	}
	retyped := `{"title":"Product","type":"object","properties":{"price":{"type":"string"},"sku":{"type":"string"}}}`
	if err := r.installPack(packWith("shop", retyped)); err != nil {
		t.Fatal(err)
	}
	dropped := `{"title":"Product","type":"object","properties":{"color":{"type":"string"}}}`
	if err := r.installPack(packWith("shop", dropped)); err != nil {
		t.Fatal(err)
	}
	p := r.storedProps("product")
	if !reflect.DeepEqual(p["price"], map[string]any{"type": "number"}) {
		t.Fatalf("price = %v, want the stored number type", p["price"])
	}
	for _, k := range []string{"sku", "color"} {
		if _, ok := p[k]; !ok {
			t.Fatalf("%s missing: %v", k, p)
		}
	}
}

func TestUpdateKeepsOwnerAddedField(t *testing.T) {
	r, svc := schemaRig(t)
	ctx := context.Background()
	if err := r.installPack(schemaPack("shop")); err != nil {
		t.Fatal(err)
	}
	sc, err := svc.GetSchemaBySlug(ctx, "product")
	if err != nil {
		t.Fatal(err)
	}
	s := jsontext.Value(`{"title":"Product","type":"object","properties":{"price":{"type":"number"},"note":{"type":"string"}}}`)
	if _, err := svc.UpdateSchema(ctx, sc.ID, &entity.UpdateSchemaInput{Schema: &s}); err != nil {
		t.Fatal(err)
	}
	if err := r.installPack(packWith("shop", productWithSKU)); err != nil {
		t.Fatal(err)
	}
	p := r.storedProps("product")
	for _, k := range []string{"note", "sku", "price"} {
		if _, ok := p[k]; !ok {
			t.Fatalf("%s missing: %v", k, p)
		}
	}
	after, err := svc.GetSchemaBySlug(ctx, "product")
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != sc.Name || !reflect.DeepEqual(after.Description, sc.Description) {
		t.Fatalf("name/description changed: %q %v -> %q %v", sc.Name, sc.Description, after.Name, after.Description)
	}
}

func TestUpdateLeavesOthersTypesAlone(t *testing.T) {
	t.Run("owner type", func(t *testing.T) {
		r, svc := schemaRig(t)
		if _, err := svc.CreateSchema(context.Background(), &entity.CreateSchemaInput{Name: "Mine", Slug: "product", Schema: []byte(productSchema)}); err != nil {
			t.Fatal(err)
		}
		before := r.storedSchema("product")
		if err := r.installPack(packWith("shop", productWithSKU)); !errors.Is(err, ErrContentTypeTaken) {
			t.Fatalf("err = %v, want ErrContentTypeTaken", err)
		}
		if !reflect.DeepEqual(before, r.storedSchema("product")) {
			t.Fatalf("owner type changed: %v", r.storedSchema("product"))
		}
	})
	t.Run("other plugin", func(t *testing.T) {
		r, _ := schemaRig(t)
		ctx := context.Background()
		if err := r.installPack(schemaPack("shop")); err != nil {
			t.Fatal(err)
		}
		if _, err := r.pool.Exec(ctx, `UPDATE hatamoto_entity_schemas SET origin_plugin = 'other' WHERE slug = 'product'`); err != nil {
			t.Fatal(err)
		}
		before := r.storedSchema("product")
		// the type is taken by another plugin, so the update is refused
		if err := r.installPack(packWith("shop", productWithSKU)); !errors.Is(err, ErrContentTypeTaken) {
			t.Fatalf("err = %v, want ErrContentTypeTaken", err)
		}
		if !reflect.DeepEqual(before, r.storedSchema("product")) {
			t.Fatalf("another plugin's type changed: %v", r.storedSchema("product"))
		}
	})
}

func TestReenableIsIdempotent(t *testing.T) {
	r, _ := schemaRig(t)
	ctx := context.Background()
	if err := r.installPack(schemaPack("shop")); err != nil {
		t.Fatal(err)
	}
	if err := r.installPack(packWith("shop", productWithSKU)); err != nil {
		t.Fatal(err)
	}
	updatedAt := func() (ts time.Time) {
		if err := r.pool.QueryRow(ctx, `SELECT updated_at FROM hatamoto_entity_schemas WHERE slug = 'product'`).Scan(&ts); err != nil {
			t.Fatal(err)
		}
		return ts
	}
	before := updatedAt()
	if err := r.mgr.SetEnabled(ctx, "shop", false); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.SetEnabled(ctx, "shop", true); err != nil {
		t.Fatal(err)
	}
	if err := r.installPack(packWith("shop", productWithSKU)); err != nil {
		t.Fatal(err)
	}
	if got := updatedAt(); !got.Equal(before) {
		t.Fatalf("updated_at moved %v -> %v", before, got)
	}
	if n, _ := auditWidgets(t, r, "package.schemas.extend", "plugin:shop"); n != 1 {
		t.Fatalf("package.schemas.extend audited %d times, want 1", n)
	}
}

func TestInvalidMergeKeepsSchema(t *testing.T) {
	r, _ := schemaRig(t)
	if err := r.installPack(schemaPack("shop")); err != nil {
		t.Fatal(err)
	}
	before := r.storedSchema("product")
	bad := `{"title":"Product","type":"object","properties":{"price":{"type":"number"},"broken":{"title":"no type"}}}`
	if err := entity.ValidateSchemaShape(jsontext.Value(bad)); err == nil {
		t.Fatal("test premise: the shape check accepts a property without a type")
	}
	if err := r.installPack(packWith("shop", bad)); err != nil {
		t.Fatalf("install failed on an invalid merge: %v", err)
	}
	if !reflect.DeepEqual(before, r.storedSchema("product")) {
		t.Fatalf("stored schema changed: %v", r.storedSchema("product"))
	}
	if n, _ := auditWidgets(t, r, "package.schemas.extend", "plugin:shop"); n != 0 {
		t.Fatalf("extend audited %d times for a refused merge", n)
	}
}

func TestSchemaPreviewMatchesInstall(t *testing.T) {
	r, _ := schemaRig(t)
	ctx := context.Background()
	if err := r.installPack(schemaPack("shop")); err != nil {
		t.Fatal(err)
	}
	raw, err := PackFiles(packWith("shop", productWithSKU), nil)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Parse(raw, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	create, extend, err := r.mgr.SchemaPreview(ctx, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if len(create) != 0 || len(extend) != 1 || extend[0].Slug != "product" ||
		!reflect.DeepEqual(extend[0].Added, []string{"sku"}) || !reflect.DeepEqual(extend[0].Labels, []string{"SKU"}) {
		t.Fatalf("preview create=%v extend=%+v", create, extend)
	}
	if err := r.installPack(packWith("shop", productWithSKU)); err != nil {
		t.Fatal(err)
	}
	if create, extend, err = r.mgr.SchemaPreview(ctx, pkg); err != nil || len(create) != 0 || len(extend) != 0 {
		t.Fatalf("preview after install: create=%v extend=%+v err=%v", create, extend, err)
	}
}

// A fresh install under the name of an uninstalled plugin whose content types
// were kept must leave those types alone.
func TestFreshInstallDoesNotExtendKeptTypes(t *testing.T) {
	r, _ := schemaRig(t)
	ctx := context.Background()
	if err := r.installPack(schemaPack("shop")); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.Uninstall(ctx, KindPlugin, "shop", UninstallOpts{}); err != nil {
		t.Fatal(err)
	}
	before := r.storedSchema("product")
	if err := r.installPack(packWith("shop", productWithSKU)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, r.storedSchema("product")) {
		t.Fatalf("a fresh install changed the kept type: %v", r.storedProps("product"))
	}
	if n, _ := auditWidgets(t, r, "package.schemas.extend", "plugin:shop"); n != 0 {
		t.Fatalf("extend audited %d times on a fresh install", n)
	}
	// Once installed, the next update extends it.
	if err := r.installPack(packWith("shop", productWithSKU+" ")); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.storedProps("product")["sku"]; !ok {
		t.Fatalf("update after the fresh install did not extend: %v", r.storedProps("product"))
	}
}

// A new type that fails to create does not keep the owned types from gaining
// their fields.
func TestExtendSurvivesFailedCreate(t *testing.T) {
	r, _ := schemaRig(t)
	if err := r.installPack(schemaPack("shop")); err != nil {
		t.Fatal(err)
	}
	files := packWith("shop", productWithSKU)
	files["schemas/venue.json"] = []byte(`{"title":"Venue","type":"object","properties":{"broken":{"title":"no type"}}}`)
	if err := r.installPack(files); err != nil {
		t.Fatal(err)
	}
	if _, found := r.typeOwner("venue"); found {
		t.Fatal("test premise: the invalid type was created")
	}
	if _, ok := r.storedProps("product")["sku"]; !ok {
		t.Fatalf("sku not added after a failed create: %v", r.storedProps("product"))
	}
}

// Looking at what an install would do writes nothing, so it must not log an
// error worded as a failed install.
func TestSchemaPreviewLogsNoError(t *testing.T) {
	r, _ := schemaRig(t)
	ctx := context.Background()
	if err := r.installPack(schemaPack("shop")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE hatamoto_entity_schemas SET schema = '[]' WHERE slug = 'product'`); err != nil {
		t.Fatal(err)
	}
	raw, err := PackFiles(packWith("shop", productWithSKU), nil)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Parse(raw, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	r.mgr.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	if _, extend, err := r.mgr.SchemaPreview(ctx, pkg); err != nil || len(extend) != 0 {
		t.Fatalf("preview extend=%+v err=%v", extend, err)
	}
	if !strings.Contains(logs.String(), "cannot be merged") {
		t.Fatalf("test premise: the unmergeable type was not reported: %q", logs.String())
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Fatalf("preview logged an error: %s", logs.String())
	}
}
