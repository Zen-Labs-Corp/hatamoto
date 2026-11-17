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

func TestOwnerStatsBannerDefaulted(t *testing.T) {
	g, f := statsBannerRig(t, StatsView{Level: "detailed"}, statsBannerDefaulted)
	body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String()
	for _, w := range []string{statsBannerMarker + `="defaulted"`, "Hatamoto shares usage stats (version, site address, plugins, theme, error counts).", `rel="noopener">What is collected</a>`, `action="/admin/settings/updates/stats/dismiss"`, `href="/admin/settings/updates"`, "Change in Settings", ">Got it<"} {
		if !strings.Contains(body, w) {
			t.Errorf("page lacks %q", w)
		}
	}
	if strings.Contains(body, "data-hm-callout-dismiss") || strings.Contains(body, "No thanks") || strings.Contains(body, "/stats/choice") {
		t.Error("the notice has the wrong buttons")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	compareGolden(t, "pages/banner-stats-defaulted.html", normalizePage(body))

	if rec := g.do("POST", "/admin/settings/updates/stats/dismiss", "owner-token", url.Values{}, nil); rec.Code != 303 {
		t.Fatalf("got it = %d", rec.Code)
	}
	if f.dismissals != 1 {
		t.Fatalf("DismissNudge calls = %d, want 1", f.dismissals)
	}
	if f.views != 1 {
		t.Errorf("View calls = %d, want only the dismiss handler's own read", f.views)
	}
	f.banner = ""
	if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, statsBannerMarker) {
		t.Error("the notice is still shown after Got it")
	}
}

func TestStatsBannerAbsent(t *testing.T) {
	t.Run("editor", func(t *testing.T) {
		g, f := statsBannerRig(t, StatsView{Level: "unset"}, statsBannerDefaulted)
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
		"env off":  {StatsView{Level: "off", ForcedOff: true}, statsBannerDefaulted},
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
		g, f := statsBannerRig(t, StatsView{Level: "detailed"}, statsBannerDefaulted)
		g.a.deps.Demo.Enabled = true
		if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, statsBannerMarker) {
			t.Error("the banner is shown in the demo")
		}
		if f.views != 0 || f.asks != 0 {
			t.Errorf("demo asked the service: View %d, Banner %d", f.views, f.asks)
		}
	})
	t.Run("updates page", func(t *testing.T) {
		g, _ := statsBannerRig(t, StatsView{Level: "detailed"}, statsBannerDefaulted)
		body := g.do("GET", "/admin/settings/updates", "owner-token", nil, nil).Body.String()
		if strings.Contains(body, statsBannerMarker) || !strings.Contains(body, `id="updates-stats"`) {
			t.Error("the Updates page must show the card and no banner")
		}
	})
	t.Run("setup pending", func(t *testing.T) {
		g, f := statsBannerRig(t, StatsView{Level: "detailed"}, statsBannerDefaulted)
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
		g, f := statsBannerRig(t, StatsView{Level: "detailed"}, statsBannerDefaulted)
		g.do("POST", "/admin/settings/updates/stats/dismiss", "owner-token", url.Values{}, nil)
		if f.views != 1 || f.asks != 0 {
			t.Errorf("a POST asked the service View %d, Banner %d, want 1 and 0 (the handler's own read)", f.views, f.asks)
		}
	})
}
