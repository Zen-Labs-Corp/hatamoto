package agents

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type applied struct {
	pair   PendingPair
	state  State
	before time.Time
}

type fakeVerifyRepo struct {
	mu      sync.Mutex
	pairs   []PendingPair
	listErr error
	applyEr error
	applied []applied
	listed  int
	swept   int
	sweepEr error
}

func (f *fakeVerifyRepo) SweepStalePending(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.swept++
	return 0, f.sweepEr
}

func (f *fakeVerifyRepo) PendingPairs(context.Context, int) ([]PendingPair, error) {
	f.listed++
	return f.pairs, f.listErr
}

func (f *fakeVerifyRepo) ApplyVerification(_ context.Context, p PendingPair, s State, before time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, applied{p, s, before})
	return 1, f.applyEr
}

type mapVerifier map[string]State

func (m mapVerifier) Verify(_ context.Context, a *Agent, ip netip.Addr) State {
	return m[a.ID+"|"+ip.String()]
}

type fakeRefresher struct {
	rep   RefreshReport
	calls int
}

func (f *fakeRefresher) Refresh(context.Context) RefreshReport { f.calls++; return f.rep }

func TestRunVerifyAppliesStates(t *testing.T) {
	now := time.Now()
	repo := &fakeVerifyRepo{pairs: []PendingPair{
		{"gptbot", netip.MustParseAddr("20.171.206.9"), now.Add(-time.Minute), false},
		{"gptbot", netip.MustParseAddr("8.8.8.8"), now.Add(-time.Minute), false},
		{"googlebot", netip.MustParseAddr("1.1.1.1"), now.Add(-2 * time.Minute), false}, // still pending, young
		{"googlebot", netip.MustParseAddr("2.2.2.2"), now.Add(-25 * time.Hour), false},  // pending and old
		{"gptbot", netip.Addr{}, now.Add(-time.Minute), false},                          // no ip
		{"retired-agent", netip.MustParseAddr("3.3.3.3"), now.Add(-time.Minute), false}, // not in registry
	}}
	ver := mapVerifier{
		"gptbot|20.171.206.9": StateVerified,
		"gptbot|8.8.8.8":      StateSpoofed,
		"googlebot|1.1.1.1":   StatePending,
		"googlebot|2.2.2.2":   StatePending,
		"gptbot|invalid IP":   StateUnverifiable,
	}
	w := NewVerifyWorker(repo, Default(), ver, nil, nil, nil, discard)
	if err := w.RunVerify(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := map[string]applied{}
	for _, a := range repo.applied {
		got[a.pair.AgentID+"|"+a.pair.IP.String()] = a
	}
	if len(got) != 5 {
		t.Fatalf("applied %d pairs, want 5 (young pending skipped): %+v", len(got), repo.applied)
	}
	if got["gptbot|20.171.206.9"].state != StateVerified || got["gptbot|8.8.8.8"].state != StateSpoofed {
		t.Errorf("states wrong: %+v", got)
	}
	if _, ok := got["googlebot|1.1.1.1"]; ok {
		t.Error("young pending pair must be retried later, not applied")
	}
	old := got["googlebot|2.2.2.2"]
	if old.state != StateUnverifiable || old.before.IsZero() || time.Since(old.before) < 23*time.Hour {
		t.Errorf("old pending must be forced unverifiable with a 24h cutoff: %+v", old)
	}
	if got["retired-agent|3.3.3.3"].state != StateUnverifiable {
		t.Error("unknown agent id must be unverifiable")
	}
	if got["gptbot|invalid IP"].state != StateUnverifiable {
		t.Error("pair without ip must be unverifiable")
	}
}

func TestRunVerifyErrors(t *testing.T) {
	repo := &fakeVerifyRepo{listErr: errors.New("db down")}
	w := NewVerifyWorker(repo, Default(), mapVerifier{}, nil, nil, nil, discard)
	if err := w.RunVerify(context.Background()); err == nil {
		t.Error("list failure must surface")
	}
	// An apply failure on one pair does not stop the others or fail the run.
	repo = &fakeVerifyRepo{applyEr: errors.New("boom"), pairs: []PendingPair{
		{"gptbot", netip.MustParseAddr("8.8.8.8"), time.Now(), false}, {"gptbot", netip.MustParseAddr("8.8.4.4"), time.Now(), false},
	}}
	w = NewVerifyWorker(repo, Default(), mapVerifier{"gptbot|8.8.8.8": StateSpoofed, "gptbot|8.8.4.4": StateSpoofed}, nil, nil, nil, discard)
	if err := w.RunVerify(context.Background()); err != nil || len(repo.applied) != 2 {
		t.Errorf("err=%v applied=%d", err, len(repo.applied))
	}
}

// Before any range list is loaded or fetched, verifying would mark every
// range-only agent unverifiable; the worker waits instead.
func TestRunVerifyWaitsForRanges(t *testing.T) {
	repo := &fakeVerifyRepo{pairs: []PendingPair{{"gptbot", netip.MustParseAddr("8.8.8.8"), time.Now(), false}}}
	store := NewRangeStore()
	ref := &fakeRefresher{}
	w := NewVerifyWorker(repo, Default(), mapVerifier{"gptbot|8.8.8.8": StateUnverifiable}, ref, store, &fakePersister{}, discard)

	_ = w.RunVerify(context.Background())
	if repo.listed != 0 {
		t.Fatal("must not touch hits before ranges are available")
	}
	// A completed refresh attempt (even with every key failed) ends the wait: A4.
	ref.rep = RefreshReport{Failed: []RefreshFailure{{Key: "openai-gptbot", Err: errors.New("offline")}}}
	_ = w.RunRefresh(context.Background())
	_ = w.RunVerify(context.Background())
	if len(repo.applied) != 1 || repo.applied[0].state != StateUnverifiable {
		t.Errorf("applied = %+v", repo.applied)
	}
}

// A ranges-only agent whose own key never loaded (while the gate is open
// because another key is) stays pending when young and is forced unverifiable
// only past pendingMaxAge, through the real verifier.
func TestRunVerifyUnloadedKeyPendingThenUnverifiable(t *testing.T) {
	now := time.Now()
	ip := netip.MustParseAddr("160.79.104.1")
	store := NewRangeStore()
	store.Set("google", parseFixture(t, "google.json")) // opens the gate; anthropic missing
	run := func(oldest time.Time) *fakeVerifyRepo {
		repo := &fakeVerifyRepo{pairs: []PendingPair{{"claudebot", ip, oldest, false}}}
		w := NewVerifyWorker(repo, Default(), NewVerifier(store, &fakeResolver{}), nil, store, nil, discard)
		if err := w.RunVerify(context.Background()); err != nil {
			t.Fatal(err)
		}
		return repo
	}
	if repo := run(now.Add(-time.Hour)); len(repo.applied) != 0 {
		t.Errorf("young pair must stay pending, applied %+v", repo.applied)
	}
	repo := run(now.Add(-25 * time.Hour))
	if len(repo.applied) != 1 || repo.applied[0].state != StateUnverifiable || repo.applied[0].before.IsZero() {
		t.Errorf("old pair must be forced unverifiable with cutoff: %+v", repo.applied)
	}
}

func TestRunVerifyUsesPersistedRanges(t *testing.T) {
	repo := &fakeVerifyRepo{pairs: []PendingPair{{"gptbot", netip.MustParseAddr("8.8.8.8"), time.Now(), false}}}
	persist := &fakePersister{m: map[string][]netip.Prefix{"openai-gptbot": parseFixture(t, "openai.json")}}
	w := NewVerifyWorker(repo, Default(), mapVerifier{"gptbot|8.8.8.8": StateSpoofed}, nil, NewRangeStore(), persist, discard)
	_ = w.RunVerify(context.Background())
	if len(repo.applied) != 1 {
		t.Errorf("persisted ranges must satisfy readiness, applied=%d", len(repo.applied))
	}
}

// ISC-25: a refresh failure leaves stored hits alone and never fails the job.
func TestRunRefreshFailureTouchesNoHits(t *testing.T) {
	repo := &fakeVerifyRepo{}
	ref := &fakeRefresher{rep: RefreshReport{Failed: []RefreshFailure{{Key: "google", Err: errors.New("dial tcp: i/o timeout")}}}}
	w := NewVerifyWorker(repo, Default(), mapVerifier{}, ref, NewRangeStore(), nil, discard)
	if err := w.RunRefresh(context.Background()); err != nil {
		t.Fatalf("refresh failure must not fail the job: %v", err)
	}
	if ref.calls != 1 || repo.listed != 0 || len(repo.applied) != 0 {
		t.Errorf("calls=%d listed=%d applied=%d", ref.calls, repo.listed, len(repo.applied))
	}
}

func TestNilWorkerIsNoop(t *testing.T) {
	var w *VerifyWorker
	if w.RunVerify(context.Background()) != nil || w.RunRefresh(context.Background()) != nil {
		t.Error("nil worker must be a no-op")
	}
}

// A deadlock/serialization abort rolls the pair back: it stays pending and is
// not counted as an error.
func TestVerifyPairRollbackStaysPending(t *testing.T) {
	repo := &fakeVerifyRepo{applyEr: fmt.Errorf("lock: %w", &pgconn.PgError{Code: "40P01"})}
	w := NewVerifyWorker(repo, Default(), mapVerifier{"gptbot|8.8.8.8": StateSpoofed}, nil, nil, nil, discard)
	n, pending, err := w.verifyPair(context.Background(), PendingPair{"gptbot", netip.MustParseAddr("8.8.8.8"), time.Now(), false})
	if err != nil || !pending || n != 0 {
		t.Errorf("n=%d pending=%v err=%v, want 0/true/nil", n, pending, err)
	}
	repo.applyEr = errors.New("boom")
	if _, _, err := w.verifyPair(context.Background(), PendingPair{"gptbot", netip.MustParseAddr("8.8.8.8"), time.Now(), false}); err == nil {
		t.Error("other errors must surface")
	}
}

// H4: pending hits older than the window are swept on every run, even before
// ranges are loaded, and a sweep failure never blocks verification.
func TestRunVerifySweepsStalePending(t *testing.T) {
	repo := &fakeVerifyRepo{sweepEr: errors.New("boom"), pairs: []PendingPair{{"gptbot", netip.MustParseAddr("8.8.8.8"), time.Now(), false}}}
	w := NewVerifyWorker(repo, Default(), mapVerifier{"gptbot|8.8.8.8": StateSpoofed}, nil, nil, nil, discard)
	if err := w.RunVerify(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.swept != 1 || len(repo.applied) != 1 {
		t.Errorf("swept=%d applied=%d, want 1/1", repo.swept, len(repo.applied))
	}
	gate := &fakeVerifyRepo{}
	w = NewVerifyWorker(gate, Default(), mapVerifier{}, nil, NewRangeStore(), &fakePersister{}, discard)
	_ = w.RunVerify(context.Background())
	if gate.swept != 1 {
		t.Errorf("sweep must run before the ranges gate, swept=%d", gate.swept)
	}
}

func TestSweepStaleSQLShape(t *testing.T) {
	for _, want := range []string{
		"verification = 'pending'", "occurred_at <= now() - interval '7 days'",
		"SET verification = 'unverifiable'", "unverifiable_hits = d.unverifiable_hits + c.n",
		"AT TIME ZONE 'UTC'", "COALESCE(status, 0)", "d.agent_id = c.agent_id",
		"OR ip_truncated", // truncated pending rows retire at any age
	} {
		if !strings.Contains(sweepStaleSQL, want) {
			t.Errorf("sweepStaleSQL missing %q", want)
		}
	}
	for _, want := range []string{`ORDER BY d.day, d.agent_id COLLATE "C", d.path COLLATE "C", d.status`, "FOR UPDATE OF d", "occurred_at <= now() - interval '7 days'", "OR ip_truncated"} {
		if !strings.Contains(lockStaleDailySQL, want) {
			t.Errorf("lockStaleDailySQL missing %q", want)
		}
	}
}

func TestLockDailySQLShape(t *testing.T) {
	for _, want := range []string{
		`ORDER BY d.day, d.agent_id COLLATE "C", d.path COLLATE "C", d.status`,
		"FOR UPDATE OF d", "verification = 'pending'", "AT TIME ZONE 'UTC'", "COALESCE(status, 0)",
		"AND NOT ip_truncated",
	} {
		if !strings.Contains(lockDailySQL, want) {
			t.Errorf("lockDailySQL missing %q", want)
		}
	}
}

func TestApplyVerificationSQLShape(t *testing.T) {
	for _, want := range []string{
		"verification = 'pending'", // only still-pending rows flip, so counters move once
		"IS NOT DISTINCT FROM $2::inet",
		"AT TIME ZONE 'UTC'", // same day key as the writer's rollup
		"UPDATE hatamoto_agent_hits",
		"UPDATE hatamoto_agent_daily",
		"verified_hits", "spoofed_hits", "unverifiable_hits",
		"d.agent_id = $1", "d.path = c.path", "d.status = c.status", "d.day = c.day",
		"COALESCE(status, 0)", "AND NOT ip_truncated",
	} {
		if !strings.Contains(applyVerificationSQL, want) {
			t.Errorf("applyVerificationSQL missing %q", want)
		}
	}
	for _, want := range []string{"verification = 'pending'", "interval '7 days'", "LIMIT $1", "GROUP BY 1, 2", "AND NOT ip_truncated"} {
		if !strings.Contains(pendingPairsSQL, want) {
			t.Errorf("pendingPairsSQL missing %q", want)
		}
	}
}

// With ranges refresh switched off there is nothing to wait for: verification
// runs at once, from stored ranges and reverse DNS.
func TestRunVerifyRunsWhenRangeRefreshIsOff(t *testing.T) {
	repo := &fakeVerifyRepo{pairs: []PendingPair{{"gptbot", netip.MustParseAddr("8.8.8.8"), time.Now(), false}}}
	w := NewVerifyWorker(repo, Default(), mapVerifier{"gptbot|8.8.8.8": StateUnverifiable}, nil, NewRangeStore(), &fakePersister{}, discard)
	w.NoRangeRefresh()
	_ = w.RunVerify(context.Background())
	if len(repo.applied) != 1 {
		t.Errorf("applied = %+v, want the pending hit decided", repo.applied)
	}
}

// A pair that carries a truncated address is never handed to the verifier.
func TestVerifyPairRefusesTruncated(t *testing.T) {
	repo := &fakeVerifyRepo{}
	w := NewVerifyWorker(repo, Default(), mapVerifier{"gptbot|203.0.113.0": StateSpoofed}, nil, nil, nil, discard)
	n, pending, err := w.verifyPair(context.Background(), PendingPair{"gptbot", netip.MustParseAddr("203.0.113.0"), time.Now(), true})
	if n != 0 || pending || err != nil || len(repo.applied) != 0 {
		t.Errorf("n=%d pending=%v err=%v applied=%v, want nothing applied", n, pending, err, repo.applied)
	}
}

// The three pair statements must select the same rows.
func TestPairStatementsShareTruncationPredicate(t *testing.T) {
	for name, q := range map[string]string{"pending": pendingPairsSQL, "lock": lockDailySQL, "apply": applyVerificationSQL} {
		if !strings.Contains(q, "NOT ip_truncated") {
			t.Errorf("%s statement lacks the truncation predicate", name)
		}
	}
}
