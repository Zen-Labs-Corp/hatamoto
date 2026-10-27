package supervisor

import (
	"context"
	"log/slog"
	"time"

	"hatamoto.dev/core/internal/release"
)

// Config is what Run needs. Timings are shrinkable for tests; zero values
// take the defaults.
type Config struct {
	Data             string // absolute data dir; releases live in Data/releases
	Bootstrap        string // path of the binary the supervisor itself runs from
	BootstrapVersion string // its version stamp (may be a non-semver build string)
	ReadyURL         string // the child's /ready, e.g. http://127.0.0.1:8080/ready

	StopGrace     time.Duration // SIGTERM to SIGKILL; must exceed the child's own shutdown (default 40s)
	ReadyTimeout  time.Duration // default 120s
	ObserveWindow time.Duration // default 60s
	SmokePoll     time.Duration // default 5s
	BootWindow    time.Duration // a child that lives this long is not crash-looping (default 120s)
	BackoffBase   time.Duration // restart delay base, doubled per crash up to 30s (default 500ms)

	// Release is the trust used to re-verify staged releases before they run
	// (zero value: the keys built into this binary).
	Release release.Options

	Force bool // CLI: let `update rollback` target a bad-listed release

	Env []string // base environment for the child (default os.Environ())
	Log *slog.Logger

	// test seams
	verify func(version string) error // replaces VerifyStored
	notify func(pid int) error        // CLI side: tell the supervisor (default SIGHUP)
	launch func(path string, env []string) (proc, error)
	gate   func(ctx context.Context, g GateConfig, crashed <-chan struct{}) (SmokeResult, error)
}

const (
	maxBackoff     = 30 * time.Second
	crashLoopCount = 3
)

func (c *Config) setDefaults() {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&c.StopGrace, 40*time.Second)
	def(&c.ReadyTimeout, 120*time.Second)
	def(&c.ObserveWindow, 60*time.Second)
	def(&c.SmokePoll, 5*time.Second)
	def(&c.BootWindow, 120*time.Second)
	def(&c.BackoffBase, 500*time.Millisecond)
	if c.Log == nil {
		c.Log = slog.Default()
	}
	if c.launch == nil {
		c.launch = func(path string, env []string) (proc, error) { return StartChild(path, env) }
	}
	if c.gate == nil {
		c.gate = RunGate
	}
}

// proc is the part of Child the main loop uses, so tests can fake it.
type proc interface {
	Pid() int
	Done() <-chan struct{}
	Err() error
	Stop(grace time.Duration) bool
}

// crashTracker counts consecutive boot-window crashes.
type crashTracker struct {
	window time.Duration
	n      int
}

// record notes a crash after uptime and returns the consecutive count; a
// child that lived at least the window starts the count over.
func (t *crashTracker) record(uptime time.Duration) int {
	if uptime >= t.window {
		t.n = 0
	}
	t.n++
	return t.n
}

func (t *crashTracker) looping() bool { return t.n >= crashLoopCount }
func (t *crashTracker) reset()        { t.n = 0 }

// backoff is the restart delay after the n-th consecutive crash (n >= 1).
func backoff(base time.Duration, n int) time.Duration {
	if n < 1 {
		n = 1
	}
	if n > 16 {
		n = 16
	}
	return min(maxBackoff, base<<(n-1))
}
