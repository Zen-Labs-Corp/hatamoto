package supervisor

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- fakes

type fakeProc struct {
	onStop func() // runs when Stop is called (a stop request arriving mid-switch)
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	err    error
}

func newFakeProc() *fakeProc { return &fakeProc{done: make(chan struct{})} }

func (f *fakeProc) exit(err error) {
	f.once.Do(func() {
		f.mu.Lock()
		f.err = err
		f.mu.Unlock()
		close(f.done)
	})
}
func (f *fakeProc) Pid() int              { return 4242 }
func (f *fakeProc) Done() <-chan struct{} { return f.done }
func (f *fakeProc) Err() error            { f.mu.Lock(); defer f.mu.Unlock(); return f.err }
func (f *fakeProc) Stop(time.Duration) bool {
	if f.onStop != nil {
		f.onStop()
	}
	f.exit(nil)
	return false
}

type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) { l.mu.Lock(); defer l.mu.Unlock(); return l.b.Write(p) }
func (l *logBuf) String() string              { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// harness runs a sup with fake children. crashAfter maps a version dir name
// (or "bootstrap") to how long its child lives before crashing; absent means
// it stays up. gateFail maps a version to a gate failure reason.
type harness struct {
	t          *testing.T
	l          Layout
	boot       string
	s          *sup
	logs       *logBuf
	mu         sync.Mutex
	starts     []string // version names in launch order
	last       string
	crashAfter map[string]time.Duration
	gateFail   map[string]string
	gateBlock  bool               // gate waits for ctx instead of deciding
	verifyFail map[string]bool    // versions whose stored copy fails verification
	rootStatus int                // status the fake smoke reports for GET / (default 200)
	bootFaults []string           // boot faults the fake smoke reports
	gateCfgs   []GateConfig       // every gate call, in order
	bootWindow time.Duration      // default 10 s
	onStop     func()             // set on every fake child
	gateErrs   map[string][]error // per version: what the next gate calls return, in order
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, l: NewLayout(t.TempDir()), logs: &logBuf{},
		crashAfter: map[string]time.Duration{}, gateFail: map[string]string{}}
	if err := os.MkdirAll(h.l.Releases, 0o755); err != nil {
		t.Fatal(err)
	}
	h.boot = filepath.Join(t.TempDir(), "bootstrap")
	if err := os.WriteFile(h.boot, []byte("boot"), 0o755); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) name(path string) string {
	if path == h.boot {
		return RunBootstrap
	}
	return filepath.Base(filepath.Dir(path))
}

// build creates the sup; call after seeding disk state.
func (h *harness) build() *sup {
	cfg := Config{
		Data: h.l.Data, Bootstrap: h.boot, BootstrapVersion: "dev-build", ReadyURL: "http://127.0.0.1:1/ready",
		StopGrace: time.Second, BootWindow: cmpOr(h.bootWindow, 10*time.Second), BackoffBase: time.Millisecond,
		Log: slog.New(slog.NewTextHandler(h.logs, nil)),
		verify: func(v string) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.verifyFail[v] {
				return errors.New("sha mismatch")
			}
			return nil
		},
		launch: func(path string, env []string) (proc, error) {
			n := h.name(path)
			h.mu.Lock()
			h.starts = append(h.starts, n)
			h.last = n
			after, crash := h.crashAfter[n]
			h.mu.Unlock()
			p := newFakeProc()
			p.onStop = h.onStop
			if crash {
				time.AfterFunc(after, func() { p.exit(errors.New("exit status 1")) })
			}
			return p, nil
		},
		gate: func(ctx context.Context, g GateConfig, crashed <-chan struct{}) (SmokeResult, error) {
			h.mu.Lock()
			n, block := h.last, h.gateBlock
			why := h.gateFail[n]
			root, faults := h.rootStatus, h.bootFaults
			h.gateCfgs = append(h.gateCfgs, g)
			h.mu.Unlock()
			if root == 0 {
				root = 200
			}
			var scripted error
			if errs := h.gateErrs[n]; len(errs) > 0 && !g.Initial {
				scripted, h.gateErrs[n] = errs[0], errs[1:]
			}
			if scripted != nil {
				return SmokeResult{}, scripted
			}
			if block {
				<-ctx.Done()
				return SmokeResult{}, ctx.Err()
			}
			select {
			case <-crashed:
				return SmokeResult{}, errChildExited
			case <-ctx.Done():
				return SmokeResult{}, ctx.Err()
			case <-time.After(30 * time.Millisecond):
			}
			if why != "" {
				return SmokeResult{}, errors.New(why)
			}
			return SmokeResult{Pass: true, PluginErrors: []string{"p1"}, BootFaults: faults, Checks: []SmokeCheck{{Name: "GET /", OK: true, Status: root}}}, nil
		},
	}
	s, err := newSup(cfg)
	if err != nil {
		h.t.Fatal(err)
	}
	h.s = s
	return s
}

func (h *harness) run() (cancel func(), done chan error) {
	ctx, cancelCtx := context.WithCancel(context.Background())
	done = make(chan error, 1)
	fin := make(chan struct{})
	go func() { done <- h.s.loop(ctx); close(fin) }()
	h.t.Cleanup(func() { cancelCtx(); <-fin })
	return func() { cancelCtx() }, done
}

func (h *harness) release(v string) { mkRelease(h.t, h.l, v, 0o755) }

func (h *harness) seed(st *State, current, previous string) {
	h.t.Helper()
	sha, err := fileSHA(h.boot)
	if err != nil {
		h.t.Fatal(err)
	}
	st.BootstrapSHA256 = sha
	if err := SaveState(h.l.StatePath(), st); err != nil {
		h.t.Fatal(err)
	}
	if current != "" {
		if err := h.l.SwapSymlink("current", current); err != nil {
			h.t.Fatal(err)
		}
	}
	if previous != "" {
		if err := h.l.SwapSymlink("previous", previous); err != nil {
			h.t.Fatal(err)
		}
	}
}

func (h *harness) state() *State {
	h.t.Helper()
	st, err := LoadState(h.l.StatePath())
	if err != nil {
		h.t.Fatal(err)
	}
	return st
}

func (h *harness) startsList() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.starts...)
}

func (h *harness) lastStart() string { h.mu.Lock(); defer h.mu.Unlock(); return h.last }

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func idleWith(h *harness) func() bool {
	return func() bool {
		st, err := LoadState(h.l.StatePath())
		return err == nil && st.Phase == "idle" && st.Pending == nil && len(h.startsList()) > 0
	}
}

func pendingApply(v, from string) *Pending {
	return &Pending{Kind: "apply", Version: v, From: from, RequestedAt: time.Now().UTC()}
}

// awaitEvents waits for the first event: the supervisor saves the idle state
// before it appends the switch event, so idleWith alone can precede it.
func awaitEvents(t *testing.T, h *harness) []Event {
	t.Helper()
	var evs []Event
	eventually(t, "a switch event", func() bool {
		evs = nil
		if f, err := os.Open(h.l.EventsPath()); err == nil {
			f.Close()
			evs = readEvents(t, h.l.EventsPath())
		}
		return len(evs) > 0
	})
	return evs
}

func lastEvent(t *testing.T, h *harness) Event {
	t.Helper()
	evs := awaitEvents(t, h)
	return evs[len(evs)-1]
}

// ---- pure pieces

func TestCrashTracker(t *testing.T) {
	tr := crashTracker{window: time.Minute}
	for i, want := range []bool{false, false, true} {
		tr.record(time.Second)
		if tr.looping() != want {
			t.Errorf("after %d short crashes looping = %v", i+1, tr.looping())
		}
	}
	tr.record(2 * time.Minute) // lived through the window: starts over
	if tr.n != 1 || tr.looping() {
		t.Errorf("after a long-lived child n = %d", tr.n)
	}
	tr.record(time.Second)
	tr.record(time.Second)
	if !tr.looping() {
		t.Error("three consecutive short crashes after a reset not detected")
	}
	tr.reset()
	if tr.n != 0 {
		t.Error("reset")
	}
}

func TestBackoffSequence(t *testing.T) {
	base := 500 * time.Millisecond
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, w := range want {
		if got := backoff(base, i+1); got != w {
			t.Errorf("backoff(%d) = %v, want %v", i+1, got, w)
		}
	}
	if backoff(base, 1000) != maxBackoff {
		t.Error("huge n not capped")
	}
}

// ---- loop behaviour

func TestStopIsNotACrash(t *testing.T) {
	h := newHarness(t)
	h.build()
	cancel, done := h.run()
	eventually(t, "child start", func() bool { return len(h.startsList()) == 1 })
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("loop returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not return after stop")
	}
	if strings.Contains(h.logs.String(), "event=crash") || len(h.startsList()) != 1 {
		t.Errorf("stop treated as a crash; starts %v\n%s", h.startsList(), h.logs)
	}
	if _, err := os.Stat(h.l.PIDPath()); err == nil {
		t.Error("pid file left behind after a clean stop")
	}
}

func TestCrashLoopWithNoTargetKeepsRestarting(t *testing.T) {
	h := newHarness(t)
	h.crashAfter[RunBootstrap] = 0
	h.build()
	_, done := h.run()
	eventually(t, "restarts", func() bool { return len(h.startsList()) >= 6 })
	select {
	case err := <-done:
		t.Fatalf("supervisor exited: %v", err)
	default:
	}
	if !strings.Contains(h.logs.String(), "crash loop, restarting with backoff") {
		t.Errorf("missing no-target log:\n%s", h.logs)
	}
	if len(h.state().Bad) != 0 {
		t.Error("bootstrap bad-listed")
	}
}

func TestApplySuccess(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0", Pending: pendingApply("v1.0.1", "v1.0.0"), PluginErrors: []string{"old"}}, "v1.0.0", "")
	h.build()
	h.run()
	eventually(t, "idle", idleWith(h))
	st := h.state()
	if h.l.Link("current") != "v1.0.1" || h.l.Link("previous") != "v1.0.0" || st.CurrentSeen != "v1.0.1" || st.Run != RunRelease {
		t.Errorf("links current=%s previous=%s state=%+v", h.l.Link("current"), h.l.Link("previous"), st)
	}
	if h.lastStart() != "v1.0.1" || st.IsBad("v1.0.1") || st.LastError != "" || st.Attempts != 1 {
		t.Errorf("last start %s, state %+v", h.lastStart(), st)
	}
	if len(st.PluginErrors) != 1 || st.PluginErrors[0] != "p1" {
		t.Errorf("plugin errors = %v, want replaced by gate result", st.PluginErrors)
	}
	if st.SmokeBaseline["GET /"] != 200 {
		t.Errorf("smoke baseline = %v, want the gate result", st.SmokeBaseline)
	}
	if e := lastEvent(t, h); e.Phase != PhaseSuccess || e.From != "v1.0.0" || e.To != "v1.0.1" {
		t.Errorf("event = %+v", e)
	}
}

// A child that was not switched to (first start, restart) leaves the baseline
// for the next switch.
func TestInitialChildRecordsBaseline(t *testing.T) {
	h := newHarness(t)
	h.build()
	h.run()
	eventually(t, "baseline", func() bool { return h.state().SmokeBaseline["GET /"] == 200 })
	if got := h.state().PluginErrors; len(got) != 1 || got[0] != "p1" {
		t.Errorf("plugin errors = %v", got)
	}
}

func TestApplyGateFailureRollsBack(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.release("v0.9.0")
	h.gateFail["v1.0.1"] = "smoke failed: GET / (500)"
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0", Pending: pendingApply("v1.0.1", "v1.0.0")}, "v1.0.0", "v0.9.0")
	h.build()
	h.run()
	eventually(t, "idle", idleWith(h))
	eventually(t, "restored child", func() bool { return h.lastStart() == "v1.0.0" })
	st := h.state()
	if h.l.Link("current") != "v1.0.0" || h.l.Link("previous") != "v0.9.0" || st.CurrentSeen != "v1.0.0" {
		t.Errorf("links not restored: current=%s previous=%s", h.l.Link("current"), h.l.Link("previous"))
	}
	if !st.IsBad("v1.0.1") || !strings.Contains(st.LastError, "GET / (500)") {
		t.Errorf("bad list / last_error: %+v", st)
	}
	if e := lastEvent(t, h); e.Phase != PhaseRolledBack || !strings.Contains(e.Error, "500") {
		t.Errorf("event = %+v", e)
	}
	if !strings.Contains(h.logs.String(), "event=rollback") {
		t.Error("no rollback log line")
	}
}

func TestApplyCrashLoopRollsBackAndBadLists(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.crashAfter["v1.0.1"] = 0
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0", Pending: pendingApply("v1.0.1", "v1.0.0")}, "v1.0.0", "")
	h.build()
	h.run()
	eventually(t, "idle", idleWith(h))
	eventually(t, "restored child", func() bool { return h.lastStart() == "v1.0.0" })
	st := h.state()
	if !st.IsBad("v1.0.1") || !strings.Contains(st.LastError, "crash loop") || h.l.Link("current") != "v1.0.0" {
		t.Errorf("state %+v current %s", st, h.l.Link("current"))
	}
	n := 0
	for _, s := range h.startsList() {
		if s == "v1.0.1" {
			n++
		}
	}
	if n != crashLoopCount {
		t.Errorf("v1.0.1 started %d times, want %d", n, crashLoopCount)
	}
}

func TestApplyFromBootstrapRollsBackToBootstrap(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.1")
	h.gateFail["v1.0.1"] = "ready timeout"
	h.seed(&State{Run: RunBootstrap, Pending: pendingApply("v1.0.1", "bootstrap")}, "", "")
	h.build()
	h.run()
	eventually(t, "idle", idleWith(h))
	eventually(t, "bootstrap restarted", func() bool { return h.lastStart() == RunBootstrap })
	st := h.state()
	if h.l.Link("current") != "" || h.l.Link("previous") != "" || st.Run != RunBootstrap || st.PreviousIsBootstrap || !st.IsBad("v1.0.1") {
		t.Errorf("state %+v current=%q previous=%q", st, h.l.Link("current"), h.l.Link("previous"))
	}
}

func TestApplySuccessFromBootstrapMarksPreviousBootstrap(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.1")
	h.seed(&State{Run: RunBootstrap, Pending: pendingApply("v1.0.1", "bootstrap")}, "", "")
	h.build()
	h.run()
	eventually(t, "idle", idleWith(h))
	st := h.state()
	if h.l.Link("current") != "v1.0.1" || h.l.Link("previous") != "" || !st.PreviousIsBootstrap || st.Run != RunRelease {
		t.Errorf("state %+v", st)
	}
}

func TestRefusedApplyKeepsRunning(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0",
		Bad:     []BadEntry{{Version: "v1.0.1", Reason: "earlier"}},
		Pending: pendingApply("v1.0.1", "v1.0.0")}, "v1.0.0", "")
	h.build()
	h.run()
	eventually(t, "idle", idleWith(h))
	if h.l.Link("current") != "v1.0.0" || h.lastStart() != "v1.0.0" {
		t.Errorf("a bad-listed version was switched to: current=%s start=%s", h.l.Link("current"), h.lastStart())
	}
	if e := lastEvent(t, h); e.Phase != PhaseFailed || !strings.Contains(e.Error, "bad list") {
		t.Errorf("event = %+v", e)
	}
}

// Pending replayed after links were already flipped (supervisor died mid-switch).
func TestPendingReplayIsIdempotent(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	before := &LinkSnapshot{Current: "v1.0.0", Previous: "", Run: RunRelease, CurrentSeen: "v1.0.0"}
	p := pendingApply("v1.0.1", "v1.0.0")
	p.Before, p.StartedAt = before, time.Now().UTC()
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0", Phase: "probation", Attempts: 1, Pending: p}, "v1.0.1", "v1.0.0")
	h.build()
	h.run()
	eventually(t, "idle", idleWith(h))
	st := h.state()
	if h.l.Link("current") != "v1.0.1" || h.l.Link("previous") != "v1.0.0" || st.Attempts != 1 {
		t.Errorf("replay changed links: current=%s previous=%s attempts=%d", h.l.Link("current"), h.l.Link("previous"), st.Attempts)
	}
	if e := lastEvent(t, h); e.Phase != PhaseSuccess || e.From != "v1.0.0" {
		t.Errorf("event = %+v", e)
	}
}

// Same replay, but the gate fails: the snapshot from the first pass is restored.
func TestPendingReplayRollbackUsesSnapshot(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.release("v0.9.0")
	h.gateFail["v1.0.1"] = "nope"
	p := pendingApply("v1.0.1", "v1.0.0")
	p.Before, p.StartedAt = &LinkSnapshot{Current: "v1.0.0", Previous: "v0.9.0", Run: RunRelease, CurrentSeen: "v1.0.0"}, time.Now().UTC()
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.1", Phase: "probation", Pending: p}, "v1.0.1", "v1.0.0")
	h.build()
	h.run()
	eventually(t, "idle", idleWith(h))
	if h.l.Link("current") != "v1.0.0" || h.l.Link("previous") != "v0.9.0" {
		t.Errorf("current=%s previous=%s", h.l.Link("current"), h.l.Link("previous"))
	}
}

func TestManualRollbackBadListsRolledOffVersion(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.1",
		Pending: &Pending{Kind: "rollback", Version: "v1.0.0", RequestedAt: time.Now().UTC()}}, "v1.0.1", "v1.0.0")
	h.build()
	h.run()
	eventually(t, "idle", idleWith(h))
	st := h.state()
	if h.l.Link("current") != "v1.0.0" || h.l.Link("previous") != "v1.0.1" || h.lastStart() != "v1.0.0" {
		t.Errorf("links current=%s previous=%s start=%s", h.l.Link("current"), h.l.Link("previous"), h.lastStart())
	}
	if !st.IsBad("v1.0.1") || st.Bad[0].Reason != "manual rollback" {
		t.Errorf("bad = %+v", st.Bad)
	}
	if e := lastEvent(t, h); e.Phase != PhaseManualRollback || e.To != "v1.0.0" {
		t.Errorf("event = %+v", e)
	}
}

func TestFailedManualRollbackRestoresAndBadListsNothing(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.gateFail["v1.0.0"] = "ready timeout"
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.1",
		Pending: &Pending{Kind: "rollback", Version: "v1.0.0", RequestedAt: time.Now().UTC()}}, "v1.0.1", "v1.0.0")
	h.build()
	h.run()
	eventually(t, "idle", idleWith(h))
	eventually(t, "original restarted", func() bool { return h.lastStart() == "v1.0.1" })
	st := h.state()
	if h.l.Link("current") != "v1.0.1" || h.l.Link("previous") != "v1.0.0" || len(st.Bad) != 0 {
		t.Errorf("current=%s previous=%s bad=%+v", h.l.Link("current"), h.l.Link("previous"), st.Bad)
	}
	if e := lastEvent(t, h); e.Phase != PhaseFailed {
		t.Errorf("event = %+v", e)
	}
}

func TestRollbackToBootstrap(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.1")
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.1", PreviousIsBootstrap: true,
		Pending: &Pending{Kind: "rollback", Version: RunBootstrap, RequestedAt: time.Now().UTC()}}, "v1.0.1", "")
	h.build()
	h.run()
	eventually(t, "idle", idleWith(h))
	st := h.state()
	if h.lastStart() != RunBootstrap || st.Run != RunBootstrap || h.l.Link("current") != "v1.0.1" || !st.IsBad("v1.0.1") {
		t.Errorf("start %s state %+v", h.lastStart(), st)
	}
}

func TestSIGHUPTriggersPending(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0"}, "v1.0.0", "")
	s := h.build()
	h.run()
	eventually(t, "first child", func() bool { return h.lastStart() == "v1.0.0" })
	s.poke() // nothing pending: nothing happens
	time.Sleep(50 * time.Millisecond)
	if len(h.startsList()) != 1 {
		t.Fatalf("hup with nothing pending restarted the child: %v", h.startsList())
	}
	err := WithLock(h.l.LockPath(), func() error {
		st := h.state()
		st.Pending = pendingApply("v1.0.1", "v1.0.0")
		return SaveState(h.l.StatePath(), st)
	})
	if err != nil {
		t.Fatal(err)
	}
	s.poke()
	eventually(t, "switched", func() bool { return h.lastStart() == "v1.0.1" && idleWith(h)() })
	if got := h.startsList(); len(got) != 2 {
		t.Errorf("starts = %v", got)
	}
}

// A release switched to without a gate (offline apply) that crashes right
// after boot goes back to previous.
func TestCrashLoopOfUnprovenReleaseRollsBack(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.crashAfter["v1.0.1"] = 0
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.1", Unproven: "v1.0.1"}, "v1.0.1", "v1.0.0")
	h.build()
	h.run()
	eventually(t, "fallback child", func() bool { return h.lastStart() == "v1.0.0" })
	st := h.state()
	if h.l.Link("current") != "v1.0.0" || !st.IsBad("v1.0.1") || st.Bad[0].Reason != "crash loop" {
		t.Errorf("current=%s bad=%+v", h.l.Link("current"), st.Bad)
	}
	if e := lastEvent(t, h); e.Phase != PhaseRolledBack || e.From != "v1.0.1" || e.To != "v1.0.0" {
		t.Errorf("event = %+v", e)
	}
}

// A release that already passed a gate and then crashes five times in a row
// (database down at boot) is restarted with backoff: no rollback, no bad list.
func TestCrashLoopOfGatedReleaseNeverRollsBack(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.crashAfter["v1.0.1"] = 0
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.1"}, "v1.0.1", "v1.0.0")
	h.build()
	h.run()
	eventually(t, "five restarts", func() bool { return len(h.startsList()) >= 6 })
	st := h.state()
	if len(st.Bad) != 0 || h.l.Link("current") != "v1.0.1" || h.l.Link("previous") != "v1.0.0" {
		t.Errorf("gated release rolled back: bad=%+v current=%s", st.Bad, h.l.Link("current"))
	}
	for _, n := range h.startsList() {
		if n != "v1.0.1" {
			t.Fatalf("started %s after crashes; starts %v", n, h.startsList())
		}
	}
}

// Crash-loop rollback never lands on a bad-listed previous.
func TestCrashLoopRollbackSkipsBadPrevious(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.crashAfter["v1.0.1"] = 0
	st := &State{Run: RunRelease, CurrentSeen: "v1.0.1", Unproven: "v1.0.1"}
	st.AddBad("v1.0.0", "earlier")
	h.seed(st, "v1.0.1", "v1.0.0")
	h.build()
	h.run()
	eventually(t, "bootstrap child", func() bool { return h.lastStart() == RunBootstrap })
	if got := h.state(); !got.IsBad("v1.0.1") || h.lastStart() == "v1.0.0" {
		t.Errorf("state %+v", got)
	}
	for _, n := range h.startsList() {
		if n == "v1.0.0" {
			t.Fatalf("started the bad-listed previous: %v", h.startsList())
		}
	}
}

func TestCrashLoopOutsideSwitchFallsBackToBootstrap(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.1")
	h.crashAfter["v1.0.1"] = 0
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.1", PreviousIsBootstrap: true, Unproven: "v1.0.1"}, "v1.0.1", "")
	h.build()
	h.run()
	eventually(t, "bootstrap child", func() bool { return h.lastStart() == RunBootstrap })
	if st := h.state(); st.Run != RunBootstrap || !st.IsBad("v1.0.1") {
		t.Errorf("state %+v", st)
	}
}

// A stop in the middle of the gate leaves a pending switch that the next
// supervisor finishes; state.json stays parseable throughout.
func TestStopMidSwitchThenResume(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.gateBlock = true
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0", Pending: pendingApply("v1.0.1", "v1.0.0")}, "v1.0.0", "")
	h.build()
	cancel, done := h.run()
	eventually(t, "target started", func() bool { return h.lastStart() == "v1.0.1" })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	st := h.state()
	if st.Pending == nil || st.Phase != "probation" || st.Pending.Before == nil {
		t.Fatalf("interrupted switch lost its pending entry: %+v", st)
	}
	for _, name := range []string{"current", "previous"} {
		if v := h.l.Link(name); v != "" && !h.l.Valid(v) {
			t.Errorf("%s -> %q is not a valid release", name, v)
		}
	}

	h2 := &harness{t: t, l: h.l, boot: h.boot, logs: &logBuf{}, crashAfter: map[string]time.Duration{}, gateFail: map[string]string{}}
	h2.build()
	h2.run()
	eventually(t, "resumed to idle", idleWith(h2))
	if h.l.Link("current") != "v1.0.1" || h.l.Link("previous") != "v1.0.0" || h2.state().IsBad("v1.0.1") {
		t.Errorf("resume ended inconsistent: current=%s previous=%s", h.l.Link("current"), h.l.Link("previous"))
	}
}

func TestBootstrapChangeWinsAtStart(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0"}, "v1.0.0", "")
	if err := os.WriteFile(h.boot, []byte("new bootstrap"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.build()
	h.run()
	eventually(t, "child", func() bool { return len(h.startsList()) > 0 })
	if h.lastStart() != RunBootstrap || h.state().Run != RunBootstrap {
		t.Errorf("start %s state %+v", h.lastStart(), h.state())
	}
}

func TestHandFlippedCurrentHonoured(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v0.9.0")
	h.seed(&State{Run: RunBootstrap, CurrentSeen: "v1.0.0"}, "v0.9.0", "") // flipped by hand
	h.build()
	h.run()
	eventually(t, "child", func() bool { return len(h.startsList()) > 0 })
	if h.lastStart() != "v0.9.0" {
		t.Errorf("start %s", h.lastStart())
	}
}

func TestUnwritableReleasesDirRunsBootstrap(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	h := newHarness(t)
	h.release("v1.0.0")
	if err := h.l.SwapSymlink("current", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(h.l.Releases, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(h.l.Releases, 0o755) })
	h.build()
	h.run()
	eventually(t, "child", func() bool { return len(h.startsList()) > 0 })
	if h.lastStart() != RunBootstrap || !strings.Contains(h.logs.String(), "not writable") {
		t.Errorf("start %s\n%s", h.lastStart(), h.logs)
	}
	if _, err := os.Stat(h.l.StatePath()); err == nil {
		t.Error("state written to a read-only releases dir")
	}
}

func TestSupervisorLogsAvoidReservedWords(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.gateFail["v1.0.1"] = "smoke failed"
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0", Pending: pendingApply("v1.0.1", "v1.0.0")}, "v1.0.0", "")
	h.build()
	h.run()
	eventually(t, "idle", idleWith(h))
	for _, w := range []string{"panic", "FATAL"} {
		if strings.Contains(strings.ToLower(h.logs.String()), strings.ToLower(w)) {
			t.Errorf("log contains %q:\n%s", w, h.logs)
		}
	}
}

// A state.json the supervisor cannot read (permissions, I/O) is never set
// aside: only invalid JSON is. A directory in its place stands in for EACCES
// because it fails the same way for root.
func TestUnreadableStateIsNotRenamedAway(t *testing.T) {
	h := newHarness(t)
	h.build()
	if err := os.MkdirAll(filepath.Join(h.l.StatePath(), "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.run()
	eventually(t, "bootstrap child", func() bool { return h.lastStart() == RunBootstrap })
	if fi, err := os.Stat(h.l.StatePath()); err != nil || !fi.IsDir() {
		t.Fatalf("state.json was touched: %v", err)
	}
	if _, err := os.Stat(h.l.StatePath() + ".unreadable"); err == nil {
		t.Fatal("unreadable state was renamed away")
	}
	if !strings.Contains(h.logs.String(), "cannot read state.json; leaving it in place") {
		t.Fatalf("no loud log:\n%s", h.logs)
	}
}

func TestCorruptStateIsSetAside(t *testing.T) {
	h := newHarness(t)
	h.build()
	if err := os.WriteFile(h.l.StatePath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.run()
	eventually(t, "child", func() bool { return h.lastStart() != "" })
	if _, err := os.Stat(h.l.StatePath() + ".unreadable"); err != nil {
		t.Fatalf("corrupt state not set aside: %v", err)
	}
}

// A current release that fails re-verification is bad-listed and never run;
// the previous release (which verifies) runs instead.
func TestFailedVerificationRunsPrevious(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.verifyFail = map[string]bool{"v1.0.1": true}
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.1"}, "v1.0.1", "v1.0.0")
	h.build()
	h.run()
	eventually(t, "previous runs", func() bool { return h.lastStart() == "v1.0.0" })
	st := h.state()
	if !st.IsBad("v1.0.1") || h.l.Link("current") != "v1.0.0" {
		t.Errorf("bad=%+v current=%s", st.Bad, h.l.Link("current"))
	}
	for _, n := range h.startsList() {
		if n == "v1.0.1" {
			t.Fatalf("ran the unverified release: %v", h.startsList())
		}
	}
}

// With nothing verified to go back to, the bootstrap runs and the bad list
// keeps the release.
func TestFailedVerificationFallsBackToBootstrap(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.1")
	h.verifyFail = map[string]bool{"v1.0.1": true}
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.1", PreviousIsBootstrap: true}, "v1.0.1", "")
	h.build()
	h.run()
	eventually(t, "bootstrap runs", func() bool { return h.lastStart() == RunBootstrap })
	if st := h.state(); !st.IsBad("v1.0.1") || st.Run != RunBootstrap {
		t.Errorf("state %+v", st)
	}
}

// A switch target that fails verification is refused and bad-listed without
// ever starting.
func TestSwitchRefusesUnverifiedTarget(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.verifyFail = map[string]bool{"v1.0.1": true}
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0", Pending: pendingApply("v1.0.1", "v1.0.0")}, "v1.0.0", "")
	h.build()
	h.run()
	eventually(t, "switch refused", idleWith(h))
	if !h.state().IsBad("v1.0.1") {
		t.Errorf("state %+v", h.state())
	}
	for _, n := range h.startsList() {
		if n == "v1.0.1" {
			t.Fatalf("started the unverified target: %v", h.startsList())
		}
	}
}

// The baseline is refreshed from the running child right before a switch
// stops it, so content that changed since its boot is not held against the
// next release.
func TestSwitchRefreshesBaselineFromRunningChild(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.rootStatus = 404 // the home page was unpublished after the box booted
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0",
		SmokeBaseline: map[string]int{"GET /": 200}}, "v1.0.0", "")
	h.build()
	h.run()
	eventually(t, "v1.0.0 running", func() bool { return h.lastStart() == "v1.0.0" })
	eventually(t, "initial probe stored", func() bool { return h.state().SmokeBaseline["GET /"] == 404 })
	// Put the stale record back, as if the boot probe had run long ago.
	st := h.state()
	st.SmokeBaseline = map[string]int{"GET /": 200}
	st.Pending = pendingApply("v1.0.1", "v1.0.0")
	if err := WithLock(h.l.LockPath(), func() error { return SaveState(h.l.StatePath(), st) }); err != nil {
		t.Fatal(err)
	}
	h.s.poke()
	eventually(t, "switched", func() bool { return h.lastStart() == "v1.0.1" && idleWith(h)() })
	h.mu.Lock()
	last := h.gateCfgs[len(h.gateCfgs)-1]
	h.mu.Unlock()
	if last.Baseline["GET /"] != 404 {
		t.Errorf("the switch gate used a stale baseline: %v", last.Baseline)
	}
}

// The boot faults of the running child are stored as identifiers by the
// initial probe and refreshed right before a switch, so a fault that appeared
// after boot is in the baseline the next gate uses.
func TestBootFaultIdentifiersReachTheBaseline(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.bootFaults = []string{"plugin templates rejected: p"}
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0"}, "v1.0.0", "")
	h.build()
	h.run()
	eventually(t, "initial probe stored", func() bool { return slices.Equal(h.state().BootFaultBaseline, h.bootFaults) })
	st := h.state()
	st.BootFaultBaseline = nil // as if the boot probe had run before the fault appeared
	st.Pending = pendingApply("v1.0.1", "v1.0.0")
	if err := WithLock(h.l.LockPath(), func() error { return SaveState(h.l.StatePath(), st) }); err != nil {
		t.Fatal(err)
	}
	h.s.poke()
	eventually(t, "switched", func() bool { return h.lastStart() == "v1.0.1" && idleWith(h)() })
	h.mu.Lock()
	last := h.gateCfgs[len(h.gateCfgs)-1]
	h.mu.Unlock()
	if !slices.Equal(last.BootFaultBaseline, h.bootFaults) {
		t.Errorf("the switch gate's boot fault baseline = %v", last.BootFaultBaseline)
	}
}

// A stop that arrives while the old child is shutting down must not start the
// target: the supervisor leaves the switch pending and returns.
func TestStopDuringSwitchNeverStartsTheTarget(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0"}, "v1.0.0", "")
	h.build()
	cancel, done := h.run()
	eventually(t, "v1.0.0 running", func() bool { return h.lastStart() == "v1.0.0" })
	h.mu.Lock()
	h.onStop = cancel
	h.mu.Unlock()
	// The running child was launched before onStop was set: swap in a hook.
	h.s.child.(*fakeProc).onStop = cancel
	st := h.state()
	st.Pending = pendingApply("v1.0.1", "v1.0.0")
	if err := WithLock(h.l.LockPath(), func() error { return SaveState(h.l.StatePath(), st) }); err != nil {
		t.Fatal(err)
	}
	h.s.poke()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not return after the stop")
	}
	for _, n := range h.startsList() {
		if n == "v1.0.1" {
			t.Fatalf("started the target after a stop: %v", h.startsList())
		}
	}
	if h.state().Pending == nil {
		t.Error("the interrupted switch lost its pending entry")
	}
}

// One unreachable smoke port is retried with a new child; the release is not
// bad-listed for it. A second failure is a real failure.
func TestSmokePortRaceRetriesOnce(t *testing.T) {
	for name, tc := range map[string]struct {
		errs   []error
		bad    bool
		starts int
	}{
		"once":  {[]error{errSmokeUnreachable}, false, 2},
		"twice": {[]error{errSmokeUnreachable, errSmokeUnreachable}, true, 2},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.release("v1.0.0")
			h.release("v1.0.1")
			h.gateErrs = map[string][]error{"v1.0.1": tc.errs}
			h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0", Pending: pendingApply("v1.0.1", "v1.0.0")}, "v1.0.0", "")
			h.build()
			h.run()
			eventually(t, "switch decided", idleWith(h))
			if got := h.state().IsBad("v1.0.1"); got != tc.bad {
				t.Errorf("bad = %v, want %v", got, tc.bad)
			}
			n := 0
			for _, s := range h.startsList() {
				if s == "v1.0.1" {
					n++
				}
			}
			if n != tc.starts {
				t.Errorf("v1.0.1 started %d times, want %d", n, tc.starts)
			}
		})
	}
}

func cmpOr(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}

// A switched-to release stays unproven after its gate passes: a crash loop
// that starts between the gate and the end of the boot window still rolls it
// back (the 60-120 s band in production).
func TestCrashLoopAfterGateButInsideBootWindowRollsBack(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.crashAfter["v1.0.1"] = 200 * time.Millisecond // the gate (30 ms here) passes first
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0", Pending: pendingApply("v1.0.1", "v1.0.0")}, "v1.0.0", "")
	h.build()
	h.run()
	eventually(t, "rolled back to v1.0.0", func() bool {
		return h.lastStart() == "v1.0.0" && h.state().IsBad("v1.0.1")
	})
	if e := lastEvent(t, h); e.Phase != PhaseRolledBack || e.To != "v1.0.0" {
		t.Errorf("event = %+v", e)
	}
}

// A release that lives the whole boot window after its gate becomes proven.
func TestSwitchedReleaseBecomesProvenAfterBootWindow(t *testing.T) {
	h := newHarness(t)
	h.bootWindow = 400 * time.Millisecond
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0", Pending: pendingApply("v1.0.1", "v1.0.0")}, "v1.0.0", "")
	h.build()
	h.run()
	eventually(t, "gate passed", idleWith(h))
	if h.state().Unproven != "v1.0.1" {
		t.Fatalf("right after the gate the release must still be unproven: %+v", h.state())
	}
	eventually(t, "proven", func() bool { return h.state().Unproven == "" })
}

func offlineApplied(h *harness) {
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.1", Unproven: "v1.0.1", Ungated: &Pending{
		Kind: "apply", Version: "v1.0.1", From: "v1.0.0", RequestedAt: time.Now().UTC(),
		Before: &LinkSnapshot{Current: "v1.0.0", Run: RunRelease, CurrentSeen: "v1.0.0"},
	}}, "v1.0.1", "v1.0.0")
}

// A release applied offline is gated at the next start; once proven, a later
// crash loop (database down) never bad-lists it.
func TestOfflineAppliedReleaseIsGatedThenProven(t *testing.T) {
	h := newHarness(t)
	h.bootWindow = 300 * time.Millisecond
	h.release("v1.0.0")
	h.release("v1.0.1")
	offlineApplied(h)
	h.build()
	stop, done := h.run()
	eventually(t, "gated and proven", func() bool {
		st := h.state()
		return st.Unproven == "" && st.Ungated == nil && st.Pending == nil
	})
	if len(h.gateCfgs) == 0 || h.gateCfgs[0].Initial {
		t.Fatalf("the offline release must run the full gate: %+v", h.gateCfgs)
	}

	stop()
	<-done
	h2 := newHarness(t)
	h2.l = h.l
	h2.boot = h.boot
	h2.crashAfter["v1.0.1"] = 0
	h2.build()
	h2.run()
	eventually(t, "many restarts", func() bool { return len(h2.startsList()) >= 6 })
	if st := h2.state(); len(st.Bad) != 0 || h.l.Link("current") != "v1.0.1" {
		t.Errorf("proven release was rolled back: bad=%+v current=%s", st.Bad, h.l.Link("current"))
	}
}

func TestOfflineAppliedReleaseFailingItsGateRollsBack(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	h.gateFail["v1.0.1"] = "smoke: GET / returned 404"
	offlineApplied(h)
	h.build()
	h.run()
	eventually(t, "rolled back", func() bool { return h.lastStart() == "v1.0.0" && h.state().IsBad("v1.0.1") })
	if st := h.state(); st.Ungated != nil || st.Unproven != "" || h.l.Link("current") != "v1.0.0" {
		t.Errorf("state %+v current=%s", st, h.l.Link("current"))
	}
}

// An offline apply is gated only when the usual choice would run it: a
// bootstrap that changed since the last start still wins, and the Ungated
// record is dropped.
func TestOfflineAppliedReleaseDoesNotBeatAChangedBootstrap(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	offlineApplied(h)
	st := h.state()
	st.BootstrapSHA256 = "0000-an-older-bootstrap"
	if err := SaveState(h.l.StatePath(), st); err != nil {
		t.Fatal(err)
	}
	h.build()
	h.run()
	eventually(t, "bootstrap started", func() bool { return h.lastStart() == RunBootstrap })
	if st := h.state(); st.Ungated != nil || st.Unproven != "" || st.Pending != nil {
		t.Errorf("record not dropped: %+v", st)
	}
	for _, s := range h.startsList() {
		if s == "v1.0.1" {
			t.Fatalf("the offline release was started over the new bootstrap: %v", h.startsList())
		}
	}
}

// The gate at the next start is what logs success for an offline apply.
func TestOfflineAppliedReleaseLogsSuccessOnlyFromTheGate(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.release("v1.0.1")
	offlineApplied(h)
	h.build()
	h.run()
	eventually(t, "gate passed", idleWith(h))
	evs := awaitEvents(t, h)
	if len(evs) != 1 || evs[0].Phase != PhaseSuccess || evs[0].To != "v1.0.1" {
		t.Fatalf("events: %+v", evs)
	}
}

// The switch gate is told when its baseline is older than a day.
func TestSwitchGateKnowsAStaleBaseline(t *testing.T) {
	for name, tc := range map[string]struct {
		at    time.Time
		stale bool
	}{
		"never recorded": {time.Time{}, true},
		"two days old":   {time.Now().Add(-48 * time.Hour), true},
		"fresh":          {time.Now().Add(-time.Hour), false},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.release("v1.0.0")
			h.release("v1.0.1")
			h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0", BaselineAt: tc.at,
				SmokeBaseline: map[string]int{"GET /": 200}, Pending: pendingApply("v1.0.1", "v1.0.0")}, "v1.0.0", "")
			h.build()
			h.run()
			eventually(t, "gate passed", idleWith(h))
			h.mu.Lock()
			defer h.mu.Unlock()
			if len(h.gateCfgs) == 0 || h.gateCfgs[0].StaleBaseline != tc.stale {
				t.Fatalf("gate configs %+v, want stale=%v", h.gateCfgs, tc.stale)
			}
		})
	}
}

// The startup gate logs how an offline rollback's target came up and clears
// the marker.
func TestStartupGateReportsOfflineRollbackOutcome(t *testing.T) {
	h := newHarness(t)
	h.release("v1.0.0")
	h.seed(&State{Run: RunRelease, CurrentSeen: "v1.0.0", OfflineRollback: "v1.0.0"}, "v1.0.0", "")
	h.build()
	h.run()
	eventually(t, "marker cleared", func() bool { return h.state().OfflineRollback == "" })
	if !strings.Contains(h.logs.String(), "startup gate passed for the offline rollback") {
		t.Fatalf("outcome not logged:\n%s", h.logs.String())
	}
}
