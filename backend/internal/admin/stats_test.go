package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeStats is a StatsService that records what the handlers asked of it.
type fakeStats struct {
	view       StatsView
	banner     string // what Banner answers; "" for none
	viewErr    error
	chooseE    error
	resetE     error
	dismissE   error
	views      int
	asks       int
	chosen     []string
	resets     int
	dismissals int
	defaults   int
	defaultE   error
}

func (f *fakeStats) View(context.Context) (StatsView, error) {
	f.views++
	return f.view, f.viewErr
}

func (f *fakeStats) Banner(context.Context) (string, error) {
	f.asks++
	if f.view.ForcedOff {
		return "", f.viewErr
	}
	return f.banner, f.viewErr
}

func (f *fakeStats) ForcedOff() bool { return f.view.ForcedOff }

func (f *fakeStats) Choose(_ context.Context, level string) error {
	f.chosen = append(f.chosen, level)
	return f.chooseE
}

func (f *fakeStats) DismissNudge(context.Context) error {
	f.dismissals++
	return f.dismissE
}

func (f *fakeStats) ApplyDefault(context.Context) error {
	f.defaults++
	return f.defaultE
}

func (f *fakeStats) Reset(context.Context) error {
	f.resets++
	return f.resetE
}

const statsTestID = "0123456789abcdef0123456789abcdef"

func newStatsRig(t *testing.T, f *fakeStats) *updatesRig {
	t.Helper()
	u := newUpdatesRig(t, baseUpdates())
	if f != nil {
		u.a.deps.Stats = f
	}
	return u
}

var statsPosts = []string{"/stats", "/stats/choice", "/stats/dismiss", "/stats/reset"}

func TestStatsPostsAreOwnerOnlyAndNeedCSRF(t *testing.T) {
	f := &fakeStats{view: StatsView{Level: "unset"}}
	u := newStatsRig(t, f)
	for _, p := range statsPosts {
		target := "/admin/settings/updates" + p
		if rec := u.do("POST", target, "editor-token", url.Values{"choice": {"basic"}, "level": {"basic"}}, nil); rec.Code != 403 {
			t.Errorf("editor POST %s = %d, want 403", target, rec.Code)
		}
		if rec := u.do("POST", target, "", url.Values{"choice": {"basic"}, "level": {"basic"}}, nil); rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/login") {
			t.Errorf("anonymous POST %s = %d %s", target, rec.Code, rec.Header().Get("Location"))
		}
		if rec := u.do("POST", target, "owner-token", url.Values{"choice": {"basic"}, "level": {"basic"}}, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
			t.Errorf("no CSRF POST %s = %d, want 403", target, rec.Code)
		}
	}
	if f.views != 0 || len(f.chosen) != 0 || f.resets != 0 || f.dismissals != 0 || len(u.events) != 0 {
		t.Error("a refused request reached the service")
	}
}

func TestStatsPostsWithoutServiceAre503(t *testing.T) {
	u := newStatsRig(t, nil)
	for _, p := range statsPosts {
		if rec := u.do("POST", "/admin/settings/updates"+p, "owner-token", url.Values{"choice": {"basic"}, "level": {"basic"}}, nil); rec.Code != 503 {
			t.Errorf("POST %s without a service = %d, want 503", p, rec.Code)
		}
	}
}

func TestStatsChoiceCallsServiceOnceAndAuditsWithoutID(t *testing.T) {
	for _, choice := range []string{"off", "basic", "detailed"} {
		f := &fakeStats{view: StatsView{Level: "unset"}}
		u := newStatsRig(t, f)
		rec := u.do("POST", "/admin/settings/updates/stats/choice", "owner-token", url.Values{"choice": {choice}}, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/updates" {
			t.Fatalf("choice %s = %d %s", choice, rec.Code, rec.Header().Get("Location"))
		}
		if len(f.chosen) != 1 || f.chosen[0] != choice {
			t.Errorf("choice %s: Choose calls = %v", choice, f.chosen)
		}
		if len(u.events) != 1 || u.events[0].Action != "stats.consent" || u.events[0].Detail["choice"] != choice {
			t.Fatalf("choice %s: audit = %+v", choice, u.events)
		}
		if strings.Contains(fmt.Sprint(u.events), statsTestID) {
			t.Error("audit detail carries the install ID")
		}
	}
}

func TestStatsChoiceRefusesAnythingElse(t *testing.T) {
	f := &fakeStats{view: StatsView{Level: "unset"}}
	u := newStatsRig(t, f)
	for _, form := range []url.Values{{}, {"choice": {""}}, {"choice": {"maybe"}}, {"choice": {"yes"}}, {"choice": {"Basic"}}} {
		if rec := u.do("POST", "/admin/settings/updates/stats/choice", "owner-token", form, nil); rec.Code != 400 {
			t.Errorf("choice %v = %d, want 400", form, rec.Code)
		}
	}
	if len(f.chosen) != 0 || len(u.events) != 0 {
		t.Error("an invalid choice reached the service")
	}
}

func TestStatsLevelPost(t *testing.T) {
	for _, level := range []string{"off", "basic", "detailed"} {
		f := &fakeStats{view: StatsView{Level: "off"}}
		u := newStatsRig(t, f)
		rec := u.do("POST", "/admin/settings/updates/stats", "owner-token", url.Values{"level": {level}}, nil)
		if rec.Code != 303 {
			t.Fatalf("level %q = %d", level, rec.Code)
		}
		if len(f.chosen) != 1 || f.chosen[0] != level {
			t.Errorf("level %q: Choose calls = %v", level, f.chosen)
		}
		if len(u.events) != 1 || u.events[0].Detail["choice"] != level {
			t.Errorf("level %q: audit = %+v", level, u.events)
		}
	}
	f := &fakeStats{view: StatsView{Level: "basic"}}
	u := newStatsRig(t, f)
	for _, form := range []url.Values{{}, {"level": {""}}, {"level": {"on"}}, {"stats": {"on"}}} {
		if rec := u.do("POST", "/admin/settings/updates/stats", "owner-token", form, nil); rec.Code != 400 {
			t.Errorf("level %v = %d, want 400", form, rec.Code)
		}
	}
	if len(f.chosen) != 0 {
		t.Errorf("an invalid level reached the service: %v", f.chosen)
	}
}

func TestStatsDismissCallsServiceOnce(t *testing.T) {
	f := &fakeStats{view: StatsView{Level: "basic"}, banner: statsBannerNudge}
	u := newStatsRig(t, f)
	rec := u.do("POST", "/admin/settings/updates/stats/dismiss", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || f.dismissals != 1 || len(f.chosen) != 0 {
		t.Errorf("dismiss = %d, dismissals %d, chosen %v", rec.Code, f.dismissals, f.chosen)
	}
}

func TestStatsForcedOffChangesNothing(t *testing.T) {
	f := &fakeStats{view: StatsView{Level: "off", ForcedOff: true}}
	u := newStatsRig(t, f)
	for _, p := range statsPosts {
		rec := u.do("POST", "/admin/settings/updates"+p, "owner-token", url.Values{"choice": {"detailed"}, "level": {"detailed"}}, nil)
		if rec.Code != 303 {
			t.Fatalf("POST %s = %d", p, rec.Code)
		}
		if body := u.followFlash(rec); !strings.Contains(body, statsForcedOffNote) {
			t.Errorf("POST %s: flash missing %q", p, statsForcedOffNote)
		}
	}
	if len(f.chosen) != 0 || f.resets != 0 || f.dismissals != 0 || len(u.events) != 0 {
		t.Errorf("forced off: Choose %v, Reset %d, dismissals %d, audit %d", f.chosen, f.resets, f.dismissals, len(u.events))
	}
}

func TestStatsResetCallsServiceOnce(t *testing.T) {
	f := &fakeStats{view: StatsView{Level: "basic"}}
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

func TestStatsServiceErrorsAreQuietAndNotAudited(t *testing.T) {
	boom := errors.New("db down " + statsTestID)
	for _, c := range []struct {
		name string
		f    *fakeStats
		path string
	}{
		{"choose", &fakeStats{view: StatsView{Level: "unset"}, chooseE: boom}, "/stats/choice"},
		{"level", &fakeStats{view: StatsView{Level: "basic"}, chooseE: boom}, "/stats"},
		{"dismiss", &fakeStats{view: StatsView{Level: "basic"}, dismissE: boom}, "/stats/dismiss"},
		{"view", &fakeStats{viewErr: boom}, "/stats/choice"},
		{"reset view", &fakeStats{viewErr: boom}, "/stats/reset"},
		{"reset", &fakeStats{view: StatsView{Level: "basic"}, resetE: boom}, "/stats/reset"},
	} {
		t.Run(c.name, func(t *testing.T) {
			u := newStatsRig(t, c.f)
			rec := u.do("POST", "/admin/settings/updates"+c.path, "owner-token", url.Values{"choice": {"basic"}, "level": {"basic"}}, nil)
			if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/updates" {
				t.Fatalf("= %d %s, want a quiet 303 to Settings > Updates", rec.Code, rec.Header().Get("Location"))
			}
			if rec.Header().Get("Set-Cookie") != "" {
				t.Errorf("a flash was set: %v", rec.Header().Get("Set-Cookie"))
			}
			if body := rec.Body.String(); strings.Contains(body, statsTestID) || strings.Contains(body, "went wrong") {
				t.Errorf("body shows the failure: %q", body)
			}
			if len(u.events) != 0 {
				t.Error("a failed change was audited")
			}
		})
	}
}

func TestStatsServiceStillRefusesWithoutServiceOrChoice(t *testing.T) {
	u := newStatsRig(t, &fakeStats{view: StatsView{Level: "unset"}})
	if rec := u.do("POST", "/admin/settings/updates/stats/choice", "owner-token", url.Values{"choice": {"maybe"}}, nil); rec.Code != 400 {
		t.Errorf("bad choice = %d, want 400", rec.Code)
	}
}

func TestUpdatesInstallStatsCardStates(t *testing.T) {
	sent := updatesClock.Add(-3 * time.Hour)
	common := []string{`id="updates-stats"`, `name="level"`, "Nothing is sent", "Version and update channel", "Basic, plus plugins, theme and error counts", `href="https://hatamoto.dev/docs/operate/install-stats" target="_blank" rel="noopener">What is collected</a>`}
	for _, c := range []struct {
		name    string
		view    StatsView
		wants   []string
		absents []string
	}{
		{"unset", StatsView{Level: "unset"}, common, []string{`name="level" value="off" checked`, `name="level" value="basic" checked`, `name="level" value="detailed" checked`, "Reset ID", "Last sent"}},
		{"off", StatsView{Level: "off"}, append([]string{`name="level" value="off" checked`}, common...), []string{`name="level" value="basic" checked`, "Reset ID", "Last sent"}},
		{"basic", StatsView{Level: "basic", LastSent: &sent}, append([]string{`name="level" value="basic" checked`, "Reset ID", `id="reset-stats-form"`, "Last sent"}, common...), []string{`name="level" value="off" checked`, "Not sent yet."}},
		{"detailed", StatsView{Level: "detailed", LastSent: &sent}, append([]string{`name="level" value="detailed" checked`, "Reset ID", "Last sent"}, common...), []string{`name="level" value="off" checked`, "Not sent yet."}},
		{"detailed_never_sent", StatsView{Level: "detailed"}, []string{"Last sent", "Not sent yet. Hatamoto tries again once a day."}, nil},
		{"forced_off", StatsView{Level: "off", ForcedOff: true}, []string{"Turned off by the server configuration.", "hm-badge"}, []string{`name="level"`, `id="stats-form"`, "Reset ID"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeStats{view: c.view}
			body := newStatsRig(t, f).get(t)
			for _, w := range c.wants {
				if !strings.Contains(body, w) {
					t.Errorf("page lacks %q", w)
				}
			}
			for _, b := range append(c.absents, "hm-code", "What is sent", "error: ") {
				if strings.Contains(body, b) {
					t.Errorf("page has %q", b)
				}
			}
			if c.name != "detailed_never_sent" {
				compareGolden(t, "pages/settings-updates-stats-"+c.name+".html", normalizePage(body))
			}
		})
	}
}

func TestStatsCardAbsentInDemo(t *testing.T) {
	u := newStatsRig(t, &fakeStats{view: StatsView{Level: "unset"}})
	u.a.deps.Demo.Enabled = true
	if body := u.get(t); strings.Contains(body, `id="updates-stats"`) {
		t.Error("the demo shows the Install stats card")
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

func TestSetupStatsStepOffersThreeLevelsAndPreselectsDetailed(t *testing.T) {
	t.Parallel()

	// Detailed is preselected whatever level is stored.
	g, f := newSetupStatsRig(t, StatsView{Level: "off"})
	body := g.do("GET", "/admin/setup/stats", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Help improve Hatamoto", `name="choice" value="off"`, `name="choice" value="basic"`, `name="choice" value="detailed"`, `href="https://hatamoto.dev/docs/operate/install-stats" target="_blank" rel="noopener">What is collected</a>`, "Continue", `href="/admin/setup/theme"><span>Back</span>`, "Step 6 of 8"} {
		if !strings.Contains(body, want) {
			t.Errorf("stats step lacks %q", want)
		}
	}
	if n := strings.Count(body, "checked"); n != 1 || !strings.Contains(body, `value="detailed" checked`) {
		t.Errorf("want only Detailed checked, got %d checked in %s", n, body)
	}
	for _, not := range []string{"required", "hm-code", "No thanks", "Send stats"} {
		if strings.Contains(body, not) {
			t.Errorf("stats step has %q", not)
		}
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

	for _, choice := range []string{"off", "basic", "detailed"} {
		g, f := newSetupStatsRig(t, StatsView{Level: "unset"})
		var events []AuditEvent
		g.a.deps.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }
		rec := g.do("POST", "/admin/setup/stats", "owner-token", url.Values{"choice": {choice}}, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/content" {
			t.Fatalf("%s: %d %s", choice, rec.Code, rec.Header().Get("Location"))
		}
		if len(f.chosen) != 1 || f.chosen[0] != choice {
			t.Errorf("%s: Choose calls = %v", choice, f.chosen)
		}
		if len(events) != 1 || events[0].Action != "stats.consent" || events[0].Detail["choice"] != choice {
			t.Errorf("%s: audit = %+v", choice, events)
		}
		if g.site.cfg.SetupCompletedAt != nil {
			t.Error("the Stats step finished setup")
		}
	}
}

func TestSetupStatsWithoutAChoiceIsDetailed(t *testing.T) {
	t.Parallel()

	for _, form := range []url.Values{{}, {"choice": {""}}} {
		g, f := newSetupStatsRig(t, StatsView{Level: "unset"})
		rec := g.do("POST", "/admin/setup/stats", "owner-token", form, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/content" {
			t.Errorf("post %v = %d %s", form, rec.Code, rec.Header().Get("Location"))
		}
		if len(f.chosen) != 1 || f.chosen[0] != "detailed" {
			t.Errorf("post %v chose %v", form, f.chosen)
		}
	}
}

func TestSetupStatsInvalidChoiceAsksAgain(t *testing.T) {
	t.Parallel()

	g, f := newSetupStatsRig(t, StatsView{Level: "unset"})
	for _, form := range []url.Values{{"choice": {"maybe"}}, {"choice": {"yes"}}} {
		rec := g.do("POST", "/admin/setup/stats", "owner-token", form, nil)
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Choose Off, Basic or Detailed.") {
			t.Errorf("post %v = %d", form, rec.Code)
		}
	}
	if len(f.chosen) != 0 {
		t.Errorf("an invalid form chose %v", f.chosen)
	}
}

func TestSetupFinishAppliesStatsDefault(t *testing.T) {
	t.Parallel()

	for name, failing := range map[string]error{"ok": nil, "failing": context.DeadlineExceeded} {
		g, f := newSetupStatsRig(t, StatsView{Level: "unset"})
		f.defaultE = failing
		rec := g.do("POST", "/admin/setup/content", "owner-token", url.Values{"content": {"empty"}}, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/done" {
			t.Errorf("%s: finish = %d %s", name, rec.Code, rec.Header().Get("Location"))
		}
		if f.defaults != 1 {
			t.Errorf("%s: ApplyDefault calls = %d, want 1", name, f.defaults)
		}
	}
}

func TestSetupStatsSkippedUnderEnvOffOrWithoutService(t *testing.T) {
	t.Parallel()

	for name, setup := range map[string]func(g *setupRig, f *fakeStats){
		"forced off": func(g *setupRig, f *fakeStats) { f.view.ForcedOff = true },
		"no service": func(g *setupRig, f *fakeStats) { g.a.deps.Stats = nil },
	} {
		g, f := newSetupStatsRig(t, StatsView{Level: "off"})
		setup(g, f)
		for _, m := range []string{"GET", "POST"} {
			rec := g.do(m, "/admin/setup/stats", "owner-token", url.Values{"choice": {"basic"}, "level": {"basic"}}, nil)
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

	g, f := newSetupStatsRig(t, StatsView{Level: "unset"})
	if rec := g.do("POST", "/admin/setup/content", "owner-token", url.Values{"content": {"empty"}}, nil); rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/done" || g.site.cfg.SetupCompletedAt == nil {
		t.Errorf("content: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(f.chosen) != 0 {
		t.Error("finishing setup chose for the owner")
	}
}

func TestSetupStatsIsOwnerOnly(t *testing.T) {
	t.Parallel()

	g, f := newSetupStatsRig(t, StatsView{Level: "unset"})
	for _, m := range []string{"GET", "POST"} {
		if rec := g.do(m, "/admin/setup/stats", "editor-token", url.Values{"choice": {"basic"}, "level": {"basic"}}, nil); rec.Code != 403 {
			t.Errorf("editor %s = %d, want 403", m, rec.Code)
		}
	}
	if len(f.chosen) != 0 {
		t.Error("an editor chose")
	}
}

func TestSetupStatsStoreErrorsContinueToContent(t *testing.T) {
	t.Parallel()

	boom := errors.New("db down " + statsTestID)
	for _, c := range []struct {
		name   string
		method string
		form   url.Values
		setup  func(f *fakeStats)
	}{
		{"post choose", "POST", url.Values{"choice": {"basic"}}, func(f *fakeStats) { f.chooseE = boom }},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			g, f := newSetupStatsRig(t, StatsView{Level: "unset"})
			c.setup(f)
			var logs strings.Builder
			g.a.deps.Logger = slog.New(slog.NewTextHandler(&logs, nil))
			var events []AuditEvent
			g.a.deps.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }
			rec := g.do(c.method, "/admin/setup/stats", "owner-token", c.form, nil)
			if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/content" {
				t.Fatalf("= %d %s, want 303 to Content", rec.Code, rec.Header().Get("Location"))
			}
			if len(events) != 0 {
				t.Errorf("audit = %+v", events)
			}
			if !strings.Contains(logs.String(), "level=WARN") {
				t.Errorf("log = %q, want a warning", logs.String())
			}
		})
	}
}

func TestSetupStatsSkippedInDemo(t *testing.T) {
	t.Parallel()

	g, f := newSetupStatsRig(t, StatsView{Level: "unset"})
	g.a.deps.Demo.Enabled = true
	for _, m := range []string{"GET", "POST"} {
		rec := g.do(m, "/admin/setup/stats", "owner-token", url.Values{"choice": {"basic"}, "level": {"basic"}}, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/content" {
			t.Errorf("%s = %d %s", m, rec.Code, rec.Header().Get("Location"))
		}
	}
	if len(f.chosen) != 0 {
		t.Errorf("Choose called %v", f.chosen)
	}
	rec := g.do("POST", "/admin/setup/theme", "owner-token", url.Values{"colour": {"#aa3300"}}, nil)
	if rec.Header().Get("Location") != "/admin/setup/content" {
		t.Errorf("theme goes to %s", rec.Header().Get("Location"))
	}
}
