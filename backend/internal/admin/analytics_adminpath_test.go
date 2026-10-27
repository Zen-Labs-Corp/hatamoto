package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/agents"
)

// The analytics pages follow a non-default admin prefix: no /admin segment in
// any body, the agent page's crumbs, the pager links and the policy form's
// return path all sit under it, and a save lands back under it.
func TestAnalyticsUnderACustomAdminPath(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) {
		d.AdminPath = "/x-test"
		d.Growth.Agents, d.Growth.Referrals = &fakeAgentStats{series: []agents.SeriesPoint{{Day: day(0), Key: "OpenAI", Hits: 6}}}, fakeReferrals{}
	})

	for _, p := range []string{"", "/crawlers", "/content", "/referrals", "/health?cpage=1&fpage=1", "/agents/gptbot"} {
		path := "/x-test/analytics" + p
		rec := g.do("GET", path, "owner-token", nil, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d", path, rec.Code)
			continue
		}
		body := rec.Body.String()
		if m := adminSegment.FindStringIndex(body); m != nil {
			t.Errorf("GET %s still names /admin: ...%s...", path, body[max(m[0]-60, 0):min(m[1]+60, len(body))])
		}
		if !strings.Contains(body, `href="/x-test/analytics`) {
			t.Errorf("GET %s: no /x-test/analytics link", path)
		}
		if p == "/crawlers" {
			for _, want := range []string{`action="/x-test/analytics/crawlers/robots"`, `name="return" value="/x-test/analytics/crawlers"`} {
				if !strings.Contains(body, want) {
					t.Errorf("crawlers form lacks %q", want)
				}
			}
		}
		if p == "/agents/gptbot" && !strings.Contains(body, `<li><a href="/x-test/analytics/crawlers">Crawlers</a></li>`) {
			t.Error("agent page: Crawlers crumb is not under /x-test")
		}
	}

	form := url.Values{"return": {"/x-test/analytics/agents/gptbot?compare=1"}}
	for _, p := range robotsPurposes {
		form.Set("purpose."+string(p), "allow")
	}
	rec := g.do("POST", "/x-test/analytics/crawlers/robots", "owner-token", form, nil)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || loc != "/x-test/analytics/agents/gptbot?compare=1" {
		t.Errorf("policy save: %d Location %q", rec.Code, loc)
	}
	form.Set("return", "/admin/analytics/crawlers") // the default prefix is not this instance's
	rec = g.do("POST", "/x-test/analytics/crawlers/robots", "owner-token", form, nil)
	if loc := rec.Header().Get("Location"); loc != "/x-test/analytics/crawlers" {
		t.Errorf("foreign return path: Location %q, want the crawlers list", loc)
	}
}
