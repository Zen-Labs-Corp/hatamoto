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
	view     StatsView
	viewErr  error
	chooseE  error
	views    int
	chosen   []string
	defaults int
	defaultE error
}

func (f *fakeStats) View(context.Context) (StatsView, error) {
	f.views++
	return f.view, f.viewErr
}

func (f *fakeStats) ForcedOff() bool { return f.view.ForcedOff }

func (f *fakeStats) Choose(_ context.Context, level string) error {
	f.chosen = append(f.chosen, level)
	return f.chooseE
}

func (f *fakeStats) ApplyDefault(context.Context) error {
	f.defaults++
	return f.defaultE
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

var statsPosts = []string{"/stats"}

func TestStatsPostsAreOwnerOnlyAndNeedCSRF(t *testing.T) {
	f := &fakeStats{view: StatsView{Level: "unset"}}
	u := newStatsRig(t, f)
	for _, p := range statsPosts {
		target := "/admin/settings/updates" + p
		if rec := u.do("POST", target, "editor-token", url.Values{"level": {"detailed"}}, nil); rec.Code != 403 {
			t.Errorf("editor POST %s = %d, want 403", target, rec.Code)
		}
		if rec := u.do("POST", target, "", url.Values{"level": {"detailed"}}, nil); rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/login") {
			t.Errorf("anonymous POST %s = %d %s", target, rec.Code, rec.Header().Get("Location"))
		}
		if rec := u.do("POST", target, "owner-token", url.Values{"level": {"detailed"}}, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
			t.Errorf("no CSRF POST %s = %d, want 403", target, rec.Code)
		}
	}
	if f.views != 0 || len(f.chosen) != 0 || len(u.events) != 0 {
		t.Error("a refused request reached the service")
	}
}

func TestStatsPostsWithoutServiceAre503(t *testing.T) {
	u := newStatsRig(t, nil)
	for _, p := range statsPosts {
		if rec := u.do("POST", "/admin/settings/updates"+p, "owner-token", url.Values{"choice": {"detailed"}, "level": {"detailed"}}, nil); rec.Code != 503 {
			t.Errorf("POST %s without a service = %d, want 503", p, rec.Code)
		}
	}
}

func TestStatsLevelPost(t *testing.T) {
	for _, level := range []string{"off", "detailed"} {
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
		if strings.Contains(fmt.Sprint(u.events), statsTestID) {
			t.Error("audit detail carries the install ID")
		}
	}
	// The checkbox posts level=detailed when ticked and the hidden level=off after it
	// when not; the first value wins, so a ticked box is never read as off.
	f0 := &fakeStats{view: StatsView{Level: "off"}}
	u0 := newStatsRig(t, f0)
	if rec := u0.do("POST", "/admin/settings/updates/stats", "owner-token", url.Values{"level": {"detailed", "off"}}, nil); rec.Code != 303 || len(f0.chosen) != 1 || f0.chosen[0] != "detailed" {
		t.Errorf("ticked box: %d, chose %v", rec.Code, f0.chosen)
	}
	f := &fakeStats{view: StatsView{Level: "basic"}}
	u := newStatsRig(t, f)
	for _, form := range []url.Values{{}, {"level": {""}}, {"level": {"on"}}, {"level": {"basic"}}, {"stats": {"on"}}} {
		if rec := u.do("POST", "/admin/settings/updates/stats", "owner-token", form, nil); rec.Code != 400 {
			t.Errorf("level %v = %d, want 400", form, rec.Code)
		}
	}
	if len(f.chosen) != 0 {
		t.Errorf("an invalid level reached the service: %v", f.chosen)
	}
}

func TestStatsDismissRouteIsGone(t *testing.T) {
	u := newStatsRig(t, &fakeStats{view: StatsView{Level: "basic"}})
	if rec := u.do("POST", "/admin/settings/updates/stats/dismiss", "owner-token", url.Values{}, nil); rec.Code == 303 {
		t.Errorf("dismiss route still answers %d", rec.Code)
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
	if len(f.chosen) != 0 || len(u.events) != 0 {
		t.Errorf("forced off: Choose %v, audit %d", f.chosen, len(u.events))
	}
}

func TestStatsServiceErrorsAreQuietAndNotAudited(t *testing.T) {
	boom := errors.New("db down " + statsTestID)
	for _, c := range []struct {
		name string
		f    *fakeStats
		path string
	}{
		{"level", &fakeStats{view: StatsView{Level: "detailed"}, chooseE: boom}, "/stats"},
		{"view", &fakeStats{viewErr: boom}, "/stats"},
	} {
		t.Run(c.name, func(t *testing.T) {
			u := newStatsRig(t, c.f)
			rec := u.do("POST", "/admin/settings/updates"+c.path, "owner-token", url.Values{"choice": {"detailed"}, "level": {"detailed"}}, nil)
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

func TestUpdatesInstallStatsCardStates(t *testing.T) {
	sent := updatesClock.Add(-3 * time.Hour)
	common := []string{`id="updates-stats"`, `name="level"`, `id="stats-on"`, "Share usage stats with Hatamoto", `href="https://hatamoto.dev/docs/operate/install-stats" target="_blank" rel="noopener">What is collected</a>`}
	const ticked, off = `value="detailed" checked`, `value="off"`
	for _, c := range []struct {
		name    string
		view    StatsView
		wants   []string
		absents []string
	}{
		{"unset", StatsView{Level: "unset"}, append([]string{ticked, "Last sent: not sent yet."}, common...), nil},
		{"off", StatsView{Level: "off"}, append([]string{off}, common...), []string{ticked, "Last sent"}},
		{"detailed", StatsView{Level: "detailed", LastSent: &sent}, append([]string{ticked, "Last sent: "}, common...), []string{"not sent yet"}},
		{"forced_off", StatsView{Level: "off", ForcedOff: true}, []string{`id="stats-on"`, "disabled", "Turned off by the server configuration."}, []string{ticked, `id="stats-form"`, "What is collected"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeStats{view: c.view}
			body := newStatsRig(t, f).get(t)
			for _, w := range c.wants {
				if !strings.Contains(body, w) {
					t.Errorf("page lacks %q", w)
				}
			}
			for _, b := range append(c.absents, "Reset ID", `name="level" value="basic"`, "Basic", "hm-code", "What is sent", "error: ") {
				if strings.Contains(body, b) {
					t.Errorf("page has %q", b)
				}
			}
			compareGolden(t, "pages/settings-updates-stats-"+c.name+".html", normalizePage(body))
		})
	}
	// A leftover basic level is shown as on.
	if body := newStatsRig(t, &fakeStats{view: StatsView{Level: "basic"}}).get(t); !strings.Contains(body, ticked) {
		t.Error("a basic level is not ticked")
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

// The wizard has no Stats step: Look goes straight to Content and Content's Back goes to Look.
func TestSetupStatsStepGone(t *testing.T) {
	t.Parallel()

	g, f := newSetupStatsRig(t, StatsView{Level: "unset"})
	for _, m := range []string{"GET", "POST"} {
		if rec := g.do(m, "/admin/setup/stats", "owner-token", url.Values{"choice": {"detailed"}}, nil); rec.Code != 404 {
			t.Errorf("%s /admin/setup/stats = %d, want 404", m, rec.Code)
		}
	}
	if len(f.chosen) != 0 {
		t.Errorf("Choose called %v", f.chosen)
	}
	rec := g.do("POST", "/admin/setup/theme", "owner-token", url.Values{"colour": {"#aa3300"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/content" {
		t.Errorf("theme goes to %d %s", rec.Code, rec.Header().Get("Location"))
	}
	body := g.do("GET", "/admin/setup/content", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `href="/admin/setup/theme"><span>Back</span>`) || strings.Contains(body, "/admin/setup/stats") {
		t.Error("Back from Content must go to Look")
	}
	for _, step := range []string{"Your account", "Admin address", "Your site", "Email", "Look", "Content", "Done"} {
		if !strings.Contains(body, step) {
			t.Errorf("step list lacks %q", step)
		}
	}
	if strings.Contains(body, ">Stats<") || !strings.Contains(body, "Step 6 of 7") {
		t.Errorf("step list still has Stats or wrong count")
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

// The seed script posts Look and Content directly; finishing setup never chooses for the owner.
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
