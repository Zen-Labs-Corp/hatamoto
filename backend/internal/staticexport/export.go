package staticexport

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/google/uuid"
)

// RunRecorder records runs; RunStore is the database-backed one.
type RunRecorder interface {
	Start(ctx context.Context, trigger, target string) (uuid.UUID, error)
	Heartbeat(ctx context.Context, id uuid.UUID) error
	Finish(ctx context.Context, id uuid.UUID, r RunResult) error
}

const defaultHeartbeat = 30 * time.Second

// Options tunes Export.
type Options struct {
	// Trigger names the caller in the run record ("cli", ...).
	Trigger     string
	Concurrency int
	// Out receives the progress lines; nil discards them.
	Out io.Writer
	// HeartbeatEvery defaults to 30 seconds.
	HeartbeatEvery time.Duration
	// Full re-publishes every file, ignoring what the previous export already
	// holds (Rebuild all). Removed files are still deleted. Every export
	// renders the whole site either way.
	Full bool
}

// Result describes a finished export.
type Result struct {
	RunID    uuid.UUID
	Report   Report
	Diff     Diff
	Duration time.Duration
}

// Export renders the site through src into a staging area, diffs it against
// what t holds and publishes it. On any error nothing is published: the
// staging area is discarded and the run is recorded as failed. ErrBusy is
// returned without a run row.
func Export(ctx context.Context, src Source, runs RunRecorder, t Target, o Options) (Result, error) {
	began := time.Now()
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	say := func(format string, a ...any) { fmt.Fprintf(out, format+"\n", a...) }

	id, err := runs.Start(ctx, o.Trigger, t.Key())
	if err != nil {
		return Result{}, err
	}
	res := Result{RunID: id}

	stopBeat := heartbeat(ctx, runs, id, o.HeartbeatEvery)
	defer stopBeat()

	var staged string
	fail := func(err error, rep Report) (Result, error) {
		stopBeat()
		if staged != "" {
			t.Discard(staged)
		}
		errs := rep.Errors
		if len(errs) == 0 {
			errs = []string{err.Error()}
		}
		res.Duration = time.Since(began)
		// The record must land even when ctx was cancelled.
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = runs.Finish(fctx, id, RunResult{
			Pages: rep.Pages, Assets: rep.Assets, Media: rep.Media, Redirects: rep.Redirects,
			Warnings: len(rep.Warnings), Bytes: rep.Bytes, Duration: res.Duration, Errors: errs,
		})
		for _, e := range errs {
			say("static: error: %s", e)
		}
		return res, err
	}

	site, err := src.Site(ctx)
	if err != nil {
		return fail(err, Report{})
	}
	staged, err = t.Stage(ctx)
	if err != nil {
		return fail(fmt.Errorf("stage: %w", err), Report{})
	}

	htaccess, _ := t.(interface{ WritesHTAccess() bool })
	manifest, rep, err := Build(ctx, src, site, staged, BuildOptions{Concurrency: o.Concurrency, Progress: say,
		HTAccess: htaccess != nil && htaccess.WritesHTAccess()})
	res.Report = rep
	for _, w := range rep.Warnings {
		say("static: warning: %s", w)
	}
	if err != nil {
		return fail(err, rep)
	}
	pages := fmt.Sprintf("%d pages", rep.Pages)
	if rep.ListingPages > 0 {
		pages += fmt.Sprintf(" (+%d listing pages)", rep.ListingPages)
	}
	say("static: %s, %d assets, %d media files  (%.1f MB)", pages, rep.Assets, rep.Media, float64(rep.Bytes)/(1<<20))

	prev, found, err := t.Previous(ctx)
	if err != nil {
		return fail(err, rep)
	}
	if !found {
		say("static: no previous export found, publishing everything and deleting nothing")
	}
	diff := DiffManifests(prev, manifest)
	if o.Full {
		diff.Changed = append(diff.Changed, diff.Unchanged...)
		diff.Unchanged = nil
		sort.Strings(diff.Changed)
		say("static: full rebuild, re-publishing every file")
	}
	res.Diff = diff
	say("static: changed %d, unchanged %d, removed %d", len(diff.Changed), len(diff.Unchanged), len(diff.Removed))
	say("static: wrote %s (%d rules) + %d redirect stubs, 404.html", RedirectsFile, rep.Redirects, rep.Stubs)
	if _, isDir := t.(*DirTarget); !isDir {
		say("static: uploading %d changed, deleting %d removed (last) to %s", len(diff.Changed), len(diff.Removed), t.Describe())
	}

	if err := t.Publish(ctx, staged, diff, manifest); err != nil {
		return fail(err, Report{Pages: rep.Pages, Assets: rep.Assets, Media: rep.Media, Redirects: rep.Redirects,
			Bytes: rep.Bytes, Warnings: rep.Warnings})
	}
	staged = ""
	stopBeat()
	if _, isDir := t.(*DirTarget); isDir {
		say("static: swapped %s (previous kept at %s.prev)", t.Describe(), t.Describe())
	}

	res.Duration = time.Since(began)
	if err := runs.Finish(ctx, id, RunResult{
		OK: true, Pages: rep.Pages, Assets: rep.Assets, Media: rep.Media, Redirects: rep.Redirects,
		Changed: len(diff.Changed), Unchanged: len(diff.Unchanged), Removed: len(diff.Removed),
		Warnings: len(rep.Warnings), Bytes: rep.Bytes, Duration: res.Duration,
	}); err != nil {
		say("static: warning: the run record could not be saved: %v", err)
	}
	say("static: done in %.1fs, 0 errors", res.Duration.Seconds())
	return res, nil
}

// heartbeat pings the run until the returned stop is called (idempotent).
func heartbeat(ctx context.Context, runs RunRecorder, id uuid.UUID, every time.Duration) (stop func()) {
	if every <= 0 {
		every = defaultHeartbeat
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-tick.C:
				_ = runs.Heartbeat(ctx, id)
			}
		}
	}()
	var once bool
	return func() {
		if !once {
			once = true
			close(done)
		}
		<-stopped
	}
}
