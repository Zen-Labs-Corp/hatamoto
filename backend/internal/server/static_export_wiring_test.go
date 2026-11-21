package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/staticexport"
	"hatamoto.dev/core/internal/statictrigger"
)

type fakeExportRuns struct {
	started  []string // "reason|target"
	finished []staticexport.RunResult
	pruned   []time.Time
	id       uuid.UUID
}

func (f *fakeExportRuns) Start(_ context.Context, trigger, target string) (uuid.UUID, error) {
	f.started = append(f.started, trigger+"|"+target)
	return f.id, nil
}

func (f *fakeExportRuns) Finish(_ context.Context, _ uuid.UUID, r staticexport.RunResult) error {
	f.finished = append(f.finished, r)
	return nil
}

func (f *fakeExportRuns) PruneRuns(_ context.Context, before time.Time) (int64, error) {
	f.pruned = append(f.pruned, before)
	return 0, nil
}

type exportRig struct {
	r         *staticExportRunner
	runs      *fakeExportRuns
	set       staticexport.Settings
	found     bool
	exported  []staticexport.Options
	exportErr error
	runID     uuid.UUID
	notified  []bool
	notifiedA []uuid.UUID
	targetErr error
}

var exportRigNow = time.Date(2030, 3, 4, 5, 6, 0, 0, time.UTC)

func newExportRig() *exportRig {
	g := &exportRig{runs: &fakeExportRuns{id: uuid.New()}, found: true, runID: uuid.New()}
	g.set = staticexport.Defaults()
	g.set.Mode, g.set.DirPath = staticexport.ModeStatic, "/srv/site-static"
	g.r = &staticExportRunner{
		settings: func(context.Context) (staticexport.Settings, bool, error) { return g.set, g.found, nil },
		runs:     g.runs,
		target:   func(staticexport.Settings) (staticexport.Target, error) { return nil, g.targetErr },
		export: func(_ context.Context, _ staticexport.Target, o staticexport.Options) (staticexport.Result, error) {
			g.exported = append(g.exported, o)
			return staticexport.Result{RunID: g.runID}, g.exportErr
		},
		notify: func(_ context.Context, id uuid.UUID, ok bool, _ time.Time, _ string) {
			g.notified, g.notifiedA = append(g.notified, ok), append(g.notifiedA, id)
		},
		now: func() time.Time { return exportRigNow },
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return g
}

func TestStaticExportRun_ServedNeverExports(t *testing.T) {
	g := newExportRig()
	g.set.Mode = staticexport.ModeServed // what a restored copy reads
	if err := g.r.run(context.Background(), false, "change"); err != nil || len(g.exported) != 0 || len(g.notified) != 0 {
		t.Fatalf("served: err %v exported %d notified %d", err, len(g.exported), len(g.notified))
	}
	g.found = false // default install, no row
	if err := g.r.run(context.Background(), false, "change"); err != nil || len(g.exported) != 0 {
		t.Fatalf("no row: err %v exported %d", err, len(g.exported))
	}
	for _, found := range []bool{false, true} {
		g.found = found
		if on, err := g.r.modeOn(context.Background()); on || err != nil {
			t.Fatalf("modeOn found=%v: %v %v", found, on, err)
		}
	}
}

func TestStaticExportRun_StaticExportsWithOptions(t *testing.T) {
	g := newExportRig()
	if on, _ := g.r.modeOn(context.Background()); !on {
		t.Fatal("static must gate on")
	}
	if err := g.r.run(context.Background(), false, "change"); err != nil {
		t.Fatal(err)
	}
	if err := g.r.run(context.Background(), true, "rebuild"); err != nil {
		t.Fatal(err)
	}
	if len(g.exported) != 2 || g.exported[0] != (staticexport.Options{Trigger: "change"}) ||
		g.exported[1] != (staticexport.Options{Trigger: "rebuild", Full: true}) {
		t.Fatalf("options = %+v", g.exported)
	}
	if len(g.notified) != 2 || !g.notified[0] || g.notifiedA[0] != g.runID {
		t.Fatalf("notified = %v %v", g.notified, g.notifiedA)
	}
	want := exportRigNow.Add(-exportRunsKeep)
	if len(g.runs.pruned) != 2 || !g.runs.pruned[0].Equal(want) {
		t.Fatalf("pruned = %v, want %v", g.runs.pruned, want)
	}
}

func TestStaticExportRun_FailureNotifies(t *testing.T) {
	g := newExportRig()
	g.exportErr = errors.New("render 500 on /x")
	if err := g.r.run(context.Background(), false, "change"); err == nil {
		t.Fatal("expected the export error")
	}
	if len(g.notified) != 1 || g.notified[0] || g.notifiedA[0] != g.runID {
		t.Fatalf("notified = %v %v", g.notified, g.notifiedA)
	}
}

func TestStaticExportRun_BusyRetriesWithoutNotice(t *testing.T) {
	g := newExportRig()
	g.exportErr = staticexport.ErrBusy
	err := g.r.run(context.Background(), false, "change")
	if !errors.Is(err, statictrigger.ErrRetry) {
		t.Fatalf("err = %v", err)
	}
	if len(g.notified) != 0 || len(g.runs.pruned) != 0 {
		t.Fatalf("busy: notified %d pruned %d", len(g.notified), len(g.runs.pruned))
	}
}

func TestStaticExportRun_TargetErrorRecordsFailedRun(t *testing.T) {
	g := newExportRig()
	g.targetErr = errors.New("S3 secret key cannot be opened")
	if err := g.r.run(context.Background(), false, "mode"); err == nil {
		t.Fatal("expected the target error")
	}
	if len(g.exported) != 0 {
		t.Fatal("no export without a target")
	}
	if len(g.runs.started) != 1 || !strings.HasPrefix(g.runs.started[0], "mode|") ||
		len(g.runs.finished) != 1 || g.runs.finished[0].OK || len(g.runs.finished[0].Errors) != 1 {
		t.Fatalf("recorded run = %v %+v", g.runs.started, g.runs.finished)
	}
	if len(g.notified) != 1 || g.notified[0] || g.notifiedA[0] != g.runs.id {
		t.Fatalf("notified = %v %v", g.notified, g.notifiedA)
	}
}

func TestStaticExportRun_ReachedThroughTrigger(t *testing.T) {
	g := newExportRig()
	got := make(chan staticexport.Options, 1)
	g.r.export = func(_ context.Context, _ staticexport.Target, o staticexport.Options) (staticexport.Result, error) {
		got <- o
		return staticexport.Result{}, nil
	}
	tr := statictrigger.New(func() context.Context { return context.Background() }, g.r.run, g.r.modeOn, g.r.log)
	t.Cleanup(tr.Close)
	if err := tr.Now("manual", true); err != nil {
		t.Fatal(err)
	}
	select {
	case o := <-got:
		if o.Trigger != "manual" || !o.Full {
			t.Fatalf("options = %+v", o)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("trigger did not run the export")
	}
}

func TestLiveAdminSiteUsesLiveOriginInStaticMode(t *testing.T) {
	site := func(context.Context) (string, string, string) {
		return "Site", "https://cdn.example.com", "/secret-admin"
	}
	get := func(set staticexport.Settings, found bool) func(context.Context) (staticexport.Settings, bool, error) {
		return func(context.Context) (staticexport.Settings, bool, error) { return set, found, nil }
	}
	static := staticexport.Settings{Mode: staticexport.ModeStatic, LiveOrigin: "https://live.example.com"}
	for _, tc := range []struct {
		name  string
		set   staticexport.Settings
		found bool
		want  string
	}{
		{"static with live origin", static, true, "https://live.example.com"},
		{"static without live origin", staticexport.Settings{Mode: staticexport.ModeStatic}, true, "https://cdn.example.com"},
		{"served", staticexport.Settings{Mode: staticexport.ModeServed, LiveOrigin: "https://live.example.com"}, true, "https://cdn.example.com"},
		{"no row", static, false, "https://cdn.example.com"},
	} {
		_, base, adminPath := liveAdminSite(site, get(tc.set, tc.found))(context.Background())
		if base != tc.want || adminPath != "/secret-admin" {
			t.Errorf("%s: base %q admin %q, want %q", tc.name, base, adminPath, tc.want)
		}
	}
}
