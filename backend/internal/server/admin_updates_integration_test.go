//go:build integration

package server

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/updater"
)

// The Updates adapter's SQL against a migrated Postgres:
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run AdminUpdates
func TestAdminUpdatesLogIntegration(t *testing.T) {
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("DATABASE_URL not set: skipping (needs Postgres)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, scratchMigratedDB(t, dsn))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	lg := pgUpdatesLog{db: pool}

	// No row yet: no stamp, no history.
	if at, err := lg.OffSince(ctx); err != nil || at != nil {
		t.Fatalf("empty OffSince = %v %v", at, err)
	}
	if h, err := lg.History(ctx, 20); err != nil || len(h) != 0 {
		t.Fatalf("empty History = %v %v", h, err)
	}

	// Stamping creates the row and keeps the first value.
	first := time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC)
	if err := lg.SetOffSince(ctx, &first); err != nil {
		t.Fatal(err)
	}
	later := first.Add(48 * time.Hour)
	if err := lg.SetOffSince(ctx, &later); err != nil {
		t.Fatal(err)
	}
	if at, err := lg.OffSince(ctx); err != nil || at == nil || !at.Equal(first) {
		t.Fatalf("OffSince = %v %v, want the first stamp %v", at, err, first)
	}
	if err := lg.SetOffSince(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if at, _ := lg.OffSince(ctx); at != nil {
		t.Errorf("OffSince after clear = %v", at)
	}

	// The updater's own store still reads and writes the row.
	st := updater.NewStore(pool)
	if err := lg.SetOffSince(ctx, &first); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSettings(ctx, updater.Settings{Mode: "off", DeferDays: 2}); err != nil {
		t.Fatal(err)
	}
	set, _, err := st.Load(ctx)
	if err != nil || set.Mode != "off" || set.DeferDays != 2 {
		t.Fatalf("store Load = %+v %v", set, err)
	}
	if at, _ := lg.OffSince(ctx); at == nil {
		t.Error("a settings save cleared off_since")
	}

	// History: newest first, limited.
	for i, ph := range []string{"success", "rolled_back", "failed"} {
		_, err := pool.Exec(ctx, `INSERT INTO hatamoto_updates (event_id, from_version, to_version, phase, error, started_at)
			VALUES ($1, 'v1.4.0', 'v1.4.1', $2, $3, $4)`, "e"+ph, ph, "boom", first.Add(time.Duration(i)*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
	}
	h, err := lg.History(ctx, 2)
	if err != nil || len(h) != 2 || h[0].Phase != "failed" || h[1].Phase != "rolled_back" || h[0].Error != "boom" || h[0].From != "v1.4.0" {
		t.Fatalf("History = %+v %v", h, err)
	}
}
