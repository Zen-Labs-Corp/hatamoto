//go:build integration

package updater

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// scratchPool creates a throwaway database with only the settings migration
// applied and drops it afterwards.
func scratchPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("HATAMOTO_TEST_DB_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_upd_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(dsn)
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	sqlText, err := os.ReadFile("../../migrations/202610230900_hatamoto_update_settings.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(strings.TrimPrefix(string(sqlText), "-- +goose Up"), "-- +goose Down")
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	offSQL, err := os.ReadFile("../../migrations/202610240900_hatamoto_update_off_since.sql")
	if err != nil {
		t.Fatal(err)
	}
	offUp, _, _ := strings.Cut(strings.TrimPrefix(string(offSQL), "-- +goose Up"), "-- +goose Down")
	if _, err := pool.Exec(ctx, offUp); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestOffSinceColumn_Integration(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	st := NewStore(pool)
	if err := st.SaveStatus(ctx, Status{LastResult: "ok"}); err != nil {
		t.Fatal(err)
	}
	var at *time.Time
	if err := pool.QueryRow(ctx, `SELECT off_since FROM hatamoto_update_settings`).Scan(&at); err != nil || at != nil {
		t.Fatalf("off_since must exist and default to NULL: %v %v", at, err)
	}
	// The updater's own writes must not touch it.
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_update_settings SET off_since = now()`); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveStatus(ctx, Status{LastResult: "ok", Decision: "up_to_date"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSettings(ctx, Settings{Mode: ModeOff}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT off_since FROM hatamoto_update_settings`).Scan(&at); err != nil || at == nil {
		t.Fatalf("updater writes must leave off_since alone: %v %v", at, err)
	}
}

func TestStoreRoundTrip_Integration(t *testing.T) {
	ctx := context.Background()
	st := NewStore(scratchPool(t))

	set, status, err := st.Load(ctx)
	if err != nil || set != (Settings{}) || status.LastResult != "" || len(status.FirstSeen) != 0 {
		t.Fatalf("missing row must load as defaults: %+v %+v %v", set, status, err)
	}

	// Status first: creates the row with policy columns at their defaults.
	at := time.Date(2026, 11, 5, 3, 12, 0, 0, time.UTC)
	want := Status{LastCheckAt: &at, LastResult: "ok", Decision: "scheduled", Reason: "bucket 37",
		Target: "v1.4.2", AvailableMajor: "v2.0.0", FirstSeen: map[string]time.Time{"v1.4.2": at}}
	if err := st.SaveStatus(ctx, want); err != nil {
		t.Fatal(err)
	}
	set, got, err := st.Load(ctx)
	if err != nil || set != (Settings{}) {
		t.Fatalf("policy after status-only save: %+v %v", set, err)
	}
	if !got.LastCheckAt.Equal(at) || got.Decision != "scheduled" || got.Target != "v1.4.2" ||
		got.AvailableMajor != "v2.0.0" || !got.FirstSeen["v1.4.2"].Equal(at) {
		t.Fatalf("status round trip: %+v", got)
	}

	// Settings upsert leaves the status alone and replaces the policy.
	a, b := 22, 6
	pol := Settings{Mode: "notify", Channel: "edge", WindowStart: &a, WindowEnd: &b, Pin: "v1.4", DeferDays: 3}
	if err := st.SaveSettings(ctx, pol); err != nil {
		t.Fatal(err)
	}
	set, got, err = st.Load(ctx)
	if err != nil || set.Mode != "notify" || set.Channel != "edge" || *set.WindowStart != 22 || *set.WindowEnd != 6 ||
		set.Pin != "v1.4" || set.DeferDays != 3 || got.Decision != "scheduled" {
		t.Fatalf("settings upsert: %+v %+v %v", set, got, err)
	}
	// Clearing returns columns to NULL, so the defaults apply again.
	if err := st.SaveSettings(ctx, Settings{DeferDays: 99}); err != nil {
		t.Fatal(err)
	}
	set, _, _ = st.Load(ctx)
	if set.Mode != "" || set.WindowStart != nil || set.Pin != "" || set.DeferDays != 14 {
		t.Fatalf("cleared settings: %+v", set)
	}
	if err := st.SaveSettings(ctx, Settings{Pin: "1.4"}); err == nil {
		t.Fatal("invalid pin must be rejected")
	}
}

// SavePolicy writes only the named columns: a column left NULL stays the
// default, and a pin set by the command line in between survives.
func TestSavePolicyWritesOnlyNamedColumns_Integration(t *testing.T) {
	ctx := context.Background()
	st := NewStore(scratchPool(t))
	if err := st.SavePolicy(ctx, Settings{Mode: "auto", Channel: "stable", DeferDays: 3}, ColDefer); err != nil {
		t.Fatal(err)
	}
	set, _, err := st.Load(ctx)
	if err != nil || set.Mode != "" || set.Channel != "" || set.DeferDays != 3 {
		t.Fatalf("mode and channel must stay NULL (the defaults): %+v %v", set, err)
	}
	// The CLI pins after the form was loaded; saving the mode must not unpin.
	if err := st.SaveSettings(ctx, Settings{DeferDays: 3, Pin: "v1.4"}); err != nil {
		t.Fatal(err)
	}
	a, b := 2, 5
	if err := st.SavePolicy(ctx, Settings{Mode: "notify", WindowStart: &a, WindowEnd: &b}, ColMode, ColWindow); err != nil {
		t.Fatal(err)
	}
	set, _, err = st.Load(ctx)
	if err != nil || set.Mode != "notify" || *set.WindowStart != 2 || *set.WindowEnd != 5 || set.Pin != "v1.4" || set.DeferDays != 3 || set.Channel != "" {
		t.Fatalf("after a partial save: %+v %v", set, err)
	}
	if err := st.SavePolicy(ctx, Settings{Mode: "yolo"}, ColMode); err == nil {
		t.Fatal("an invalid mode must be rejected")
	}
}
