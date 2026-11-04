//go:build integration

package pluginhost

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/core/internal/plugrun"
	"hatamoto.dev/sdk"
)

const cronPlugin = "cronplug"

// fakeGate stands in for the server's StandingGate.
type fakeGate struct {
	mu        sync.Mutex
	active    bool
	activeAsk int
	published []int64
}

func (g *fakeGate) Active(context.Context, string, string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.activeAsk++
	return g.active, nil
}

func (g *fakeGate) AutoPublish(_ context.Context, _, _ string, id int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.published = append(g.published, id)
	return nil
}

type cronRig struct {
	*driverRig
	rt    *Runtime
	sched *fakeScheduler
	gate  *fakeGate
}

// newCronRig loads a plugin whose "tick" cron job is bound to a run with a
// standing approval block, over the driver rig's run store and changesets.
func newCronRig(t *testing.T) *cronRig {
	t.Helper()
	d := newDriverRig(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rt, err := NewRuntime(log, hooks.New(log), cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(context.Background()) })
	r := &cronRig{driverRig: d, rt: rt, sched: &fakeScheduler{}, gate: &fakeGate{active: true}}
	rt.Configure(Config{Services: Services{Runs: d.runs, Changesets: d.eng}, Scheduler: r.sched})
	rt.SetStandingGate(r.gate)
	m := sdk.Manifest{Name: cronPlugin, Version: "0.1.0", SDK: "^0.17",
		Cron: []sdk.CronJob{{Name: "tick", Every: "1h"}},
		Runs: []sdk.Run{{Name: "sync", Title: "Sync", Stages: []string{sdk.RunPhaseFetch, sdk.RunPhaseTransform},
			Cron: "tick", StandingApproval: &sdk.StandingApproval{Fields: []string{"price"}, Schemas: []string{"product"}}}}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if st := rt.Load(context.Background(), m, plugintest.Build(t, "cronrun")); !st.Loaded {
		t.Fatalf("plugin not loaded: %s", st.Error)
	}
	t.Cleanup(func() {
		_, _ = d.db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_runs WHERE plugin = $1`, cronPlugin)
	})
	return r
}

func (r *cronRig) tick() error {
	r.sched.mu.Lock()
	fn := r.sched.fns[cronID(cronPlugin, "tick")]
	r.sched.mu.Unlock()
	if fn == nil {
		r.t.Fatal("the bound cron job is not scheduled")
	}
	return fn(context.Background())
}

func (r *cronRig) runs2() []plugrun.Run {
	r.t.Helper()
	all, err := r.runs.Recent(context.Background(), cronPlugin, 10)
	if err != nil {
		r.t.Fatal(err)
	}
	return all
}

// A tick starts the run through StartRun: the run row says "cron", it has a
// changeset that is still staged when the gate is asked, and the runtime
// itself publishes nothing (ADR 0004 invariant 1).
func TestCronRunAutoPublishUsesChangeset(t *testing.T) {
	r := newCronRig(t)
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	r.rt.WaitRuns()
	runs := r.runs2()
	if len(runs) != 1 || runs[0].Trigger != plugrun.TriggerCron || runs[0].State != plugrun.StateFinished {
		t.Fatalf("runs = %+v", runs)
	}
	if runs[0].ChangesetID == nil {
		t.Fatal("the cron run has no changeset")
	}
	if got := r.gate.published; len(got) != 1 || got[0] != *runs[0].ChangesetID {
		t.Fatalf("gate was asked about %v, want the run's changeset %d", got, *runs[0].ChangesetID)
	}
	if got := r.changesetState(*runs[0].ChangesetID); got != changeset.StateStaged {
		t.Fatalf("changeset is %s: only the gate may publish", got)
	}
}

// A cron started run goes through the same phase table as an owner's run.
func TestCronStartedRunUsesRunPhases(t *testing.T) {
	r := newCronRig(t)
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	r.rt.WaitRuns()
	run := r.runs2()[0]
	if run.Phase != plugrun.PhaseTransform || run.Steps != 2 { // fetch, then transform
		t.Fatalf("phase %s after %d steps, reason %q", run.Phase, run.Steps, run.Reason)
	}
}

// Without an active approval a tick starts nothing, and a busy plugin skips
// the tick without an error.
func TestCronTickWithoutApprovalOrWhileBusy(t *testing.T) {
	r := newCronRig(t)
	r.gate.active = false
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	r.rt.WaitRuns()
	if n := len(r.runs2()); n != 0 {
		t.Fatalf("%d runs started without an approval", n)
	}

	r.gate.active = true
	busy, err := r.runs.Create(context.Background(), plugrun.Run{Plugin: cronPlugin, Name: "sync"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.tick(); err != nil {
		t.Fatalf("a busy tick must be skipped quietly: %v", err)
	}
	if n := len(r.runs2()); n != 1 {
		t.Fatalf("%d runs, want only the busy one %s", n, busy.ID)
	}
	if len(r.gate.published) != 0 {
		t.Fatalf("gate asked about %v", r.gate.published)
	}
}

// A runtime with no gate never starts a bound run.
func TestCronTickWithoutGate(t *testing.T) {
	r := newCronRig(t)
	r.rt.SetStandingGate(nil)
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	r.rt.WaitRuns()
	if n := len(r.runs2()); n != 0 {
		t.Fatalf("%d runs started without a gate", n)
	}
}

// ADR 0004 invariant 1: starting a run from cron adds no host function. The
// list is the committed contract; a new entry needs a deliberate edit here.
func TestNoRunStartHostFunction(t *testing.T) {
	want := []string{
		"buffer.get",
		"buffer.list",
		"buffer.put",
		"changeset.delete",
		"changeset.entity.upsert",
		"changeset.media.attach",
		"changeset.menu.upsert",
		"changeset.person.upsert",
		"changeset.propose",
		"changeset.redirect.upsert",
		"changeset.term.upsert",
		"db.exec",
		"db.query",
		"entities.get",
		"entities.list",
		"entities.slug_taken",
		"entities.translations",
		"forms.list",
		"forms.submissions",
		"http.fetch",
		"kv.delete",
		"kv.get",
		"kv.list",
		"kv.set",
		"log",
		"mail.send",
		"media.get",
		"media.import_url",
		"media.list",
		"menus.get",
		"menus.list",
		"permissions",
		"redirects.list",
		"run.checkpoint",
		"run.fail",
		"run.finish",
		"run.info",
		"run.progress",
		"settings.get",
		"site.info",
		"terms.get",
		"terms.list",
		"upload.read",
	}
	if got := HostFunctionsFor(sdk.Permissions); !reflect.DeepEqual(got, want) {
		t.Fatalf("host functions changed:\n got %q\nwant %q", got, want)
	}
}
