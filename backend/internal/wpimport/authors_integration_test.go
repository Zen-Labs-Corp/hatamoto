//go:build integration

package wpimport

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// WordPress authors become people: not public, keyed so a re-run reuses them,
// an existing person with the slug is left alone, and posts credit them.
func TestImportAuthorsMakePeople_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	d := NewDeps(db, DepsConfig{MediaDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(d.Close)
	pool := db.Pool()
	cleanup := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_entities WHERE slug IN ('wpa-jane','wpa-sam') OR metadata->>'import_key' LIKE 'wp:wpa.example.test:author:%'`)
	}
	cleanup()
	t.Cleanup(cleanup)

	// An owner-made person with the slug is used as is.
	if _, err := pool.Exec(ctx, `
		INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, published_at)
		SELECT id, 'Sam Owner', 'wpa-sam', 'published', '{"public":true,"job_title":"Editor"}', now()
		FROM hatamoto_entity_schemas WHERE slug = 'person'`); err != nil {
		t.Fatal(err)
	}

	dump := &Dump{Site: Site{URL: "https://wpa.example.test"}, Authors: []Author{
		{Key: "wpa-jane", Slug: "wpa-jane", Name: "Jane Writer", Bio: "Writes <b>things</b>"},
		{Key: "wpa-sam", Slug: "wpa-sam", Name: "Sam WP", Bio: "other"},
	}}
	run := func() *Runner {
		r, err := NewRunner(d, nil, dump, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := r.importAuthors(ctx); err != nil {
			t.Fatal(err)
		}
		if len(r.res.Errors) > 0 {
			t.Fatalf("errors: %+v", r.res.Errors)
		}
		return r
	}
	r1 := run()
	r2 := run()

	jane, err := d.People.GetBySlug(ctx, "wpa-jane")
	if err != nil {
		t.Fatal(err)
	}
	if jane.Public || jane.Name != "Jane Writer" || jane.Bio != "<p>Writes &lt;b&gt;things&lt;/b&gt;</p>" {
		t.Errorf("jane = %+v", jane)
	}
	if r1.authorIDs["wpa-jane"] != jane.ID || r2.authorIDs["wpa-jane"] != jane.ID {
		t.Errorf("author ids %v %v, want %v", r1.authorIDs["wpa-jane"], r2.authorIDs["wpa-jane"], jane.ID)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities WHERE slug = 'wpa-jane'`).Scan(&n)
	if n != 1 {
		t.Errorf("%d rows for jane after a re-run, want 1", n)
	}
	sam, _ := d.People.GetBySlug(ctx, "wpa-sam")
	if sam.Name != "Sam Owner" || sam.JobTitle != "Editor" || r1.authorIDs["wpa-sam"] != sam.ID {
		t.Errorf("owner's person was changed or not reused: %+v", sam)
	}
}
