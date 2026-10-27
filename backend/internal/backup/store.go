package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNotFound is returned for an unknown or invalid id, and for a download of
// a backup that is not ok.
var ErrNotFound = errors.New("backup not found")

// ErrRunning is returned when deleting a backup that is still being made.
var ErrRunning = errors.New("backup is running")

// Store reads and writes the records in $DATA/backups. It needs no database.
type Store struct{ dir string }

// NewStore opens (and creates) $data/backups, mode 0700: the archives hold the
// whole database.
func NewStore(data string) (*Store, error) {
	dir := filepath.Join(data, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	_ = os.Chmod(dir, 0o700) // MkdirAll leaves an older dir as it was
	return &Store{dir: dir}, nil
}

// Dir is the backups directory.
func (s *Store) Dir() string { return s.dir }

func (s *Store) sidecar(id string) string { return filepath.Join(s.dir, id+sidecarExt) }
func (s *Store) archive(id string) string { return filepath.Join(s.dir, id+archiveExt) }

// Write stores the sidecar through a temp file and a rename.
func (s *Store) Write(in Info) error {
	if !ValidID(in.ID) {
		return fmt.Errorf("invalid backup id %q", in.ID)
	}
	b, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, tmpPrefix+"*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // gone already after a successful rename
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.sidecar(in.ID))
}

// Get returns one record. A sidecar that says running while nobody holds the
// lock is reported (and rewritten) as failed: the process died.
func (s *Store) Get(id string) (Info, error) {
	if !ValidID(id) {
		return Info{}, ErrNotFound
	}
	b, err := os.ReadFile(s.sidecar(id))
	if errors.Is(err, os.ErrNotExist) {
		return Info{}, ErrNotFound
	}
	if err != nil {
		return Info{}, err
	}
	var in Info
	if err := json.Unmarshal(b, &in); err != nil {
		return Info{}, fmt.Errorf("read backup %s: %w", id, err)
	}
	if in.State == StateRunning {
		if release, err := TryLock(s.dir); err == nil {
			release()
			in.State, in.Error, in.Step = StateFailed, "interrupted", ""
			_ = s.Write(in)
		}
	}
	return in, nil
}

// List returns every backup, newest first. Legacy pre-*.dump files and
// anything hidden are not backups of this kind and are never touched.
func (s *Store) List() ([]Info, error) {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []Info
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), sidecarExt)
		if !ok || e.IsDir() || !ValidID(id) {
			continue
		}
		in, err := s.Get(id)
		if err != nil {
			continue
		}
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.After(out[j].Created)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// Delete removes the archive, then the sidecar. A backup that is really
// running is refused; an interrupted one reads as failed and can go.
func (s *Store) Delete(id string) error {
	in, err := s.Get(id)
	if err != nil {
		return err
	}
	if in.State == StateRunning {
		return ErrRunning
	}
	if err := os.Remove(s.archive(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_ = os.RemoveAll(filepath.Join(s.dir, tmpPrefix+id))
	if err := os.Remove(s.sidecar(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Open returns the archive of an ok backup for download.
func (s *Store) Open(id string) (*os.File, Info, error) {
	in, err := s.Get(id)
	if err != nil {
		return nil, Info{}, err
	}
	if in.State != StateOK {
		return nil, Info{}, ErrNotFound
	}
	f, err := os.Open(s.archive(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, Info{}, ErrNotFound
	}
	if err != nil {
		return nil, Info{}, err
	}
	return f, in, nil
}

// Prune removes ok backups of kind beyond the newest keep. Failed and running
// ones stay for the owner to look at and delete.
func (s *Store) Prune(kind Kind, keep int) error {
	all, err := s.List()
	if err != nil {
		return err
	}
	n := 0
	for _, in := range all {
		if in.Kind != kind || in.State != StateOK {
			continue
		}
		if n++; n <= keep {
			continue
		}
		if err := s.Delete(in.ID); err != nil {
			return err
		}
	}
	return nil
}
