//go:build integration

package frameticket

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

func openDB(t *testing.T) *database.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := database.New(context.Background(), config.DatabaseConfig{URL: dsn, MaxConnections: 8, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

// prefix returns a random nonce prefix and deletes its rows on cleanup.
func prefix(t *testing.T, db *database.DB) string {
	t.Helper()
	p := "t" + uuid.NewString()[:8] + "-"
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_frame_tickets WHERE nonce LIKE $1`, p+"%")
	})
	return p
}

func count(t *testing.T, db *database.DB, p string) int {
	t.Helper()
	var n int
	if err := db.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM hatamoto_frame_tickets WHERE nonce LIKE $1`, p+"%").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStoreRoundTripOnce_Integration(t *testing.T) {
	db := openDB(t)
	s, p, ctx, now := NewStore(db), prefix(t, db), context.Background(), time.Now()
	for _, payload := range [][]byte{[]byte("body"), nil} {
		in := Ticket{Nonce: p + string(rune('a'+len(payload))), Purpose: "preview", Session: uuid.New(),
			Target: "cs:1:0", Expiry: now.Add(time.Minute), Payload: payload}
		if err := s.Put(ctx, in, now); err != nil {
			t.Fatal(err)
		}
		got, ok, err := s.Take(ctx, in.Nonce, now)
		if err != nil || !ok {
			t.Fatalf("take: ok=%v err=%v", ok, err)
		}
		if got.Purpose != in.Purpose || got.Session != in.Session || got.Target != in.Target ||
			got.Expiry.Unix() != in.Expiry.Unix() || string(got.Payload) != string(payload) || (payload == nil) != (got.Payload == nil) {
			t.Fatalf("got %+v, want %+v", got, in)
		}
		if _, ok, _ := s.Take(ctx, in.Nonce, now); ok {
			t.Error("second take succeeded")
		}
	}
}

func TestStoreTwoServersOneWinner_Integration(t *testing.T) {
	dbA, dbB := openDB(t), openDB(t)
	a, b, p, ctx, now := NewStore(dbA), NewStore(dbB), prefix(t, dbA), context.Background(), time.Now()
	in := Ticket{Nonce: p + "x", Purpose: "preview", Session: uuid.New(), Target: "e", Expiry: now.Add(time.Minute)}
	if err := a.Put(ctx, in, now); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			if _, ok, err := s.Take(ctx, in.Nonce, now); err != nil {
				t.Error(err)
			} else if ok {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d winners, want 1", wins.Load())
	}
}

func TestStoreExpiredIsSpentAndMiss_Integration(t *testing.T) {
	db := openDB(t)
	s, p, ctx, now := NewStore(db), prefix(t, db), context.Background(), time.Now()
	in := Ticket{Nonce: p + "x", Purpose: "preview", Session: uuid.New(), Target: "e", Expiry: now.Add(60 * time.Second)}
	if err := s.Put(ctx, in, now); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Take(ctx, in.Nonce, now.Add(61*time.Second)); ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if n := count(t, db, p); n != 0 {
		t.Errorf("%d rows left", n)
	}
}

func TestStoreCaps_Integration(t *testing.T) {
	db := openDB(t)
	s, p, ctx, now := NewStore(db), prefix(t, db), context.Background(), time.Now()
	// Start from a clean table so the cap is measured on this test's rows.
	if _, err := db.Pool().Exec(ctx, `DELETE FROM hatamoto_frame_tickets`); err != nil {
		t.Fatal(err)
	}
	var last string
	for i := range MaxTickets + 5 {
		last = p + uuid.NewString()
		in := Ticket{Nonce: last, Purpose: "preview", Session: uuid.New(), Target: "e",
			Expiry: now.Add(time.Minute + time.Duration(i)*time.Millisecond)}
		if err := s.Put(ctx, in, now); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, db, p); n > MaxTickets {
		t.Fatalf("%d rows, cap %d", n, MaxTickets)
	}
	if _, ok, _ := s.Take(ctx, last, now); !ok {
		t.Error("the newest ticket did not survive")
	}
}

func TestStoreSweepKeepsRowsWithinGrace_Integration(t *testing.T) {
	db := openDB(t)
	s, p, ctx, now := NewStore(db), prefix(t, db), context.Background(), time.Now()
	mk := func(name string, exp time.Time) Ticket {
		return Ticket{Nonce: p + name, Purpose: "preview", Session: uuid.New(), Target: "e", Expiry: exp}
	}
	// "recent" looks expired only to a clock a little ahead; "old" is long gone.
	for _, tk := range []Ticket{mk("recent", now.Add(-30*time.Second)), mk("old", now.Add(-5*time.Minute))} {
		if err := s.Put(ctx, tk, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Put(ctx, mk("new", now.Add(time.Minute)), now); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, p); n != 2 {
		t.Fatalf("%d rows, want 2 (recent and new)", n)
	}
	if _, ok, _ := s.Take(ctx, p+"old", now); ok {
		t.Error("old row survived the sweep")
	}
}
