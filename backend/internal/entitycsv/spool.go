package entitycsv

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/importjob"
	"hatamoto.dev/core/internal/jsonx"
)

// Spool limits.
const (
	MaxFileBytes = 32 << 20
	// SpoolRetention is how long an unfinished upload is kept.
	SpoolRetention = 24 * time.Hour
)

// Spool errors.
var (
	ErrFileEmpty    = errors.New("the file is empty")
	ErrFileTooLarge = fmt.Errorf("the file is larger than %d MiB", MaxFileBytes>>20)
	ErrNoUpload     = errors.New("this upload no longer exists")
)

// Spool keeps uploaded CSV files and their wizard state as plain files in
// <Dir>/csv: <id>.csv and <id>.json. There is no database state.
type Spool struct {
	// Dir is the import spool directory (the one the WordPress importer uses).
	Dir string
}

// State is the wizard state of one upload.
type State struct {
	Type     string     `json:"type"`
	FileName string     `json:"file_name"`
	Header   []string   `json:"header"`
	Sample   [][]string `json:"sample,omitempty"`
	Rows     int        `json:"rows"`
	Mapping  *Mapping   `json:"mapping,omitempty"`
	Dry      *Report    `json:"dry,omitempty"`
	Done     *Report    `json:"done,omitempty"`
}

func (s *Spool) dir() string { return filepath.Join(s.Dir, "csv") }

func (s *Spool) path(id uuid.UUID, ext string) string {
	return filepath.Join(s.dir(), id.String()+ext)
}

func (s *Spool) ensure() error {
	if err := importjob.CheckSpoolDir(s.Dir); err != nil {
		return err
	}
	return os.MkdirAll(s.dir(), 0o700)
}

// Save stores the upload (at most max bytes) and deletes uploads older than
// SpoolRetention.
func (s *Spool) Save(r io.Reader, max int64) (uuid.UUID, error) {
	if err := s.ensure(); err != nil {
		return uuid.Nil, err
	}
	s.Sweep()
	id := uuid.New()
	f, err := os.OpenFile(s.path(id, ".csv"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return uuid.Nil, err
	}
	n, err := io.Copy(f, io.LimitReader(r, max+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
	case n == 0:
		err = ErrFileEmpty
	case n > max:
		err = ErrFileTooLarge
	}
	if err != nil {
		_ = os.Remove(s.path(id, ".csv"))
		return uuid.Nil, err
	}
	return id, nil
}

// Open opens the stored CSV file.
func (s *Spool) Open(id uuid.UUID) (*os.File, error) {
	f, err := os.Open(s.path(id, ".csv"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoUpload
	}
	return f, err
}

// SaveState writes the wizard state of an upload.
func (s *Spool) SaveState(id uuid.UUID, st *State) error {
	b, err := jsonx.Marshal(st)
	if err != nil {
		return err
	}
	tmp := s.path(id, ".json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(id, ".json"))
}

// LoadState reads the wizard state of an upload.
func (s *Spool) LoadState(id uuid.UUID) (*State, error) {
	b, err := os.ReadFile(s.path(id, ".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoUpload
	}
	if err != nil {
		return nil, err
	}
	var st State
	if err := jsonx.Unmarshal(b, &st, jsonx.Stored); err != nil {
		return nil, err
	}
	return &st, nil
}

// Remove deletes the CSV file of an upload and keeps its state (the report).
func (s *Spool) Remove(id uuid.UUID) error {
	if err := os.Remove(s.path(id, ".csv")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Sweep deletes uploads and reports older than SpoolRetention. Every wizard
// request calls it, so an abandoned upload does not outlive the retention
// until the next upload.
func (s *Spool) Sweep() { s.sweep(time.Now().Add(-SpoolRetention)) }

// sweep deletes spooled files last written before cutoff.
func (s *Spool) sweep(cutoff time.Time) {
	entries, err := os.ReadDir(s.dir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && !e.IsDir() && fi.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(s.dir(), e.Name()))
		}
	}
}
