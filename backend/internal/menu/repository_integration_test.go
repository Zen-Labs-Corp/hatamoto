//go:build integration

package menu

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// An entity menu item stores no schema_slug; GetMenuWithItems must still
// report the linked entity's schema so renderers can build the right URL.
func TestGetMenuWithItemsReportsEntitySchemaSlug(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: 30 * time.Minute, MaxConnIdleTime: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	exec := func(sql string, args ...any) error {
		conn, err := db.GetConn(ctx)
		if err != nil {
			return err
		}
		defer conn.Release()
		_, err = conn.Exec(ctx, sql, args...)
		return err
	}

	schemaID, entID := uuid.New(), uuid.New()
	suffix := schemaID.String()[:8]
	if err := exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ($1, $2, $2, '{"type":"object","properties":{}}')`, schemaID, "it-menu-"+suffix); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec(`DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID) })
	if err := exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status) VALUES ($1,$2,'Hello',$3,'published')`, entID, schemaID, "hello-"+suffix); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec(`DELETE FROM hatamoto_entities WHERE id=$1`, entID) })

	repo := NewPostgresRepository(db)
	m, err := repo.CreateMenu(ctx, &CreateMenuInput{Name: "it-menu-" + suffix, Slug: "it-menu-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.DeleteMenu(ctx, m.ID) })
	if _, err := repo.CreateMenuItem(ctx, m.ID, &CreateMenuItemInput{Label: "Hello", LinkType: "entity", EntityID: &entID}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetMenuWithItems(ctx, m.ID)
	if err != nil || len(got.Items) != 1 {
		t.Fatalf("items = %v, %v", got, err)
	}
	if it := got.Items[0]; it.SchemaSlug != nil || it.EntitySchemaSlug == nil || *it.EntitySchemaSlug != "it-menu-"+suffix {
		t.Fatalf("EntitySchemaSlug = %v (SchemaSlug %v)", it.EntitySchemaSlug, it.SchemaSlug)
	}
}
