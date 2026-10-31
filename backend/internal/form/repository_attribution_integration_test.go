//go:build integration

package form_test

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/migrations"
)

// testDB returns a scratch database migrated to head, dropped with the test.
func testDB(t *testing.T) *database.DB {
	t.Helper()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_form_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	prov, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prov.Up(ctx); err != nil {
		t.Fatal(err)
	}
	sqlDB.Close()
	pool.Close()
	db, err := database.New(ctx, config.DatabaseConfig{URL: u.String(), MaxConnections: 4, MinConnections: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	return db
}

func TestSubmissionAttributionRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := form.NewPostgresRepository(testDB(t))
	f, err := repo.CreateForm(ctx, &form.CreateFormInput{Name: "Contact", Fields: []form.FormField{}})
	if err != nil {
		t.Fatal(err)
	}

	want := &form.Attribution{
		First: &form.Touch{Channel: "ai_assistants", Source: "perplexity", Landing: "/guide"},
		Last:  &form.Touch{Channel: "email", Source: "newsletter", Medium: "email", Campaign: "spring", Landing: "/pricing"},
	}
	with, err := repo.CreateSubmission(ctx, f.ID, &form.CreateSubmissionInput{Data: map[string]any{"a": "b"}, Attribution: want})
	if err != nil {
		t.Fatal(err)
	}
	without, err := repo.CreateSubmission(ctx, f.ID, &form.CreateSubmissionInput{Data: map[string]any{"a": "c"}})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := repo.CreateSubmission(ctx, f.ID, &form.CreateSubmissionInput{Data: map[string]any{}, Attribution: &form.Attribution{First: &form.Touch{}}})
	if err != nil {
		t.Fatal(err)
	}
	if with.Attribution == nil || *with.Attribution.First != *want.First || *with.Attribution.Last != *want.Last {
		t.Fatalf("create returned %+v, want %+v", with.Attribution, want)
	}
	if without.Attribution != nil || empty.Attribution != nil {
		t.Fatalf("NULL/empty rows must have nil Attribution, got %+v / %+v", without.Attribution, empty.Attribution)
	}

	got, err := repo.GetSubmissionByID(ctx, with.ID)
	if err != nil || got.Attribution == nil || *got.Attribution.First != *want.First || *got.Attribution.Last != *want.Last {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if got, err := repo.GetSubmissionByID(ctx, without.ID); err != nil || got.Attribution != nil {
		t.Fatalf("get NULL row = %+v, %v", got, err)
	}

	list, total, err := repo.ListSubmissions(ctx, f.ID, nil)
	if err != nil || total != 3 || len(list) != 3 {
		t.Fatalf("list = %d rows total %d, %v", len(list), total, err)
	}
	for _, s := range list {
		if (s.ID == with.ID) != (s.Attribution != nil) {
			t.Errorf("list row %s attribution = %+v", s.ID, s.Attribution)
		}
	}

	read := "read"
	up, err := repo.UpdateSubmission(ctx, with.ID, &form.UpdateSubmissionInput{Status: &read})
	if err != nil || up.Attribution == nil || up.Attribution.Last.Campaign != "spring" {
		t.Fatalf("update = %+v, %v", up, err)
	}
}
