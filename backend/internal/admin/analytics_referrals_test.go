package admin

import (
	"context"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/analytics"
)

// keyedReferrals answers with the lower-case assistant keys the rollup stores.
type keyedReferrals struct{ fakeReferrals }

func (keyedReferrals) AIReferrals(context.Context, time.Time, time.Time) ([]analytics.ReferralDay, error) {
	return []analytics.ReferralDay{{Day: day(-1), Assistant: "chatgpt", Visits: 6}, {Day: day(0), Assistant: "meta_ai", Visits: 2}, {Day: day(0), Assistant: "gemini", Visits: 2}}, nil
}

func TestReferralsSection(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	rec := g.do("GET", "/admin/analytics/referrals", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{
		"What comes back?", "Visits from people who clicked a link inside an AI assistant. Counted per day; no visitors are tracked.",
		"AI-referred visits by assistant", "Daily visits, stacked by assistant. The five largest are named, the rest are grouped as Other.",
		"By assistant", "Share of AI-referred visits.", "AI share of traffic", "AI-referred visits as a share of all pageviews.",
		"Landing pages from AI assistants are not recorded yet. Referrals are counted per day per assistant.",
		`data-chart="referrals"`, `data-ratio="referrals,pageviews"`, `data-ratio-label="AI share of traffic"`, `data-override="{&#34;AI share of traffic&#34;:{&#34;n&#34;:3}}"`, " data-hero",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("referrals missing %q", want)
		}
	}
	last := -1
	for _, id := range []string{`id="w-rf-hero"`, `id="w-rf-by"`, `id="w-rf-share"`, "Landing pages from AI"} {
		i := strings.Index(body, id)
		if i < 0 || i < last {
			t.Errorf("%s is missing or out of order", id)
		}
		last = i
	}
	by := cardOf(t, body, "w-rf-by")
	for _, want := range []string{`<span class="hm-barlist__value">9</span>`, `<span class="hm-barlist__share">75%</span>`, "ChatGPT", "Perplexity"} {
		if !strings.Contains(by, want) {
			t.Errorf("by assistant missing %q", want)
		}
	}
	// both sides of the ratio are in the page as a table, not added into one column
	share := cardOf(t, body, "w-rf-share")
	for _, want := range []string{">AI-referred visits</th>", ">Pageviews</th>"} {
		if !strings.Contains(share, want) {
			t.Errorf("share table missing %q", want)
		}
	}
	if strings.Contains(share, `scope="col">Total</th>`) {
		t.Error("the share table must not add visits to pageviews")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestReferralsAssistantKeysShowTheirNames(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) { d.Growth.Agents, d.Growth.Referrals = &fakeAgentStats{}, keyedReferrals{} })
	body := g.do("GET", "/admin/analytics/referrals", "owner-token", nil, nil).Body.String()
	by := cardOf(t, body, "w-rf-by")
	for _, want := range []string{"ChatGPT", "Meta AI", "Gemini"} {
		if !strings.Contains(by, want) {
			t.Errorf("assistant list missing %q", want)
		}
	}
	if strings.Contains(body, "meta_ai") || strings.Contains(body, ">chatgpt<") {
		t.Error("an assistant key leaked into the page")
	}
	_, m := jsonOf(t, g, "/admin/analytics/data/referrals.json?from="+day(-6)+"&to="+day(0))
	var labels []string
	for _, d := range m["charts"].(map[string]any)["referrals"].(map[string]any)["datasets"].([]any) {
		labels = append(labels, d.(map[string]any)["label"].(string))
	}
	if got := strings.Join(labels, ","); got != "ChatGPT,Gemini,Meta AI" {
		t.Errorf("chart series = %s", got)
	}
}
