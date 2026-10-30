//go:build integration

package agents

import (
	"context"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// End to end against a migrated database (migrations 009-011):
// DATABASE_URL=... go test -tags=integration ./internal/agents -run VerifyWorker
func TestVerifyWorker_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	path := "/it-verify-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_agent_hits WHERE path = $1`, path)
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_agent_daily WHERE path = $1`, path)
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_agent_ip_ranges WHERE key = 'it-roundtrip'`)
	})

	good, bad := netip.MustParseAddr("20.171.206.77"), netip.MustParseAddr("203.0.113.77")
	now := time.Now().UTC()
	mk := func(ip netip.Addr, at time.Time) Hit {
		ip2 := ip
		return Hit{At: at, AgentID: "gptbot", Operator: "OpenAI", Purpose: PurposeTraining, Verification: VerificationPending,
			IP: &ip2, Method: "GET", Path: path, Status: 200, Source: SourceMiddleware}
	}
	if err := NewRepository(db).InsertHits(ctx, []Hit{mk(good, now), mk(good, now), mk(bad, now)}); err != nil {
		t.Fatal(err)
	}

	// Persisted ranges round-trip into a fresh store.
	rr := NewRangeRepository(db)
	if err := rr.SaveRanges(ctx, "it-roundtrip", parseFixture(t, "openai.json"), []string{"https://example.test/x.json"}, now); err != nil {
		t.Fatal(err)
	}
	loaded := NewRangeStore()
	if err := loaded.Load(ctx, rr); err != nil || !loaded.Has("it-roundtrip") {
		t.Fatalf("persisted ranges not loaded: %v", err)
	}

	store := NewRangeStore()
	store.Set("openai-gptbot", parseFixture(t, "openai.json"))
	w := NewVerifyWorker(NewVerifyRepository(db), Default(), NewVerifier(store, &fakeResolver{}), nil, store, nil, discard)

	counts := func() (verified, spoofed, unverifiable, hits int64) {
		t.Helper()
		err := db.Pool().QueryRow(ctx, `SELECT COALESCE(sum(verified_hits),0), COALESCE(sum(spoofed_hits),0),
			COALESCE(sum(unverifiable_hits),0), COALESCE(sum(hits),0) FROM hatamoto_agent_daily WHERE path = $1`, path).
			Scan(&verified, &spoofed, &unverifiable, &hits)
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	for run := 1; run <= 2; run++ { // the second run must change nothing
		if err := w.RunVerify(ctx); err != nil {
			t.Fatal(err)
		}
		v, s, u, h := counts()
		if v != 2 || s != 1 || u != 0 || h != 3 {
			t.Fatalf("run %d: verified=%d spoofed=%d unverifiable=%d hits=%d, want 2/1/0/3", run, v, s, u, h)
		}
	}
	rows, err := db.Pool().Query(ctx, `SELECT host(ip), verification FROM hatamoto_agent_hits WHERE path = $1`, path)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var ip, state string
		if err := rows.Scan(&ip, &state); err != nil {
			t.Fatal(err)
		}
		want := VerificationVerified
		if ip == bad.String() {
			want = VerificationSpoofed
		}
		if state != want {
			t.Errorf("%s = %s, want %s", ip, state, want)
		}
	}
}

// A truncated pending row is never range-checked: it is not listed as pending,
// is retired to unverifiable with consistent counters, while an untruncated
// non-range IP still ends spoofed.
func TestVerifyTruncatedNeverChecked_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	path := "/it-trunc-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_agent_hits WHERE path = $1`, path)
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_agent_daily WHERE path = $1`, path)
	})

	// 20.171.206.0/24 is inside the published range: if the truncated network
	// address were checked it would come out verified.
	inRange, spoof := netip.MustParseAddr("20.171.206.0"), netip.MustParseAddr("203.0.113.77")
	now := time.Now().UTC()
	mk := func(ip netip.Addr) Hit {
		ip2 := ip
		return Hit{At: now, AgentID: "gptbot", Operator: "OpenAI", Purpose: PurposeTraining, Verification: VerificationPending,
			IP: &ip2, Method: "GET", Path: path, Status: 200, Source: SourceBackend}
	}
	if err := NewRepository(db).InsertHits(ctx, []Hit{mk(inRange), mk(spoof)}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_agent_hits SET ip_truncated = true WHERE path = $1 AND host(ip) = $2`, path, inRange.String()); err != nil {
		t.Fatal(err)
	}

	repo := NewVerifyRepository(db)
	pairs, err := repo.PendingPairs(ctx, 10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pairs {
		if p.IP == inRange {
			t.Fatalf("truncated pending row listed for verification: %+v", p)
		}
	}

	store := NewRangeStore()
	store.Set("openai-gptbot", parseFixture(t, "openai.json"))
	w := NewVerifyWorker(repo, Default(), NewVerifier(store, &fakeResolver{}), nil, store, nil, discard)
	for run := 1; run <= 2; run++ {
		if err := w.RunVerify(ctx); err != nil {
			t.Fatal(err)
		}
		var v, s, u, h int64
		err := db.Pool().QueryRow(ctx, `SELECT COALESCE(sum(verified_hits),0), COALESCE(sum(spoofed_hits),0),
			COALESCE(sum(unverifiable_hits),0), COALESCE(sum(hits),0) FROM hatamoto_agent_daily WHERE path = $1`, path).Scan(&v, &s, &u, &h)
		if err != nil {
			t.Fatal(err)
		}
		if v != 0 || s != 1 || u != 1 || h != 2 {
			t.Fatalf("run %d: verified=%d spoofed=%d unverifiable=%d hits=%d, want 0/1/1/2", run, v, s, u, h)
		}
	}
	var truncState, spoofState string
	if err := db.Pool().QueryRow(ctx, `SELECT verification FROM hatamoto_agent_hits WHERE path = $1 AND ip_truncated`, path).Scan(&truncState); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool().QueryRow(ctx, `SELECT verification FROM hatamoto_agent_hits WHERE path = $1 AND NOT ip_truncated`, path).Scan(&spoofState); err != nil {
		t.Fatal(err)
	}
	if truncState != VerificationUnverifiable || spoofState != VerificationSpoofed {
		t.Errorf("truncated=%s untruncated=%s, want unverifiable/spoofed", truncState, spoofState)
	}
}
