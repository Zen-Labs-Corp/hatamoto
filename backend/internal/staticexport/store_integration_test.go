//go:build integration

package staticexport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

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

func TestRunStore_ClearAbandoned(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	rs := NewRunStore(pool)
	id, err := rs.Start(ctx, "cli", "dir:/srv/x")
	if err != nil {
		t.Fatal(err)
	}
	if err := rs.ClearAbandoned(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	q := `SELECT status FROM hatamoto_static_export_runs WHERE id = $1`
	if err := pool.QueryRow(ctx, q, id).Scan(&status); err != nil || status != "running" {
		t.Fatalf("fresh run: %s %v", status, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_static_export_runs SET heartbeat_at = now() - interval '10 minutes' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if err := rs.ClearAbandoned(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, q, id).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("stale run: %s %v", status, err)
	}
}

func TestRunStore_ListLiveSkipsTestRuns(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	rs := NewRunStore(pool)
	for i, trig := range []string{"change", "test", "test", "test"} {
		id, err := rs.Start(ctx, trig, "dir:/srv/x")
		if err != nil {
			t.Fatal(err)
		}
		if err := rs.Finish(ctx, id, RunResult{OK: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE hatamoto_static_export_runs SET started_at = now() - ($2 || ' minutes')::interval WHERE id = $1`, id, fmt.Sprint(10-i)); err != nil {
			t.Fatal(err)
		}
	}
	all, err := rs.List(ctx, 2)
	if err != nil || len(all) != 2 || all[0].Trigger != "test" {
		t.Fatalf("List: %+v %v", all, err)
	}
	live, err := rs.ListLive(ctx, 2)
	if err != nil || len(live) != 1 || live[0].Trigger != "change" {
		t.Fatalf("ListLive: %+v %v", live, err)
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

func TestRunStore_PruneRuns(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	rs := NewRunStore(pool)
	insert := func(status string, age string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_static_export_runs (trigger, target, status, started_at, heartbeat_at)
			VALUES ('cli', 'dir:/x', $1, now() - $2::interval, now() - $2::interval)`, status, age); err != nil {
			t.Fatal(err)
		}
	}
	insert("ok", "100 days")
	insert("failed", "95 days")
	insert("ok", "1 day")
	insert("running", "100 days")
	n, err := rs.PruneRuns(ctx, time.Now().Add(-90*24*time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("pruned %d, err %v; want 2", n, err)
	}
	runs, err := rs.List(ctx, 10)
	if err != nil || len(runs) != 2 {
		t.Fatalf("left %d runs, err %v", len(runs), err)
	}
}

func TestSettingsStore_SFTPRoundTrip(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	st := NewSettingsStore(pool)
	sealed, err := SealSFTPSecret(SecretKey("server-secret"), "hunter2")
	if err != nil {
		t.Fatal(err)
	}
	set := validSFTP()
	set.SFTPHost, set.SFTPPort, set.SFTPUser, set.SFTPAuth = "sftp.example.com", 2222, "owner", SFTPAuthKey
	set.SFTPSecretSealed, set.SFTPDir, set.SFTPHostKey, set.SFTPHTAccess = sealed, "www/site", "SHA256:abc", false
	if err := st.Save(ctx, set); err != nil {
		t.Fatal(err)
	}
	got, found, err := st.Get(ctx)
	if err != nil || !found {
		t.Fatalf("get: %v %v", found, err)
	}
	if got.Target != TargetSFTP || got.SFTPHost != "sftp.example.com" || got.SFTPPort != 2222 || got.SFTPUser != "owner" ||
		got.SFTPAuth != SFTPAuthKey || got.SFTPDir != "www/site" || got.SFTPHostKey != "SHA256:abc" || got.SFTPHTAccess {
		t.Fatalf("got = %+v", got)
	}
	if s, ok := got.SFTPSecret(SecretKey("server-secret")); !ok || s != "hunter2" {
		t.Fatal("sealed SFTP secret did not round trip")
	}
	if got.HasSecret() {
		t.Fatal("the S3 secret column was filled by an SFTP save")
	}
}
