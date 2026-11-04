//go:build integration

package packages

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
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
			if err := r.mgr.Uninstall(ctx, KindPlugin, "shop", UninstallOpts{DropContentTypes: drop}); err != nil {
				t.Fatal(err)
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
