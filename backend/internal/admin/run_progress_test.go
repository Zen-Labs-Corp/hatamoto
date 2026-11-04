package admin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/a11y"
)

type fakeRunReader struct {
	runs map[uuid.UUID]RunView
	err  error
}

func (f *fakeRunReader) Run(_ context.Context, id uuid.UUID) (RunView, error) {
	if f.err != nil {
		return RunView{}, f.err
	}
	rv, ok := f.runs[id]
	if !ok {
		return RunView{}, ErrRunNotFound
	}
	return rv, nil
}

func (f *fakeRunReader) Recent(_ context.Context, plugin string, _ int) ([]RunView, error) {
	var out []RunView
	for _, rv := range f.runs {
		if plugin == "" || rv.Plugin == plugin {
			out = append(out, rv)
		}
	}
	return out, f.err
}

type fakeRunCanceller struct {
	err    error
	plugin string
	id     uuid.UUID
	calls  int
}

func (f *fakeRunCanceller) CancelRun(_ context.Context, plugin string, id uuid.UUID) error {
	f.calls++
	f.plugin, f.id = plugin, id
	return f.err
}

var runPlugin = PluginInfo{
	Name: "hello", Version: "1.2.0", Loaded: true, AllowedHosts: []string{"old.example.com"},
	Package: &PackageMeta{Enabled: true},
	UI:      &PluginUIInfo{Title: "Hello board", Entry: "index.html"},
	Runs: []RunDecl{{Name: "import", Title: "Import", Inputs: []RunInputDecl{
		{Name: "site", Label: "Site address", Type: "string", Required: true}, {Name: "user", Label: "User", Type: "string"},
	}}},
}

func newRunsRig(t *testing.T, runs ...RunView) (*rig, *fakeRunReader, *fakeRunCanceller, *fakeRunStarter) {
	t.Helper()
	rr := &fakeRunReader{runs: map[uuid.UUID]RunView{}}
	for _, rv := range runs {
		rr.runs[rv.ID] = rv
	}
	fc, fs := &fakeRunCanceller{}, &fakeRunStarter{}
	g := newRig(t, func(d *Deps) {
		d.Growth.Plugins = &fakePlugins{settings: map[string]map[string]any{}, list: []PluginInfo{runPlugin}}
		d.Growth.RunReader, d.Growth.RunCancel, d.Growth.PluginRuns = rr, fc, fs
	})
	return g, rr, fc, fs
}

func runView(state, phase string) RunView {
	return RunView{ID: uuid.New(), Plugin: "hello", Name: "import", State: state, Phase: phase, Done: 37, Total: 60,
		Hosts: []string{"old.example.com", "cdn.example.com"}, StagedItems: 1861, StagedBytes: 2 << 20, CreatedAt: time.Now()}
}

func TestRunProgressPage(t *testing.T) {
	t.Parallel()

	rv := runView("running", "transform")
	g, _, _, _ := newRunsRig(t, rv)
	rec := g.do("GET", "/admin/plugins/hello/runs/"+rv.ID.String(), "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("progress: %d", rec.Code)
	}
	for _, want := range []string{
		"Import from old.example.com", "1. Fetch", "2. Build", "Internet: 2 hosts", "Internet: none", "Sees your site: published only",
		"Plugin notes: locked", "Step 37 of about 60", "hx-trigger=\"every 3s\"", `data-hm-post="/admin/plugins/hello/runs/` + rv.ID.String() + `/cancel"`,
		"Cancel run", "1,861 of 50,000", "hm-rooms__room--active",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("progress page lacks %q", want)
		}
	}
	// Only the build room is the active one.
	if n := strings.Count(body, "hm-rooms__room--active"); n != 1 {
		t.Errorf("%d active rooms", n)
	}
	if strings.Contains(body, "Review changes") {
		t.Error("a running run offers no review")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
}

type fakeRunLimits struct{ ok bool }

func (f fakeRunLimits) RunLimitsFor(string) (RunStepLimits, bool) {
	return RunStepLimits{StepTimeout: 10 * time.Second, MemoryBytes: 16 << 20}, f.ok
}

func TestRunProgressLimits(t *testing.T) {
	t.Parallel()

	rv := runView("running", "transform")
	g, _, _, _ := newRunsRig(t, rv)
	g.a.deps.Growth.RunLimits = fakeRunLimits{ok: true}
	body := g.do("GET", "/admin/plugins/hello/runs/"+rv.ID.String(), "owner-token", nil, nil).Body.String()
	for _, want := range []string{
		"Each step", "10 seconds at most", "Memory", "16 MiB per instance", "Staging space", "2.0 MB of 256.0 MB for this run",
		"Longest run", "48 hours, then it is stopped", "Most steps", "100,000 in one run",
		"If abandoned", "Deleted 72 hours after the run started if nobody publishes or discards it",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("limits card lacks %q", want)
		}
	}

	rv.ChangesetID, rv.ExpiresAt = 7, time.Now().Add(30*time.Hour+30*time.Minute)
	g, _, _, _ = newRunsRig(t, rv)
	body = g.do("GET", "/admin/plugins/hello/runs/"+rv.ID.String(), "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Deleted in 31 h (") || strings.Contains(body, "after the run started") {
		t.Error("a run with a changeset shows its real expiry")
	}

	g.a.deps.Growth.RunLimits = fakeRunLimits{}
	body = g.do("GET", "/admin/plugins/hello/runs/"+rv.ID.String(), "owner-token", nil, nil).Body.String()
	if strings.Contains(body, "Each step") || !strings.Contains(body, "If abandoned") {
		t.Error("an unloaded plugin shows no step limits but keeps the rest")
	}
}

func TestRunProgressFinished(t *testing.T) {
	t.Parallel()

	rv := runView("finished", "transform")
	rv.ChangesetID = 42
	g, _, _, _ := newRunsRig(t, rv)
	body := g.do("GET", "/admin/plugins/hello/runs/"+rv.ID.String(), "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `href="/admin/plugins/hello/changesets/42"`) || !strings.Contains(body, "Review changes") {
		t.Error("a finished run links to its review")
	}
	if strings.Contains(body, "hx-trigger") || strings.Contains(body, "Cancel run") {
		t.Error("a finished run neither polls nor offers cancel")
	}
}

func TestRunProgressPollMovesOn(t *testing.T) {
	t.Parallel()

	rv := runView("finished", "transform")
	g, _, _, _ := newRunsRig(t, rv)
	rec := g.do("GET", "/admin/plugins/hello/runs/"+rv.ID.String()+"?poll=running", "owner-token", nil, map[string]string{"HX-Request": "true"})
	if rec.Header().Get("HX-Redirect") == "" {
		t.Errorf("a poll that finds a finished run reloads the page: %d %v", rec.Code, rec.Header())
	}
}

func TestRunProgressNotFound(t *testing.T) {
	t.Parallel()

	rv := runView("running", "fetch")
	other := runView("running", "fetch")
	other.Plugin = "other"
	g, _, _, _ := newRunsRig(t, rv, other)
	for _, path := range []string{
		"/admin/plugins/hello/runs/" + uuid.NewString(), "/admin/plugins/hello/runs/not-a-uuid", "/admin/plugins/hello/runs/" + other.ID.String(),
	} {
		if rec := g.do("GET", path, "owner-token", nil, nil); rec.Code != 404 {
			t.Errorf("%s: %d, want 404", path, rec.Code)
		}
	}
	if rec := g.do("GET", "/admin/plugins/hello/runs/"+rv.ID.String(), "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor: %d, want 403", rec.Code)
	}
	g2 := newRig(t, func(*Deps) {})
	if rec := g2.do("GET", "/admin/plugins/hello/runs/"+rv.ID.String(), "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("not wired: %d, want 503", rec.Code)
	}
}

func TestRunCancel(t *testing.T) {
	t.Parallel()

	rv := runView("running", "fetch")
	g, _, fc, _ := newRunsRig(t, rv)
	path := "/admin/plugins/hello/runs/" + rv.ID.String() + "/cancel"
	if rec := g.do("POST", path, "editor-token", nil, nil); rec.Code != 403 || fc.calls != 0 {
		t.Errorf("editor: %d, %d calls", rec.Code, fc.calls)
	}
	rec := g.do("POST", path, "owner-token", nil, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/plugins/hello/runs/"+rv.ID.String() {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if fc.calls != 1 || fc.plugin != "hello" || fc.id != rv.ID {
		t.Errorf("canceller got %d calls (%s %s)", fc.calls, fc.plugin, fc.id)
	}
	if body := g.do("GET", rec.Header().Get("Location"), "owner-token", nil, map[string]string{"Cookie": rec.Header().Get("Set-Cookie")}).Body.String(); body == "" {
		t.Error("run page after cancel renders")
	}
	for _, tc := range []struct {
		err  error
		want int
	}{{ErrRunNotFound, 404}, {ErrRunNotCancellable, 303}, {errors.New("db down"), 500}} {
		fc.err = tc.err
		if rec := g.do("POST", path, "owner-token", nil, nil); rec.Code != tc.want {
			t.Errorf("%v: %d, want %d", tc.err, rec.Code, tc.want)
		}
	}
	g2 := newRig(t, func(*Deps) {})
	if rec := g2.do("POST", path, "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("not wired: %d, want 503", rec.Code)
	}
}
