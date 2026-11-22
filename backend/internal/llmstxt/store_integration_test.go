//go:build integration

package llmstxt

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"database/sql"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/migrations"
)

// scratchPool migrates a fresh database next to DATABASE_URL and returns a pool on it.
func scratchPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_llmstxt_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	var db *sql.DB = stdlib.OpenDBFromPool(pool)
	defer db.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

func TestStoreRecordIndexAndFull(t *testing.T) {
	ctx := context.Background()
	st := NewStore(scratchPool(t))

	if got, err := st.Get(ctx); err != nil || got.LLMsHash != "" || len(got.Changes) != 0 || got.LLMsChangedAt != nil {
		t.Fatalf("empty Get = %+v, %v", got, err)
	}

	t1 := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	t3 := t2.Add(time.Hour)

	mk := func(job string) Snapshot {
		return Snapshot{
			"site:name": {Name: "Acme Studio"},
			"en|elena":  {Name: "Elena Voss", Path: "/team/elena-voss", Job: job},
		}
	}

	// First build: baseline, no changes.
	if err := st.RecordIndex(ctx, mk("Designer"), "h1", 100, nil, t1); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Changes) != 0 || got.LLMsBytes != 100 || got.LLMsChangedAt == nil || !got.LLMsChangedAt.Equal(t1) {
		t.Fatalf("after first build: %+v", got)
	}
	if got.Snapshot["en|elena"].Job != "Designer" {
		t.Fatalf("snapshot not stored: %+v", got.Snapshot)
	}

	// A changed job title is one change and moves the change time.
	if err := st.RecordIndex(ctx, mk("Lead designer"), "h2", 120, nil, t2); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Get(ctx)
	if len(got.Changes) != 1 || got.Changes[0].Text != "Elena Voss: job title changed" || !got.Changes[0].At.Equal(t2) {
		t.Fatalf("after job change: %+v", got.Changes)
	}
	if !got.LLMsChangedAt.Equal(t2) {
		t.Fatalf("changed_at = %v, want %v", got.LLMsChangedAt, t2)
	}

	// Same hash: no new change, change time kept.
	if err := st.RecordIndex(ctx, mk("Lead designer"), "h2", 120, nil, t3); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Get(ctx)
	if len(got.Changes) != 1 || !got.LLMsChangedAt.Equal(t2) {
		t.Fatalf("same hash moved state: %+v", got)
	}

	// Full file is tracked separately and keeps the index state.
	if err := st.RecordFull(ctx, "f1", 4096, t3); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordFull(ctx, "f1", 4096, t3.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Get(ctx)
	if got.FullBytes != 4096 || got.FullChangedAt == nil || !got.FullChangedAt.Equal(t3) || got.LLMsHash != "h2" || len(got.Changes) != 1 {
		t.Fatalf("after full: %+v", got)
	}
}
