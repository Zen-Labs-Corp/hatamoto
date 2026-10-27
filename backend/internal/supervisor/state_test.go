package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := LoadState(path)
	if err != nil || s.Phase != "idle" || s.V != 1 {
		t.Fatalf("fresh state = %+v, %v", s, err)
	}
	s.Run = RunRelease
	s.AddBad("v1.0.1", "smoke failed")
	s.AddBad("v1.0.1", "again")
	s.Pending = &Pending{Kind: "apply", Version: "v1.0.2"}
	if err := SaveState(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Run != RunRelease || len(got.Bad) != 1 || !got.IsBad("v1.0.1") || got.IsBad("v1.0.2") ||
		got.Pending == nil || got.Pending.Version != "v1.0.2" {
		t.Errorf("round trip = %+v", got)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	ents, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}

func TestLoadStateCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(path); err == nil {
		t.Error("corrupt state accepted")
	}
}

func TestWithLockSerializes(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "state.lock")
	state := filepath.Join(dir, "state.json")
	var inside atomic.Int32
	var overlap atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				err := WithLock(lock, func() error {
					if inside.Add(1) != 1 {
						overlap.Store(true)
					}
					defer inside.Add(-1)
					s, err := LoadState(state)
					if err != nil {
						return err
					}
					s.Attempts++
					return SaveState(state, s)
				})
				if err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if overlap.Load() {
		t.Error("critical sections overlapped")
	}
	s, err := LoadState(state)
	if err != nil || s.Attempts != 160 {
		t.Errorf("attempts = %d (%v), want 160: lost update", s.Attempts, err)
	}
}
