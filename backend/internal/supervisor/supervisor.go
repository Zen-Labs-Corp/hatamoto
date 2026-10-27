package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// errStopped means a stop was requested while an operation was in flight.
var errStopped = errors.New("supervisor stopping")

type sup struct {
	cfg    Config
	layout Layout
	log    *slog.Logger

	writable bool
	st       *State // in-memory copy; reloaded under the lock before each change
	hup      chan struct{}

	child      proc
	childName  string // RunBootstrap or a version
	childStart time.Time
	smokeAddr  string
	token      string
	crashes    crashTracker

	pluginErrors []string       // from the last passed gate, stored on success
	baseline     map[string]int // same: per-check status of that run
	bootFaults   []string       // same: its boot fault identifiers
	renderFirst  int            // same: render errors of its first smoke call

	provenTimer *time.Timer // fires when the unproven child has lived the boot window
	provenC     <-chan time.Time
	provenChild proc

	initial       chan initialResult // smoke of a child that was not switched to
	cancelInitial context.CancelFunc
}

// initialResult is what the smoke probe of an initially running child found.
type initialResult struct {
	child proc
	res   SmokeResult
}

// Run supervises the child until ctx is cancelled or SIGTERM/SIGINT arrives,
// then stops it and returns nil. SIGHUP means "look at state.json" and is
// never forwarded to the child.
func Run(ctx context.Context, c Config) error {
	s, err := newSup(c)
	if err != nil {
		return err
	}
	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(sigs)
	stopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		for {
			select {
			case sg := <-sigs:
				if sg == syscall.SIGHUP {
					s.poke()
					continue
				}
				s.log.Info("stop requested", "event", "stop", "signal", sg.String())
				cancel()
				return
			case <-stopCtx.Done():
				return
			}
		}
	}()
	return s.loop(stopCtx)
}

func newSup(c Config) (*sup, error) {
	c.setDefaults()
	if !filepath.IsAbs(c.Data) {
		return nil, fmt.Errorf("supervisor: data dir %q is not absolute", c.Data)
	}
	s := &sup{
		cfg:     c,
		layout:  NewLayout(c.Data),
		log:     c.Log,
		hup:     make(chan struct{}, 1),
		initial: make(chan initialResult, 1),
		crashes: crashTracker{window: c.BootWindow},
		st:      &State{V: 1, Phase: "idle"},
	}
	return s, nil
}

// poke is the SIGHUP path: wake the loop (signals coalesce).
func (s *sup) poke() {
	select {
	case s.hup <- struct{}{}:
	default:
	}
}

func (s *sup) loop(ctx context.Context) error {
	if err := s.layout.Writable(); err != nil {
		s.log.Warn("releases dir not writable, running the bootstrap binary and writing no state",
			"event", "start", "dir", s.layout.Releases, "err", err)
	} else {
		s.writable = true
		s.sweepTmp()
		lock, err := acquirePIDLock(s.layout.PIDPath())
		if err != nil {
			return err
		}
		defer lock.release()
		if err := s.update(func(st *State) error {
			if st.Phase == "probation" && st.Pending == nil {
				st.Phase = "idle"
			}
			return nil
		}); err != nil {
			s.st = &State{V: 1, Phase: "idle"}
			var corrupt *CorruptStateError
			if errors.As(err, &corrupt) {
				s.log.Warn("state.json is corrupt, setting it aside and starting from a clean one", "event", "start", "err", err)
				_ = os.Rename(s.layout.StatePath(), s.layout.StatePath()+".unreadable")
			} else {
				// Permissions or I/O: the file may be fine. Keep it, and the bad
				// list in it, untouched; run without it until it can be read.
				s.log.Error("cannot read state.json; leaving it in place and running without it (fix its owner or mode)",
					"event", "start", "path", s.layout.StatePath(), "err", err)
			}
		}
	}
	defer s.stopChild()

	if s.writable {
		s.gateOfflineApply()
	}
	if s.writable && s.st.Pending != nil {
		if err := s.switchTo(ctx); errors.Is(err, errStopped) {
			return nil
		}
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		if s.child == nil {
			s.startChosen()
			s.armProven()
			s.probeInitial(ctx)
		}
		select {
		case <-s.provenC:
			s.markProven()
		case r := <-s.initial:
			s.recordInitial(r)
		case <-ctx.Done():
			return nil
		case <-s.hup:
			if !s.writable {
				s.log.Info("hup ignored, no writable state", "event", "switch")
				continue
			}
			if err := s.reload(); err != nil {
				s.log.Warn("cannot read state", "event", "switch", "err", err)
				continue
			}
			if s.st.Pending == nil {
				s.log.Info("hup: nothing pending", "event", "switch")
				continue
			}
			if err := s.switchTo(ctx); errors.Is(err, errStopped) {
				return nil
			}
		case <-s.child.Done():
			if !s.onCrash(ctx) {
				return nil
			}
		}
	}
}

// gateOfflineApply turns a release applied with no supervisor running into a
// pending switch, so the normal gate (and rollback on failure) runs on it
// before anything else starts. The usual choice comes first: a bootstrap that
// changed since the last start, or is newer, still wins, and then the record is
// dropped (the release stays installed, unproven nowhere, as before J1).
func (s *sup) gateOfflineApply() {
	_ = s.update(func(st *State) error {
		u := st.Ungated
		if u == nil {
			return nil
		}
		st.Ungated = nil
		if st.Pending != nil || u.Before == nil || s.layout.Link("current") != u.Version {
			return nil
		}
		trial := *st // Choose edits the state it is given; this is only a look
		c, err := s.lookChoose(&trial)
		if err != nil || c.Bootstrap || c.Name != u.Version {
			why := "bootstrap wins"
			if err != nil {
				why = err.Error()
			}
			if st.Unproven == u.Version {
				st.Unproven = ""
			}
			s.log.Info("offline apply not gated: the usual choice does not run it", "event", "start",
				"version", u.Version, "why", why)
			return nil
		}
		st.Pending = u
		return nil
	})
}

// onCrash handles a child exit nobody asked for. It reports false when a stop
// arrived while waiting to restart.
func (s *sup) onCrash(ctx context.Context) bool {
	uptime := time.Since(s.childStart)
	n := s.crashes.record(uptime)
	s.log.Warn("child exited unexpectedly", "event", "crash", "version", s.childName,
		"uptime", uptime.Round(time.Millisecond).String(), "consecutive", n, "err", s.child.Err())
	name := s.childName
	s.child = nil
	if uptime >= s.cfg.BootWindow && s.writable { // it served long enough: no longer in its boot window
		_ = s.update(func(st *State) error {
			if st.Unproven == name {
				st.Unproven = ""
			}
			return nil
		})
	}
	if s.crashes.looping() {
		if s.crashLoopRollback(name) {
			s.crashes.reset()
			return true
		}
		s.log.Warn("crash loop, restarting with backoff (no rollback: not in a switch's boot window)",
			"event", "crash", "version", name)
	}
	return s.sleep(ctx, backoff(s.cfg.BackoffBase, n))
}

// sleep waits d; false when ctx ended first.
func (s *sup) sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// startChosen picks a binary and starts it. It cannot fail: a binary that
// cannot launch is a child that has already exited, which the loop treats as
// a crash.
func (s *sup) startChosen() {
	c, err := s.choose()
	if err != nil {
		s.log.Warn("cannot choose a binary, using bootstrap", "event", "start", "err", err)
		c = Choice{Bootstrap: true, Name: RunBootstrap, Reason: "choice failed"}
	}
	s.log.Info("starting child", "event", "start", "version", c.Name, "why", c.Reason)
	s.startChild(c)
}

// chooseInput re-hashes the bootstrap binary and reads the links.
func (s *sup) chooseInput() (ChooseInput, error) {
	in := ChooseInput{BootstrapVersion: s.cfg.BootstrapVersion}
	if !s.writable {
		in.WritableErr = errors.New("running without state")
		return in, nil
	}
	sha, err := fileSHA(s.cfg.Bootstrap)
	if err != nil {
		return in, err
	}
	in.BootstrapSHA = sha
	in.Current = s.layout.Link("current")
	in.CurrentOK = s.layout.Valid(in.Current)
	return in, nil
}

// lookChoose is Choose on st with nothing verified or changed on disk.
func (s *sup) lookChoose(st *State) (Choice, error) {
	in, err := s.chooseInput()
	if err != nil {
		return Choice{}, err
	}
	return Choose(in, st), nil
}

// choose applies Choose to the stored state and verifies the release it picks.
func (s *sup) choose() (Choice, error) {
	in, err := s.chooseInput()
	if err != nil {
		return Choice{}, err
	}
	if !s.writable {
		return Choose(in, s.st), nil
	}
	var c Choice
	err = s.update(func(st *State) error {
		c = Choose(in, st)
		if c.Bootstrap {
			return nil
		}
		verr := s.verifyRelease(c.Name)
		if verr == nil {
			return nil
		}
		// The staged binary is not the one that was signed. Never run it.
		s.log.Error("staged release failed verification, not running it", "event", "verify", "version", c.Name, "err", verr)
		st.AddBad(c.Name, "failed verification: "+verr.Error())
		st.LastError = "failed verification: " + verr.Error()
		c = s.fallbackFrom(st, c.Name, in.Current)
		return nil
	})
	return c, err
}

// fallbackFrom picks what runs when current failed verification: previous if
// it is valid, not bad and verifies (current then points at it), else the
// bootstrap, kept while current stays as it is.
func (s *sup) fallbackFrom(st *State, bad, cur string) Choice {
	prev := s.layout.Link("previous")
	if bad == cur && prev != "" && prev != bad && s.layout.Valid(prev) && !st.IsBad(prev) && s.verifyRelease(prev) == nil {
		if s.layout.SwapSymlink("current", prev) == nil && s.layout.RemoveLink("previous") == nil {
			st.Run, st.CurrentSeen, st.PreviousIsBootstrap = RunRelease, prev, false
			return Choice{Name: prev, Reason: "previous release (current failed verification)"}
		}
	}
	st.Run, st.CurrentSeen = RunBootstrap, s.layout.Link("current")
	return Choice{Bootstrap: true, Name: RunBootstrap, Reason: "current release failed verification"}
}

func (s *sup) binaryPath(c Choice) string {
	if c.Bootstrap {
		return s.cfg.Bootstrap
	}
	return s.layout.Binary(c.Name)
}

// startChild launches c with a fresh smoke address and token.
func (s *sup) startChild(c Choice) {
	addr, err := FreeLoopbackAddr()
	if err == nil {
		s.token, err = NewToken()
	}
	if err != nil {
		s.log.Warn("cannot prepare child environment", "event", "start", "err", err)
		s.child, s.childName, s.childStart = deadProc{err}, c.Name, time.Now()
		return
	}
	s.smokeAddr = addr
	if s.writable {
		if err := s.update(func(st *State) error { st.SmokeToken = s.token; return nil }); err != nil {
			s.log.Warn("cannot store smoke token", "event", "start", "err", err)
		}
	}
	env := s.cfg.Env
	if env == nil {
		env = os.Environ()
	}
	p, err := s.cfg.launch(s.binaryPath(c), ChildEnv(env, s.cfg.Data, addr, s.token))
	s.childName, s.childStart = c.Name, time.Now()
	if err != nil {
		s.log.Warn("child failed to launch", "event", "start", "version", c.Name, "err", err)
		s.child = deadProc{err}
		return
	}
	s.child = p
	s.log.Info("child started", "event", "start", "version", c.Name, "pid", p.Pid(), "smoke_addr", addr)
}

func (s *sup) stopChild() {
	if s.child == nil {
		return
	}
	if s.cancelInitial != nil {
		s.cancelInitial()
	}
	killed := s.child.Stop(s.cfg.StopGrace)
	s.log.Info("child stopped", "event", "stop", "version", s.childName, "forced", killed)
	s.child = nil
}

// deadProc is a child that never started.
type deadProc struct{ err error }

func (deadProc) Pid() int                { return 0 }
func (d deadProc) Err() error            { return d.err }
func (deadProc) Stop(time.Duration) bool { return false }
func (deadProc) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// update is the one way state changes: under the shared lock, load, mutate,
// save. Without a writable releases dir it changes the in-memory copy only.
func (s *sup) update(fn func(*State) error) error {
	if !s.writable {
		return fn(s.st)
	}
	return WithLock(s.layout.LockPath(), func() error {
		st, err := LoadState(s.layout.StatePath())
		if err != nil {
			return err
		}
		if err := fn(st); err != nil {
			return err
		}
		if err := SaveState(s.layout.StatePath(), st); err != nil {
			return err
		}
		s.st = st
		return nil
	})
}

func (s *sup) reload() error { return s.update(func(*State) error { return nil }) }

func (s *sup) event(e Event) {
	if !s.writable {
		return
	}
	if err := AppendEvent(s.layout.EventsPath(), e); err != nil {
		s.log.Warn("cannot record event", "event", "switch", "err", err)
	}
}

// sweepTmp removes staging dirs an interrupted apply left behind.
func (s *sup) sweepTmp() {
	ents, _ := os.ReadDir(s.layout.Releases)
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), ".tmp-") {
			continue
		}
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > 10*time.Minute {
			_ = os.RemoveAll(filepath.Join(s.layout.Releases, e.Name()))
		}
	}
}

func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// probeInitial smokes a child that was started without a switch, once, so its
// per-check status can become the baseline for the next switch. Nothing is
// decided here; a failed probe records nothing.
func (s *sup) probeInitial(ctx context.Context) {
	if s.child == nil || s.st.Pending != nil || !s.writable {
		return
	}
	if s.cancelInitial != nil {
		s.cancelInitial()
	}
	pctx, cancel := context.WithCancel(ctx)
	s.cancelInitial = cancel
	child := s.child
	g := GateConfig{
		ReadyURL:     s.cfg.ReadyURL,
		SmokeAddr:    s.smokeAddr,
		Token:        s.token,
		Started:      s.childStart,
		ReadyTimeout: s.cfg.ReadyTimeout,
		Initial:      true,
	}
	rollbackTarget := s.st.OfflineRollback
	go func() {
		res, err := s.cfg.gate(pctx, g, child.Done())
		if err != nil {
			if pctx.Err() == nil {
				s.log.Warn("startup gate failed", "event", "gate", "reason", err, "offline_rollback", rollbackTarget)
			}
			return
		}
		select {
		case s.initial <- initialResult{child, res}:
		case <-pctx.Done():
		}
	}()
}

// recordInitial stores the probe of the running child as the last good run.
func (s *sup) recordInitial(r initialResult) {
	if r.child != s.child {
		return
	}
	err := s.update(func(st *State) error {
		st.PluginErrors, st.SmokeBaseline, st.BaselineAt = r.res.PluginErrors, BaselineOf(r.res), time.Now().UTC()
		st.RenderBaseline, st.BootFaultBaseline = r.res.FirstRender, r.res.BootFaults
		return nil
	})
	if err != nil {
		s.log.Warn("cannot record smoke baseline", "event", "gate", "err", err)
		return
	}
	s.log.Info("smoke baseline recorded", "event", "gate", "version", s.childName, "checks", len(r.res.Checks))
	if target := s.st.OfflineRollback; target != "" {
		s.log.Info("startup gate passed for the offline rollback", "event", "gate", "version", target)
		_ = s.update(func(st *State) error { st.OfflineRollback = ""; return nil })
	}
}

// refreshBaseline smokes the running child once, right before a switch takes
// it down, and stores what it served as the last good run. The baseline taken
// at its boot can be months old: pages unpublished or a home page changed
// since then must not count against the next release. A failed or unfit probe
// leaves the stored baseline alone. The render baseline is not touched: it is
// the first-pass figure of a child's boot, which a later pass cannot redo.
func (s *sup) refreshBaseline(ctx context.Context) {
	if s.child == nil || s.smokeAddr == "" || !s.writable {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, min(5*time.Second, s.cfg.ReadyTimeout))
	defer cancel()
	res, err := s.cfg.gate(pctx, GateConfig{
		ReadyURL: s.cfg.ReadyURL, SmokeAddr: s.smokeAddr, Token: s.token,
		Started: s.childStart, ReadyTimeout: s.cfg.ReadyTimeout, Initial: true,
	}, s.child.Done())
	if err != nil {
		s.log.Info("baseline refresh skipped", "event", "gate", "reason", err)
		return
	}
	err = s.update(func(st *State) error {
		st.PluginErrors, st.SmokeBaseline, st.BaselineAt = res.PluginErrors, BaselineOf(res), time.Now().UTC()
		st.BootFaultBaseline = res.BootFaults
		return nil
	})
	if err != nil {
		s.log.Warn("cannot refresh smoke baseline", "event", "gate", "err", err)
	}
}
