package admin

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeStats is a StatsService that records what the handlers asked of it.
type fakeStats struct {
	view    StatsView
	viewErr error
	chooseE error
	resetE  error
	views   int
	asks    int
	chosen  []bool
	resets  int
}

func (f *fakeStats) View(context.Context) (StatsView, error) {
	f.views++
	return f.view, f.viewErr
}

func (f *fakeStats) Unanswered(context.Context) (bool, error) {
	f.asks++
	return f.view.Consent == "unset" && !f.view.ForcedOff, f.viewErr
}

func (f *fakeStats) ForcedOff() bool { return f.view.ForcedOff }

func (f *fakeStats) Choose(_ context.Context, yes bool) error {
	f.chosen = append(f.chosen, yes)
	return f.chooseE
}

func (f *fakeStats) Reset(context.Context) error {
	f.resets++
	return f.resetE
}

const statsTestID = "0123456789abcdef0123456789abcdef"

// statsTestPreview is a request body as the wizard shows it.
const statsTestPreview = "{\n  \"install\": \"6f1c0a9e5b7d4e28a3c1f09d7b2e4a65\",\n  \"version\": \"v1.4.1\",\n  \"channel\": \"stable\"\n}"

func newStatsRig(t *testing.T, f *fakeStats) *updatesRig {
	t.Helper()
	u := newUpdatesRig(t, baseUpdates())
	if f != nil {
		u.a.deps.Stats = f
	}
	return u
}

var statsPosts = []string{"/stats", "/stats/choice", "/stats/reset"}

func TestStatsPostsAreOwnerOnlyAndNeedCSRF(t *testing.T) {
	f := &fakeStats{view: StatsView{Consent: "unset"}}
	u := newStatsRig(t, f)
	for _, p := range statsPosts {
		target := "/admin/settings/updates" + p
		if rec := u.do("POST", target, "editor-token", url.Values{"choice": {"yes"}}, nil); rec.Code != 403 {
			t.Errorf("editor POST %s = %d, want 403", target, rec.Code)
		}
		if rec := u.do("POST", target, "", url.Values{"choice": {"yes"}}, nil); rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/login") {
			t.Errorf("anonymous POST %s = %d %s", target, rec.Code, rec.Header().Get("Location"))
		}
		if rec := u.do("POST", target, "owner-token", url.Values{"choice": {"yes"}}, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
			t.Errorf("no CSRF POST %s = %d, want 403", target, rec.Code)
		}
	}
	if f.views != 0 || len(f.chosen) != 0 || f.resets != 0 || len(u.events) != 0 {
		t.Error("a refused request reached the service")
	}
}

func TestStatsPostsWithoutServiceAre503(t *testing.T) {
	u := newStatsRig(t, nil)
	for _, p := range statsPosts {
		if rec := u.do("POST", "/admin/settings/updates"+p, "owner-token", url.Values{"choice": {"yes"}}, nil); rec.Code != 503 {
			t.Errorf("POST %s without a service = %d, want 503", p, rec.Code)
		}
	}
}

func TestStatsChoiceCallsServiceOnceAndAuditsWithoutID(t *testing.T) {
	for _, c := range []struct {
		choice string
		want   bool
	}{{"yes", true}, {"no", false}} {
		f := &fakeStats{view: StatsView{Consent: "unset", PreviewJSON: statsTestID}}
		u := newStatsRig(t, f)
		rec := u.do("POST", "/admin/settings/updates/stats/choice", "owner-token", url.Values{"choice": {c.choice}}, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/updates" {
			t.Fatalf("choice %s = %d %s", c.choice, rec.Code, rec.Header().Get("Location"))
		}
		if len(f.chosen) != 1 || f.chosen[0] != c.want {
			t.Errorf("choice %s: Choose calls = %v", c.choice, f.chosen)
		}
		if len(u.events) != 1 || u.events[0].Action != "stats.consent" || u.events[0].Detail["choice"] != c.choice {
			t.Fatalf("choice %s: audit = %+v", c.choice, u.events)
		}
		if strings.Contains(fmt.Sprint(u.events), statsTestID) {
			t.Error("audit detail carries the install ID")
		}
	}
}

func TestStatsChoiceRefusesAnythingElse(t *testing.T) {
	f := &fakeStats{view: StatsView{Consent: "unset"}}
	u := newStatsRig(t, f)
	for _, form := range []url.Values{{}, {"choice": {""}}, {"choice": {"maybe"}}} {
		if rec := u.do("POST", "/admin/settings/updates/stats/choice", "owner-token", form, nil); rec.Code != 400 {
			t.Errorf("choice %v = %d, want 400", form, rec.Code)
		}
	}
	if len(f.chosen) != 0 || len(u.events) != 0 {
		t.Error("an invalid choice reached the service")
	}
}

func TestStatsSwitchOnAndOff(t *testing.T) {
	for _, c := range []struct {
		value string
		want  bool
	}{{"on", true}, {"off", false}, {"", false}} {
		f := &fakeStats{view: StatsView{Consent: "no"}}
		u := newStatsRig(t, f)
		form := url.Values{}
		if c.value != "" {
			form.Set("stats", c.value)
		}
		if rec := u.do("POST", "/admin/settings/updates/stats", "owner-token", form, nil); rec.Code != 303 {
			t.Fatalf("switch %q = %d", c.value, rec.Code)
		}
		if len(f.chosen) != 1 || f.chosen[0] != c.want {
			t.Errorf("switch %q: Choose calls = %v", c.value, f.chosen)
		}
	}
}

func TestStatsForcedOffChangesNothing(t *testing.T) {
	f := &fakeStats{view: StatsView{Consent: "no", ForcedOff: true}}
	u := newStatsRig(t, f)
	for _, p := range statsPosts {
		rec := u.do("POST", "/admin/settings/updates"+p, "owner-token", url.Values{"choice": {"yes"}, "stats": {"on"}}, nil)
		if rec.Code != 303 {
			t.Fatalf("POST %s = %d", p, rec.Code)
		}
		if body := u.followFlash(rec); !strings.Contains(body, statsForcedOffNote) {
			t.Errorf("POST %s: flash missing %q", p, statsForcedOffNote)
		}
	}
	if len(f.chosen) != 0 || f.resets != 0 || len(u.events) != 0 {
		t.Errorf("forced off: Choose %v, Reset %d, audit %d", f.chosen, f.resets, len(u.events))
	}
}

func TestStatsResetCallsServiceOnce(t *testing.T) {
	f := &fakeStats{view: StatsView{Consent: "yes", PreviewJSON: statsTestID}}
	u := newStatsRig(t, f)
	if rec := u.do("POST", "/admin/settings/updates/stats/reset", "owner-token", url.Values{}, nil); rec.Code != 303 {
		t.Fatalf("reset = %d", rec.Code)
	}
	if f.resets != 1 {
		t.Errorf("Reset calls = %d, want 1", f.resets)
	}
	if len(u.events) != 1 || u.events[0].Action != "stats.reset" || len(u.events[0].Detail) != 0 {
		t.Errorf("audit = %+v", u.events)
	}
}

func TestStatsServiceErrorsAre500AndNotAudited(t *testing.T) {
	f := &fakeStats{view: StatsView{Consent: "unset"}, chooseE: errors.New("db down " + statsTestID)}
	u := newStatsRig(t, f)
	rec := u.do("POST", "/admin/settings/updates/stats/choice", "owner-token", url.Values{"choice": {"yes"}}, nil)
	if rec.Code != 500 || strings.Contains(rec.Body.String(), statsTestID) {
		t.Errorf("choose error = %d, body leaks: %v", rec.Code, strings.Contains(rec.Body.String(), statsTestID))
	}
	if len(u.events) != 0 {
		t.Error("a failed choice was audited")
	}
	f.chooseE, f.resetE = nil, errors.New("boom")
	if rec := u.do("POST", "/admin/settings/updates/stats/reset", "owner-token", url.Values{}, nil); rec.Code != 500 {
		t.Errorf("reset error = %d, want 500", rec.Code)
	}
}

func TestUpdatesInstallStatsCardStates(t *testing.T) {
	sent := updatesClock.Add(-3 * time.Hour)
	preview := "{\n  \"install\": \"" + statsTestID + "\",\n  \"version\": \"v1.4.1\",\n  \"channel\": \"stable\"\n}"
	for _, c := range []struct {
		name    string
		view    StatsView
		wants   []string
		absents []string
	}{
		{"unset", StatsView{Consent: "unset", PreviewJSON: preview}, []string{`id="updates-stats"`, "Send install stats", `role="switch"`, "A real ID is created when you turn this on.", "Nothing sent yet.", "&#34;install&#34;"}, []string{"Reset ID", `value="on" checked`, "server configuration"}},
		{"yes", StatsView{Consent: "yes", PreviewJSON: preview, LastSent: &sent, LastStatus: "ok"}, []string{"This is the exact request body.", `value="on" checked`, "Reset ID", `id="reset-stats-form"`, "ok"}, []string{"Nothing sent yet.", "A real ID is created"}},
		{"no", StatsView{Consent: "no", PreviewJSON: preview}, []string{`role="switch"`}, []string{`value="on" checked`, "Reset ID"}},
		{"forced_off", StatsView{Consent: "no", ForcedOff: true, PreviewJSON: preview}, []string{"Turned off by the server configuration.", "hm-badge"}, []string{`role="switch"`, `id="stats-form"`, "Reset ID"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeStats{view: c.view}
			body := newStatsRig(t, f).get(t)
			for _, w := range c.wants {
				if !strings.Contains(body, w) {
					t.Errorf("page lacks %q", w)
				}
			}
			for _, b := range c.absents {
				if strings.Contains(body, b) {
					t.Errorf("page has %q", b)
				}
			}
			compareGolden(t, "pages/settings-updates-stats-"+c.name+".html", normalizePage(body))
		})
	}
}

func newSetupStatsRig(t *testing.T, view StatsView) (*setupRig, *fakeStats) {
	t.Helper()
	g := newSetupRig(t)
	g.count.n = 1
	f := &fakeStats{view: view}
	g.a.deps.Stats = f
	return g, f
}

func TestSetupStatsStepShowsExactJSONAndPreselectsNothing(t *testing.T) {
	t.Parallel()

	g, f := newSetupStatsRig(t, StatsView{Consent: "unset", PreviewJSON: statsTestPreview})
	body := g.do("GET", "/admin/setup/stats", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Help improve Hatamoto", "&#34;install&#34;: &#34;6f1c0a9e5b7d4e28a3c1f09d7b2e4a65&#34;", "No site address, no content, no names.", `name="choice" value="no"`, `name="choice" value="yes"`, "Step 6 of 8"} {
		if !strings.Contains(body, want) {
			t.Errorf("stats step lacks %q", want)
		}
	}
	if strings.Contains(body, "checked") {
		t.Error("a choice is preselected")
	}
	if len(f.chosen) != 0 {
		t.Error("viewing the step stored a choice")
	}
	// Look goes on to it, and the owner can go on past it without ever choosing.
	rec := g.do("POST", "/admin/setup/theme", "owner-token", url.Values{"colour": {"#aa3300"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/stats" {
		t.Errorf("theme goes to %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestSetupStatsChoice(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		choice string
		want   bool
	}{{"yes", true}, {"no", false}} {
		g, f := newSetupStatsRig(t, StatsView{Consent: "unset"})
		var events []AuditEvent
		g.a.deps.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }
		rec := g.do("POST", "/admin/setup/stats", "owner-token", url.Values{"choice": {c.choice}}, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/content" {
			t.Fatalf("%s: %d %s", c.choice, rec.Code, rec.Header().Get("Location"))
		}
		if len(f.chosen) != 1 || f.chosen[0] != c.want {
			t.Errorf("%s: Choose calls = %v", c.choice, f.chosen)
		}
		if len(events) != 1 || events[0].Action != "stats.consent" || events[0].Detail["choice"] != c.choice {
			t.Errorf("%s: audit = %+v", c.choice, events)
		}
		if g.site.cfg.SetupCompletedAt != nil {
			t.Error("the Stats step finished setup")
		}
	}
}

func TestSetupStatsWithoutAChoiceAsksAgain(t *testing.T) {
	t.Parallel()

	g, f := newSetupStatsRig(t, StatsView{Consent: "unset", PreviewJSON: statsTestPreview})
	for _, form := range []url.Values{{}, {"choice": {""}}, {"choice": {"maybe"}}} {
		rec := g.do("POST", "/admin/setup/stats", "owner-token", form, nil)
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Choose No thanks or Send stats.") {
			t.Errorf("post %v = %d", form, rec.Code)
		}
	}
	if len(f.chosen) != 0 {
		t.Errorf("an unanswered form chose %v", f.chosen)
	}
}

func TestSetupStatsSkippedUnderEnvOffOrWithoutService(t *testing.T) {
	t.Parallel()

	for name, setup := range map[string]func(g *setupRig, f *fakeStats){
		"forced off": func(g *setupRig, f *fakeStats) { f.view.ForcedOff = true },
		"no service": func(g *setupRig, f *fakeStats) { g.a.deps.Stats = nil },
	} {
		g, f := newSetupStatsRig(t, StatsView{Consent: "no"})
		setup(g, f)
		for _, m := range []string{"GET", "POST"} {
			rec := g.do(m, "/admin/setup/stats", "owner-token", url.Values{"choice": {"yes"}}, nil)
			if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/content" {
				t.Errorf("%s %s = %d %s", name, m, rec.Code, rec.Header().Get("Location"))
			}
		}
		if len(f.chosen) != 0 {
			t.Errorf("%s: Choose called %v", name, f.chosen)
		}
		rec := g.do("POST", "/admin/setup/theme", "owner-token", url.Values{"colour": {"#aa3300"}}, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/content" {
			t.Errorf("%s: theme goes to %s", name, rec.Header().Get("Location"))
		}
		body := g.do("GET", "/admin/setup/content", "owner-token", nil, nil).Body.String()
		if !strings.Contains(body, `href="/admin/setup/theme"><span>Back</span>`) || strings.Contains(body, "/admin/setup/stats") {
			t.Errorf("%s: Back from Content must skip the Stats step", name)
		}
	}
}

// The seed script posts Look and Content directly: the Stats step is optional
// and leaves the choice unset.
func TestSetupCompletesWithoutTheStatsStep(t *testing.T) {
	t.Parallel()

	g, f := newSetupStatsRig(t, StatsView{Consent: "unset"})
	if rec := g.do("POST", "/admin/setup/content", "owner-token", url.Values{"content": {"empty"}}, nil); rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/done" || g.site.cfg.SetupCompletedAt == nil {
		t.Errorf("content: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(f.chosen) != 0 {
		t.Error("finishing setup chose for the owner")
	}
}

func TestSetupStatsIsOwnerOnly(t *testing.T) {
	t.Parallel()

	g, f := newSetupStatsRig(t, StatsView{Consent: "unset"})
	for _, m := range []string{"GET", "POST"} {
		if rec := g.do(m, "/admin/setup/stats", "editor-token", url.Values{"choice": {"yes"}}, nil); rec.Code != 403 {
			t.Errorf("editor %s = %d, want 403", m, rec.Code)
		}
	}
	if len(f.chosen) != 0 {
		t.Error("an editor chose")
	}
}
