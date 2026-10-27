package supervisor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
)

// ApplyOpts names the files `hatamoto update apply` verifies and stages.
type ApplyOpts struct {
	File    string // the binary
	Record  string // release record (signed bytes)
	Sig     string // "<key_id>:<base64>" file; default Record + ".sig"
	Release release.Options
	// Platform overrides runtime.GOOS-runtime.GOARCH (tests).
	Platform string
}

// Result says what an Apply or Rollback did; Message is for the operator.
type Result struct {
	Version  string
	Notified bool // the running supervisor was asked to do the gated switch
	Message  string
}

// Apply verifies the artifact against its signed record, stages it under
// releases/<version> and asks the supervisor (or, when none is running,
// switches the links itself) to run it. It fetches nothing.
func Apply(c Config, o ApplyOpts) (Result, error) {
	l, rec, err := verifyAndStage(c, o)
	if err != nil {
		return Result{}, err
	}
	var p Pending
	err = WithLock(l.LockPath(), func() error {
		st, err := LoadState(l.StatePath())
		if err != nil {
			return err
		}
		running := RunningVersion(l, st, c.BootstrapVersion)
		if err := checkApply(l, st, rec, running); err != nil {
			return err
		}
		if st.Pending != nil {
			return fmt.Errorf("another switch to %s is still in progress", st.Pending.Version)
		}
		p = Pending{Kind: "apply", Version: rec.Version, From: running, PrevBefore: l.Link("previous"),
			RequestedAt: time.Now().UTC()}
		st.Pending = &p
		return SaveState(l.StatePath(), st)
	})
	if err != nil {
		return Result{}, err
	}
	return dispatch(l, c, p)
}

// Stage verifies and stages the artifact exactly as Apply does, but never
// records a pending switch and never moves a link: the release is only made
// ready under releases/<version>.
func Stage(c Config, o ApplyOpts) (Result, error) {
	_, rec, err := verifyAndStage(c, o)
	if err != nil {
		return Result{}, err
	}
	return Result{Version: rec.Version, Message: fmt.Sprintf("staged %s; restart to apply", rec.Version)}, nil
}

// verifyAndStage is the part of Apply that touches no link and no pending
// marker: owner check, record and artifact verification, refusals, staging.
func verifyAndStage(c Config, o ApplyOpts) (Layout, *release.Record, error) {
	if !filepath.IsAbs(c.Data) {
		return Layout{}, nil, fmt.Errorf("data dir %q is not absolute", c.Data)
	}
	if err := CheckOwner(NewLayout(c.Data)); err != nil {
		return Layout{}, nil, err
	}
	rec, recBytes, sig, err := readSigned(o)
	if err != nil {
		return Layout{}, nil, err
	}
	platform := o.Platform
	if platform == "" {
		platform = runtime.GOOS + "-" + runtime.GOARCH
	}
	f, err := os.Open(o.File)
	if err != nil {
		return Layout{}, nil, err
	}
	defer f.Close()
	art, ok := rec.Artifacts[platform]
	if !ok {
		return Layout{}, nil, fmt.Errorf("artifact check failed: %w: no artifact for %s", release.ErrRecordMismatch, platform)
	}
	l := NewLayout(c.Data)
	if err := os.MkdirAll(l.Releases, 0o755); err != nil {
		return Layout{}, nil, err
	}
	if st, err := LoadState(l.StatePath()); err == nil { // refuse before staging; checked again under the lock
		if err := checkApply(l, st, rec, RunningVersion(l, st, c.BootstrapVersion)); err != nil {
			return Layout{}, nil, err
		}
	}
	if err := stage(l, rec.Version, f, art, o.Release, recBytes, sig); err != nil {
		return Layout{}, nil, err
	}
	return l, rec, nil
}

// StoredRecord returns the verified record of a staged release.
func StoredRecord(c Config, version string) (*release.Record, error) {
	l := NewLayout(c.Data)
	if !validName(version) {
		return nil, fmt.Errorf("invalid release name %q", version)
	}
	dir := filepath.Join(l.Releases, version)
	recBytes, err := readNoFollow(filepath.Join(dir, "release.json"))
	if err != nil {
		return nil, fmt.Errorf("stored record: %w", err)
	}
	sigBytes, err := readNoFollow(filepath.Join(dir, "release.json.sig"))
	if err != nil {
		return nil, fmt.Errorf("stored signature: %w", err)
	}
	rec, err := release.VerifyRecord(recBytes, strings.TrimSpace(string(sigBytes)), c.Release)
	if err != nil {
		return nil, fmt.Errorf("stored record check failed: %w", err)
	}
	if rec.Version != version {
		return nil, fmt.Errorf("stored record is for %s, not %s", rec.Version, version)
	}
	return rec, nil
}

// Running reports whether a supervisor holds the pid lock under c.Data.
func Running(c Config) bool {
	_, ok := supervisorPID(NewLayout(c.Data))
	return ok
}

// readSigned loads and verifies the record and its signature.
func readSigned(o ApplyOpts) (*release.Record, []byte, string, error) {
	recBytes, err := os.ReadFile(o.Record)
	if err != nil {
		return nil, nil, "", err
	}
	sigPath := o.Sig
	if sigPath == "" {
		sigPath = o.Record + ".sig"
	}
	sigBytes, err := os.ReadFile(sigPath)
	if err != nil {
		return nil, nil, "", fmt.Errorf("signature: %w (pass --sig)", err)
	}
	sig := strings.TrimSpace(string(sigBytes))
	rec, err := release.VerifyRecord(recBytes, sig, o.Release)
	if err != nil {
		return nil, nil, "", fmt.Errorf("record check failed: %w", err)
	}
	return rec, recBytes, sig, nil
}

// RunningVersion is the version the child runs now: the current release when
// the supervisor runs one, else the bootstrap's own stamp. Everything that
// reports or decides on "the running version" uses it, so the CLI and serve
// agree with the supervisor.
func RunningVersion(l Layout, st *State, bootstrapVersion string) string {
	if st.Run == RunRelease && l.Link("current") != "" {
		return l.Link("current")
	}
	return bootstrapVersion
}

// BootstrapVersionOf is the bootstrap binary's version: the stamp the supervisor
// recorded in st, else fallback (the caller's own stamp, right only when the
// caller is the bootstrap binary).
func BootstrapVersionOf(st *State, fallback string) string {
	if st.BootstrapVersion != "" {
		return st.BootstrapVersion
	}
	return fallback
}

// checkApply holds the refusals; values that are not semver never block.
func checkApply(l Layout, st *State, rec *release.Record, running string) error {
	if st.IsBad(rec.Version) {
		return fmt.Errorf("%s is on the bad list (rolled back on this box before); a newer version supersedes it", rec.Version)
	}
	if rec.Version == running {
		return fmt.Errorf("%s is already running", rec.Version)
	}
	if rec.MinFrom != "" && release.ValidVersion(running) && packages.Newer(rec.MinFrom, running) {
		return fmt.Errorf("%s needs at least %s first (running %s)", rec.Version, rec.MinFrom, running)
	}
	if floor := storedFloor(l, running); floor != "" && packages.Newer(floor, rec.Version) {
		return fmt.Errorf("%s is below the rollback floor %s of the running release %s", rec.Version, floor, running)
	}
	return nil
}

// storedFloor reads rollback_floor from a version's stored release.json.
func storedFloor(l Layout, version string) string {
	if !validName(version) {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(l.Releases, version, "release.json"))
	if err != nil {
		return ""
	}
	var r struct {
		RollbackFloor string `json:"rollback_floor"`
	}
	if json.Unmarshal(b, &r) != nil {
		return ""
	}
	return r.RollbackFloor
}

// stage copies the artifact into releases/<version> through a temp dir and a
// rename. An existing directory with the same binary is reused; a different
// binary under the same version is an error.
func stage(l Layout, version string, artifact io.Reader, art release.Artifact, ro release.Options, rec []byte, sig string) error {
	sha := art.SHA256
	final := filepath.Join(l.Releases, version)
	if _, err := os.Stat(final); err == nil {
		have, err := fileSHA(l.Binary(version))
		if err != nil {
			return fmt.Errorf("%s exists but is unreadable: %w", final, err)
		}
		if !strings.EqualFold(have, sha) {
			return fmt.Errorf("%s is already staged with a different binary", version)
		}
		return nil
	}
	// An unpredictable name: nobody can pre-plant a symlink or directory there.
	tmp, err := os.MkdirTemp(l.Releases, ".tmp-"+version+"-")
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	fail := func(err error) error { _ = os.RemoveAll(tmp); return err }
	if err := writeVerified(filepath.Join(tmp, "hatamoto"), artifact, art, ro, 0o755); err != nil {
		return fail(fmt.Errorf("artifact check failed: %w", err))
	}
	if err := writeSynced(filepath.Join(tmp, "release.json"), bytes.NewReader(rec), 0o644); err != nil {
		return fail(err)
	}
	if err := writeSynced(filepath.Join(tmp, "release.json.sig"), strings.NewReader(sig+"\n"), 0o644); err != nil {
		return fail(err)
	}
	if err := syncDir(tmp); err != nil {
		return fail(err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fail(err)
	}
	return syncDir(l.Releases)
}

// writeVerified copies the artifact once, hashing the bytes it writes: the
// file on disk is exactly what was checked, whatever happens to the source
// meanwhile. Reading stops one byte past the recorded size.
func writeVerified(path string, src io.Reader, art release.Artifact, ro release.Options, mode os.FileMode) error {
	limit := ro.MaxArtifact
	if limit <= 0 {
		limit = release.MaxArtifactSize
	}
	if art.Size < 0 || art.Size > limit {
		return fmt.Errorf("%w: record says %d bytes, the limit is %d", release.ErrArtifactSize, art.Size, limit)
	}
	h := sha256.New()
	n, err := writeSyncedN(path, io.TeeReader(io.LimitReader(src, art.Size+1), h), mode)
	if err != nil {
		return err
	}
	if n != art.Size {
		return fmt.Errorf("%w: got %d bytes, record says %d", release.ErrArtifactSize, n, art.Size)
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), art.SHA256) {
		return release.ErrArtifactHash
	}
	return nil
}

func writeSynced(path string, r io.Reader, mode os.FileMode) error {
	_, err := writeSyncedN(path, r, mode)
	return err
}

func writeSyncedN(path string, r io.Reader, mode os.FileMode) (int64, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|noFollow, mode)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if err != nil {
		f.Close()
		return n, err
	}
	if err := f.Chmod(mode); err != nil { // umask must not strip the executable bit
		f.Close()
		return n, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return n, err
	}
	return n, f.Close()
}

// Rollback switches current back to previous (or the bootstrap when that was
// what ran before), health-gated when a supervisor is running. It refuses a
// previous release that is on the bad list unless c.Force is set.
func Rollback(c Config) (Result, error) {
	if !filepath.IsAbs(c.Data) {
		return Result{}, fmt.Errorf("data dir %q is not absolute", c.Data)
	}
	l := NewLayout(c.Data)
	if err := CheckOwner(l); err != nil {
		return Result{}, err
	}
	if _, err := os.Stat(l.Releases); err != nil {
		return Result{}, errors.New("no previous release")
	}
	var p Pending
	err := WithLock(l.LockPath(), func() error {
		st, err := LoadState(l.StatePath())
		if err != nil {
			return err
		}
		prev := l.Link("previous")
		target := prev
		switch {
		case l.Valid(prev):
		case st.PreviousIsBootstrap:
			target, prev = RunBootstrap, BootstrapVersionOf(st, c.BootstrapVersion)
		default:
			return errors.New("no previous release")
		}
		if st.Pending != nil {
			return fmt.Errorf("another switch to %s is still in progress", st.Pending.Version)
		}
		if st.Ungated != nil && st.Ungated.Version == target && !c.Force {
			return fmt.Errorf("%s was applied but never ran or passed a health gate; use `update rollback --force` to go back to it anyway", prev)
		}
		if st.IsBad(target) && !c.Force {
			return fmt.Errorf("%s is on the bad list (it failed before); use `update rollback --force` to go back to it anyway", prev)
		}
		running := RunningVersion(l, st, c.BootstrapVersion)
		floor := storedFloor(l, running)
		if floor != "" && !release.ValidVersion(prev) {
			return fmt.Errorf("the bootstrap build's version is unknown, so it cannot be checked against the rollback floor %s of the running release %s", floor, running)
		}
		if floor != "" && packages.Newer(floor, prev) {
			return fmt.Errorf("%s is below the rollback floor %s of the running release %s", prev, floor, running)
		}
		p = Pending{Kind: "rollback", Version: target, From: running, PrevBefore: l.Link("previous"),
			RequestedAt: time.Now().UTC()}
		st.Pending = &p
		return SaveState(l.StatePath(), st)
	})
	if err != nil {
		return Result{}, err
	}
	return dispatch(l, c, p)
}

// supervisorPID is the pid in supervisor.pid when a process holds its lock.
func supervisorPID(l Layout) (int, bool) { return pidLockHolder(l.PIDPath()) }

// dispatch hands the pending switch to a running supervisor, or does the link
// swap itself, ungated, when none is running.
func dispatch(l Layout, c Config, p Pending) (Result, error) {
	if pid, ok := supervisorPID(l); ok {
		notify := c.notify
		if notify == nil {
			notify = hupProc
		}
		if err := notify(pid); err == nil {
			return Result{Version: p.Version, Notified: true,
				Message: fmt.Sprintf("staged %s; supervisor notified (health-gated switch in progress; see logs)", p.Version)}, nil
		}
	}
	if err := switchOffline(l, c, p); err != nil {
		return Result{}, err
	}
	return Result{Version: p.Version, Message: fmt.Sprintf(
		"staged and switched to %s; restart hatamoto (systemctl restart hatamoto-api / docker compose restart) to use it", p.Version)}, nil
}

// pointPreviousAt makes `previous` the release b was running, or none (the
// bootstrap) when b was not running a release.
func pointPreviousAt(l Layout, st *State, b *LinkSnapshot) error {
	if b.Run == RunRelease && b.Current != "" {
		if err := l.SwapSymlink("previous", b.Current); err != nil {
			return err
		}
		st.PreviousIsBootstrap = false
		return nil
	}
	if err := l.RemoveLink("previous"); err != nil {
		return err
	}
	st.PreviousIsBootstrap = true
	return nil
}

// switchOffline is switchTo without the child and the gate: the same link
// moves and bookkeeping, for a box where no supervisor is running.
func switchOffline(l Layout, c Config, p Pending) error {
	return WithLock(l.LockPath(), func() error {
		st, err := LoadState(l.StatePath())
		if err != nil {
			return err
		}
		p.Before = snapshotOf(l, st)
		p.StartedAt = time.Now().UTC()
		if err := swapLinks(l, st, &p); err != nil {
			return err
		}
		if p.Kind == "apply" && st.Ungated != nil && st.Ungated.Version == p.Before.Current && st.Ungated.Before != nil {
			// The release being replaced never ran here. `previous` stays the
			// last release that did run and passed.
			if err := pointPreviousAt(l, st, st.Ungated.Before); err != nil {
				return err
			}
		}
		if st.BootstrapSHA256 == "" && c.Bootstrap != "" {
			// The supervisor has never run here; its first start must not
			// mistake this binary for a fresh bootstrap and ignore the switch.
			if sha, err := fileSHA(c.Bootstrap); err == nil {
				st.BootstrapSHA256 = sha
			}
		}
		phase := PhaseStagedOffline
		if p.Kind == "rollback" && p.Before.Run == RunRelease && p.Before.Current != "" {
			phase = PhaseManualRollback
			st.AddBad(p.Before.Current, "manual rollback")
		} else if p.Kind == "rollback" {
			phase = PhaseRolledBackOffline
			st.OfflineRollback = p.Version
		}
		st.Attempts++
		st.Pending, st.Phase, st.LastError = nil, "idle", ""
		if p.Kind == "apply" || st.Unproven != p.Version {
			st.Unproven = "" // a rollback target keeps its proven status: it was good before
		}
		if p.Kind == "apply" && st.Run == RunRelease {
			// No gate ran. The supervisor gates it at its next start; until
			// then it is unproven. A second offline apply keeps the first
			// snapshot: that is the last state that was known good.
			st.Unproven = p.Version
			u := p
			if st.Ungated != nil {
				u.Before = st.Ungated.Before
			}
			st.Ungated = &u
		} else if p.Kind != "apply" && (st.Ungated == nil || st.Ungated.Version != p.Version) {
			st.Ungated = nil
		}
		if err := SaveState(l.StatePath(), st); err != nil {
			return err
		}
		return AppendEvent(l.EventsPath(), Event{From: fromName(p.Before), To: p.Version, Phase: phase,
			StartedAt: p.StartedAt, FinishedAt: time.Now().UTC()})
	})
}
