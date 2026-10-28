package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

const statsBannerMarker = "data-hm-stats-banner"

func statsBannerRig(t *testing.T, view StatsView) (*updatesRig, *fakeStats) {
	t.Helper()
	f := &fakeStats{view: view}
	return newStatsRig(t, f), f
}

func TestOwnerStatsBanner(t *testing.T) {
	g, _ := statsBannerRig(t, StatsView{Consent: "unset"})
	rec := g.do("GET", "/admin/settings/general", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, w := range []string{statsBannerMarker, "Help improve Hatamoto with anonymous install stats?", `href="/admin/settings/updates#updates-stats">See what is sent</a>`, `action="/admin/settings/updates/stats/choice"`, `name="choice" value="no"`, `name="choice" value="yes"`, "No thanks", "Send stats"} {
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

func TestStatsBannerAbsent(t *testing.T) {
	t.Run("editor", func(t *testing.T) {
		g, f := statsBannerRig(t, StatsView{Consent: "unset"})
		if body := g.do("GET", "/admin/settings/general", "editor-token", nil, nil).Body.String(); strings.Contains(body, statsBannerMarker) {
			t.Error("editor sees the banner")
		}
		if f.views != 0 {
			t.Errorf("service asked %d times for an editor", f.views)
		}
	})
	for name, view := range map[string]StatsView{
		"consent yes": {Consent: "yes"},
		"consent no":  {Consent: "no"},
		"env off":     {Consent: "unset", ForcedOff: true},
	} {
		t.Run(name, func(t *testing.T) {
			g, _ := statsBannerRig(t, view)
			if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, statsBannerMarker) {
				t.Error("the banner is shown")
			}
		})
	}
	t.Run("updates page", func(t *testing.T) {
		g, _ := statsBannerRig(t, StatsView{Consent: "unset", PreviewJSON: statsTestPreview})
		body := g.do("GET", "/admin/settings/updates", "owner-token", nil, nil).Body.String()
		if strings.Contains(body, statsBannerMarker) || !strings.Contains(body, `id="updates-stats"`) {
			t.Error("the Updates page must show the card and no banner")
		}
	})
	t.Run("setup pending", func(t *testing.T) {
		g, f := statsBannerRig(t, StatsView{Consent: "unset"})
		g.a.deps.Site.(*fakeSite).cfg.SetupCompletedAt = nil
		if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, statsBannerMarker) {
			t.Error("the banner is shown while setup is pending")
		}
		if f.views != 0 {
			t.Errorf("service asked %d times while setup is pending", f.views)
		}
	})
	t.Run("no service", func(t *testing.T) {
		g := newStatsRig(t, nil)
		if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, statsBannerMarker) {
			t.Error("the banner is shown without a service")
		}
	})
	t.Run("post", func(t *testing.T) {
		g, f := statsBannerRig(t, StatsView{Consent: "unset"})
		g.do("POST", "/admin/settings/updates/stats/choice", "owner-token", url.Values{"choice": {"no"}}, nil)
		if f.views != 1 {
			t.Errorf("a POST asked the service %d times, want 1 (the handler's own read)", f.views)
		}
	})
}

// Answering hides the banner afterwards.
func TestStatsBannerGoneAfterTheAnswer(t *testing.T) {
	g, f := statsBannerRig(t, StatsView{Consent: "unset"})
	if rec := g.do("POST", "/admin/settings/updates/stats/choice", "owner-token", url.Values{"choice": {"no"}}, nil); rec.Code != 303 {
		t.Fatalf("choice = %d", rec.Code)
	}
	f.view.Consent = "no"
	if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, statsBannerMarker) {
		t.Error("the banner is still shown after the answer")
	}
}
