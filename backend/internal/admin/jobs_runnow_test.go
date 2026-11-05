package admin

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/worker"
)

const jobsRunPath = "/admin/settings/jobs/prune/run"

func TestJobsRunNowButtonOnlyForRunnableRows(t *testing.T) {
	t.Parallel()

	g, _, _ := newJobsRig(t)
	body := g.do("GET", "/admin/settings/jobs", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`action="/admin/settings/jobs/prune/run"`, `action="/admin/settings/jobs/changeset_sweep/run"`, "Run now"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, not := range []string{"jobs/stats_ping/run", "jobs/agent_verify/run", "jobs/plugin:seo:rebuild/run"} {
		if strings.Contains(body, not) {
			t.Errorf("unexpected Run now form %q", not)
		}
	}
}

func TestJobsRunNowSuccessAndAudit(t *testing.T) {
	t.Parallel()

	g, f, events := newJobsRig(t)
	rec := g.do("POST", jobsRunPath, "owner-token", url.Values{}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/jobs" {
		t.Fatalf("run = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(f.ran) != 1 || f.ran[0] != "prune" {
		t.Fatalf("ran %v", f.ran)
	}
	if body := followFlashTo(g, "/admin/settings/jobs", rec); !strings.Contains(body, "ran: OK in 0.1 s.") {
		t.Error("flash missing")
	}
	if len(*events) != 1 || (*events)[0].Action != "jobs.run" || (*events)[0].Target != "prune" ||
		(*events)[0].Detail["ok"] != true || (*events)[0].Detail["duration_ms"] != int64(100) {
		t.Errorf("audit = %+v", *events)
	}
}

func TestJobsRunNowFailedStillAudited(t *testing.T) {
	t.Parallel()

	g, f, events := newJobsRig(t)
	f.result = JobRunResult{Name: "Table prune", OK: false, Took: 2 * time.Second, Error: "disk full"}
	rec := g.do("POST", jobsRunPath, "owner-token", url.Values{}, nil)
	if rec.Code != 303 {
		t.Fatalf("run = %d", rec.Code)
	}
	if body := followFlashTo(g, "/admin/settings/jobs", rec); !strings.Contains(body, "Failed in 2.0 s: disk full.") {
		t.Error("flash missing the failure")
	}
	if len(*events) != 1 || (*events)[0].Detail["ok"] != false {
		t.Errorf("audit = %+v", *events)
	}
}

func TestJobsRunNowStillRunning(t *testing.T) {
	t.Parallel()

	g, f, events := newJobsRig(t)
	f.result = JobRunResult{Name: "Table prune", Started: true}
	rec := g.do("POST", jobsRunPath, "owner-token", url.Values{}, nil)
	if rec.Code != 303 {
		t.Fatalf("run = %d", rec.Code)
	}
	if body := followFlashTo(g, "/admin/settings/jobs", rec); !strings.Contains(body, "still running") {
		t.Error("flash missing")
	}
	if len(*events) != 1 || (*events)[0].Detail["started"] != true {
		t.Errorf("audit = %+v", *events)
	}
}

func TestJobsRunNowRefusals(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		err  error
		code int
	}{
		{worker.ErrAlreadyRunning, 409},
		{worker.ErrNotSafe, 403},
		{worker.ErrUnknownJob, 404},
		{worker.ErrNotRunning, 409},
		{errors.New("db down"), 500},
	} {
		g, f, events := newJobsRig(t)
		f.err = c.err
		if rec := g.do("POST", jobsRunPath, "owner-token", url.Values{}, nil); rec.Code != c.code {
			t.Errorf("%v: code = %d, want %d", c.err, rec.Code, c.code)
		}
		if len(*events) != 0 {
			t.Errorf("%v: audit written for a refused run: %+v", c.err, *events)
		}
	}
}

func TestJobsRunNowEditorAndCSRFRefused(t *testing.T) {
	t.Parallel()

	g, f, events := newJobsRig(t)
	if rec := g.do("POST", jobsRunPath, "editor-token", url.Values{}, nil); rec.Code != 403 {
		t.Errorf("editor = %d, want 403", rec.Code)
	}
	if rec := g.do("POST", jobsRunPath, "owner-token", url.Values{}, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
		t.Errorf("no csrf = %d, want 403", rec.Code)
	}
	if len(f.ran) != 0 || len(*events) != 0 {
		t.Errorf("a refused request ran %v, audit %v", f.ran, *events)
	}
	g.a.deps.Jobs = nil
	if rec := g.do("POST", jobsRunPath, "owner-token", url.Values{}, nil); rec.Code != 503 {
		t.Errorf("no service = %d, want 503", rec.Code)
	}
}
