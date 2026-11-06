package wasmhost

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	extism "github.com/extism/go-sdk"

	"hatamoto.dev/sdk"
)

func onlyPlugin(t *testing.T, h *Host) PluginStats {
	t.Helper()
	st := h.Stats()
	if len(st.Plugins) != 1 {
		t.Fatalf("plugins = %d, want 1", len(st.Plugins))
	}
	return st.Plugins[0]
}

func TestStatsConcurrentCalls(t *testing.T) {
	h := newHost(t)
	load(t, h, "good", sdk.Limits{MaxInstances: 4})
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, err := call(h, "good", "echo", "x"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	ps := onlyPlugin(t, h)
	if ps.Name != "good" || ps.Pooled < 1 || ps.Pooled > 4 || ps.Live != 0 || ps.MaxInstances != 4 {
		t.Fatalf("stats = %+v", ps)
	}
	if ps.PooledWasmBytes == 0 || ps.ModuleBytes != int64(len(hostileWasm)) {
		t.Fatalf("bytes = %+v", ps)
	}
}

func TestStatsNeverCalledPlugin(t *testing.T) {
	h := newHost(t)
	load(t, h, "idle", sdk.Limits{})
	ps := onlyPlugin(t, h)
	if ps.Pooled != 1 || ps.Live != 0 || ps.PooledWasmBytes == 0 {
		t.Fatalf("stats = %+v", ps)
	}
}

func TestStatsAfterUnloadZero(t *testing.T) {
	h := newHost(t)
	load(t, h, "good", sdk.Limits{})
	if _, err := call(h, "good", "echo", "x"); err != nil {
		t.Fatal(err)
	}
	p := (*h.reg.Load())["good"]
	h.Unload(context.Background(), "good")
	if n := len(h.Stats().Plugins); n != 0 {
		t.Fatalf("plugins after unload = %d", n)
	}
	n := 0
	p.pooledMem.Range(func(_, _ any) bool { n++; return true })
	if n != 0 || len(p.pool) != 0 {
		t.Fatalf("records after unload = %d, pooled = %d", n, len(p.pool))
	}
}

// A call in progress holds its instance outside the pool: Stats must not
// count or read it, and must not take a slot. Run with -race.
func TestStatsDuringCallsRace(t *testing.T) {
	h := newHost(t)
	load(t, h, "good", sdk.Limits{MaxInstances: 2})
	p := (*h.reg.Load())["good"]

	entered := make(chan struct{})
	release := make(chan struct{})
	p.beforeCall = func(*extism.Plugin) {
		entered <- struct{}{}
		<-release
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { call(h, "good", "echo", "x") })
	}
	<-entered
	<-entered

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				h.Stats()
			}
		}
	}()
	ps := onlyPlugin(t, h)
	if ps.Live != 2 || ps.Pooled != 0 || ps.PooledWasmBytes != 0 {
		t.Fatalf("mid-call stats = %+v, want 2 live, none pooled, no memory", ps)
	}
	close(release)
	wg.Wait()
	close(stop)
	<-done

	ps = onlyPlugin(t, h)
	if ps.Live != 0 || ps.Pooled != 2 || ps.PooledWasmBytes == 0 {
		t.Fatalf("after calls stats = %+v", ps)
	}
}

// Pins the go-sdk version the reflect helper relies on: the guest module's
// memory must be counted on top of the kernel's.
func TestInstanceBytesGuestIncluded(t *testing.T) {
	h := newHost(t)
	load(t, h, "good", sdk.Limits{})
	p := (*h.reg.Load())["good"]
	inst := <-p.pool
	defer p.pushPooled(inst)
	p.popPooled(inst)
	guest := guestMemoryBytes(inst)
	kernel := uint64(inst.Memory().Size())
	if guest == 0 || guest <= kernel || instanceBytes(inst) != guest+kernel {
		t.Fatalf("guest = %d, kernel = %d: extism go-sdk layout changed, update guestMemoryBytes", guest, kernel)
	}
}

func TestCacheBytesDisk(t *testing.T) {
	dir := t.TempDir()
	h, err := New(slog.New(slog.NewTextHandler(os.Stderr, nil)), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close(context.Background()) })
	if err := h.load(context.Background(), manifest("good", sdk.Limits{}), hostileWasm); err != nil {
		t.Fatal(err)
	}
	st := h.Stats()
	if !st.CacheOnDisk || st.CacheBytes <= 0 {
		t.Fatalf("disk cache stats = %+v", st)
	}
	// Half-written files do not count.
	sub, _ := filepath.Glob(filepath.Join(dir, "wazero-*-*"))
	if len(sub) != 1 {
		t.Fatalf("cache subdirs = %v", sub)
	}
	if err := os.WriteFile(filepath.Join(sub[0], "x.tmp"), bytes.Repeat([]byte{1}, 1<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if again := h.Stats(); again.CacheBytes != st.CacheBytes {
		t.Fatalf("tmp file counted: %d vs %d", again.CacheBytes, st.CacheBytes)
	}
}

func TestCacheBytesMemory(t *testing.T) {
	h, err := New(slog.New(slog.NewTextHandler(os.Stderr, nil)), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close(context.Background()) })
	if st := h.Stats(); st.CacheOnDisk || st.CacheBytes != 0 {
		t.Fatalf("memory cache stats = %+v", st)
	}
}
