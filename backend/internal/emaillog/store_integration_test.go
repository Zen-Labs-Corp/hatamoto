//go:build integration

package emaillog

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/migrations"
)

// scratchStore migrates a fresh database next to DATABASE_URL and returns a
// store on it with one site_config row.
func scratchStore(t *testing.T) *Store {
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
	name := "hm_emaillog_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
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

	db := stdlib.OpenDBFromPool(pool)
	defer func(db *sql.DB) { _ = db.Close() }(db)
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_site_config (company_name) VALUES ('Example')`); err != nil {
		t.Fatal(err)
	}
	return NewStore(pool)
}

func entry(status, subject string) email.LogEntry {
	return email.LogEntry{
		Recipients: []string{"a@example.com"}, Subject: subject, Kind: email.KindFormNotification,
		Status: status, BodyText: "hello", BodyHTML: "<p>hello</p>",
	}
}

func TestRecordListGetStatusFilter(t *testing.T) {
	s := scratchStore(t)
	ctx := context.Background()
	if _, err := s.Record(ctx, entry("sent", "one")); err != nil {
		t.Fatal(err)
	}
	failed := entry("failed", "two")
	failed.Error = "boom"
	id, err := s.Record(ctx, failed)
	if err != nil {
		t.Fatal(err)
	}

	rows, total, err := s.List(ctx, "", 10, 0)
	if err != nil || total != 2 || len(rows) != 2 || rows[0].Subject != "two" {
		t.Fatalf("List all = %v total %d err %v", rows, total, err)
	}
	if rows[0].BodyText != "" || rows[0].BodyHTML != "" || !rows[0].HasBody {
		t.Errorf("List must not return bodies: %+v", rows[0])
	}
	rows, total, err = s.List(ctx, "failed", 10, 0)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].Status != "failed" {
		t.Fatalf("List failed = %v total %d err %v", rows, total, err)
	}
	rows, total, _ = s.List(ctx, "", 1, 1)
	if total != 2 || len(rows) != 1 || rows[0].Subject != "one" {
		t.Errorf("page 2 = %v total %d", rows, total)
	}

	got, err := s.Get(ctx, id)
	if err != nil || got.BodyText != "hello" || got.BodyHTML != "<p>hello</p>" || got.Error != "boom" || got.Recipients[0] != "a@example.com" {
		t.Fatalf("Get = %+v err %v", got, err)
	}
	if _, err := s.Get(ctx, id+100); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get unknown = %v, want ErrNotFound", err)
	}
}

func TestRetentionValidation(t *testing.T) {
	s := scratchStore(t)
	ctx := context.Background()
	if b, e, err := s.Retention(ctx); err != nil || b != 30 || e != 90 {
		t.Fatalf("defaults = %d %d %v", b, e, err)
	}
	for _, c := range [][2]int{{0, 90}, {30, 366}, {60, 30}} {
		if err := s.SetRetention(ctx, c[0], c[1]); !errors.Is(err, ErrRetention) {
			t.Errorf("SetRetention(%v) = %v, want ErrRetention", c, err)
		}
	}
	if err := s.SetRetention(ctx, 7, 14); err != nil {
		t.Fatal(err)
	}
	if b, e, _ := s.Retention(ctx); b != 7 || e != 14 {
		t.Errorf("saved = %d %d", b, e)
	}
}

func TestPurgeInjectedClock(t *testing.T) {
	s := scratchStore(t)
	ctx := context.Background()
	orig, err := s.Record(ctx, entry("failed", "orig"))
	if err != nil {
		t.Fatal(err)
	}
	re := entry("sent", "resend")
	re.ResentFrom = orig
	resendID, err := s.Record(ctx, re)
	if err != nil {
		t.Fatal(err)
	}
	// The original is 95 days old, the resend 40 days old.
	now := time.Now()
	if _, err := s.pool.Exec(ctx, `UPDATE hatamoto_email_log SET created_at = $1 WHERE id = $2`, now.AddDate(0, 0, -95), orig); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE hatamoto_email_log SET created_at = $1 WHERE id = $2`, now.AddDate(0, 0, -40), resendID); err != nil {
		t.Fatal(err)
	}

	s.now = func() time.Time { return now }
	if n, err := s.Purge(ctx); err != nil || n != 2 {
		t.Fatalf("Purge = %d, %v; want 2 (one deleted, one body dropped)", n, err)
	}
	if _, err := s.Get(ctx, orig); !errors.Is(err, ErrNotFound) {
		t.Errorf("95 day row survived: %v", err)
	}
	got, err := s.Get(ctx, resendID)
	if err != nil {
		t.Fatal(err)
	}
	if got.HasBody || got.BodyText != "" || got.BodyHTML != "" {
		t.Errorf("40 day row kept its body: %+v", got)
	}
	if got.ResentFrom != 0 {
		t.Errorf("resent_from = %d, want 0 after the original is deleted", got.ResentFrom)
	}
	// A row younger than the body period keeps its body.
	fresh, _ := s.Record(ctx, entry("sent", "fresh"))
	if _, err := s.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.Get(ctx, fresh); !g.HasBody {
		t.Error("fresh row lost its body")
	}
}
