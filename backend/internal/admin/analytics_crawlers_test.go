package admin

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/middleware"
)

// groupBody is the markup of one purpose's tbody on the Crawlers page.
func groupBody(t *testing.T, body, purpose string) string {
	t.Helper()
	i := strings.Index(body, `<h4 class="hm-table__group-title">`+purpose+`</h4>`)
	if i < 0 {
		t.Fatalf("no group %q", purpose)
	}
	rest := body[i:]
	return rest[:strings.Index(rest, "</tbody>")]
}

func TestCrawlersSection(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	fs.denied = []agents.Purpose{agents.PurposeUserFetch}
	rec := g.do("GET", "/admin/analytics/crawlers", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{
		"2 of 19 known AI agents fetched your site in the last 30 days. Allow or block a whole purpose at the top of each group.",
		"AI fetches by purpose", "Stacked by what the agent reads pages for.", `&amp;group=purpose`, "Agents by purpose",
		"Allow or block a purpose, then save once. Select an agent name for details. Only the site owner can change the robots policy.",
		"Other crawlers", "Sets the policy for the whole purpose.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("crawlers missing %q", want)
		}
	}
	// group order, then Other crawlers; policy-only tokens under Training
	last := -1
	for _, h := range []string{"AI fetches by purpose", ">Training</h4>", ">Search index</h4>", ">User fetch</h4>", ">Other AI</h4>", ">Other crawlers</h4>"} {
		i := strings.Index(body, h)
		if i < 0 || i < last {
			t.Errorf("%q is missing or out of order", h)
		}
		last = i
	}
	training := groupBody(t, body, "Training")
	for _, want := range []string{"900 fetches, 73% of all AI fetches.", "GPTBot", "Google-Extended", "Applebot-Extended", "Policy only. robots.txt name only, no fetches recorded.", "ClaudeBot", "No fetches"} {
		if !strings.Contains(training, want) {
			t.Errorf("Training group missing %q", want)
		}
	}
	// Blocked user fetch: its agents carry the badge, Training's do not show it
	uf := groupBody(t, body, "User fetch")
	if !strings.Contains(uf, `data-aia-blocked="user_fetch">`) || strings.Contains(uf, `data-aia-blocked="user_fetch" hidden`) {
		t.Error("a blocked purpose shows Blocked by policy on its agents")
	}
	if !strings.Contains(uf, `data-aia-ignorer="user_fetch"`) {
		t.Error("ChatGPT-User ignores robots.txt and fetched: it counts as an ignorer")
	}
	if !strings.Contains(training, `data-aia-blocked="training" hidden`) {
		t.Error("an allowed purpose keeps the badge hidden for policy.js")
	}
	// owner: a radio pair for each of the eight purposes, one form, a save bar
	if n := strings.Count(body, `type="radio" name="purpose.`); n != 16 {
		t.Errorf("radios = %d, want 16 (8 purposes)", n)
	}
	for _, want := range []string{`name="purpose.user_fetch" value="deny" checked`, `name="purpose.training" value="allow" checked`, `name="purpose.archive"`,
		`action="/admin/analytics/crawlers/robots"`, `name="return" value="/admin/analytics/crawlers"`, `data-aia-pending`, `/js/aia.`} {
		if !strings.Contains(body, want) {
			t.Errorf("owner form missing %q", want)
		}
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestCrawlersPolicyReadOnlyForEditors(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	fs.denied = []agents.Purpose{agents.PurposeTraining}
	body := g.do("GET", "/admin/analytics/crawlers", "editor-token", nil, nil).Body.String()
	for _, bad := range []string{`type="radio"`, `<form class="hm-aia-policyform"`, "data-aia-pending", "<span>Save</span>"} {
		if strings.Contains(body, bad) {
			t.Errorf("an editor sees %q", bad)
		}
	}
	if !strings.Contains(groupBody(t, body, "Training"), ">Blocked<") {
		t.Error("an editor sees the policy as a badge")
	}
}

// The form comes back to the page it was on with the range kept.
func TestCrawlersFormKeepsTheRange(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	body := g.do("GET", "/admin/analytics/crawlers?from="+day(-6)+"&to="+day(0)+"&compare=1", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `name="return" value="/admin/analytics/crawlers?compare=1&amp;from=`+day(-6)+`&amp;to=`+day(0)+`"`) {
		t.Error("return keeps from, to and compare")
	}
	if !strings.Contains(body, "in the last 7 days") {
		t.Error("lede uses the range words")
	}
}

func TestVerificationBadge(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		ag   agentJSON
		want string
		kind string
	}{
		"none":         {agentJSON{}, "No fetches", ""},
		"spoofed":      {agentJSON{Hits: 100, Spoofed: 13, Verified: 50}, "13 spoofed", "danger"},
		"unverifiable": {agentJSON{Hits: 10, Unverifiable: 10}, "Unverifiable", ""},
		"all":          {agentJSON{Hits: 10, Verified: 10}, "All verified", "success"},
		"some":         {agentJSON{Hits: 10, Verified: 7, Unverifiable: 3}, "70% verified", ""},
	} {
		if b := verificationBadge(tc.ag); b.Label != tc.want || b.Kind != tc.kind {
			t.Errorf("%s: %+v", name, b)
		}
	}
}

func TestPurposeID(t *testing.T) {
	t.Parallel()

	for id := range purposeLabels {
		if got := purposeID(purposeLabel(id)); got != id {
			t.Errorf("purposeID(%q) = %q", purposeLabel(id), got)
		}
	}
}

// The purpose chart keeps the prototype's series order, and so its colours,
// whatever each purpose's total is: every AI purpose is a series, then Other.
func TestPurposeSeriesOrderIsFixed(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	fs.series = []agents.SeriesPoint{
		{Day: day(0), Key: "user_fetch", Hits: 90}, {Day: day(0), Key: "other_ai", Hits: 50},
		{Day: day(0), Key: "training", Hits: 3}, {Day: day(0), Key: "seo", Hits: 1},
	}
	_, m := jsonOf(t, g, "/admin/analytics/data/overview.json?group=purpose&from="+day(-6)+"&to="+day(0))
	var got []string
	for _, d := range m["charts"].(map[string]any)["fetches"].(map[string]any)["datasets"].([]any) {
		got = append(got, d.(map[string]any)["label"].(string))
	}
	if want := "Training,Search index,User fetch,Other AI,Other"; strings.Join(got, ",") != want {
		t.Errorf("series = %v, want %s", got, want)
	}
	fs.series = nil
	_, m = jsonOf(t, g, "/admin/analytics/data/overview.json?group=purpose&from="+day(-6)+"&to="+day(0))
	if n := len(m["charts"].(map[string]any)["fetches"].(map[string]any)["datasets"].([]any)); n != 4 {
		t.Errorf("with no fetches there are still %d series, no Other", n)
	}
}

func TestCrawlersVerifyBanner(t *testing.T) {
	t.Parallel()

	red := agents.VerificationHealth{Verified: 500, Spoofed: 1629 - 500 + 71, Unverifiable: 9, SpoofedIPs: 380, SpoofedIPsInCDN: 380}
	cases := []struct {
		name    string
		verify  agents.VerificationHealth
		err     error
		want    []string
		without []string
	}{
		{"red", red, nil, []string{
			"Verification looks misconfigured.", "of fetches from agents with published IP ranges failed. 1,200 spoofed fetches from 380 IPs (",
			`>see them</a>`, "Client IPs are read from the connection, or X-Forwarded-For from a loopback proxy.",
			"Most of these IPs belong to Cloudflare: set HATAMOTO_TRUSTED_CDN=cloudflare.", "hm-callout--danger", `href="/admin/analytics/health?compare=1&amp;from=2026-01-01&amp;to=2026-01-03#w-h-ver"`,
		}, []string{"Verification is healthy."}},
		{"red, not cloud", agents.VerificationHealth{Verified: 10, Spoofed: 40, SpoofedIPs: 5, SpoofedIPsInCDN: 1}, nil,
			[]string{"80% of fetches", "Set HATAMOTO_TRUSTED_PROXIES (or the CDN preset)"}, []string{"HATAMOTO_TRUSTED_CDN=cloudflare"}},
		{"green", agents.VerificationHealth{Verified: 987, Spoofed: 13, SpoofedIPs: 3, SpoofedIPsInCDN: 3}, nil, []string{
			"Verification is healthy.", "98.7% of verifiable fetches matched; 13 spoofed fetches came from 3 cloud IPs (", `>see them</a>`, "hm-callout--success",
		}, []string{"misconfigured"}},
		{"green without spoofed", agents.VerificationHealth{Verified: 100}, nil, []string{"100.0% of verifiable fetches matched."}, []string{"see them"}},
		{"too few decided fetches", agents.VerificationHealth{Verified: 3, Spoofed: 10}, nil, nil, []string{"Verification is healthy.", "misconfigured"}},
		{"read fails", agents.VerificationHealth{}, errors.New("boom"), nil, []string{"Verification is healthy.", "misconfigured"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			g, fs := newAnalyticsRig(t)
			fs.verify, fs.verifyErr = &c.verify, c.err
			rec := g.do("GET", "/admin/analytics/crawlers?from=2026-01-01&to=2026-01-03&compare=1", "owner-token", nil, nil)
			if rec.Code != 200 {
				t.Fatalf("status %d", rec.Code)
			}
			body := rec.Body.String()
			for _, w := range c.want {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q", w)
				}
			}
			for _, w := range c.without {
				if strings.Contains(body, w) {
					t.Errorf("unexpected %q", w)
				}
			}
			if len(c.want) > 0 {
				if b, h := strings.Index(body, `id="w-cr-verify"`), strings.Index(body, `id="w-cr-hero"`); b < 0 || h < b {
					t.Errorf("banner at %d must come before the hero chart at %d", b, h)
				}
			}
			if want := agents.RangePublishedIDs(agents.Default()); !slices.Equal(fs.verifyIDs, want) {
				t.Errorf("asked for %v, want the range-published agents %v", fs.verifyIDs, want)
			}
		})
	}
}

func TestCrawlersVerifyBannerUnderACustomAdminPath(t *testing.T) {
	t.Parallel()

	fs := &fakeAgentStats{}
	g := newRig(t, func(d *Deps) { d.AdminPath, d.Growth.Agents, d.Growth.Referrals = "/x-test", fs, fakeReferrals{} })
	body := g.do("GET", "/x-test/analytics/crawlers", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `href="/x-test/analytics/health`) || !strings.Contains(body, "#w-h-ver") || strings.Contains(body, `href="/admin/analytics/health`) {
		t.Error("the see-them link must sit under the custom admin path")
	}
}

func TestVerifyBannerHeaderWording(t *testing.T) {
	t.Parallel()

	bad := agents.VerificationHealth{Verified: 10, Spoofed: 90, SpoofedIPs: 4, SpoofedIPsInCDN: 4}
	text := func(src middleware.ClientIPSource, h agents.VerificationHealth) string {
		b := verifyBannerFor(h, src, "/x")
		return b.Lead + b.Tail
	}
	off := middleware.ClientIPSource{Header: "X-Forwarded-For"}
	on := middleware.ClientIPSource{Header: "CF-Connecting-IP", CDN: true}
	got := text(off, bad)
	if !strings.Contains(got, "read from the connection, or X-Forwarded-For from a loopback proxy.") || !strings.Contains(got, "set HATAMOTO_TRUSTED_CDN=cloudflare") {
		t.Errorf("preset off: %q", got)
	}
	got = text(on, bad)
	if !strings.Contains(got, "read from CF-Connecting-IP behind your trusted proxy.") || strings.Contains(got, "set HATAMOTO_TRUSTED_CDN") || !strings.Contains(got, "until they leave the selected range") {
		t.Errorf("preset on: %q", got)
	}
	// Preset on but the spoofed IPs are not CDN addresses: the proxy is the fault, not the age of the data.
	got = text(on, agents.VerificationHealth{Verified: 10, Spoofed: 90, SpoofedIPs: 4, SpoofedIPsInCDN: 1})
	if strings.Contains(got, "until they leave the selected range") || !strings.Contains(got, "Set HATAMOTO_TRUSTED_PROXIES") {
		t.Errorf("preset on, IPs outside the CDN: %q", got)
	}
	// Rollup kept, raw hits gone: no "from 0 IPs", no link to an empty list.
	for _, h := range []agents.VerificationHealth{{Verified: 10, Spoofed: 90}, {Verified: 98, Spoofed: 2}} {
		b := verifyBannerFor(h, off, "/x")
		if got := b.Lead + b.Tail; strings.Contains(got, "0 IP") || strings.Contains(got, "()") || b.Href != "" || !strings.Contains(got, "spoofed fetches") {
			t.Errorf("no raw IPs left: %q href=%q", got, b.Href)
		}
	}
	got = text(middleware.ClientIPSource{Header: "X-Forwarded-For", ProxiesConfigured: true}, agents.VerificationHealth{Verified: 10, Spoofed: 90, SpoofedIPs: 1})
	if !strings.Contains(got, "from 1 IP (") || !strings.Contains(got, "behind your trusted proxies.") || !strings.Contains(got, "Set HATAMOTO_TRUSTED_PROXIES") {
		t.Errorf("proxies configured: %q", got)
	}
	got = text(on, agents.VerificationHealth{Verified: 98, Spoofed: 2, SpoofedIPs: 1, SpoofedIPsInCDN: 1})
	if !strings.Contains(got, "read from CF-Connecting-IP behind your trusted proxy. 98.0% of verifiable") || !strings.Contains(got, "from 1 cloud IP (") {
		t.Errorf("green: %q", got)
	}
	if verifyBannerFor(agents.VerificationHealth{Verified: 5}, on, "/x") != nil {
		t.Error("too few decided fetches must show no banner")
	}
}
