// Package supervisor runs the hatamoto server as a child process, picks which
// binary it runs from $HATAMOTO_DATA/releases, health-gates every switch and
// rolls back on its own. It has no database access and no network client
// other than loopback calls to its own child.
package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Layout is the on-disk release directory:
//
//	releases/v1.4.2/hatamoto  release.json  release.json.sig
//	releases/current -> v1.4.2     releases/previous -> v1.4.1  (relative)
//	releases/state.json  state.lock  events.jsonl  supervisor.pid
type Layout struct{ Data, Releases string }

// NewLayout builds the layout for an absolute data directory.
func NewLayout(data string) Layout {
	return Layout{Data: data, Releases: filepath.Join(data, "releases")}
}

// StatePath, LockPath, EventsPath and PIDPath name the bookkeeping files.
func (l Layout) StatePath() string  { return filepath.Join(l.Releases, "state.json") }
func (l Layout) LockPath() string   { return filepath.Join(l.Releases, "state.lock") }
func (l Layout) EventsPath() string { return filepath.Join(l.Releases, "events.jsonl") }
func (l Layout) PIDPath() string    { return filepath.Join(l.Releases, "supervisor.pid") }

// Binary is the path of a version's executable.
func (l Layout) Binary(version string) string {
	return filepath.Join(l.Releases, version, "hatamoto")
}

// validName reports whether name is a single plain path element.
func validName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, `/\`) && !strings.HasPrefix(name, ".")
}

// Valid reports whether version names a release directory holding a regular
// executable hatamoto file.
func (l Layout) Valid(version string) bool {
	if !validName(version) {
		return false
	}
	fi, err := os.Stat(l.Binary(version))
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

// Link returns the version a symlink ("current" or "previous") points at, or
// "" when it is missing or not a bare version name.
func (l Layout) Link(name string) string {
	t, err := os.Readlink(filepath.Join(l.Releases, name))
	if err != nil || !validName(t) {
		return ""
	}
	return t
}

// SwapSymlink atomically points releases/<name> at the bare directory name
// target: create <name>.new, rename over <name>, fsync the directory. This is
// the same move RUNBOOK §8 does by hand.
func (l Layout) SwapSymlink(name, target string) error {
	if !validName(target) {
		return errors.New("invalid link target " + target)
	}
	tmp := filepath.Join(l.Releases, name+".new")
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(l.Releases, name)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncDir(l.Releases)
}

// RemoveLink deletes releases/<name> (a symlink); a missing link is fine.
func (l Layout) RemoveLink(name string) error {
	if err := os.Remove(filepath.Join(l.Releases, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(l.Releases)
}

// Writable creates the releases directory if needed and proves it accepts
// files. The error says why not.
func (l Layout) Writable() error {
	if err := os.MkdirAll(l.Releases, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(l.Releases, ".probe-*")
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Remove(f.Name())
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
