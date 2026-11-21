//go:build integration

package worker

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

func openClaimDB(t *testing.T) *database.DB {
	t.Helper()
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("HATAMOTO_TEST_DB_URL and DATABASE_URL not set, skipping integration test")
	}
	db, err := database.New(context.Background(), config.DatabaseConfig{URL: dsn, MaxConnections: 25, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

func cleanClaims(t *testing.T, db *database.DB) {
	t.Helper()
	clean := func() {
		ctx := context.Background()
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_job_claims WHERE job_id LIKE 'module:test_claim:%'`)
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_job_status WHERE job_id LIKE 'module:test_claim:%'`)
	}
	clean()
	t.Cleanup(clean)
}

func TestClaimOneWinner(t *testing.T) {
	ctx := context.Background()
	db := openClaimDB(t)
	cleanClaims(t, db)
	const id = "module:test_claim:a"

	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := NewClaimer(db).Claim(ctx, id, time.Minute)
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := wins.Load(); got != 1 {
		t.Fatalf("winners = %d, want 1", got)
	}

	const short = "module:test_claim:a2"
	if ok, err := NewClaimer(db).Claim(ctx, short, 200*time.Millisecond); err != nil || !ok {
		t.Fatalf("first short claim = %v, %v", ok, err)
	}
	if ok, _ := NewClaimer(db).Claim(ctx, short, 200*time.Millisecond); ok {
		t.Fatal("claim inside the window succeeded")
	}
	time.Sleep(300 * time.Millisecond)
	if ok, err := NewClaimer(db).Claim(ctx, short, 200*time.Millisecond); err != nil || !ok {
		t.Fatalf("claim after the window = %v, %v", ok, err)
	}
}

func TestModuleJobOnceAcrossSchedulers(t *testing.T) {
	old := minJitterInterval
	minJitterInterval = time.Millisecond
	t.Cleanup(func() { minJitterInterval = old })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db1, db2 := openClaimDB(t), openClaimDB(t)
	cleanClaims(t, db1)

	var count atomic.Int32
	run := func(context.Context) error { count.Add(1); return nil }
	var scheds []*Scheduler
	for _, db := range []*database.DB{db1, db2} {
		s := NewScheduler(db, log)
		// Every 10 s polls each second.
		ScheduleModuleJobs(s, NewClaimer(db).Claim, []ModuleJob{{ID: "module:test_claim:b", Every: 10 * time.Second, Run: run}}, log)
		scheds = append(scheds, s)
	}
	for _, s := range scheds {
		s.Start(t.Context())
	}
	time.Sleep(3 * time.Second)
	for _, s := range scheds {
		s.Stop()
	}
	if got := count.Load(); got != 1 {
		t.Fatalf("runs = %d, want 1", got)
	}
}
