package backup

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"hatamoto.dev/core/internal/jsonx"
)

// Retention bounds: every count must be in MinKeep..MaxKeep.
const (
	MinKeep = 1
	MaxKeep = 50

	// RetentionFile holds the owner's counts, in the backups dir. It is never
	// part of an archive and a restore never writes it.
	RetentionFile = "retention.json"

	defaultKeep          = 10
	defaultKeepPreUpdate = 5
	defaultKeepScheduled = 7
)

// Environment variables that set how many ok backups of a kind are kept. A
// valid value wins over retention.json and locks the setting.
const (
	EnvKeep          = "HATAMOTO_BACKUP_KEEP"
	EnvKeepPreUpdate = "HATAMOTO_BACKUP_KEEP_PRE_UPDATE"
	// EnvKeepScheduled sets how many scheduled (off-site schedule) backups stay on disk.
	EnvKeepScheduled = "HATAMOTO_BACKUP_KEEP_SCHEDULED"
)

// Retention is how many ok backups of each kind stay on disk.
type Retention struct{ Manual, PreUpdate, Scheduled int }

// For returns the count for kind; 0 for an unknown kind.
func (r Retention) For(kind Kind) int {
	switch kind {
	case KindManual:
		return r.Manual
	case KindPreUpdate:
		return r.PreUpdate
	case KindScheduled:
		return r.Scheduled
	}
	return 0
}

// RetentionState is the resolved counts plus which kinds an env var fixes.
type RetentionState struct {
	Retention
	Locked map[Kind]bool   // kinds set by an env var
	EnvVar map[Kind]string // the env var that locks each locked kind
}

// retentionKinds lists every kind with its env var, JSON key and default.
var retentionKinds = []struct {
	kind Kind
	env  string
	key  string
	def  int
}{
	{KindManual, EnvKeep, "manual", defaultKeep},
	{KindPreUpdate, EnvKeepPreUpdate, "pre_update", defaultKeepPreUpdate},
	{KindScheduled, EnvKeepScheduled, "scheduled", defaultKeepScheduled},
}

func validKeep(n int) bool { return n >= MinKeep && n <= MaxKeep }

// warnedEnv remembers the env var values already warned about, so a bad value
// is logged once per process and not on every backup.
var warnedEnv sync.Map

func warnEnvOnce(env, raw, msg string) {
	if _, dup := warnedEnv.LoadOrStore(env+"="+raw, true); !dup {
		slog.Warn(msg, "env", env, "value", raw)
	}
}

// envKeep reads a retention env var. A whole number above MaxKeep is clamped
// to MaxKeep; zero, a negative or a non-number is ignored. Both log a warning.
func envKeep(env string) (n int, ok bool) {
	raw := os.Getenv(env)
	if raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(raw)
	switch {
	case err != nil || v < MinKeep:
		warnEnvOnce(env, raw, "backup retention: ignoring env value, it must be a whole number from 1 to 50")
		return 0, false
	case v > MaxKeep:
		warnEnvOnce(env, raw, "backup retention: env value is above 50, keeping 50")
		return MaxKeep, true
	}
	return v, true
}

// readRetentionFile returns the valid counts in dir/retention.json by JSON
// key. A missing or unreadable file, bad JSON, unknown keys and out-of-range
// values give no entry.
func readRetentionFile(dir string) map[string]int {
	data, err := os.ReadFile(filepath.Join(dir, RetentionFile))
	out := map[string]int{}
	if err != nil {
		return out
	}
	var raw map[string]any
	if jsonx.Unmarshal(data, &raw, jsonx.StoredLoose) != nil {
		return out
	}
	for _, k := range retentionKinds {
		f, ok := raw[k.key].(float64)
		if n := int(f); ok && float64(n) == f && validKeep(n) {
			out[k.key] = n
		}
	}
	return out
}

// ResolveRetention gives the counts for the backups dir: per kind an env
// var (1 to 50; a larger number counts as 50) beats a valid value in retention.json, which beats the
// default. It never fails and does not need the database.
func ResolveRetention(dir string) RetentionState {
	file := readRetentionFile(dir)
	st := RetentionState{Locked: map[Kind]bool{}, EnvVar: map[Kind]string{}}
	for _, k := range retentionKinds {
		n := k.def
		if v, ok := file[k.key]; ok {
			n = v
		}
		if v, ok := envKeep(k.env); ok {
			n = v
			st.Locked[k.kind], st.EnvVar[k.kind] = true, k.env
		}
		switch k.kind {
		case KindManual:
			st.Manual = n
		case KindPreUpdate:
			st.PreUpdate = n
		case KindScheduled:
			st.Scheduled = n
		}
	}
	return st
}

var saveMu sync.Mutex

// SaveRetention writes the given counts into dir/retention.json, keeping the
// valid counts already there for other kinds. Every count must be 1 to 50. The
// caller leaves env-locked kinds out. The file is replaced atomically, mode 0600.
func SaveRetention(dir string, set map[Kind]int) error {
	saveMu.Lock() // the read-merge-write must not interleave with another save
	defer saveMu.Unlock()
	cur := readRetentionFile(dir)
	for _, k := range retentionKinds {
		n, ok := set[k.kind]
		if !ok {
			continue
		}
		if !validKeep(n) {
			return fmt.Errorf("backup retention: %s count %d is not %d to %d", k.key, n, MinKeep, MaxKeep)
		}
		cur[k.key] = n
	}
	data, err := jsonx.Indent(cur)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := writeTemp0600(dir, tmpPrefix+"retention-*", append(data, '\n'))
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, RetentionFile)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Data returns the data dir the engine works in.
func (e *Engine) Data() string { return e.opts.Data }

func (e *Engine) retentionDir() string { return filepath.Join(e.opts.Data, "backups") }

// Retention resolves the current counts from env and retention.json.
func (e *Engine) Retention() RetentionState { return ResolveRetention(e.retentionDir()) }

// currentRetention is what a finished backup prunes by: the Options.Retention
// seam when set, else the disk, read fresh each time.
func (e *Engine) currentRetention() Retention {
	if e.opts.Retention != nil {
		return e.opts.Retention()
	}
	return e.Retention().Retention
}

// SaveRetention stores the counts for the given kinds (callers omit env-locked
// ones).
func (e *Engine) SaveRetention(set map[Kind]int) error {
	return SaveRetention(e.retentionDir(), set)
}

// ErrPruneDeferred is returned by Prune when a backup is running: nothing was
// deleted, and the counts only apply the next time a backup finishes.
var ErrPruneDeferred = errors.New("backup running, prune deferred")

// Prune removes ok backups beyond the newest r.<kind> of each kind and returns
// how many it deleted per kind. It stops at the first error. It holds the
// backup lock so it cannot race the prune of a backup that is finishing. When a
// backup is running it deletes nothing and returns ErrPruneDeferred: the
// running backup prunes every kind by the current counts when it finishes, but
// not if it fails, and an off-site upload after it may still be holding the lock.
func (e *Engine) Prune(r Retention) (map[Kind]int, error) {
	s, err := e.st()
	if err != nil {
		return map[Kind]int{}, err
	}
	release, err := TryLock(s.Dir())
	if errors.Is(err, ErrBusy) {
		return map[Kind]int{}, ErrPruneDeferred
	}
	if err != nil {
		return map[Kind]int{}, err
	}
	defer release()
	return s.pruneKinds(r)
}

// pruneKinds prunes every kind by r, continuing past a kind that fails and
// returning the errors joined. The caller holds the backup lock.
func (s *Store) pruneKinds(r Retention) (map[Kind]int, error) {
	deleted := map[Kind]int{}
	var errs []error
	for _, k := range retentionKinds {
		keep := r.For(k.kind)
		if keep < MinKeep {
			continue
		}
		n, err := s.Prune(k.kind, keep)
		deleted[k.kind] = n
		errs = append(errs, err)
	}
	return deleted, errors.Join(errs...)
}
