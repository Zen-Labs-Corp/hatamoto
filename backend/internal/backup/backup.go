// Package backup produces and manages owner-run backups: one tar per backup
// holding the core dump, one dump per plugin schema, the portability export
// and a manifest. Records live on disk (a sidecar next to each archive), so
// listing and deleting work with the database down and survive a restore.
package backup

import (
	"regexp"
	"time"
)

// Kind says why a backup was taken.
type Kind string

const (
	KindManual    Kind = "manual"
	KindPreUpdate Kind = "pre_update"
	// KindScheduled is reserved; nothing creates it yet.
	KindScheduled Kind = "scheduled"
)

// State is where a backup is in its life.
type State string

const (
	StateRunning State = "running"
	StateOK      State = "ok"
	StateFailed  State = "failed"
)

// Info is the sidecar record of one backup.
type Info struct {
	ID      string    `json:"id"`
	Kind    Kind      `json:"kind"`
	Target  string    `json:"target,omitempty"`
	State   State     `json:"state"`
	Step    string    `json:"step,omitempty"`
	Percent int       `json:"percent"`
	Error   string    `json:"error,omitempty"`
	Created time.Time `json:"created"`
	Size    int64     `json:"size,omitempty"`
	Parts   []string  `json:"parts,omitempty"`
	Plugins []string  `json:"plugins,omitempty"`
	// MediaMissing counts media files the database references that were not
	// on disk; the backup is still OK, it just lacks them.
	MediaMissing int `json:"media_missing,omitempty"`
}

var idPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-(manual|pre_update|scheduled)$`)

// ValidID reports whether id has the shape NewID produces. Anything that
// reaches the file system goes through it first.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// NewID is the id of a backup of kind created at t.
func NewID(t time.Time, kind Kind) string {
	return t.UTC().Format("20060102T150405Z") + "-" + string(kind)
}

const (
	archiveExt = ".tar"
	sidecarExt = ".json"
	lockName   = ".lock"
	tmpPrefix  = ".tmp-"
)
