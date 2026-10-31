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

// A verified search hit with a page writes the sibling first-crawl row; spoofed
// and AI hits never do; verifying an older hit lowers first_at; the AI table is
// untouched.
func TestVerifySearchFirstCrawl_Integration(t *testing.T) {
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

	marker := "it-sfc-" + uuid.NewString()[:8]
	schemaID, okID, spoofID, aiID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		p := db.Pool()
		_, _ = p.Exec(ctx, `DELETE FROM hatamoto_agent_hits WHERE path LIKE $1`, "/"+marker+"%")
		_, _ = p.Exec(ctx, `DELETE FROM hatamoto_agent_daily WHERE path LIKE $1`, "/"+marker+"%")
		_, _ = p.Exec(ctx, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, schemaID)
		_, _ = p.Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
	})
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ($1, $2, $2, '{}')`, schemaID, marker)
	exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, published_at) VALUES
		($1,$4,'A',$5,'published',now() - interval '2 days'), ($2,$4,'B',$6,'published',now()),
		($3,$4,'C',$7,'published',now())`, okID, spoofID, aiID, schemaID, marker+"-a", marker+"-b", marker+"-c")

	good, bad, aiIP := netip.MustParseAddr("66.249.66.1"), netip.MustParseAddr("203.0.113.9"), netip.MustParseAddr("20.171.206.9")
	now := time.Now().UTC()
	mk := func(agent string, p Purpose, ip netip.Addr, id uuid.UUID, path string, status int, at time.Time) Hit {
		ip2, id2 := ip, id
		return Hit{At: at, AgentID: agent, Operator: "Op", Purpose: p, Verification: VerificationPending,
			IP: &ip2, Method: "GET", Path: path, EntityID: &id2, Status: status, Source: SourceBackend}
	}
	late, early := now.Add(-time.Hour), now.Add(-3*time.Hour)
	pathOK, pathSpoof, pathAI := "/"+marker+"-a", "/"+marker+"-b", "/"+marker+"-c"
	if err := NewRepository(db).InsertHits(ctx, []Hit{
		mk("googlebot", PurposeSearch, good, okID, pathOK, 200, late),
		mk("googlebot", PurposeSearch, good, okID, pathOK, 404, early), // an error is not a crawl
		mk("googlebot", PurposeSearch, bad, spoofID, pathSpoof, 200, late),
		mk("gptbot", PurposeTraining, aiIP, aiID, pathAI, 200, late),
	}); err != nil {
		t.Fatal(err)
	}

	vr := NewVerifyRepository(db)
	apply := func(agent string, ip netip.Addr, st State) {
		t.Helper()
		if _, err := vr.ApplyVerification(ctx, PendingPair{AgentID: agent, IP: ip}, st, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	firstAt := func(id uuid.UUID) (at time.Time, ok bool) {
		t.Helper()
		err := db.Pool().QueryRow(ctx, `SELECT first_at FROM hatamoto_agent_search_first_crawl WHERE entity_id = $1 AND agent_id = 'googlebot'`, id).Scan(&at)
		return at, err == nil
	}
	apply("googlebot", bad, StateSpoofed)
	apply("gptbot", aiIP, StateVerified)
	if _, ok := firstAt(spoofID); ok {
		t.Error("a spoofed hit wrote a first crawl")
	}
	var n int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_agent_search_first_crawl WHERE entity_id = $1`, aiID).Scan(&n); err != nil || n != 0 {
		t.Errorf("an AI hit wrote a search first crawl (%d, %v)", n, err)
	}
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_agent_first_crawl WHERE entity_id = $1`, aiID).Scan(&n); err != nil || n != 0 {
		t.Errorf("the AI first crawl table was written by verification (%d, %v)", n, err)
	}

	apply("googlebot", good, StateVerified)
	at, ok := firstAt(okID)
	if !ok || at.Sub(late).Abs() > time.Second {
		t.Fatalf("first crawl = %v (%v), want the 200 hit at %v", at, ok, late)
	}
	var published bool
	if err := db.Pool().QueryRow(ctx, `SELECT published_at IS NOT NULL FROM hatamoto_agent_search_first_crawl WHERE entity_id = $1`, okID).Scan(&published); err != nil || !published {
		t.Errorf("published_at not copied (%v)", err)
	}

	// An older served hit verified later lowers first_at.
	older := now.Add(-5 * time.Hour)
	if err := NewRepository(db).InsertHits(ctx, []Hit{mk("googlebot", PurposeSearch, good, okID, pathOK, 200, older)}); err != nil {
		t.Fatal(err)
	}
	apply("googlebot", good, StateVerified)
	if at, _ := firstAt(okID); at.Sub(older).Abs() > time.Second {
		t.Errorf("first_at = %v, want lowered to %v", at, older)
	}
	// And a later verification of a newer hit never raises it.
	if err := NewRepository(db).InsertHits(ctx, []Hit{mk("googlebot", PurposeSearch, good, okID, pathOK, 200, now)}); err != nil {
		t.Fatal(err)
	}
	apply("googlebot", good, StateVerified)
	if at, _ := firstAt(okID); at.Sub(older).Abs() > time.Second {
		t.Errorf("first_at = %v, must stay %v", at, older)
	}

	// The seed rebuilds the same rows from the verified hits; running it again changes nothing.
	exec(`DELETE FROM hatamoto_agent_search_first_crawl WHERE entity_id = $1`, okID)
	for run := 0; run < 2; run++ {
		if _, err := SeedSearchFirstCrawl(ctx, db); err != nil {
			t.Fatal(err)
		}
		if at, ok := firstAt(okID); !ok || at.Sub(older).Abs() > time.Second {
			t.Errorf("seed run %d: first_at = %v (%v), want %v", run, at, ok, older)
		}
	}
	if _, ok := firstAt(spoofID); ok {
		t.Error("the seed took a spoofed hit")
	}
}
