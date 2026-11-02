//go:build integration

package redirect

import (
	"context"
	"os"
	"testing"
	"time"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// DATABASE_URL=... go test -tags=integration ./internal/redirect -run Visitor
func TestList404VisitorParity_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	repo := NewRepository(db)

	if _, err := db.Pool().Exec(ctx, `DELETE FROM hatamoto_404_log WHERE path LIKE '/visitor-parity/%'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_404_log WHERE path LIKE '/visitor-parity/%'`)
	})

	uas := []string{
		"", "Mozilla/5.0 Chrome/124 Safari/537.36", "node-fetch/1.0", "GPTBot/1.1",
		"Mozilla/5.0 (compatible; Googlebot/2.1)", "SOMETHING CRAWLER 1.0", "a%b_c\\d",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0)", "CURL/8.0",
	}
	wantBots := map[string]bool{}
	for i, ua := range uas {
		path := "/visitor-parity/" + string(rune('a'+i))
		if err := repo.Upsert404Entry(ctx, path, "", ua); err != nil {
			t.Fatal(err)
		}
		wantBots[path] = agents.Default().LooksLikeBot(ua)
	}

	for _, visitor := range []string{VisitorBots, VisitorPeople} {
		// PerPage 2 proves count and pagination agree with the filter.
		got := map[string]bool{}
		for page := 1; ; page++ {
			rows, total, err := repo.List404Entries(ctx, NotFoundListParams{Search: "/visitor-parity/", Visitor: visitor, Page: page, PerPage: 2})
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range rows {
				got[e.Path] = true
			}
			if len(rows) == 0 || len(got) >= total {
				if len(got) != total {
					t.Fatalf("%s: total %d, listed %d", visitor, total, len(got))
				}
				break
			}
		}
		for path, isBot := range wantBots {
			if got[path] != (isBot == (visitor == VisitorBots)) {
				t.Errorf("%s: %s listed=%v, LooksLikeBot=%v", visitor, path, got[path], isBot)
			}
		}
	}
}

// Empties the whole 404 log: run against a scratch database only.
func TestDeleteAll404Entries_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	repo := NewRepository(db)
	if _, err := repo.DeleteAll404Entries(ctx); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/del-all/a", "/del-all/b", "/del-all/c"} {
		if err := repo.Upsert404Entry(ctx, p, "", "ua"); err != nil {
			t.Fatal(err)
		}
	}
	n, err := repo.DeleteAll404Entries(ctx)
	if err != nil || n != 3 {
		t.Fatalf("deleted %d, err %v", n, err)
	}
	if _, total, err := repo.List404Entries(ctx, NotFoundListParams{}); err != nil || total != 0 {
		t.Fatalf("left %d, err %v", total, err)
	}
}
