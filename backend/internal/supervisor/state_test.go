package supervisor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
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

func TestRetryRefusal(t *testing.T) {
	bad := func(v, reason string) []BadEntry { return []BadEntry{{Version: v, Reason: reason}} }
	cases := []struct {
		name    string
		st      State
		version string
		running string
		want    string // substring of the refusal; "" = allowed
	}{
		{"allowed", State{Bad: bad("v1.1.0", "smoke failed")}, "v1.1.0", "v1.0.0", ""},
		{"not bad", State{Bad: bad("v1.2.0", "x")}, "v1.1.0", "v1.0.0", "is not on this box's bad list"},
		{"equal to running", State{Bad: bad("v1.0.0", "x")}, "v1.0.0", "v1.0.0", "is not newer than the running v1.0.0"},
		{"older than running", State{Bad: bad("v0.9.0", "x")}, "v0.9.0", "v1.0.0", "is not newer than the running v1.0.0"},
		{"running not semver", State{Bad: bad("v1.1.0", "x")}, "v1.1.0", "dev", ""},
		{"verification", State{Bad: bad("v1.1.0", ReasonVerify+"hash")}, "v1.1.0", "v1.0.0", "failed verification on this box"},
		{"staged verification", State{Bad: bad("v1.1.0", ReasonStagedVerify+"hash")}, "v1.1.0", "v1.0.0", "cannot be retried"},
		{"pending", State{Bad: bad("v1.1.0", "x"), Pending: &Pending{Version: "v1.2.0"}}, "v1.1.0", "v1.0.0", "another switch to v1.2.0 is still in progress"},
		{"probation", State{Bad: bad("v1.1.0", "x"), Phase: "probation"}, "v1.1.0", "v1.0.0", "probation"},
		{"unproven", State{Bad: bad("v1.1.0", "x"), Unproven: "v1.0.5"}, "v1.1.0", "v1.0.0", "v1.0.5 has not yet proven"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before, _ := jsonx.Marshal(c.st)
			entry, err := RetryRefusal(&c.st, c.version, c.running)
			if after, _ := jsonx.Marshal(c.st); !bytes.Equal(before, after) {
				t.Errorf("RetryRefusal changed the state: %s -> %s", before, after)
			}
			if c.want == "" {
				if err != nil || entry.Version != c.version {
					t.Errorf("got %+v, %v; want the entry and no error", entry, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestRemoveBadThenAddBadKeepsTheNewReason(t *testing.T) {
	var s State
	s.AddBad("v1.1.0", "old reason")
	s.AddBad("v1.2.0", "other")
	old, ok := s.RemoveBad("v1.1.0")
	if !ok || old.Reason != "old reason" || s.IsBad("v1.1.0") || !s.IsBad("v1.2.0") {
		t.Fatalf("RemoveBad = %+v, %v; bad = %+v", old, ok, s.Bad)
	}
	if _, ok := s.RemoveBad("v1.1.0"); ok {
		t.Error("removing an absent entry reported true")
	}
	s.AddBad("v1.1.0", "new reason")
	if len(s.Bad) != 2 || s.Bad[1].Reason != "new reason" {
		t.Errorf("bad = %+v, want the new reason", s.Bad)
	}
}

// The verification reasons are what Retry recognises; both sites that list a
// release for failing verification must build them from the constants.
func TestVerificationReasonSitesUseTheConstants(t *testing.T) {
	for file, want := range map[string]string{"switch.go": "ReasonStagedVerify", "supervisor.go": "ReasonVerify"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), "AddBad(c.Name, "+want+"+") && !strings.Contains(string(src), "refused = "+want+" +") {
			t.Errorf("%s does not build its bad reason from %s", file, want)
		}
	}
	if !(BadEntry{Reason: ReasonVerify + "x"}).VerificationFailure() || !(BadEntry{Reason: ReasonStagedVerify + "x"}).VerificationFailure() ||
		(BadEntry{Reason: "health gate failed"}).VerificationFailure() ||
		(BadEntry{Reason: "smoke: plugin boot: failed verification: x"}).VerificationFailure() {
		t.Error("VerificationFailure does not match the two reason prefixes exactly")
	}
}
