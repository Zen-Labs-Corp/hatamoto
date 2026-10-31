package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

// fakeEngagement is the engagement switch, remembering what was set.
type fakeEngagement struct {
	on  bool
	set []bool
	err error
}

func (f *fakeEngagement) Enabled(context.Context) bool { return f.on }

func (f *fakeEngagement) Set(_ context.Context, on bool) error {
	if f.err != nil {
		return f.err
	}
	f.on = on
	f.set = append(f.set, on)
	return nil
}

func trafficRig(t *testing.T, mutate ...func(*Deps)) *rig {
	t.Helper()
	rg, _ := parseRange(url.Values{})
	return newRig(t, append([]func(*Deps){func(d *Deps) {
		d.Growth.Traffic, d.Growth.Conversions = &fakeTraffic{from: rg.From}, &fakeConversions{}
	}}, mutate...)...)
}

func TestTrafficPageSections(t *testing.T) {
	t.Parallel()

	g := trafficRig(t, func(d *Deps) { d.Growth.EngagementSetting = &fakeEngagement{on: true} })
	rec := g.do("GET", "/admin/analytics/traffic", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{"Where do visitors come from?", "UTM tags win over the referrer",
		"Visits", "Pageviews", "Leads", "Conv. rate", "Top channel", "Organic search",
		"w-t-channels", "w-t-sources", "w-t-campaigns", "w-t-landing", "w-t-devices", "w-t-countries", "w-t-engagement", "w-t-measurement",
		"A visit is the first pageview of a tab or an arrival from another site.", "can differ",
		"First touch", "Last touch", "Apply",
		`data-chart="channels"`, "traffic.json", "oct-news", "(direct)", "Desktop", "Bounced", "Scrolled 75%",
		"Counted per day without visitor IDs."} {
		if !strings.Contains(body, want) {
			t.Errorf("traffic page missing %q", want)
		}
	}
	for _, bad := range []string{">--<", "coming next", "disabled aria-disabled"} {
		if strings.Contains(body, bad) {
			t.Errorf("the lead columns and the touch toggle are live, found %q", bad)
		}
	}
	if !strings.Contains(body, `<input type="radio" name="touch" value="first" checked`) && !strings.Contains(body, `value="first" checked`) {
		t.Error("first touch must be the default choice")
	}
	if !strings.Contains(body, "2 leads before attribution was recorded") {
		t.Error("the unknown leads note is missing")
	}
	if !strings.Contains(body, `AI assistants <span class="hm-badge hm-badge--info`) {
		t.Error("the AI assistants channel row must carry the new badge")
	}
	if !strings.Contains(body, "60.0%") || !strings.Contains(body, "20.0%") {
		t.Error("the Channels share column must show one decimal on every row")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestTrafficPageCountriesEmptyStateWithoutGeoIP(t *testing.T) {
	t.Parallel()

	body := trafficRig(t).do("GET", "/admin/analytics/traffic", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Set up country lookup", "Set HATAMOTO_GEOIP_DB to a DB-IP Lite or GeoLite2 country .mmdb file.", "Not configured"} {
		if !strings.Contains(body, want) {
			t.Errorf("without GeoIP missing %q", want)
		}
	}
	if strings.Contains(body, "From the IP at request time") {
		t.Error("the IP note belongs to the data state")
	}

	g := trafficRig(t, func(d *Deps) { d.Growth.GeoStatus = func() (bool, bool) { return true, true } })
	body = g.do("GET", "/admin/analytics/traffic", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Australia", "From the IP at request time; the IP is discarded.", "Loaded"} {
		if !strings.Contains(body, want) {
			t.Errorf("with GeoIP missing %q", want)
		}
	}
	if strings.Contains(body, "Set up country lookup") {
		t.Error("a loaded database has no setup state")
	}
}

func TestTrafficPageEngagementFormIsOwnerOnly(t *testing.T) {
	t.Parallel()

	g := trafficRig(t, func(d *Deps) { d.Growth.EngagementSetting = &fakeEngagement{on: true} })
	owner := g.do("GET", "/admin/analytics/traffic", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`action="/admin/analytics/traffic/settings"`, `name="engagement"`, `role="switch"`, "checked", `name="_csrf"`} {
		if !strings.Contains(owner, want) {
			t.Errorf("owner form missing %q", want)
		}
	}
	editor := g.do("GET", "/admin/analytics/traffic", "editor-token", nil, nil).Body.String()
	if strings.Contains(editor, "/analytics/traffic/settings") || strings.Contains(editor, `name="engagement"`) {
		t.Error("an editor must not get the opt-out form")
	}
	if !strings.Contains(editor, "Only the site owner can change it.") {
		t.Error("an editor sees the state, read only")
	}
}

func TestTrafficPageWithoutReaderIsUnavailable(t *testing.T) {
	t.Parallel()

	rec := newRig(t).do("GET", "/admin/analytics/traffic", "owner-token", nil, nil)
	if rec.Code != 503 {
		t.Errorf("status %d, want 503", rec.Code)
	}
}

func TestTrafficPageEmptyRange(t *testing.T) {
	t.Parallel()

	rg, _ := parseRange(url.Values{})
	g := newRig(t, func(d *Deps) {
		d.Growth.Traffic, d.Growth.Conversions = &fakeTraffic{from: rg.From, empty: true}, &fakeConversions{empty: true}
	})
	rec := g.do("GET", "/admin/analytics/traffic", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "No visits in this period.") {
		t.Fatalf("status %d, empty state missing", rec.Code)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestTrafficPageEngagementOffShowsNoBounce(t *testing.T) {
	t.Parallel()

	g := trafficRig(t, func(d *Deps) { d.Growth.EngagementSetting = &fakeEngagement{on: false} })
	body := g.do("GET", "/admin/analytics/traffic", "owner-token", nil, nil).Body.String()
	if strings.Contains(body, "Bounced") || strings.Contains(body, "Scrolled 75%") {
		t.Error("with engagement counting off the card must not report bounce figures")
	}
	if !strings.Contains(body, "Engagement counting is off") {
		t.Error("the engagement card must say counting is off")
	}
}

func TestTrafficPageLeadsFollowTheTouch(t *testing.T) {
	t.Parallel()

	g := trafficRig(t)
	first := g.do("GET", "/admin/analytics/traffic", "owner-token", nil, nil).Body.String()
	last := g.do("GET", "/admin/analytics/traffic?touch=last", "owner-token", nil, nil).Body.String()
	for _, want := range []string{">12<", "Every submission that is not spam."} {
		if !strings.Contains(first, want) {
			t.Errorf("first touch missing %q", want)
		}
	}
	if !strings.Contains(last, ">9<") || strings.Contains(last, ">12<") {
		t.Error("touch=last must show the last-touch lead total (9), not 12")
	}
	if !strings.Contains(last, `value="last" checked`) || !strings.Contains(last, "3 leads before attribution was recorded") {
		t.Error("touch=last must be the chosen toggle value with its own unknown note")
	}
}
