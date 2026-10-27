package supervisor

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// State is releases/state.json, shared by the supervisor and the CLI under
// WithLock.
type State struct {
	V                   int        `json:"v"`
	Phase               string     `json:"phase"` // idle | probation
	Run                 string     `json:"run"`   // bootstrap | release
	CurrentSeen         string     `json:"current_seen"`
	PreviousIsBootstrap bool       `json:"previous_is_bootstrap"`
	Attempts            int        `json:"attempts"`
	LastError           string     `json:"last_error"`
	Bad                 []BadEntry `json:"bad"`
	BootstrapSHA256     string     `json:"bootstrap_sha256"`
	// BootstrapVersion is the version stamp of the supervisor's own binary,
	// recorded by the supervisor so a child that runs a release (whose own
	// stamp is the release's) can still tell what a rollback would return to.
	BootstrapVersion string   `json:"bootstrap_version,omitempty"`
	SmokeToken       string   `json:"smoke_token"`
	PluginErrors     []string `json:"plugin_errors"`
	// SmokeBaseline is the per-check HTTP status of the last good child (one
	// that passed its gate, or was running without a switch). Nil: no record.
	SmokeBaseline map[string]int `json:"smoke_baseline,omitempty"`
	// BaselineAt is when SmokeBaseline was recorded.
	BaselineAt time.Time `json:"baseline_at,omitempty"`
	// RenderBaseline is the render errors the last good run logged by its
	// first smoke call; a later gate fails only on an increase.
	RenderBaseline int `json:"render_baseline,omitempty"`
	// BootFaultBaseline is the boot fault identifiers (theme fallback and the
	// like) the last good run reported; a later gate fails on any other.
	BootFaultBaseline []string `json:"known_boot_faults,omitempty"`
	Pending           *Pending `json:"pending,omitempty"`
	// Unproven is a release switched to that has not yet both passed its
	// gate and lived a full boot window. Only an unproven release's crash loop
	// may roll back; any other crash loop just restarts with backoff.
	Unproven string `json:"unproven,omitempty"`
	// OfflineRollback is the target of a rollback made with no supervisor
	// running. The startup gate logs its outcome, then clears this.
	OfflineRollback string `json:"offline_rollback,omitempty"`
	// Ungated is a release applied while no supervisor was running. The
	// supervisor runs the normal health gate on it at its next start, using
	// Before to roll back if it fails.
	Ungated *Pending `json:"ungated,omitempty"`
}

// BadEntry is a version that was rolled back and is never retried.
type BadEntry struct {
	Version string    `json:"version"`
	Reason  string    `json:"reason"`
	At      time.Time `json:"when"`
}

// Pending is an apply or rollback that has no outcome yet.
type Pending struct {
	Kind       string `json:"kind"` // apply | rollback
	Version    string `json:"version"`
	From       string `json:"from"`
	PrevBefore string `json:"prev_before"`
	// Before is taken by the first pass of the switch and is what a rollback
	// restores; a replay after a restart reuses it.
	Before      *LinkSnapshot `json:"before,omitempty"`
	RequestedAt time.Time     `json:"requested_at"`
	StartedAt   time.Time     `json:"started_at,omitempty"`
}

// LinkSnapshot is the release links and run choice as they were before a switch.
type LinkSnapshot struct {
	Current             string `json:"current"`
	Previous            string `json:"previous"`
	PreviousIsBootstrap bool   `json:"previous_is_bootstrap"`
	Run                 string `json:"run"`
	CurrentSeen         string `json:"current_seen"`
}

// IsBad reports whether version is on the bad list.
func (s *State) IsBad(version string) bool {
	for _, b := range s.Bad {
		if b.Version == version {
			return true
		}
	}
	return false
}

// AddBad records version once.
func (s *State) AddBad(version, reason string) {
	if s.IsBad(version) {
		return
	}
	s.Bad = append(s.Bad, BadEntry{Version: version, Reason: reason, At: time.Now().UTC()})
}

// CorruptStateError means state.json was read but is not valid JSON, as
// opposed to an I/O or permission error. Only this kind may be set aside.
type CorruptStateError struct{ Err error }

func (e *CorruptStateError) Error() string { return "state.json is not valid JSON: " + e.Err.Error() }
func (e *CorruptStateError) Unwrap() error { return e.Err }

// LoadState reads state.json; a missing file is a fresh state.
func LoadState(path string) (*State, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|noFollow, 0)
	if errors.Is(err, os.ErrNotExist) {
		return &State{V: 1, Phase: "idle"}, nil
	}
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, 16<<20))
	f.Close()
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, &CorruptStateError{err}
	}
	if s.V == 0 {
		s.V = 1
	}
	if s.Phase == "" {
		s.Phase = "idle"
	}
	return &s, nil
}

// SaveState writes state.json atomically: temp file in the same directory,
// fsync, rename, fsync the directory. Mode 0600.
func SaveState(path string, s *State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".state-*.tmp") // created 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return syncDir(dir)
}
