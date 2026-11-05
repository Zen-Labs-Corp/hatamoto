package pluginhost

import (
	"context"
	"encoding/json/jsontext"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"hatamoto.dev/sdk"
)

// RunCtx is what the run driver puts in the context of every on_run_step
// call. The context reaches host functions unchanged, so dispatch can tell a
// call made from inside a run (and in which phase) from any other.
type RunCtx struct {
	RunID uuid.UUID
	// Plugin is the plugin the run belongs to; Name is the run's name in the
	// manifest.
	Plugin, Name string
	// Phase is sdk.RunPhaseFetch or sdk.RunPhaseTransform.
	Phase string
	// Hosts are the hosts the owner confirmed when starting the run. They
	// replace the manifest's allowed_hosts for http.fetch and
	// media.import_url during the fetch phase.
	Hosts []string
	// Proposes are the proposal kinds the run declares.
	Proposes []string
	// Signal is where run.finish and run.fail leave their outcome for the
	// driver to read after the step returns.
	Signal *RunSignal
	// Reads counts the content reads of this fetch step; nil means uncounted.
	Reads *atomic.Int32
}

// RunSignal records how a step asked to end: finish the phase, or fail the run.
type RunSignal struct {
	mu      sync.Mutex
	Finish  bool
	Fail    string
	Summary jsontext.Value
}

// Set records the outcome of run.finish (fail == "") or run.fail.
func (s *RunSignal) Set(finish bool, fail string, summary jsontext.Value) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Finish, s.Fail, s.Summary = finish, fail, summary
}

// Get returns the recorded outcome.
func (s *RunSignal) Get() (finish bool, fail string, summary jsontext.Value) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Finish, s.Fail, s.Summary
}

type runCtxKey struct{}

// WithRun returns ctx marked as a call from inside the run rc.
func WithRun(ctx context.Context, rc RunCtx) context.Context {
	return context.WithValue(ctx, runCtxKey{}, rc)
}

// RunFrom returns the run the context belongs to.
func RunFrom(ctx context.Context) (RunCtx, bool) {
	rc, ok := ctx.Value(runCtxKey{}).(RunCtx)
	return rc, ok
}

// effectiveHosts is the host list http.fetch may call: during the fetch phase
// of a run, the hosts the owner confirmed for that run; otherwise the
// manifest's allowed_hosts.
func effectiveHosts(ctx context.Context, manifestHosts []string) []string {
	if rc, ok := RunFrom(ctx); ok && rc.Phase == sdk.RunPhaseFetch {
		return rc.Hosts
	}
	return manifestHosts
}
