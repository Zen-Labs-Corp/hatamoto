package wasmhost

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

var (
	hostileWasm []byte
	cacheDir    string
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "hostile")
	if err != nil {
		panic(err)
	}
	out := filepath.Join(dir, "hostile.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, ".")
	cmd.Dir = "testdata/hostile" // its own module, like examples/plugins/hello
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "GOWORK=off")
	if b, err := cmd.CombinedOutput(); err != nil {
		panic("building hostile guest: " + err.Error() + "\n" + string(b))
	}
	if hostileWasm, err = os.ReadFile(out); err != nil {
		panic(err)
	}
	cacheDir = filepath.Join(dir, "cache") // share compiled code: compiling dominates test time
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func manifest(name string, l sdk.Limits) sdk.Manifest {
	return sdk.Manifest{Name: name, Version: "0.1.0", SDK: "^0.5", Limits: l}
}

func newHost(t *testing.T) *Host {
	t.Helper()
	h, err := New(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})), cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close(context.Background()) })
	h.SetHostCall(func(ctx context.Context, plugin string, in []byte) []byte {
		switch string(in) {
		case "panic":
			panic("boom")
		case "ctx":
			v, _ := ctx.Value(ctxKey{}).(string)
			return []byte(plugin + ":" + v)
		}
		return in
	})
	return h
}

type ctxKey struct{}

// timeouts sets the same timeout for each export. The tests use exports that
// are not SDK hooks, so they load through h.load, which skips the manifest's
// check of hook names.
func timeouts(ms uint64, exports ...string) map[string]uint64 {
	m := map[string]uint64{}
	for _, e := range exports {
		m[e] = ms
	}
	return m
}

func load(t *testing.T, h *Host, name string, l sdk.Limits) {
	t.Helper()
	if err := h.load(context.Background(), manifest(name, l), hostileWasm); err != nil {
		t.Fatal(err)
	}
}

func call(h *Host, name, export, in string) (string, error) {
	out, err := h.Call(context.Background(), name, export, []byte(in))
	return string(out), err
}

func TestEchoAndHasExport(t *testing.T) {
	h := newHost(t)
	load(t, h, "bad", sdk.Limits{})
	if out, err := call(h, "bad", "echo", `{"a":1}`); err != nil || out != `{"a":1}` {
		t.Fatalf("echo: %q %v", out, err)
	}
	if !h.HasExport("bad", "echo") || h.HasExport("bad", "filter_nothing") {
		t.Fatal("HasExport wrong")
	}
	if _, err := call(h, "missing", "echo", ""); !errors.Is(err, ErrNotLoaded) {
		t.Fatalf("want ErrNotLoaded, got %v", err)
	}
}

// Each hostile export must fail the call, within its limits, and leave the
// plugin (and a neighbour) fully usable.
func TestHostileGuests(t *testing.T) {
	h := newHost(t)
	limits := sdk.Limits{MemoryPages: 256, HookTimeoutsMS: timeouts(300, "hostile_loop", "hostile_alloc", "hostile_panic", "hostile_recurse", "guest_error")}
	load(t, h, "bad", limits)
	load(t, h, "good", limits)

	for _, tc := range []struct {
		export string
		max    time.Duration
	}{
		{"hostile_loop", 3 * time.Second},
		{"hostile_alloc", 10 * time.Second},
		{"hostile_panic", 3 * time.Second},
		{"hostile_recurse", 10 * time.Second},
		{"guest_error", 3 * time.Second},
	} {
		t.Run(tc.export, func(t *testing.T) {
			start := time.Now()
			_, err := call(h, "bad", tc.export, "")
			if err == nil {
				t.Fatal("hostile call succeeded")
			}
			if d := time.Since(start); d > tc.max {
				t.Fatalf("took %s (> %s)", d, tc.max)
			}
			if out, err := call(h, "bad", "echo", "still-alive"); err != nil || out != "still-alive" {
				t.Fatalf("plugin unusable afterwards: %q %v", out, err)
			}
			if out, err := call(h, "good", "echo", "neighbour"); err != nil || out != "neighbour" {
				t.Fatalf("neighbour affected: %q %v", out, err)
			}
		})
	}
	// guest_error is an ordinary failure: its instance is kept.
	if got := h.Discarded("bad"); got != 4 {
		t.Fatalf("want the 4 trapped instances discarded (not guest_error's), got %d", got)
	}
}

// With a generous timeout the memory cap, not the clock, must stop the bomb.
func TestMemoryBombHitsCap(t *testing.T) {
	h := newHost(t)
	load(t, h, "bad", sdk.Limits{MemoryPages: 256})
	_, err := call(h, "bad", "hostile_alloc", "")
	if err == nil {
		t.Fatal("memory bomb succeeded")
	}
	if strings.Contains(err.Error(), "deadline") {
		t.Fatalf("stopped by the clock, not the cap: %v", err)
	}
	if out, err := call(h, "bad", "echo", "ok"); err != nil || out != "ok" {
		t.Fatalf("plugin unusable afterwards: %q %v", out, err)
	}
}

func TestTimeoutIsEnforcedWithoutCallerDeadline(t *testing.T) {
	h := newHost(t)
	load(t, h, "bad", sdk.Limits{HookTimeoutsMS: timeouts(150, "hostile_loop")})
	start := time.Now()
	_, err := h.Call(context.Background(), "bad", "hostile_loop", nil)
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("loop not stopped by manifest timeout: %v after %s", err, time.Since(start))
	}
}

func TestCallerCancelStopsGuest(t *testing.T) {
	h := newHost(t)
	load(t, h, "bad", sdk.Limits{})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := h.Call(ctx, "bad", "hostile_loop", nil)
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("loop not stopped by caller ctx: %v after %s", err, time.Since(start))
	}
}

func TestNoNetwork(t *testing.T) {
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.Write([]byte("secret"))
	}))
	defer srv.Close()

	h := newHost(t)
	// Even a manifest that lists the server's host gets no network in v1-16.
	m := manifest("bad", sdk.Limits{})
	m.AllowedHosts = []string{"127.0.0.1"}
	if err := h.Load(context.Background(), m, hostileWasm); err != nil {
		t.Fatal(err)
	}
	out, err := call(h, "bad", "hostile_http", srv.URL)
	if err == nil && strings.HasPrefix(out, "reached:") {
		t.Fatalf("guest reached the network: %q", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Fatalf("server was hit %d times", hits)
	}
}

func TestNoFilesystem(t *testing.T) {
	h := newHost(t)
	load(t, h, "bad", sdk.Limits{})
	if out, err := call(h, "bad", "hostile_fs", ""); err != nil || out != "denied" {
		t.Fatalf("guest read a host file: %q %v", out, err)
	}
}

func TestMemoryCapRejectsTinyModuleBudget(t *testing.T) {
	h := newHost(t)
	// Below the minimum the manifest is refused outright.
	if err := h.Load(context.Background(), manifest("bad", sdk.Limits{MemoryPages: 8}), hostileWasm); err == nil {
		t.Fatal("manifest below min memory accepted")
	}
}

func TestPoolConcurrencyAndSaturation(t *testing.T) {
	h := newHost(t)
	load(t, h, "good", sdk.Limits{MaxInstances: 2})
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			in := bytes.Repeat([]byte{'a' + byte(i%26)}, 64)
			out, err := h.Call(context.Background(), "good", "echo", in)
			if err != nil || !bytes.Equal(out, in) {
				t.Errorf("call %d: %q %v", i, out, err)
			}
		})
	}
	wg.Wait()

	// Both slots held by spinning calls: a third caller gives up on its ctx.
	var spin sync.WaitGroup
	for range 2 {
		spin.Go(func() { call(h, "good", "hostile_loop", "") })
	}
	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := h.Call(ctx, "good", "echo", nil); !errors.Is(err, ErrSaturated) {
		t.Fatalf("want ErrSaturated, got %v", err)
	}
	spin.Wait()
	if out, err := call(h, "good", "echo", "ok"); err != nil || out != "ok" {
		t.Fatalf("after saturation: %q %v", out, err)
	}
}

func TestReloadAndUnloadUnderLoad(t *testing.T) {
	h := newHost(t)
	load(t, h, "good", sdk.Limits{})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := call(h, "good", "echo", "x"); err != nil && !errors.Is(err, ErrNotLoaded) {
					t.Errorf("call during reload: %v", err)
					return
				}
			}
		})
	}
	for range 3 {
		load(t, h, "good", sdk.Limits{})
	}
	if !h.Unload(context.Background(), "good") {
		t.Fatal("unload reported not loaded")
	}
	close(stop)
	wg.Wait()
	if _, err := call(h, "good", "echo", "x"); !errors.Is(err, ErrNotLoaded) {
		t.Fatalf("after unload: %v", err)
	}
}

// A guest that returns far more than max_output_bytes is refused without the
// host decoding or holding it: allocation stays far below what decoding the
// reviewer's 15 MiB payload cost (1.2 GiB).
func TestOversizedOutputIsRefusedAndBounded(t *testing.T) {
	h := newHost(t)
	load(t, h, "bad", sdk.Limits{MemoryPages: 1024})
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := call(h, "bad", "hostile_bigout", "15728640")
	runtime.ReadMemStats(&after)
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("want ErrOutputTooLarge, got %v", err)
	}
	if got := after.TotalAlloc - before.TotalAlloc; got > 300<<20 {
		t.Fatalf("host allocated %d MiB for a refused 15 MiB output", got>>20)
	}
	if h.Discarded("bad") != 1 {
		t.Fatalf("instance that produced the flood must be discarded")
	}
}

func TestOutputLimitIsManifestConfigurable(t *testing.T) {
	h := newHost(t)
	load(t, h, "small", sdk.Limits{MemoryPages: 256})
	load(t, h, "big", sdk.Limits{MemoryPages: 256, MaxOutputBytes: sdk.MaxOutputBytes})
	if _, err := call(h, "small", "hostile_bigout", "2000000"); !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("default 1 MiB cap not enforced: %v", err)
	}
	if out, err := call(h, "big", "hostile_bigout", "2000000"); err != nil || len(out) < 2000000 {
		t.Fatalf("raised cap not honoured: %d bytes, %v", len(out), err)
	}
	if err := h.Load(context.Background(), manifest("x", sdk.Limits{MaxOutputBytes: sdk.MaxOutputBytes + 1}), hostileWasm); err == nil {
		t.Fatal("max_output_bytes above the hard maximum accepted")
	}
}

func TestGuestErrorKeepsInstance(t *testing.T) {
	h := newHost(t)
	load(t, h, "bad", sdk.Limits{})
	for range 5 {
		if _, err := call(h, "bad", "guest_error", ""); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("want the guest's error, got %v", err)
		}
	}
	if h.Discarded("bad") != 0 {
		t.Fatalf("ordinary guest errors discarded %d instances", h.Discarded("bad"))
	}
}

// Consecutive trapping calls open the breaker for that export only; the
// plugin's other exports keep running.
func TestCircuitBreakerOpensPerExport(t *testing.T) {
	h := newHost(t)
	load(t, h, "bad", sdk.Limits{})
	for range breakerThreshold {
		if _, err := call(h, "bad", "hostile_panic", ""); err == nil || errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("want a trap while closed, got %v", err)
		}
	}
	start := time.Now()
	if _, err := call(h, "bad", "hostile_panic", ""); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("want ErrCircuitOpen, got %v", err)
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("open breaker still ran the guest")
	}
	if out, err := call(h, "bad", "echo", "fine"); err != nil || out != "fine" {
		t.Fatalf("other export affected: %q %v", out, err)
	}
}

func TestSlotWaitIsBoundedWithoutCallerDeadline(t *testing.T) {
	h := newHost(t)
	load(t, h, "good", sdk.Limits{MaxInstances: 1, HookTimeoutsMS: timeouts(400, "hostile_loop", "echo")})
	started := make(chan struct{})
	go func() {
		close(started)
		call(h, "good", "hostile_loop", "")
	}()
	<-started
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	_, err := h.Call(context.Background(), "good", "echo", nil)
	// A bounded outcome is all this test asks for: the call ran, found no slot
	// in time, or ran out of its deadline while a fresh instance was starting
	// (slow under -race once the looping instance has been discarded).
	if err != nil && !errors.Is(err, ErrSaturated) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected error %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("waited %s for a slot", time.Since(start))
	}
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestGuestLogsAreTruncatedAndRateLimited(t *testing.T) {
	logs := &lockedBuf{}
	h, err := New(slog.New(slog.NewTextHandler(logs, nil)), cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close(context.Background()) })
	load(t, h, "chatty", sdk.Limits{})
	start := time.Now()
	if _, err := call(h, "chatty", "hostile_log", "500"); err != nil {
		t.Fatal(err)
	}
	// The allowance grows with the time the call took (slow under -race on CI).
	allowed := logBurst + logPerSec*int(time.Since(start).Seconds()+2)
	out := logs.String()
	if n := strings.Count(out, "level=INFO"); n > allowed {
		t.Fatalf("%d guest log lines passed the rate limit", n)
	}
	if !strings.Contains(out, "suppressed") {
		t.Fatalf("want a suppression notice:\n%.300s", out)
	}
	if len(out) > 2<<20 || !strings.Contains(out, "(truncated)") {
		t.Fatalf("log lines not truncated (%d bytes)", len(out))
	}
}

func TestHostCall(t *testing.T) {
	h := newHost(t)
	load(t, h, "caller", sdk.Limits{})
	if out, err := call(h, "caller", "host_call", "hello"); err != nil || out != "hello" {
		t.Fatalf("echo through hm_call: %q %v", out, err)
	}
	// The host function sees the calling context's values and the plugin name.
	ctx := context.WithValue(context.Background(), ctxKey{}, "v1")
	out, err := h.Call(ctx, "caller", "host_call", []byte("ctx"))
	if err != nil || string(out) != "caller:v1" {
		t.Fatalf("ctx through hm_call: %q %v", out, err)
	}
	// A panic in the host side is contained: an empty response, no crash.
	if _, err := call(h, "caller", "host_call", "panic"); err != nil {
		t.Fatalf("panic must not fail the guest call: %v", err)
	}
	if out, err := call(h, "caller", "host_call", "hello"); err != nil || out != "hello" {
		t.Fatalf("after panic: %q %v", out, err)
	}
	// An oversized request is refused by the host, not forwarded.
	big := strings.Repeat("x", maxHostCallBytes+1)
	if out, err := call(h, "caller", "host_call", big); err != nil || !strings.Contains(out, "too large") {
		t.Fatalf("oversized: %.60q %v", out, err)
	}
}

// Time spent waiting for a slot and starting a cold instance must not eat the
// guest's own deadline: echo's 150 ms timeout is shorter than the 500 ms the
// looping call holds the only slot, and the loop's discarded instance forces a
// cold start, yet echo succeeds.
func TestSlotWaitAndColdStartDoNotConsumeGuestDeadline(t *testing.T) {
	h := newHost(t)
	to := timeouts(500, "hostile_loop")
	to["echo"] = 150
	load(t, h, "good", sdk.Limits{MaxInstances: 1, HookTimeoutsMS: to})
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		close(started)
		call(h, "good", "hostile_loop", "")
	}()
	<-started
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	out, err := call(h, "good", "echo", "still here")
	if err != nil || out != "still here" {
		t.Fatalf("echo after %s: %q %v, want success (slot wait must not count against its 150ms)", time.Since(start), out, err)
	}
	if time.Since(start) < 250*time.Millisecond {
		t.Fatalf("echo did not wait for the slot (%s); the test is not exercising the wait", time.Since(start))
	}
	<-done
}

// A caller that gives up while waiting for a slot gets ErrSaturated and costs
// no instance: only the looping call's own instance is discarded.
func TestCallerGivingUpInSlotWaitDiscardsNothing(t *testing.T) {
	h := newHost(t)
	load(t, h, "good", sdk.Limits{MaxInstances: 1, HookTimeoutsMS: timeouts(400, "hostile_loop", "echo")})
	done := make(chan struct{})
	go func() {
		defer close(done)
		call(h, "good", "hostile_loop", "")
	}()
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := h.Call(ctx, "good", "echo", nil); !errors.Is(err, ErrSaturated) {
		t.Fatalf("want ErrSaturated, got %v", err)
	}
	<-done
	if got := h.Discarded("good"); got != 1 {
		t.Fatalf("discarded %d instances, want only the looping one", got)
	}
}

// A hung guest whose own timeout is longer than its caller's deadline must
// still open the breaker (it had the time to itself), not dodge it.
func TestCallerDeadlineDoesNotShieldHangFromBreaker(t *testing.T) {
	h := newHost(t)
	load(t, h, "bad", sdk.Limits{HookTimeoutsMS: timeouts(5000, "hostile_loop")})
	for i := 0; i < breakerThreshold; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, err := h.Call(ctx, "bad", "hostile_loop", nil)
		cancel()
		if err == nil || errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("call %d: want a timeout, got %v", i, err)
		}
	}
	if _, err := h.Call(context.Background(), "bad", "hostile_loop", nil); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("want ErrCircuitOpen after %d hung calls, got %v", breakerThreshold, err)
	}
}

// A guest cut to a sliver by our own slot wait is not the plugin's fault: the
// breaker stays closed.
func TestSlotWaitCutDoesNotChargeBreaker(t *testing.T) {
	h := newHost(t)
	load(t, h, "good", sdk.Limits{MaxInstances: 1, HookTimeoutsMS: timeouts(5000, "hostile_loop", "echo")})
	call(h, "good", "echo", "") // warm the pool: no cold start in the way
	p := (*h.reg.Load())["good"]
	for i := 0; i < breakerThreshold+1; i++ {
		p.slots <- struct{}{} // hold the only slot
		go func() {
			time.Sleep(300 * time.Millisecond)
			<-p.slots
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 340*time.Millisecond)
		_, err := h.Call(ctx, "good", "hostile_loop", nil)
		cancel()
		if err == nil || errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("call %d: breaker opened or call succeeded: %v", i, err)
		}
	}
}
