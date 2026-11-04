package media

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ErrRebuildRunning is returned by Start while a rebuild is under way.
var ErrRebuildRunning = errors.New("media: a rebuild is already running")

const (
	rebuildBatch    = 20
	rebuildPerImage = 60 * time.Second
)

// StateStore keeps the rebuild job's progress.
type StateStore interface {
	Load(ctx context.Context) (Settings, State, error)
	SaveState(ctx context.Context, st State) error
}

// RebuildSource is what a rebuild reads and writes. *Service implements it.
type RebuildSource interface {
	CountImages(ctx context.Context) (int, error)
	ListImagesAfter(ctx context.Context, after uuid.UUID, limit int) ([]*Media, error)
	RegenerateVariants(ctx context.Context, id uuid.UUID) (*Media, error)
	// RemoveStale deletes the copies RegenerateVariants replaced. It runs after
	// the Done hook, once rendered pages no longer link to them.
	RemoveStale(ctx context.Context)
}

// RebuildHooks are the optional side effects of a rebuild.
type RebuildHooks struct {
	// Audit records the start and the end of a run (actions "media.rebuild.start"
	// and "media.rebuild.finish"). actor is who started the run.
	Audit func(ctx context.Context, action, actor string, detail map[string]any)
	// Done runs once when a run ends, for example to purge rendered pages.
	Done func(ctx context.Context)
}

// Rebuilder regenerates the resized copies of every image, one run at a time.
// It walks the library by id in batches, saves its cursor after every image
// and stops when the server's context is cancelled; Resume continues a run
// that was interrupted.
type Rebuilder struct {
	src    RebuildSource
	store  StateStore
	hooks  RebuildHooks
	logger *slog.Logger
	// base is the context runs live in (the server's lifecycle context), not
	// the request that started one.
	base func() context.Context

	mu      sync.Mutex
	running bool
	wg      sync.WaitGroup

	perImage time.Duration
}

// NewRebuilder creates a rebuilder. base returns the context runs live in.
func NewRebuilder(src RebuildSource, store StateStore, base func() context.Context, hooks RebuildHooks, logger *slog.Logger) *Rebuilder {
	return &Rebuilder{src: src, store: store, base: base, hooks: hooks, logger: logger, perImage: rebuildPerImage}
}

// Status is the current progress.
func (r *Rebuilder) Status(ctx context.Context) (State, error) {
	_, st, err := r.store.Load(ctx)
	return st, err
}

// Start begins a run. It returns ErrRebuildRunning when one is active.
func (r *Rebuilder) Start(ctx context.Context, actor string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return ErrRebuildRunning
	}
	_, prev, err := r.store.Load(ctx)
	if err != nil {
		return err
	}
	if prev.Running {
		return ErrRebuildRunning // saved as running, not resumed yet
	}
	total, err := r.src.CountImages(ctx)
	if err != nil {
		return err
	}
	st := State{
		Running: true, RunID: uuid.NewString(), StartedAt: time.Now().UTC(), StartedBy: actor,
		Total: total, Last: prev.Last,
	}
	if err := r.store.SaveState(ctx, st); err != nil {
		return err
	}
	r.audit(ctx, "media.rebuild.start", actor, map[string]any{"run_id": st.RunID, "images": total})
	r.launch(r.base(), st)
	return nil
}

// Resume continues a run that was saved as running, as after a restart. It
// does nothing when no run is saved or one is already active. ctx is the
// context the run lives in.
func (r *Rebuilder) Resume(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return
	}
	_, st, err := r.store.Load(ctx)
	if err != nil || !st.Running {
		return
	}
	r.logger.Info("resuming the image rebuild", slog.Int("done", st.Done), slog.Int("total", st.Total))
	r.launch(ctx, st)
}

// Wait blocks until the active run has stopped or ctx ends.
func (r *Rebuilder) Wait(ctx context.Context) {
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// launch starts the run goroutine. r.mu is held.
func (r *Rebuilder) launch(ctx context.Context, st State) {
	r.running = true
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.run(ctx, st)
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()
}

func (r *Rebuilder) audit(ctx context.Context, action, actor string, detail map[string]any) {
	if r.hooks.Audit != nil {
		r.hooks.Audit(ctx, action, actor, detail)
	}
}

// save writes the state; a failed write is logged and the run goes on.
func (r *Rebuilder) save(ctx context.Context, st State) {
	if err := r.store.SaveState(ctx, st); err != nil && ctx.Err() == nil {
		r.logger.Warn("saving the image rebuild state failed", slog.String("error", err.Error()))
	}
}

// run walks the images after st.Cursor. When ctx is cancelled it returns at
// once and leaves the saved state running, for Resume.
func (r *Rebuilder) run(ctx context.Context, st State) {
	for {
		if ctx.Err() != nil {
			return
		}
		batch, err := r.src.ListImagesAfter(ctx, st.Cursor, rebuildBatch)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			r.logger.Error("listing images for the rebuild failed", slog.String("error", err.Error()))
			break
		}
		if len(batch) == 0 {
			break
		}
		for _, m := range batch {
			if ctx.Err() != nil {
				return
			}
			ictx, cancel := context.WithTimeout(ctx, r.perImage)
			_, err := r.src.RegenerateVariants(ictx, m.ID)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return // cut off by shutdown: the image is redone on resume
				}
				name := m.OriginalFilename
				if name == "" {
					name = m.Filename
				}
				st.AddSkipped(Skipped{ID: m.ID, Name: name, Reason: err.Error()})
				r.logger.Warn("image rebuild skipped an image", slog.String("id", m.ID.String()), slog.String("error", err.Error()))
			}
			st.Done++
			st.Cursor = m.ID
			r.save(ctx, st)
		}
	}

	// The run is over; this write and the notices must outlive a shutdown that
	// arrives right now.
	fctx := context.WithoutCancel(ctx)
	st.Running = false
	st.Total = max(st.Total, st.Done)
	st.Last = &LastRun{FinishedAt: time.Now().UTC(), Images: st.Done, Skipped: st.SkippedCount}
	r.save(fctx, st)
	if r.hooks.Done != nil {
		r.hooks.Done(fctx)
	}
	r.src.RemoveStale(fctx)
	r.audit(fctx, "media.rebuild.finish", st.StartedBy, map[string]any{
		"run_id": st.RunID, "images": st.Done, "skipped": st.SkippedCount,
	})
}
