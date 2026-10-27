package supervisor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeChild struct {
	t       *testing.T
	readyAt time.Time // /ready answers 200 from here on (zero: never)
	smoke   func(n int) (int, SmokeResult)
	n       atomic.Int32
	ready   *httptest.Server
	sm      *httptest.Server
	token   string
}

func newFakeChild(t *testing.T) *fakeChild {
	f := &fakeChild{t: t, token: "secret", readyAt: time.Now()}
	f.smoke = func(int) (int, SmokeResult) { return 200, SmokeResult{Pass: true} }
	f.ready = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.readyAt.IsZero() || time.Now().Before(f.readyAt) {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	f.sm = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+f.token {
			w.WriteHeader(401)
			return
		}
		code, res := f.smoke(int(f.n.Add(1)))
		if code != 200 {
			w.WriteHeader(code)
			return
		}
		json.NewEncoder(w).Encode(res)
	}))
	t.Cleanup(func() { f.ready.Close(); f.sm.Close() })
	return f
}

func (f *fakeChild) cfg() GateConfig {
	return GateConfig{
		ReadyURL:      f.ready.URL + "/ready",
		SmokeAddr:     strings.TrimPrefix(f.sm.URL, "http://"),
		Token:         f.token,
		ReadyTimeout:  2 * time.Second,
		ReadyPoll:     10 * time.Millisecond,
		ObserveWindow: 300 * time.Millisecond,
		SmokePoll:     50 * time.Millisecond,
	}
}

func runGate(f *fakeChild, g GateConfig) (SmokeResult, error) {
	return RunGate(context.Background(), g, make(chan struct{}))
}

func TestGateReadyDelayThenPass(t *testing.T) {
	f := newFakeChild(t)
	f.readyAt = time.Now().Add(300 * time.Millisecond)
	g := f.cfg()
	g.ObserveWindow = 700 * time.Millisecond
	if _, err := runGate(f, g); err != nil {
		t.Fatal(err)
	}
	if f.n.Load() < 3 {
		t.Errorf("only %d smoke polls in the observation window", f.n.Load())
	}
}

func TestGateNeverReady(t *testing.T) {
	f := newFakeChild(t)
	f.readyAt = time.Time{}
	g := f.cfg()
	g.ReadyTimeout = 200 * time.Millisecond
	_, err := runGate(f, g)
	if err == nil || !strings.Contains(err.Error(), "/ready") {
		t.Fatalf("err = %v, want one naming /ready", err)
	}
	if f.n.Load() != 0 {
		t.Error("smoke was called before ready")
	}
}

func TestGateSmokeFailures(t *testing.T) {
	cases := map[string]struct {
		smoke func(int) (int, SmokeResult)
		token string
		want  string
	}{
		"pass false names the check": {smoke: func(int) (int, SmokeResult) {
			return 200, SmokeResult{Pass: false, Checks: []SmokeCheck{{Name: "GET /", OK: false, Detail: "500"}, {Name: "GET /sitemap.xml", OK: true}}}
		}, want: "GET / (500)"},
		"HTTP 500":    {smoke: func(int) (int, SmokeResult) { return 500, SmokeResult{} }, want: "HTTP 500"},
		"wrong token": {smoke: func(int) (int, SmokeResult) { return 200, SmokeResult{Pass: true} }, token: "nope", want: "HTTP 401"},
		"render errors mid-window": {smoke: func(n int) (int, SmokeResult) {
			if n >= 3 {
				return 200, SmokeResult{Pass: true, RenderErrors: 2}
			}
			return 200, SmokeResult{Pass: true}
		}, want: "2 render errors"},
		"smoke starts failing mid-window": {smoke: func(n int) (int, SmokeResult) {
			if n >= 4 {
				return 200, SmokeResult{Pass: false, Checks: []SmokeCheck{{Name: "GET /", Detail: "503"}}}
			}
			return 200, SmokeResult{Pass: true}
		}, want: "smoke failed"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeChild(t)
			f.smoke = tc.smoke
			g := f.cfg()
			if tc.token != "" {
				g.Token = tc.token
			}
			_, err := runGate(f, g)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestGateUnreachableSmoke(t *testing.T) {
	f := newFakeChild(t)
	g := f.cfg()
	f.sm.Close()
	if _, err := runGate(f, g); err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("err = %v", err)
	}
}

// ISC-13: a plugin error that was not in the last good run fails the gate; the
// same list passes.
func TestGatePluginErrors(t *testing.T) {
	f := newFakeChild(t)
	f.smoke = func(int) (int, SmokeResult) {
		return 200, SmokeResult{Pass: true, PluginErrors: []string{"seo", "forms"}}
	}

	g := f.cfg()
	g.KnownPluginErrors = []string{"forms", "seo"}
	if _, err := runGate(f, g); err != nil {
		t.Errorf("same error list failed the gate: %v", err)
	}
	g.KnownPluginErrors = []string{"seo"}
	if _, err := runGate(f, g); err == nil || !strings.Contains(err.Error(), "forms") {
		t.Errorf("new plugin error: err = %v", err)
	}
	g.KnownPluginErrors = nil // first run: any errored plugin is new
	if _, err := runGate(f, g); err == nil {
		t.Error("first run with errored plugins passed")
	}
}

// ISC-14: render errors anywhere in the window fail it, even on the last poll.
func TestGateRenderErrorOnLastPoll(t *testing.T) {
	f := newFakeChild(t)
	g := f.cfg()
	g.ObserveWindow = 200 * time.Millisecond
	g.SmokePoll = 40 * time.Millisecond
	start := time.Now()
	f.smoke = func(int) (int, SmokeResult) {
		if time.Since(start) > 150*time.Millisecond {
			return 200, SmokeResult{Pass: true, RenderErrors: 1}
		}
		return 200, SmokeResult{Pass: true}
	}
	if _, err := runGate(f, g); err == nil {
		t.Error("render error at the end of the window passed")
	}
}

func TestGateChildCrashAndContext(t *testing.T) {
	f := newFakeChild(t)
	f.readyAt = time.Time{}
	crashed := make(chan struct{})
	go func() { time.Sleep(50 * time.Millisecond); close(crashed) }()
	if _, err := RunGate(context.Background(), f.cfg(), crashed); err == nil || !strings.Contains(err.Error(), "exited") {
		t.Errorf("crash: err = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if _, err := RunGate(ctx, f.cfg(), make(chan struct{})); err == nil {
		t.Error("cancelled context: no error")
	}
}

func TestGateObservationRunsFromChildStart(t *testing.T) {
	f := newFakeChild(t)
	g := f.cfg()
	g.Started = time.Now().Add(-time.Hour) // window already over: one smoke call only
	if _, err := runGate(f, g); err != nil {
		t.Fatal(err)
	}
	if n := f.n.Load(); n != 1 {
		t.Errorf("%d smoke calls, want exactly 1", n)
	}
}

func smokeWith(root, login int) func(int) (int, SmokeResult) {
	return func(int) (int, SmokeResult) {
		return 200, SmokeResult{Pass: root < 500 && login < 500, Checks: []SmokeCheck{
			{Name: "GET /", OK: root < 500, Status: root},
			{Name: "published page", OK: true, Detail: "no pages"},
			{Name: "GET /admin/login", OK: login < 500, Status: login},
		}}
	}
}

// Without a record of a good run: no 5xx anywhere, "GET /" 200, 404 or a redirect.
func TestGateWithoutBaseline(t *testing.T) {
	for name, tc := range map[string]struct {
		root, login int
		fails       bool
	}{
		"200":            {200, 200, false},
		"no home page":   {404, 303, false},
		"root 500":       {500, 200, true},
		"login 502":      {200, 502, true},
		"root redirects": {302, 200, false},
		"root 401":       {401, 200, true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeChild(t)
			f.smoke = smokeWith(tc.root, tc.login)
			_, err := runGate(f, f.cfg())
			if (err != nil) != tc.fails {
				t.Errorf("err = %v, fails want %v", err, tc.fails)
			}
		})
	}
}

// With a record, each check is compared with it.
func TestGateRelativeToBaseline(t *testing.T) {
	good := BaselineOf(SmokeResult{Checks: []SmokeCheck{{Name: "GET /", Status: 200}, {Name: "GET /admin/login", Status: 303}}})
	noHome := BaselineOf(SmokeResult{Checks: []SmokeCheck{{Name: "GET /", Status: 404}, {Name: "GET /admin/login", Status: 200}}})
	for name, tc := range map[string]struct {
		base        map[string]int
		root, login int
		fails       bool
	}{
		"same":                       {good, 200, 303, false},
		"200 to 301 still good":      {good, 301, 200, false},
		"200 to 404":                 {good, 404, 303, true},
		"200 to 500":                 {good, 500, 303, true},
		"login 303 to 404":           {good, 200, 404, true},
		"404 stays 404":              {noHome, 404, 200, false},
		"404 to 200 is fine":         {noHome, 200, 200, false},
		"404 to 500":                 {noHome, 500, 200, true},
		"404 base, login 200 to 404": {noHome, 404, 404, true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeChild(t)
			f.smoke = smokeWith(tc.root, tc.login)
			g := f.cfg()
			g.Baseline = tc.base
			_, err := runGate(f, g)
			if (err != nil) != tc.fails {
				t.Errorf("err = %v, fails want %v", err, tc.fails)
			}
		})
	}
}

func TestBaselineOfSkipsSkippedChecks(t *testing.T) {
	m := BaselineOf(SmokeResult{Checks: []SmokeCheck{{Name: "GET /", Status: 404}, {Name: "published page", OK: true}}})
	if len(m) != 1 || m["GET /"] != 404 {
		t.Fatalf("%v", m)
	}
}

// Render errors are judged against the last good run: the same count per
// pass is fine, only more fails.
func TestGateRenderErrorsRelativeToLastGoodRun(t *testing.T) {
	// Render errors of one smoke pass: first for the first call, each for later ones.
	perPass := func(first, each int) func(int) (int, SmokeResult) {
		return func(n int) (int, SmokeResult) {
			if n == 1 {
				return 200, SmokeResult{Pass: true, RenderErrors: first}
			}
			return 200, SmokeResult{Pass: true, RenderErrors: each}
		}
	}
	for name, tc := range map[string]struct {
		smoke    func(int) (int, SmokeResult)
		baseline int
		fails    bool
	}{
		"same as before":          {perPass(3, 0), 3, false},
		"one per pass, tolerated": {perPass(1, 1), 1, false},
		"fewer than before":       {perPass(1, 0), 3, false},
		"first call higher":       {perPass(4, 0), 3, true},
		"increase per pass":       {perPass(1, 2), 1, true},
		"none before, one now":    {perPass(1, 0), 0, true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeChild(t)
			f.smoke = tc.smoke
			g := f.cfg()
			g.RenderBaseline = tc.baseline
			_, err := runGate(f, g)
			if (err != nil) != tc.fails {
				t.Errorf("err = %v, fails want %v", err, tc.fails)
			}
		})
	}
}

func TestGateReportsFirstRenderCount(t *testing.T) {
	f := newFakeChild(t)
	f.smoke = func(n int) (int, SmokeResult) { return 200, SmokeResult{Pass: true, RenderErrors: 2} }
	g := f.cfg()
	g.RenderBaseline = 2
	res, err := runGate(f, g)
	if err != nil || res.FirstRender != 2 {
		t.Fatalf("first=%d err=%v", res.FirstRender, err)
	}
}

// A release whose theme falls back to the default at boot fails the gate when
// the last good run had no such fault; a run that always had it is not blamed.
func TestGateBootFaultsRelativeToLastGoodRun(t *testing.T) {
	fault := []string{"theme fell back to the default theme: x"}
	for name, tc := range map[string]struct {
		faults   []string
		baseline []string
		fails    bool
	}{
		"new fallback":         {fault, nil, true},
		"fallback as before":   {fault, fault, false},
		"fallback went away":   {nil, fault, false},
		"clean":                {nil, nil, false},
		"second fault appears": {append([]string{"plugin templates rejected: p"}, fault...), fault, true},
		"lazy fault in both":   {[]string{"plugin templates rejected: p"}, []string{"plugin templates rejected: p"}, false},
		"one fault swapped":    {[]string{"plugin templates rejected: q"}, []string{"plugin templates rejected: p"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeChild(t)
			f.smoke = func(n int) (int, SmokeResult) {
				return 200, SmokeResult{Pass: true, BootFaults: tc.faults}
			}
			g := f.cfg()
			g.BootFaultBaseline = tc.baseline
			_, err := runGate(f, g)
			if (err != nil) != tc.fails {
				t.Errorf("err = %v, fails want %v", err, tc.fails)
			}
		})
	}
}

// A baseline that could not be refreshed and is more than a day old forgives
// only a 404 on "GET /" and "published page" (an editor unpublishing them is
// not the release's fault); every other check stays strict.
func TestGateStaleBaselineIsLenient(t *testing.T) {
	good := BaselineOf(SmokeResult{Checks: []SmokeCheck{{Name: "GET /", Status: 200}, {Name: "GET /admin/login", Status: 303}}})
	for name, tc := range map[string]struct {
		stale       bool
		root, login int
		fails       bool
	}{
		"fresh: 200 to 404 fails":  {false, 404, 303, true},
		"stale: 200 to 404 passes": {true, 404, 303, false},
		"stale: login 404 fails":   {true, 200, 404, true},
		"stale: login 403 fails":   {true, 200, 403, true},
		"stale: root 403 fails":    {true, 403, 303, true},
		"stale: root 302 passes":   {true, 302, 303, false},
		"stale: all 404 fails":     {true, 404, 404, true},
		"stale: 5xx still fails":   {true, 500, 303, true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeChild(t)
			f.smoke = smokeWith(tc.root, tc.login)
			g := f.cfg()
			g.Baseline, g.StaleBaseline = good, tc.stale
			_, err := runGate(f, g)
			if (err != nil) != tc.fails {
				t.Errorf("err = %v, fails want %v", err, tc.fails)
			}
		})
	}
}

// The sampled published page may be unpublished within the baseline's age
// too; fresh baselines still fail it.
func TestStaleBaselineForgivesPublishedPage404(t *testing.T) {
	base := map[string]int{"published page": 200}
	r := SmokeResult{Checks: []SmokeCheck{{Name: "published page", OK: true, Status: 404}}}
	if got := relativeFailures(r, base, true); len(got) != 0 {
		t.Errorf("stale: %v", got)
	}
	if got := relativeFailures(r, base, false); len(got) != 1 {
		t.Errorf("fresh: %v", got)
	}
}
