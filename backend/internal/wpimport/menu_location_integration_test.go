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
	"hatamoto.dev/core/internal/menu"
)

// A WordPress menu mapped to a location takes it from the menu that held it
// (a seeded Header, say). The old menu is kept, unassigned, and nothing is
// reported as a warning. Importing again changes nothing.
func TestImportMenu_TakesLocationFromPreviousHolder(t *testing.T) {
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

	const host = "wpm.example.test"
	// A shared test database may hold a real Header menu: park it and put it back.
	var parked []string
	rows, err := pool.Query(ctx, `SELECT id::text FROM hatamoto_menus WHERE location = 'header'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		parked = append(parked, id)
	}
	rows.Close()
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_menus SET location = NULL WHERE location = 'header'`); err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_menus WHERE slug IN ('wpm-seeded', 'wpm-primary')`)
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_import_map WHERE source = $1`, SourceName+":https://"+host)
	}
	t.Cleanup(func() {
		cleanup()
		for _, id := range parked {
			_, _ = pool.Exec(ctx, `UPDATE hatamoto_menus SET location = 'header' WHERE id = $1::uuid AND NOT EXISTS (SELECT 1 FROM hatamoto_menus WHERE location = 'header')`, id)
		}
	})
	cleanup()

	header := menu.LocationHeader
	seeded, err := d.Menus.CreateMenu(ctx, &menu.CreateMenuInput{Name: "Header", Slug: "wpm-seeded", Location: &header})
	if err != nil {
		t.Fatal(err)
	}

	dump := &Dump{Site: Site{URL: "https://" + host}, Menus: []Menu{{ID: "7", Name: "Primary", Slug: "wpm-primary", Location: "primary",
		Items: []MenuItem{{ID: "1", Title: "Home", URL: "/", Type: "custom"}}}}}
	r, err := NewRunner(d, NewStore(pool, "https://"+host), dump, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.run, err = r.st.StartRun(ctx, RunRunning, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // the second pass is the idempotent re-run
		if err := r.importMenus(ctx); err != nil {
			t.Fatal(err)
		}
		if len(r.res.Errors) > 0 || len(r.res.Warnings) > 0 {
			t.Fatalf("pass %d: errors=%+v warnings=%v", i, r.res.Errors, r.res.Warnings)
		}
		holder, err := d.Menus.GetMenuByLocation(ctx, "header")
		if err != nil || holder.Slug != "wpm-primary" {
			t.Fatalf("pass %d: header holder = %+v, %v", i, holder, err)
		}
		old, err := d.Menus.GetMenu(ctx, seeded.ID)
		if err != nil || old.Location != nil {
			t.Fatalf("pass %d: previous holder = %+v, %v; want kept and unassigned", i, old, err)
		}
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_menus WHERE location = 'header'`).Scan(&n)
		if n != 1 {
			t.Fatalf("pass %d: %d menus at header", i, n)
		}
	}
}
