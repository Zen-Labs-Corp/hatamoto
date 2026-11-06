//go:build integration

package person

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
)

// A dry run of rows with a new byline name makes no person and no
// hatamoto_authors row; the real run does.
func TestImportDryRunMakesNoPeople(t *testing.T) {
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
	schemas := entity.NewPostgresSchemaRepository(db)
	svc := entity.NewService(schemas, entity.NewPostgresEntityRepository(db), slog.Default())
	svc.SetBylines(NewByline(db.Pool()))

	slug := "dryby-" + uuid.NewString()[:8]
	sc, err := schemas.Create(ctx, &entity.CreateSchemaInput{Name: slug, Slug: slug,
		Schema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"author":{"type":"string"}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	who := "Dry Run Writer " + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE schema_id = $1 OR name = $2`, sc.ID, who)
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_authors WHERE name = $1`, who)
		_ = schemas.Delete(ctx, sc.ID)
	})
	count := func(q string, args ...any) (n int) {
		t.Helper()
		if err := db.Pool().QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	people := func() int { return count(`SELECT count(*) FROM hatamoto_entities WHERE name = $1`, who) }
	authors := func() int { return count(`SELECT count(*) FROM hatamoto_authors WHERE name = $1`, who) }

	batch := entity.ImportBatch{Source: "test", DryRun: true, Items: []entity.ImportItem{
		{Key: "k:" + slug + ":1", SchemaSlug: slug, Name: "One", Attributes: json.RawMessage(`{"author":"` + who + `"}`)},
		{Key: "k:" + slug + ":2", SchemaSlug: slug, Name: "Two", AuthorName: who},
	}}
	res, err := svc.Import(ctx, nil, batch)
	if err != nil || res.Created != 2 || res.Failed != 0 {
		t.Fatalf("dry run = %+v (%v)", res, err)
	}
	if people() != 0 || authors() != 0 {
		t.Fatalf("a dry run made %d people and %d authors", people(), authors())
	}

	batch.DryRun = false
	if res, err = svc.Import(ctx, nil, batch); err != nil || res.Created != 2 || res.Failed != 0 {
		t.Fatalf("real run = %+v (%v)", res, err)
	}
	if people() != 1 || authors() != 1 {
		t.Fatalf("real run made %d people and %d authors, want 1 and 1", people(), authors())
	}
}
