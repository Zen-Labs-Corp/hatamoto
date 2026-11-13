package analytics

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/analytics/geotest"
)

func writeGeo(t *testing.T, path string, low, high string) {
	t.Helper()
	if err := os.WriteFile(path, geotest.TinyMMDB(low, high), 0o600); err != nil {
		t.Fatal(err)
	}
}

// swapGeo replaces path atomically, as a refresh job does.
func swapGeo(t *testing.T, path string, content []byte) {
	t.Helper()
	tmp := path + ".new"
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func newHolder(path string) (*GeoHolder, *bytes.Buffer) {
	var buf bytes.Buffer
	return NewGeoHolder(path, slog.New(slog.NewTextHandler(&buf, nil))), &buf
}

func TestGeoHolderAppearsAfterStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.mmdb")
	h, _ := newHolder(path)
	h.Reload()
	if !h.Configured() || h.Loaded() || h.Country("1.2.3.4") != "" {
		t.Fatal("missing file: want configured, not loaded, empty country")
	}
	writeGeo(t, path, "DE", "FR")
	h.Reload()
	if !h.Loaded() || h.Country("1.2.3.4") != "DE" {
		t.Fatalf("after appearing: loaded=%v country=%q", h.Loaded(), h.Country("1.2.3.4"))
	}
}

func TestGeoHolderAtomicSwap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.mmdb")
	writeGeo(t, path, "DE", "FR")
	h, _ := newHolder(path)
	h.Reload()
	if got := h.Country("1.2.3.4"); got != "DE" {
		t.Fatalf("before swap = %q", got)
	}
	swapGeo(t, path, geotest.TinyMMDB("GB", "FR"))
	h.Reload()
	if got := h.Country("1.2.3.4"); got != "GB" {
		t.Fatalf("after swap = %q, want GB", got)
	}
}

func TestGeoHolderKeepsLastGoodAndLogsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.mmdb")
	writeGeo(t, path, "DE", "FR")
	h, logs := newHolder(path)
	h.Reload()
	swapGeo(t, path, []byte("TOPSECRET not a database"))
	for range 3 {
		h.Reload()
	}
	if got := h.Country("1.2.3.4"); got != "DE" || !h.Loaded() {
		t.Fatalf("corrupt file dropped the last good database: %q", got)
	}
	if n := strings.Count(logs.String(), "not loaded"); n != 1 {
		t.Fatalf("logged %d times, want 1:\n%s", n, logs)
	}
	if strings.Contains(logs.String(), "TOPSECRET") || strings.Contains(logs.String(), filepath.Dir(path)) {
		t.Fatalf("log leaks detail: %s", logs)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	h.Reload()
	if h.Country("1.2.3.4") != "DE" {
		t.Fatal("deleted file dropped the last good database")
	}
	swapGeo(t, path, geotest.TinyMMDB("GB", "FR"))
	h.Reload()
	if h.Country("1.2.3.4") != "GB" {
		t.Fatal("a valid file after failures was not loaded")
	}
}

func TestGeoHolderUnconfiguredAndNil(t *testing.T) {
	h, _ := newHolder("")
	h.Reload()
	h.MaybeReload(0)
	if h.Configured() || h.Loaded() || h.Country("1.2.3.4") != "" {
		t.Fatal("empty path must stay inert")
	}
	var n *GeoHolder
	if n.Configured() || n.Loaded() || n.Country("1.2.3.4") != "" {
		t.Fatal("nil holder must be inert")
	}
	n.Reload()
	n.MaybeReload(0)
}

func TestGeoHolderMaybeReloadMinGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.mmdb")
	writeGeo(t, path, "DE", "FR")
	h, _ := newHolder(path)
	h.Reload()
	swapGeo(t, path, geotest.TinyMMDB("GB", "FR"))
	h.MaybeReload(time.Hour)
	if h.Country("1.2.3.4") != "DE" {
		t.Fatal("MaybeReload inside the gap must not reload")
	}
	h.MaybeReload(0)
	if h.Country("1.2.3.4") != "GB" {
		t.Fatal("MaybeReload past the gap must reload")
	}
}

func TestGeoHolderConcurrentLookupAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.mmdb")
	writeGeo(t, path, "DE", "FR")
	h, _ := newHolder(path)
	h.Reload()
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				_ = h.Country("1.2.3.4")
				_ = h.Loaded()
			}
		}()
	}
	for i := range 20 {
		code := "GB"
		if i%2 == 0 {
			code = "NL"
		}
		swapGeo(t, path, geotest.TinyMMDB(code, "FR"))
		h.Reload()
	}
	wg.Wait()
}

func TestGeoHolderMmapRenameReplaceUnderLookups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.mmdb")
	writeGeo(t, path, "DE", "FR")
	h, _ := newHolder(path)
	h.SetMmap(true)
	h.Reload()
	if !h.Loaded() || h.Country("1.2.3.4") != "DE" {
		t.Fatalf("mapped load: loaded=%v country=%q", h.Loaded(), h.Country("1.2.3.4"))
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if c := h.Country("1.2.3.4"); c != "DE" && c != "IT" {
					t.Errorf("lookup during swap = %q", c)
					return
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		code := "IT"
		if i%2 == 1 {
			code = "DE"
		}
		swapGeo(t, path, geotest.TinyMMDB(code, "FR"))
		// Distinct sizes/mtimes are not guaranteed, so force a new signature.
		future := time.Now().Add(time.Duration(i+1) * time.Hour)
		_ = os.Chtimes(path, future, future)
		h.Reload()
		runtime.GC()
	}
	close(stop)
	wg.Wait()
	runtime.GC()
	if got := h.Country("1.2.3.4"); got != "DE" {
		t.Fatalf("after last swap = %q, want DE", got)
	}
}

func TestGeoHolderMmapConcurrentReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.mmdb")
	writeGeo(t, path, "DE", "FR")
	h, _ := newHolder(path)
	h.SetMmap(true)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); h.Reload() }()
	}
	wg.Wait()
	if h.Country("200.1.1.1") != "FR" {
		t.Fatal("lookup after concurrent reload failed")
	}
}

func TestGeoHolderDefaultCopiesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.mmdb")
	writeGeo(t, path, "DE", "FR")
	h, _ := newHolder(path)
	h.Reload()
	// The default copy survives an in-place overwrite of the file.
	writeGeo(t, path, "IT", "FR")
	if got := h.Country("1.2.3.4"); got != "DE" {
		t.Fatalf("default path lookup after overwrite = %q, want DE", got)
	}
}
