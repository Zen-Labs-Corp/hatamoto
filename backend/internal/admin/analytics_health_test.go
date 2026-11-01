package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/agents"
)

func TestHealthSection(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	fs.denied = []agents.Purpose{agents.PurposeUserFetch}
	rec := g.do("GET", "/admin/analytics/health", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{
		"Is it running cleanly?", "Whether crawlers are who they say they are and whether your site answers them cleanly.",
		"Verified and spoofed fetches", "Each fetch is checked against the operator\u2019s published method.",
		"Verification", "Fetches by verification result.", "Spoofed fetches by claimed agent", "Responses", "Status classes served to AI crawlers.",
		"Failing paths", "Pages that answered AI crawlers with an error.", "Robots.txt compliance",
		"Whether each agent honours robots.txt, and the policy you have set for its purpose.",
		`data-panels="verified|spoofed"`, `data-panel-labels="Verified fetches per day|Spoofed fetches per day"`, `&#34;cls&#34;:&#34;hm-viz--danger&#34;`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("health missing %q", want)
		}
	}
	last := -1
	for _, id := range []string{`id="w-h-hero"`, `id="w-h-ver"`, `id="w-h-stat"`, `id="w-h-fail"`, `id="w-h-rob"`} {
		i := strings.Index(body, id)
		if i < 0 || i < last {
			t.Errorf("%s is missing or out of order", id)
		}
		last = i
	}
	// the data behind the hero is in the page, verified and spoofed apart
	hero := cardOf(t, body, "w-h-hero")
	for _, want := range []string{">Verified</th>", ">Spoofed</th>"} {
		if !strings.Contains(hero, want) {
			t.Errorf("hero table missing %q", want)
		}
	}
	// Verification: three results in ink, a light step and red, then who was spoofed
	ver := cardOf(t, body, "w-h-ver")
	for _, want := range []string{`Verified <span class="hm-catbar__value">30</span><span class="hm-catbar__share">75%</span>`, "Unverifiable", "hm-viz--series-4", `Spoofed <span class="hm-catbar__value">5</span><span class="hm-catbar__share">12%</span>`, "hm-viz--danger"} {
		if !strings.Contains(ver, want) {
			t.Errorf("verification missing %q", want)
		}
	}
	// Responses: status classes and the tracker over days, with its numbers
	resp := cardOf(t, body, "w-h-stat")
	for _, want := range []string{"2xx success", "3xx redirect", "4xx client error", "5xx server error", "Errors by day", `aria-label="Days with crawler errors"`,
		"data-hm-aia-tracker", "hm-tracker--states", "hm-catbar__legend--cols", "fetches failed (", "Show data", ">Failed fetches</th>", ">All fetches</th>"} {
		if !strings.Contains(resp, want) {
			t.Errorf("responses missing %q", want)
		}
	}
	// Failing paths: a redirect link on a missing page only, with the status badge and agent count
	fail := cardOf(t, body, "w-h-fail")
	for _, want := range []string{`<code class="hm-aia-mono">/gone</code>`, `<code class="hm-aia-mono">/boom</code>`, ">404<", ">500<", "/admin/settings/redirects/new?source=%2Fgone"} {
		if !strings.Contains(fail, want) {
			t.Errorf("failing paths missing %q", want)
		}
	}
	if !strings.Contains(fail, `class="hm-btn hm-btn--secondary hm-btn--sm" href="/admin/settings/redirects/new?source=%2Fgone"`) {
		t.Errorf("Create redirect must be a small secondary button:\n%s", fail)
	}
	for _, want := range []string{`data-aia-sd`, `aria-expanded="false"`, `aria-controls="w-h-trk-data"`, `id="w-h-trk-data"`} {
		if !strings.Contains(resp, want) {
			t.Errorf("tracker Show data missing %q", want)
		}
	}
	if n := strings.Count(fail, ">Create redirect<"); n != 1 {
		t.Errorf("Create redirect links = %d, want 1 (the 404 only)", n)
	}
	// The figures are right-aligned and the action header is read by screen readers only.
	for _, want := range []string{`<th scope="col" class="hm-table__right">Fetches</th>`, `<th scope="col" class="hm-table__right">Agents</th>`, `<th scope="col"><span class="hm-visually-hidden">Action</span></th>`, `data-label="Fetches" class="hm-table__right"`} {
		if !strings.Contains(fail, want) {
			t.Errorf("failing paths table missing %q", want)
		}
	}
	// Compliance: agents that ignore robots.txt first, shared rows partial, no "Show all" fold
	rob := cardOf(t, body, "w-h-rob")
	for _, want := range []string{">ChatGPT-User<", `data-label="Honours robots.txt"`, `data-label="Policy"`, ">Blocked<", ">Allowed<", `<th scope="col" class="hm-table__right">Fetches</th>`,
		"Change policy", "/admin/analytics/crawlers#w-cr-list"} {
		if !strings.Contains(rob, want) {
			t.Errorf("compliance missing %q", want)
		}
	}
	for _, gone := range []string{"Show all", "data-aia-extra", "data-aia-showall", "data-aia-more"} {
		if strings.Contains(rob, gone) {
			t.Errorf("compliance still carries the old fold %q", gone)
		}
	}
	if i, j := strings.Index(rob, ">ChatGPT-User<"), strings.Index(rob, ">GPTBot<"); i < 0 || j < 0 || i > j {
		t.Error("an agent ignoring robots.txt comes before one that honours it")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestTrackerKind(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		failed, total int64
		want          string
	}{{0, 0, "neutral"}, {0, 100, healthyDay}, {5, 100, healthyDay}, {6, 100, "warning"}, {10, 100, "warning"}, {11, 100, "danger"}, {100, 100, "danger"}} {
		if got := trackerKind(c.failed, c.total); got != c.want {
			t.Errorf("trackerKind(%d, %d) = %s, want %s", c.failed, c.total, got, c.want)
		}
	}
	if got := failingText(3, 50); got != "3 of 50 fetches failed (6.0%)" {
		t.Errorf("failingText = %q", got)
	}
	if got := failingText(0, 0); got != "No fetches" {
		t.Errorf("failingText with no fetches = %q", got)
	}
}

func TestHealthOverviewJSONHasPanelSeries(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	_, m := jsonOf(t, g, "/admin/analytics/data/overview.json?compare=1&from="+day(-6)+"&to="+day(0))
	charts := m["charts"].(map[string]any)
	for key, label := range map[string]string{"verified": "Verified", "spoofed": "Spoofed"} {
		c := charts[key].(map[string]any)
		ds := c["datasets"].([]any)
		if len(ds) != 1 || ds[0].(map[string]any)["label"] != label {
			t.Errorf("%s datasets = %v", key, ds)
		}
		prev := c["previous"].(map[string]any)["datasets"].([]any)
		if len(prev) != 1 || prev[0].(map[string]any)["label"] != label {
			t.Errorf("%s previous datasets = %v", key, prev)
		}
	}
}
