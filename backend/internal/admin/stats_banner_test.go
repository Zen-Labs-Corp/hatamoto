package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

const statsBannerMarker = "data-hm-stats-banner"

func statsBannerRig(t *testing.T, view StatsView, banner string) (*updatesRig, *fakeStats) {
	t.Helper()
	f := &fakeStats{view: view, banner: banner}
	return newStatsRig(t, f), f
}

func TestOwnerStatsBannerUnanswered(t *testing.T) {
	g, _ := statsBannerRig(t, StatsView{Level: "unset"}, statsBannerUnanswered)
	rec := g.do("GET", "/admin/settings/general", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, w := range []string{statsBannerMarker + `="unanswered"`, "Help improve Hatamoto with anonymous stats.", `href="https://hatamoto.dev/docs/operate/install-stats" target="_blank" rel="noopener">What is collected</a>`, `action="/admin/settings/updates/stats/choice"`, `name="choice" value="off"`, `name="choice" value="basic"`, `name="choice" value="detailed"`, "No thanks", ">Basic<", ">Detailed<"} {
		if !strings.Contains(body, w) {
			t.Errorf("page lacks %q", w)
		}
	}
	// The answer is the POST: there is no browser-side dismissal to come back from.
	if strings.Contains(body, "data-hm-callout-dismiss") {
		t.Error("the banner has a dismiss button")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	compareGolden(t, "pages/banner-stats.html", normalizePage(body))
}

func TestOwnerStatsBannerNudge(t *testing.T) {
	g, f := statsBannerRig(t, StatsView{Level: "basic"}, statsBannerNudge)
	body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String()
	for _, w := range []string{statsBannerMarker + `="nudge"`, "Detailed stats help catch plugin and theme problems.", `rel="noopener">What is collected</a>`, `action="/admin/settings/updates/stats/dismiss"`, ">Dismiss<", "Switch to Detailed", `formaction="/admin/settings/updates/stats/choice"`, `name="choice" value="detailed"`} {
		if !strings.Contains(body, w) {
			t.Errorf("page lacks %q", w)
		}
	}
	// Its dismissal is stored by the server, not the browser-side callout one.
	if strings.Contains(body, "data-hm-callout-dismiss") || strings.Contains(body, "No thanks") {
		t.Error("the nudge has the wrong buttons")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	compareGolden(t, "pages/banner-stats-nudge.html", normalizePage(body))

	// Dismissing persists, and the service then has no question.
	if rec := g.do("POST", "/admin/settings/updates/stats/dismiss", "owner-token", url.Values{}, nil); rec.Code != 303 {
		t.Fatalf("dismiss = %d", rec.Code)
	}
	if f.dismissals != 1 {
		t.Fatalf("DismissNudge calls = %d, want 1", f.dismissals)
	}
	f.banner = ""
	if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, statsBannerMarker) {
		t.Error("the nudge is still shown after the dismissal")
	}
}

func TestStatsBannerAbsent(t *testing.T) {
	t.Run("editor", func(t *testing.T) {
		g, f := statsBannerRig(t, StatsView{Level: "unset"}, statsBannerUnanswered)
		if body := g.do("GET", "/admin/settings/general", "editor-token", nil, nil).Body.String(); strings.Contains(body, statsBannerMarker) {
			t.Error("editor sees the banner")
		}
		if f.views != 0 || f.asks != 0 {
			t.Errorf("service asked for an editor: View %d, Banner %d", f.views, f.asks)
		}
	})
	for name, c := range map[string]struct {
		view   StatsView
		banner string
	}{
		"detailed": {StatsView{Level: "detailed"}, ""},
		"off":      {StatsView{Level: "off"}, ""},
		"env off":  {StatsView{Level: "off", ForcedOff: true}, statsBannerUnanswered},
	} {
		t.Run(name, func(t *testing.T) {
			g, f := statsBannerRig(t, c.view, c.banner)
			if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, statsBannerMarker) {
				t.Error("the banner is shown")
			}
			if f.views != 0 || f.asks == 0 {
				t.Errorf("banner used View %d times and Banner %d times, want 0 and one cheap ask", f.views, f.asks)
			}
		})
	}
	t.Run("demo", func(t *testing.T) {
		g, f := statsBannerRig(t, StatsView{Level: "unset"}, statsBannerUnanswered)
		g.a.deps.Demo.Enabled = true
		if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, statsBannerMarker) {
			t.Error("the banner is shown in the demo")
		}
		if f.views != 0 || f.asks != 0 {
			t.Errorf("demo asked the service: View %d, Banner %d", f.views, f.asks)
		}
	})
	t.Run("updates page", func(t *testing.T) {
		g, _ := statsBannerRig(t, StatsView{Level: "unset"}, statsBannerUnanswered)
		body := g.do("GET", "/admin/settings/updates", "owner-token", nil, nil).Body.String()
		if strings.Contains(body, statsBannerMarker) || !strings.Contains(body, `id="updates-stats"`) {
			t.Error("the Updates page must show the card and no banner")
		}
	})
	t.Run("setup pending", func(t *testing.T) {
		g, f := statsBannerRig(t, StatsView{Level: "unset"}, statsBannerUnanswered)
		g.a.deps.Site.(*fakeSite).cfg.SetupCompletedAt = nil
		if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, statsBannerMarker) {
			t.Error("the banner is shown while setup is pending")
		}
		if f.views != 0 || f.asks != 0 {
			t.Errorf("service asked while setup is pending: View %d, Banner %d", f.views, f.asks)
		}
	})
	t.Run("no service", func(t *testing.T) {
		g := newStatsRig(t, nil)
		if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, statsBannerMarker) {
			t.Error("the banner is shown without a service")
		}
	})
	t.Run("post", func(t *testing.T) {
		g, f := statsBannerRig(t, StatsView{Level: "unset"}, statsBannerUnanswered)
		g.do("POST", "/admin/settings/updates/stats/choice", "owner-token", url.Values{"choice": {"off"}}, nil)
		if f.views != 1 || f.asks != 0 {
			t.Errorf("a POST asked the service View %d, Banner %d, want 1 and 0 (the handler's own read)", f.views, f.asks)
		}
	})
}

// Answering hides the banner afterwards.
func TestStatsBannerGoneAfterTheAnswer(t *testing.T) {
	g, f := statsBannerRig(t, StatsView{Level: "unset"}, statsBannerUnanswered)
	if rec := g.do("POST", "/admin/settings/updates/stats/choice", "owner-token", url.Values{"choice": {"off"}}, nil); rec.Code != 303 {
		t.Fatalf("choice = %d", rec.Code)
	}
	f.view.Level, f.banner = "off", ""
	if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, statsBannerMarker) {
		t.Error("the banner is still shown after the answer")
	}
}
