package media

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// The theme_extra keys that hold the modern-format switches and the state of
// the rebuild job. They are separate keys so saving job progress (after every
// image) can never overwrite a switch the owner just changed.
const (
	SettingsKey = "media_formats"
	StateKey    = "media_rebuild"
)

// maxSkipped caps the list of images a rebuild could not process.
const maxSkipped = 20

// Settings are the owner's switches. A missing key means on.
type Settings struct {
	WebP bool `json:"webp"`
	AVIF bool `json:"avif"`
}

// Formats is the policy view of the settings.
func (s Settings) Formats() Formats { return Formats(s) }

// Skipped is an image a rebuild could not process.
type Skipped struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Reason string    `json:"reason"`
}

// LastRun summarises the previous finished rebuild.
type LastRun struct {
	FinishedAt time.Time `json:"finished_at"`
	Images     int       `json:"images"`
	Skipped    int       `json:"skipped"`
}

// State is the rebuild job's progress. It is saved after every image, so a
// restarted server continues from Cursor.
type State struct {
	Running   bool      `json:"running"`
	RunID     string    `json:"run_id,omitempty"`
	StartedAt time.Time `json:"started_at,omitzero"`
	StartedBy string    `json:"started_by,omitempty"`
	Total     int       `json:"total"`
	Done      int       `json:"done"`
	// SkippedCount counts every skipped image; Skipped lists the first few.
	SkippedCount int       `json:"skipped_count,omitempty"`
	Skipped      []Skipped `json:"skipped,omitempty"`
	// Cursor is the id of the last image handled; the run continues after it.
	Cursor uuid.UUID `json:"cursor"`
	Last   *LastRun  `json:"last,omitempty"`
}

// AddSkipped records an image that could not be processed.
func (s *State) AddSkipped(sk Skipped) {
	s.SkippedCount++
	if len(s.Skipped) < maxSkipped {
		s.Skipped = append(s.Skipped, sk)
	}
}

// Pct is the progress in whole percent, 0 when there is nothing to do.
func (s State) Pct() int {
	if s.Total <= 0 {
		return 0
	}
	return min(100, s.Done*100/s.Total)
}

// LoadState reads the settings and the job state out of theme_extra. It never
// fails: missing or garbled data gives the defaults (both formats on, no job).
func LoadState(themeExtra json.RawMessage) (Settings, State) {
	set := Settings{WebP: true, AVIF: true}
	var state State
	var top map[string]json.RawMessage
	if json.Unmarshal(themeExtra, &top) != nil {
		return set, state
	}
	// Per field, so a stored key that names only one switch keeps the other on.
	var f struct {
		WebP *bool `json:"webp"`
		AVIF *bool `json:"avif"`
	}
	if json.Unmarshal(top[SettingsKey], &f) == nil {
		if f.WebP != nil {
			set.WebP = *f.WebP
		}
		if f.AVIF != nil {
			set.AVIF = *f.AVIF
		}
	}
	if raw := top[StateKey]; len(raw) > 0 {
		var st State
		if json.Unmarshal(raw, &st) == nil {
			state = st
		}
	}
	return set, state
}

// SettingsRaw encodes the settings as the value of theme_extra[SettingsKey].
func SettingsRaw(set Settings) json.RawMessage {
	b, _ := json.Marshal(set)
	return b
}

// StateRaw encodes the job state as the value of theme_extra[StateKey].
func StateRaw(state State) json.RawMessage {
	b, _ := json.Marshal(state)
	return b
}
