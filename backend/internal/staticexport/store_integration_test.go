//go:build integration

package staticexport

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Run with: DATABASE_URL=... go test -tags=integration ./internal/staticexport/...
func newIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	clean := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_static_hosting_settings`)
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_static_export_runs`)
	}
	clean()
	t.Cleanup(func() { clean(); pool.Close() })
	return pool
}

func TestSettingsStore_RoundTrip(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	st := NewSettingsStore(pool)
	got, found, err := st.Get(ctx)
	if err != nil || found || got.Mode != ModeServed {
		t.Fatalf("empty: %+v found=%v err=%v", got, found, err)
	}
	sealed, err := SealSecret(SecretKey("server-secret"), "very-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	set := validS3()
	set.Prefix, set.SecretSealed, set.LiveOrigin = "site/", sealed, "https://live.example.com"
	if err := st.Save(ctx, set); err != nil {
		t.Fatal(err)
	}
	got, found, err = st.Get(ctx)
	if err != nil || !found {
		t.Fatalf("get: %v %v", found, err)
	}
	if got.Mode != ModeStatic || got.Target != TargetS3 || got.Bucket != "my-site" || got.Prefix != "site/" ||
		got.LiveOrigin != "https://live.example.com" || got.UpdatedAt.IsZero() {
		t.Fatalf("got = %+v", got)
	}
	if s, ok := got.Secret(SecretKey("server-secret")); !ok || s != "very-secret-value" {
		t.Fatal("sealed secret did not round trip")
	}
	set.Mode = "bogus"
	if err := st.Save(ctx, set); err == nil {
		t.Fatal("an invalid mode was saved")
	}
}

func TestRunStore_OneRunning(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	rs := NewRunStore(pool)
	id, err := rs.Start(ctx, "cli", "dir:/srv/site-static")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Start(ctx, "cli", "dir:/srv/site-static"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Start err = %v", err)
	}
	if err := rs.Heartbeat(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := rs.Finish(ctx, id, RunResult{OK: true, Pages: 3, Bytes: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Start(ctx, "cli", "dir:/srv/site-static"); err != nil {
		t.Fatalf("Start after Finish: %v", err)
	}
	runs, err := rs.List(ctx, 10)
	if err != nil || len(runs) != 2 {
		t.Fatalf("List: %d runs, %v", len(runs), err)
	}
	var ok *Run
	for i := range runs {
		if runs[i].ID == id {
			ok = &runs[i]
		}
	}
	if ok == nil || ok.Status != "ok" || ok.Pages != 3 || ok.FinishedAt == nil {
		t.Fatalf("finished run = %+v", ok)
	}
}

func TestRunStore_StaleTakeover(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	rs := NewRunStore(pool)
	old, err := rs.Start(ctx, "cli", "dir:/srv/x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_static_export_runs SET heartbeat_at = now() - interval '10 minutes' WHERE id = $1`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Start(ctx, "cli", "dir:/srv/x"); err != nil {
		t.Fatalf("Start over a stale run: %v", err)
	}
	var status, errs string
	if err := pool.QueryRow(ctx, `SELECT status, errors::text FROM hatamoto_static_export_runs WHERE id = $1`, old).Scan(&status, &errs); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || !strings.Contains(errs, "abandoned") {
		t.Fatalf("stale run: %s %s", status, errs)
	}
}

func TestRunStore_FinishCapsErrors(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	rs := NewRunStore(pool)
	id, err := rs.Start(ctx, "cli", "dir:/srv/x")
	if err != nil {
		t.Fatal(err)
	}
	errs := make([]string, 80)
	for i := range errs {
		errs[i] = strings.Repeat("e", 900)
	}
	if err := rs.Finish(ctx, id, RunResult{Errors: errs}); err != nil {
		t.Fatal(err)
	}
	runs, err := rs.List(ctx, 1)
	if err != nil || len(runs) != 1 {
		t.Fatal(err)
	}
	if runs[0].Status != "failed" || len(runs[0].Errors) != 50 || len(runs[0].Errors[0]) != 500 {
		t.Fatalf("status=%s errors=%d len=%d", runs[0].Status, len(runs[0].Errors), len(runs[0].Errors[0]))
	}
}
