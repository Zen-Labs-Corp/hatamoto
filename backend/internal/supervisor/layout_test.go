package supervisor

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func mkRelease(t *testing.T, l Layout, v string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(l.Releases, v), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.Binary(v), []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
}

func TestLayoutValid(t *testing.T) {
	l := NewLayout(t.TempDir())
	mkRelease(t, l, "v1.0.0", 0o755)
	mkRelease(t, l, "v1.0.1", 0o644)
	if err := os.MkdirAll(filepath.Join(l.Releases, "v1.0.2"), 0o755); err != nil {
		t.Fatal(err)
	}
	for v, want := range map[string]bool{
		"v1.0.0": true, "v1.0.1": false, "v1.0.2": false, "v9.9.9": false,
		"": false, "..": false, ".": false, "a/b": false, "../v1.0.0": false, ".tmp-v1": false,
	} {
		if got := l.Valid(v); got != want {
			t.Errorf("Valid(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestSwapSymlinkRelativeAndLink(t *testing.T) {
	l := NewLayout(t.TempDir())
	mkRelease(t, l, "v1.0.0", 0o755)
	if err := l.SwapSymlink("current", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.Readlink(filepath.Join(l.Releases, "current"))
	if raw != "v1.0.0" || l.Link("current") != "v1.0.0" {
		t.Errorf("link target %q, Link %q", raw, l.Link("current"))
	}
	if err := l.SwapSymlink("current", "../etc"); err == nil {
		t.Error("path-like target accepted")
	}
	if err := l.RemoveLink("current"); err != nil || l.Link("current") != "" {
		t.Errorf("RemoveLink: %v, Link %q", err, l.Link("current"))
	}
	if err := l.RemoveLink("current"); err != nil {
		t.Errorf("RemoveLink of missing link: %v", err)
	}
}

// A reader never sees a missing or dangling link while it is swapped.
func TestSwapSymlinkAtomic(t *testing.T) {
	l := NewLayout(t.TempDir())
	mkRelease(t, l, "v1.0.0", 0o755)
	mkRelease(t, l, "v1.0.1", 0o755)
	if err := l.SwapSymlink("current", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	var stop atomic.Bool
	var bad atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			if v := l.Link("current"); v != "v1.0.0" && v != "v1.0.1" {
				bad.Add(1)
			}
			if _, err := os.Stat(filepath.Join(l.Releases, "current", "hatamoto")); err != nil {
				bad.Add(1)
			}
		}
	}()
	for i := 0; i < 300; i++ {
		v := "v1.0.0"
		if i%2 == 0 {
			v = "v1.0.1"
		}
		if err := l.SwapSymlink("current", v); err != nil {
			t.Fatal(err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if n := bad.Load(); n != 0 {
		t.Errorf("reader saw %d broken states", n)
	}
}

func TestWritable(t *testing.T) {
	l := NewLayout(filepath.Join(t.TempDir(), "data"))
	if err := l.Writable(); err != nil {
		t.Fatalf("fresh dir: %v", err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	if err := os.Chmod(l.Releases, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(l.Releases, 0o755)
	if err := l.Writable(); err == nil {
		t.Error("read-only dir reported writable")
	}
}
