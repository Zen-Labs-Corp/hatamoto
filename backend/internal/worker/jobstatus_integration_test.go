//go:build integration

package worker

import (
	"context"
	"os"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

func openStatusDB(t *testing.T) *database.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	db, err := database.New(context.Background(), config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestStatusStore_Integration(t *testing.T) {
	ctx := context.Background()
	const id = "test_job_status"
	db := openStatusDB(t)
	t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_job_status WHERE job_id = $1`, id) })

	start := time.Now().Add(-time.Second).Truncate(time.Microsecond)
	if err := NewStatusStore(db).Record(ctx, Run{ID: id, Started: start, Finished: start.Add(1500 * time.Millisecond), OK: false, Err: "boom"}); err != nil {
		t.Fatal(err)
	}
	// Second run overwrites the first.
	if err := NewStatusStore(db).Record(ctx, Run{ID: id, Started: start, Finished: start.Add(250 * time.Millisecond), OK: true}); err != nil {
		t.Fatal(err)
	}
	if err := NewStatusStore(db).Record(ctx, Run{ID: id, Started: start, Finished: start.Add(2 * time.Second), OK: false, Err: "boom"}); err != nil {
		t.Fatal(err)
	}

	// A fresh store on a reopened pool reads it back.
	db2 := openStatusDB(t)
	got, err := NewStatusStore(db2).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := got[id]
	if !ok || r.OK || r.Err != "boom" || r.Duration() != 2*time.Second || !r.Started.Equal(start) {
		t.Fatalf("row = %+v ok=%v", r, ok)
	}
	var ms int64
	if err := db2.Pool().QueryRow(ctx, `SELECT duration_ms FROM hatamoto_job_status WHERE job_id = $1`, id).Scan(&ms); err != nil || ms != 2000 {
		t.Fatalf("duration_ms = %d, %v", ms, err)
	}
}
