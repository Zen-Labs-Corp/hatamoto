package supervisor

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Event phases.
const (
	PhaseSuccess        = "success"
	PhaseRolledBack     = "rolled_back"
	PhaseFailed         = "failed"
	PhaseManualRollback = "manual_rollback"
	// PhaseStagedOffline is an apply made while no supervisor ran: nothing has
	// been gated yet. Only the gate at the next start records success.
	PhaseStagedOffline = "staged-offline"
	// PhaseRolledBackOffline is a rollback made while no supervisor ran, from
	// a state that is not a release (nothing to bad-list). The next start's
	// gate logs how the target came up.
	PhaseRolledBackOffline = "rolled-back-offline"
)

// Event is one line of releases/events.jsonl. The child imports unseen lines
// into hatamoto_updates on boot.
type Event struct {
	ID         string    `json:"id"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	Phase      string    `json:"phase"`
	Error      string    `json:"error"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// NewEventID is "<unixnano>-<8 hex>".
func NewEventID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + hex.EncodeToString(b[:])
}

// AppendEvent writes e as one JSON line with O_APPEND and fsync. An empty ID
// is filled in.
func AppendEvent(path string, e Event) error {
	if e.ID == "" {
		e.ID = NewEventID()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY|noFollow, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return fmt.Errorf("append event: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
