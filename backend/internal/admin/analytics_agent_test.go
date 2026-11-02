package admin

import (
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/agents"
)

func TestAgentPage(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	fs.denied = []agents.Purpose{agents.PurposeUserFetch}
	rec := g.do("GET", "/admin/analytics/agents/gptbot", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	// The operator and purpose chips sit directly under the name, before the description.
	h1, openai, training, desc := strings.Index(body, "<h1>GPTBot</h1>"), strings.Index(body, ">OpenAI<"), strings.Index(body, ">Training<"), strings.Index(body, "Read pages to train AI models.")
	if !(h1 < openai && openai < training && training < desc) {
		t.Errorf("order h1 %d, OpenAI %d, Training %d, description %d: want chips between the name and the description", h1, openai, training, desc)
	}
	track := cardOf(t, body, "w-ag-track")
	for _, want := range []string{`data-aia-sd`, `aria-expanded="false"`, `aria-controls="w-ag-track-data"`, `id="w-ag-track-data"`, `hm-btn--sm`} {
		if !strings.Contains(track, want) {
			t.Errorf("activity tracker missing %q", want)
		}
	}
	for _, want := range []string{
		"<h1>GPTBot</h1>", "Read pages to train AI models.", ">OpenAI<", ">Training<",
		"Key figures", ">Fetches<", ">Verified<", ">Pages read<", ">Last seen<", ">17<", "0 spoofed, 0 unverifiable",
		"Fetches per day", "How often GPTBot read your site over the selected range.", `data-chart="daily"`, "&amp;id=gptbot",
		"Most fetched pages", "The pages this agent read most.", "Responses by status class", "How your site answered this agent.",
		"Verification", "Activity tracker", "Days this agent fetched pages.", "Active on 1 of 30 days.", "Robots policy",
		"The policy applies to the whole purpose. Save it from the bar at the bottom of the page.", "robots.txt name",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("agent page missing %q", want)
		}
	}
	last := -1
	for _, id := range []string{`id="w-ag-fetch"`, `id="w-ag-pages"`, `id="w-ag-resp"`, `id="w-ag-ver"`, `id="w-ag-track"`, `id="w-ag-pol"`} {
		i := strings.Index(body, id)
		if i < 0 || i < last {
			t.Errorf("%s is missing or out of order", id)
		}
		last = i
	}
	// status classes, in the prototype's words
	resp := cardOf(t, body, "w-ag-resp")
	for _, want := range []string{"2xx success", "3xx redirect", "4xx client error", "5xx server error", `2xx success <span class="hm-catbar__value">8</span><span class="hm-catbar__share">80%</span>`} {
		if !strings.Contains(resp, want) {
			t.Errorf("responses missing %q", want)
		}
	}
	if !strings.Contains(cardOf(t, body, "w-ag-pages"), `title="/"`) {
		t.Error("most fetched pages lists the path")
	}
	if !strings.Contains(cardOf(t, body, "w-ag-track"), `aria-label="GPTBot activity by day"`) || !strings.Contains(body, "Fetched") {
		t.Error("activity tracker is not named for the agent")
	}
	// the policy form: this purpose by radios, posted alone (scope=one)
	pol := cardOf(t, body, "w-ag-pol")
	for _, want := range []string{`action="/admin/analytics/crawlers/robots"`, `name="return" value="/admin/analytics/agents/gptbot"`, `name="purpose.training" value="allow" checked`,
		"Changes the policy for all 9 training agents, not only GPTBot.", `name="scope" value="one"`, `data-aia-allowed="training">`, `data-aia-blocked="training" hidden`,
		"data-aia-pending"} {
		if !strings.Contains(pol, want) {
			t.Errorf("policy card missing %q", want)
		}
	}
	if n := strings.Count(pol, `type="hidden" name="purpose.`); n != 0 {
		t.Errorf("hidden purposes = %d, want none: the form posts only its own purpose", n)
	}
	if n := strings.Count(pol, `type="radio"`); n != 2 {
		t.Errorf("radios = %d, want 2 (this purpose)", n)
	}
	if strings.Contains(body, "Ignores robots.txt") {
		t.Error("GPTBot honours robots.txt: no callout")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

// Saving from the agent page posts all eight purposes and comes back to the page.
func TestAgentPolicyFormSavesEveryPurpose(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	fs.denied = []agents.Purpose{agents.PurposeUserFetch}
	form := url.Values{"purpose.training": {"deny"}, "return": {"/admin/analytics/agents/gptbot?compare=1"}}
	for _, p := range robotsPurposes {
		if p != agents.PurposeTraining {
			form.Set("purpose."+string(p), map[bool]string{true: "deny", false: "allow"}[p == agents.PurposeUserFetch])
		}
	}
	rec := g.do("POST", "/admin/analytics/crawlers/robots", "owner-token", form, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/analytics/agents/gptbot?compare=1" {
		t.Fatalf("save: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(fs.denied) != 2 {
		t.Errorf("denied = %v, want training and user_fetch", fs.denied)
	}
}

func TestAgentPageIgnoringRobotsAndEditor(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	fs.denied = []agents.Purpose{agents.PurposeUserFetch}
	owner := g.do("GET", "/admin/analytics/agents/chatgpt-user", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Ignores robots.txt", "ChatGPT-User is known to fetch pages that robots.txt tells it to stay away from. Blocking it needs a firewall or server rule.", `data-aia-blocked="user_fetch">`} {
		if !strings.Contains(owner, want) {
			t.Errorf("chatgpt-user missing %q", want)
		}
	}
	// an editor sees the policy but cannot change it
	ed := g.do("GET", "/admin/analytics/agents/gptbot", "editor-token", nil, nil)
	body := ed.Body.String()
	if ed.Code != 200 {
		t.Fatalf("editor: %d", ed.Code)
	}
	if strings.Contains(body, `type="radio"`) || strings.Contains(body, `action="/admin/analytics/crawlers/robots"`) || strings.Contains(body, "data-aia-pending") {
		t.Error("an editor gets no policy controls")
	}
	if !strings.Contains(cardOf(t, body, "w-ag-pol"), "Allowed") {
		t.Error("an editor still sees the policy")
	}
}

// A robots.txt name that never fetches shows no figures, only its policy.
func TestAgentPagePolicyOnly(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	rec := g.do("GET", "/admin/analytics/agents/google-extended", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{"<h1>Google-Extended</h1>", "No fetches recorded. This is a robots.txt name only.", "hm-empty--regular", "Robots policy", "Changes the policy for the whole training purpose."} {
		if !strings.Contains(body, want) {
			t.Errorf("policy-only page missing %q", want)
		}
	}
	for _, bad := range []string{"hm-aia-kpis", "data-hm-aia-chart", "w-ag-pages", "Activity tracker"} {
		if strings.Contains(body, bad) {
			t.Errorf("policy-only page has %q", bad)
		}
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestAgentPageLoadFailureRendersTheFlash(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	fs.deniedErr = errors.New("database timeout")
	rec := g.do("GET", "/admin/analytics/agents/gptbot", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 500 || !strings.Contains(body, "This agent could not be loaded.") || !strings.Contains(body, "<h1>GPTBot</h1>") || !strings.Contains(body, `class="hm-subnav"`) {
		t.Fatalf("load failure: status %d, want the 500 shell with the flash and the title; body %.200q", rec.Code, body)
	}
}

func TestAgentPageNotFoundKeepsCrawlersCurrent(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	rec := g.do("GET", "/admin/analytics/agents/nope", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 404 || !strings.Contains(body, "Agent not found") || !strings.Contains(body, "That agent has no activity and is not in the registry.") || !strings.Contains(body, "Back to agents") {
		t.Fatalf("not found: %d", rec.Code)
	}
	i := strings.Index(body, `class="hm-subnav"`)
	if i < 0 {
		t.Fatal("the not-found page lost the Analytics sidebar")
	}
	m := regexp.MustCompile(`(?s)aria-current="page">.*?<span>([^<]*)</span>`).FindStringSubmatch(body[i:])
	if m == nil || m[1] != "Crawlers" {
		t.Errorf("current = %v, want Crawlers", m)
	}
}

func TestAgentPageCrumbs(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	body := g.do("GET", "/admin/analytics/agents/gptbot?from="+day(-6)+"&to="+day(0)+"&compare=1", "owner-token", nil, nil).Body.String()
	nav := body[strings.Index(body, `aria-label="Breadcrumb"`):]
	nav = nav[:strings.Index(nav, "</nav>")]
	last := -1
	for _, want := range []string{"Analytics", "Crawlers", "GPTBot"} {
		i := strings.Index(nav, ">"+want+"<")
		if i < 0 || i < last {
			t.Errorf("crumb %q missing or out of order in %s", want, nav)
		}
		last = i
	}
	// Going back to Crawlers keeps the range and compare setting, as the removed back link did.
	for _, want := range []string{"from=" + day(-6), "to=" + day(0), "compare=1"} {
		if !regexp.MustCompile(`href="/admin/analytics/crawlers\?[^"]*` + want).MatchString(nav) {
			t.Errorf("the Crawlers crumb lost %s: %s", want, nav)
		}
	}
}

// The operator and purpose beside the agent's name are labels, not statuses: plain tags in the
// title row, no status dot; Training carries the info tone.
func TestAgentPageChipsArePlainTags(t *testing.T) {
	g, _ := newAnalyticsRig(t)
	body := g.do("GET", "/admin/analytics/agents/gptbot", "owner-token", nil, nil).Body.String()
	i := strings.Index(body, `class="hm-pageheader__title hm-pageheader__title--chips"`)
	if i < 0 {
		t.Fatal("no chips beside the agent name")
	}
	chips := body[i : i+strings.Index(body[i:], "</div>")]
	if !strings.Contains(chips, "hm-badge--plain") || !strings.Contains(chips, "hm-badge--info") {
		t.Errorf("chips are not plain tags with an info Training: %s", chips)
	}
	if strings.Contains(body, `class="hm-pageheader__chips"`) {
		t.Error("the chips also render under the name")
	}
}

// "Not seen", "n/a" and a date are not metrics: each is a badge in the status slot, never a 30px value (this rig has no last fetch).
func TestAgentPageStatsKeepPhrasesOutOfMetricValues(t *testing.T) {
	g, _ := newAnalyticsRig(t)
	body := g.do("GET", "/admin/analytics/agents/gptbot", "owner-token", nil, nil).Body.String()
	for _, label := range []string{"Last seen"} {
		i := strings.Index(body, `<dt class="hm-statcard__label">`+label+`</dt>`)
		if i < 0 {
			t.Fatalf("no %q stat", label)
		}
		if rest := body[i:]; !strings.HasPrefix(rest, `<dt class="hm-statcard__label">`+label+`</dt><dd class="hm-statcard__status">`) {
			t.Errorf("%s is a metric value, not a status badge: %.200s", label, rest)
		}
	}
}

func TestAgentJSONCompareAddsPreviousPeriod(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	_, m := jsonOf(t, g, "/admin/analytics/data/agent.json?id=gptbot&compare=1&from="+day(-6)+"&to="+day(0))
	daily := m["charts"].(map[string]any)["daily"].(map[string]any)
	prev, ok := daily["previous"].(map[string]any)
	if !ok || len(prev["labels"].([]any)) != 7 {
		t.Errorf("daily previous = %v", daily["previous"])
	}
	_, m = jsonOf(t, g, "/admin/analytics/data/agent.json?id=gptbot&from="+day(-6)+"&to="+day(0))
	if _, has := m["charts"].(map[string]any)["daily"].(map[string]any)["previous"]; has {
		t.Error("no previous period without compare")
	}
}

// The agent page posts only its own purpose; the handler merges it into the
// stored policy, so a purpose changed elsewhere is not reverted.
func TestRobotsSaveSinglePurposeMergesIntoStoredPolicy(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	fs.denied = []agents.Purpose{agents.PurposeSearch}
	rec := g.do("POST", "/admin/analytics/crawlers/robots", "owner-token", url.Values{"scope": {"one"}, "purpose.training": {"deny"}}, nil)
	if rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	if len(fs.denied) != 2 || !slices.Contains(fs.denied, agents.PurposeSearch) || !slices.Contains(fs.denied, agents.PurposeTraining) {
		t.Errorf("denied = %v, want search kept and training added", fs.denied)
	}
	rec = g.do("POST", "/admin/analytics/crawlers/robots", "owner-token", url.Values{"scope": {"one"}, "purpose.search": {"allow"}}, nil)
	if rec.Code != 303 || len(fs.denied) != 1 || fs.denied[0] != agents.PurposeTraining {
		t.Errorf("unblock one: %d %v, want only training", rec.Code, fs.denied)
	}
}

func TestRobotsSaveRejectsMalformedPosts(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	fs.denied = []agents.Purpose{agents.PurposeSearch}
	full := func(mut func(url.Values)) url.Values {
		f := url.Values{}
		for _, p := range robotsPurposes {
			f.Set("purpose."+string(p), "allow")
		}
		mut(f)
		return f
	}
	for name, f := range map[string]url.Values{
		"full form missing a purpose": full(func(f url.Values) { f.Del("purpose.seo") }),
		"full form with Deny":         full(func(f url.Values) { f.Set("purpose.seo", "Deny") }),
		"full form with block":        full(func(f url.Values) { f.Set("purpose.seo", "block") }),
		"one: no purpose":             {"scope": {"one"}},
		"one: two purposes":           {"scope": {"one"}, "purpose.seo": {"deny"}, "purpose.training": {"deny"}},
		"one: unknown purpose":        {"scope": {"one"}, "purpose.bogus": {"deny"}},
		"one: bad value":              {"scope": {"one"}, "purpose.seo": {"yes"}},
		"unknown scope":               full(func(f url.Values) { f.Set("scope", "all") }),
	} {
		rec := g.do("POST", "/admin/analytics/crawlers/robots", "owner-token", f, nil)
		if rec.Code != 400 {
			t.Errorf("%s: %d, want 400", name, rec.Code)
		}
	}
	if len(fs.denied) != 1 || fs.denied[0] != agents.PurposeSearch {
		t.Errorf("a rejected post changed the policy: %v", fs.denied)
	}
}

// emptyBadge matches a badge with no label, with or without the default dot.
var emptyBadge = regexp.MustCompile(`class="hm-badge[^"]*">(<span class="hm-badge__dot"[^>]*></span>)?</span>`)

// An agent outside the registry whose rows all lie outside the range has no
// purpose to show or to set a policy for.
func TestAgentPageWithoutPurposeHasNoBadgeOrPolicy(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	rec := g.do("GET", "/admin/analytics/agents/ghost", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("ghost: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, bad := range []string{`name="purpose."`, "all 0", `data-aia-policy`, `w-ag-pol`} {
		if strings.Contains(body, bad) {
			t.Errorf("page contains %q", bad)
		}
	}
	if emptyBadge.MatchString(body) {
		t.Error("the page renders an empty badge")
	}
}
