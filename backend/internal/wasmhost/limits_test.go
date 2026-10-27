package wasmhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func hookManifest(name string, l sdk.Limits, config map[string]string) sdk.Manifest {
	m := manifest(name, l)
	m.Config = config
	return m
}

// Filters get 250 ms and actions 10 s unless the manifest says otherwise, and
// a hook's own value wins. A zero timeout is refused at load.
func TestPerHookTimeouts(t *testing.T) {
	h := newHost(t)
	loop := map[string]string{"mode": "loop"}
	m := hookManifest("loopy", sdk.Limits{HookTimeoutsMS: map[string]uint64{"entity.published": 400}}, loop)
	if err := h.Load(context.Background(), m, hostileWasm); err != nil {
		t.Fatal(err)
	}

	// filter_jsonld_enrich: not listed, so the 250 ms filter default.
	start := time.Now()
	_, err := call(h, "loopy", "filter_jsonld_enrich", "{}")
	d := time.Since(start)
	if err == nil || d < 200*time.Millisecond || d > 2*time.Second {
		t.Fatalf("filter default: err=%v after %s, want a kill near 250ms", err, d)
	}

	// on_entity_published: the manifest's 400 ms instead of the 10 s action default.
	start = time.Now()
	_, err = call(h, "loopy", "on_entity_published", "{}")
	d = time.Since(start)
	if err == nil || d < 350*time.Millisecond || d > 3*time.Second {
		t.Fatalf("configured action timeout: err=%v after %s, want a kill near 400ms", err, d)
	}

	for _, zero := range []map[string]uint64{{"render.page": 0}, {"entity.published": 0}} {
		bad := hookManifest("zero", sdk.Limits{HookTimeoutsMS: zero}, nil)
		err := h.Load(context.Background(), bad, hostileWasm)
		if err == nil || !strings.Contains(err.Error(), "is 0") {
			t.Fatalf("zero timeout %v: want a load error, got %v", zero, err)
		}
	}
	if len(h.Loaded()) != 1 {
		t.Fatalf("a refused manifest must not load: %v", h.Loaded())
	}
}

// peakHeap samples the Go heap until stop is closed and reports its maximum.
func peakHeap(stop <-chan struct{}) <-chan uint64 {
	out := make(chan uint64, 1)
	go func() {
		var peak uint64
		var ms runtime.MemStats
		for {
			runtime.ReadMemStats(&ms)
			peak = max(peak, ms.HeapAlloc)
			select {
			case <-stop:
				out <- peak
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	return out
}

// Documented ceiling (docs/PLUGIN_API.md, Limits): memory_pages caps each wasm
// module of an instance, the guest and Extism's kernel, so one instance can
// hold up to 2 x memory_pages; wazero's default allocator also briefly holds
// the old and new buffer while growing. The guest must stop at its own cap,
// and the host heap must stay inside 2 x cap x 2 (+ slack).
func TestMemoryCeilingIsTwiceMaxPages(t *testing.T) {
	const pages = 256 // 16 MiB
	capBytes := uint64(pages) * 64 << 10
	h := newHost(t)
	var held atomic.Int64
	h.SetHostCall(func(_ context.Context, _ string, in []byte) []byte {
		n, _ := strconv.Atoi(string(in))
		held.Store(int64(n))
		return []byte("{}")
	})
	load(t, h, "bad", sdk.Limits{MemoryPages: pages})

	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	stop := make(chan struct{})
	peak := peakHeap(stop)
	_, err := call(h, "bad", "hostile_alloc_report", "")
	close(stop)
	p := <-peak
	if err == nil {
		t.Fatal("memory bomb succeeded")
	}
	guestMiB := uint64(held.Load())
	if guestMiB == 0 || guestMiB<<20 > capBytes {
		t.Fatalf("guest held %d MiB before it was stopped; cap is %d MiB", guestMiB, capBytes>>20)
	}
	if !raceEnabled {
		if growth := p - base.HeapAlloc; growth > 4*capBytes+32<<20 {
			t.Fatalf("host heap grew %d MiB for a %d MiB cap (ceiling 2x, 2x transient, +32 MiB)", growth>>20, capBytes>>20)
		}
	}
}

// Many concurrent bombs, more than max_instances, are all killed, the memory
// stays inside max_instances x the ceiling, and the plugin serves afterwards.
func TestConcurrentMemoryBombsBounded(t *testing.T) {
	const pages, instances = 128, 4 // 8 MiB cap, 4 live instances
	capBytes := uint64(pages) * 64 << 10
	h := newHost(t)
	load(t, h, "bad", sdk.Limits{MemoryPages: pages, MaxInstances: instances,
		HookTimeoutsMS: timeouts(10000, "hostile_alloc", "echo")})

	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	stop := make(chan struct{})
	peak := peakHeap(stop)
	var wg sync.WaitGroup
	var survived atomic.Int32
	for range 3 * instances {
		wg.Go(func() {
			if _, err := call(h, "bad", "hostile_alloc", ""); err == nil {
				survived.Add(1)
			}
		})
	}
	wg.Wait()
	close(stop)
	p := <-peak
	if survived.Load() != 0 {
		t.Fatalf("%d memory bombs completed", survived.Load())
	}
	if !raceEnabled {
		if growth, limit := p-base.HeapAlloc, instances*4*capBytes+64<<20; growth > limit {
			t.Fatalf("peak host heap +%d MiB, limit %d MiB", growth>>20, limit>>20)
		}
	}
	if out, err := call(h, "bad", "echo", "alive"); err != nil || out != "alive" {
		t.Fatalf("plugin unusable afterwards: %q %v", out, err)
	}
}

// Spinning guests in one plugin must not stall another plugin.
func TestHostileDoesNotStallNeighbours(t *testing.T) {
	h := newHost(t)
	load(t, h, "bad", sdk.Limits{MaxInstances: 4, HookTimeoutsMS: timeouts(2000, "hostile_loop", "echo")})
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
					call(h, "bad", "hostile_loop", "")
				}
			}
		})
	}
	time.Sleep(100 * time.Millisecond)
	var worst time.Duration
	for range 200 {
		start := time.Now()
		if out, err := call(h, "good", "echo", "x"); err != nil || out != "x" {
			t.Fatalf("neighbour call: %q %v", out, err)
		}
		worst = max(worst, time.Since(start))
	}
	close(stop)
	wg.Wait()
	limit := 250 * time.Millisecond
	if raceEnabled {
		limit = time.Second
	}
	if worst > limit {
		t.Fatalf("worst neighbour latency %s (> %s) while 4 guests spun", worst, limit)
	}
	t.Logf("worst neighbour latency %s", worst)
}

// 50 load -> call -> unload cycles: no goroutines and no memory pile up.
func TestLoadUnloadNoLeak(t *testing.T) {
	h := newHost(t)
	load(t, h, "warm", sdk.Limits{}) // fills the compile cache and the allocator's steady state
	h.Unload(context.Background(), "warm")

	settle := func() (int, uint64) {
		var ms runtime.MemStats
		for range 20 {
			runtime.GC()
			time.Sleep(25 * time.Millisecond)
		}
		runtime.ReadMemStats(&ms)
		return runtime.NumGoroutine(), ms.HeapAlloc
	}
	g0, m0 := settle()
	for i := range 50 {
		load(t, h, "cycle", sdk.Limits{})
		if out, err := call(h, "cycle", "echo", "x"); err != nil || out != "x" {
			t.Fatalf("cycle %d: %q %v", i, out, err)
		}
		if !h.Unload(context.Background(), "cycle") {
			t.Fatalf("cycle %d: unload reported not loaded", i)
		}
	}
	g1, m1 := settle()
	if g1 > g0+2 {
		t.Fatalf("goroutines %d -> %d after 50 cycles", g0, g1)
	}
	if m1 > m0+32<<20 { // one leaked instance is several MiB: 50 would be hundreds
		t.Fatalf("heap %d -> %d MiB after 50 cycles", m0>>20, m1>>20)
	}
	t.Logf("goroutines %d -> %d, heap %d -> %d MiB", g0, g1, m0>>20, m1>>20)
}

// Replacing v1 with v2 while callers hammer the plugin fails no call, and
// every call sees one version or the other.
func TestSwapUnderLoadFailsNoCalls(t *testing.T) {
	h := newHost(t)
	swap := func(v string) {
		if err := h.Load(context.Background(), hookManifest("good", sdk.Limits{}, map[string]string{"ver": v}), hostileWasm); err != nil {
			t.Fatal(err)
		}
	}
	swap("v1")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var calls, failed, v2 atomic.Int64
	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				out, err := call(h, "good", "version", "")
				calls.Add(1)
				switch {
				case err != nil || (out != "v1" && out != "v2"):
					failed.Add(1)
					t.Errorf("call failed during swap: %q %v", out, err)
					return
				case out == "v2":
					v2.Add(1)
				}
			}
		})
	}
	time.Sleep(100 * time.Millisecond)
	swap("v2")
	for v2.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
	if failed.Load() != 0 || calls.Load() == 0 {
		t.Fatalf("%d of %d calls failed", failed.Load(), calls.Load())
	}
	if out, err := call(h, "good", "version", ""); err != nil || out != "v2" {
		t.Fatalf("after the swap: %q %v", out, err)
	}
}

// A payload of the size the sitemap filter used to move (5000 URLs) still
// round-trips, which bounds what a filter can cost; sitemap.urls now carries
// additions only.
func TestLargePayloadRoundTrips(t *testing.T) {
	h := newHost(t)
	load(t, h, "good", sdk.Limits{HookTimeoutsMS: timeouts(10000, "echo")})
	in := make([]byte, 424<<10)
	for i := range in {
		in[i] = 'a' + byte(i%26)
	}
	start := time.Now()
	out, err := h.Call(context.Background(), "good", "echo", in)
	if err != nil || len(out) != len(in) {
		t.Fatalf("424 KiB echo: %d bytes, %v", len(out), err)
	}
	t.Logf("424 KiB echo: %s", time.Since(start))
}

// Compiled code is cached under the given directory (created 0700), a second
// host warm-loads from it far faster than the cold compile, and a deleted
// cache is simply rebuilt.
func TestCompileCacheWarmLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data", "cache", "wasm")
	timeLoad := func() time.Duration {
		t.Helper()
		h, err := New(nil, dir)
		if err != nil {
			t.Fatal(err)
		}
		defer h.Close(context.Background())
		start := time.Now()
		if err := h.load(context.Background(), manifest("good", sdk.Limits{}), hostileWasm); err != nil {
			t.Fatal(err)
		}
		return time.Since(start)
	}
	cold := timeLoad()
	fi, err := os.Stat(dir)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("cache dir: %v %v (want 0700)", fi, err)
	}
	warm := timeLoad()
	t.Logf("cold %s, warm %s", cold, warm)
	// The goal is a warm load under 100 ms. Shared CI runners are too noisy for
	// that, so there (and under -race) only the structure is asserted: the cache
	// directory is populated and a warm load is much faster than a cold compile.
	// HATAMOTO_PERF_STRICT=1 enforces the 100 ms goal anywhere; =0 never
	// (shared or busy hosts such as LAN build servers, like CI).
	strict := os.Getenv("HATAMOTO_PERF_STRICT") == "1" ||
		(os.Getenv("HATAMOTO_PERF_STRICT") != "0" && os.Getenv("CI") == "" && !raceEnabled)
	if strict && warm > 100*time.Millisecond {
		t.Fatalf("warm load took %s (> 100ms); cold was %s", warm, cold)
	}
	if ents, err := os.ReadDir(dir); err != nil || len(ents) == 0 {
		t.Fatalf("compile cache not populated: %v %v", ents, err)
	}
	if !raceEnabled && (warm > cold/5 || warm > time.Second) {
		t.Fatalf("cache did not speed up loading: warm %s, cold %s (want warm < cold/5 and < 1s)", warm, cold)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	timeLoad() // rebuilds; must not fail
}

// A cache directory that cannot be created costs speed only.
func TestCompileCacheUnusableDirFallsBack(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := New(nil, filepath.Join(file, "cache"))
	if err != nil {
		t.Fatalf("New must fall back to the in-memory cache: %v", err)
	}
	defer h.Close(context.Background())
	if err := h.load(context.Background(), manifest("good", sdk.Limits{}), hostileWasm); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Call(context.Background(), "good", "echo", nil); err != nil && !errors.Is(err, ErrNotLoaded) {
		t.Fatal(err)
	}
}
