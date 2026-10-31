//go:build integration

package agents

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

func attributionDB(t *testing.T) *database.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	db, err := database.New(context.Background(), config.DatabaseConfig{URL: dsn, MaxConnections: 8, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

// Unattributed page paths get their entity once; error paths, files and
// unresolvable paths stay untouched; a second run changes nothing.
func TestAttributionBackfill_Integration(t *testing.T) {
	ctx := context.Background()
	db := attributionDB(t)
	marker := "it-attr-" + uuid.NewString()[:8]
	page, gone, file := "/post/"+marker, "/post/"+marker+"-gone", "/robots.txt"
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_agent_hits WHERE agent_id = $1`, marker)
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_agent_daily WHERE agent_id = $1`, marker)
	})
	id := uuid.New()
	var calls []string
	resolve := func(_ context.Context, p string) (*uuid.UUID, int) {
		calls = append(calls, p)
		if p == page {
			return &id, 200
		}
		return nil, 404
	}
	for _, q := range []string{
		`INSERT INTO hatamoto_agent_daily (day, agent_id, path, status, hits) VALUES
		   (current_date, $1, '` + page + `', 200, 3), (current_date - 1, $1, '` + page + `', 301, 1),
		   (current_date, $1, '` + page + `', 404, 2), (current_date, $1, '` + gone + `', 200, 1),
		   (current_date, $1, '` + file + `', 200, 1)`,
		`INSERT INTO hatamoto_agent_hits (occurred_at, agent_id, path, status, source) VALUES
		   (now(), $1, '` + page + `', 200, 'backend'), (now(), $1, '` + page + `', 404, 'backend'),
		   (now() - interval '200 days', $1, '` + page + `', 200, 'backend')`,
	} {
		if _, err := db.Pool().Exec(ctx, q, marker); err != nil {
			t.Fatal(err)
		}
	}

	b := NewAttributionBackfill(db, resolve, 0)
	n, err := b.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n < 2 {
		t.Fatalf("attributed %d daily rows, want at least the 2 below 400", n)
	}
	var withEntity, withoutEntity, hitsWith, hitsWithout int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FILTER (WHERE entity_id = $2), count(*) FILTER (WHERE entity_id IS NULL)
		FROM hatamoto_agent_daily WHERE agent_id = $1`, marker, id).Scan(&withEntity, &withoutEntity); err != nil {
		t.Fatal(err)
	}
	if withEntity != 2 || withoutEntity != 3 { // 200 + 301 of the page; its 404, the gone path and robots stay
		t.Errorf("daily with/without entity = %d/%d, want 2/3", withEntity, withoutEntity)
	}
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FILTER (WHERE entity_id = $2), count(*) FILTER (WHERE entity_id IS NULL)
		FROM hatamoto_agent_hits WHERE agent_id = $1`, marker, id).Scan(&hitsWith, &hitsWithout); err != nil {
		t.Fatal(err)
	}
	if hitsWith != 1 || hitsWithout != 2 { // the 404 and the hit beyond the retention stay
		t.Errorf("hits with/without entity = %d/%d, want 1/2", hitsWith, hitsWithout)
	}
	for _, c := range calls {
		if c == file {
			t.Errorf("resolver called for a file path")
		}
	}
	var hits int64
	if err := db.Pool().QueryRow(ctx, `SELECT sum(hits) FROM hatamoto_agent_daily WHERE agent_id = $1`, marker).Scan(&hits); err != nil || hits != 8 {
		t.Errorf("counters changed: hits = %d (%v), want 8", hits, err)
	}

	if n, err := b.Run(ctx); err != nil || n != 0 {
		t.Errorf("second run = %d (%v), want 0", n, err)
	}
}

// The backfill runs beside the writer, and beside the verify worker, over the
// same daily rows without a deadlock. (Writer against verify is not asserted
// here: that pair is not what this job changes.)
func TestAttributionBackfill_ConcurrentNoDeadlock_Integration(t *testing.T) {
	ctx := context.Background()
	db := attributionDB(t)
	marker := "it-attrc-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_agent_hits WHERE path LIKE $1`, "/post/"+marker+"%")
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_agent_daily WHERE path LIKE $1`, "/post/"+marker+"%")
	})
	const pages = 12
	id := uuid.New()
	resolve := func(context.Context, string) (*uuid.UUID, int) { return &id, 200 }
	ip := netip.MustParseAddr("203.0.113.5")
	var seed []Hit
	for i := 0; i < pages; i++ {
		a := ip
		seed = append(seed, Hit{At: time.Now().UTC(), AgentID: "gptbot", Operator: "OpenAI", Purpose: PurposeTraining,
			Verification: VerificationPending, IP: &a, Method: "GET",
			Path: "/post/" + marker + "-" + string(rune('a'+i)), Status: 200, Source: SourceBackend})
	}
	repo := NewRepository(db)
	if err := repo.InsertHits(ctx, seed); err != nil {
		t.Fatal(err)
	}
	vr := NewVerifyRepository(db)

	for name, other := range map[string]func() error{
		"writer": func() error { return repo.InsertHits(ctx, seed) },
		"verify": func() error {
			if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_agent_hits SET verification = 'pending' WHERE path LIKE $1`, "/post/"+marker+"%"); err != nil {
				return err
			}
			_, err := vr.ApplyVerification(ctx, PendingPair{AgentID: "gptbot", IP: ip}, StateSpoofed, time.Time{})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			var wg sync.WaitGroup
			errs := make(chan error, 16)
			for _, fn := range []func() error{
				func() error { _, err := NewAttributionBackfill(db, resolve, 0).Run(ctx); return err },
				func() error { _, err := NewAttributionBackfill(db, resolve, 0).Run(ctx); return err },
				other,
			} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for r := 0; r < 20; r++ {
						if err := fn(); err != nil {
							errs <- err
							return
						}
					}
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				var pe *pgconn.PgError
				if errors.As(err, &pe) && pe.Code == "40P01" {
					t.Fatalf("deadlock: %v", err)
				}
				t.Fatal(err)
			}
		})
	}
}
