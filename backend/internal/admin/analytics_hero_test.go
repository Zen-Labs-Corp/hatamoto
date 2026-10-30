package admin

import (
	"net/url"
	"testing"
)

// The Crawlers hero chart loads for every picker preset, with and without the
// previous-period comparison, including the ranges long enough to be bucketed
// by week.
func TestCrawlersHeroDataForEveryPreset(t *testing.T) {
	t.Parallel()

	g, _ := newAnalyticsRig(t)
	withPrevious := 0
	for _, p := range rangePresets(today()) {
		for _, compare := range []bool{false, true} {
			q := url.Values{"group": {"purpose"}, "from": {p.From.Format(dayLayout)}, "to": {p.To.Format(dayLayout)}}
			if compare {
				q.Set("compare", "1")
			}
			name := p.Label + " " + q.Encode()
			code, m := jsonOf(t, g, "/admin/analytics/data/overview.json?"+q.Encode())
			if code != 200 {
				t.Errorf("%s: status %d %v", name, code, m)
				continue
			}
			fetches, ok := m["charts"].(map[string]any)["fetches"].(map[string]any)
			if !ok {
				t.Errorf("%s: no fetches chart", name)
				continue
			}
			labels, _ := fetches["labels"].([]any)
			if len(labels) == 0 {
				t.Errorf("%s: no labels", name)
			}
			datasets, ok := fetches["datasets"].([]any)
			if !ok || datasets == nil {
				t.Errorf("%s: datasets must be an array, got %v", name, fetches["datasets"])
			}
			for _, d := range datasets {
				if n := len(d.(map[string]any)["data"].([]any)); n != len(labels) {
					t.Errorf("%s: a dataset has %d points for %d labels", name, n, len(labels))
				}
			}
			prev, hasPrev := fetches["previous"].(map[string]any)
			if !compare {
				if hasPrev {
					t.Errorf("%s: previous period without compare", name)
				}
				continue
			}
			if !hasPrev {
				continue // no earlier data to compare with: the picker disables compare
			}
			withPrevious++
			if pl, _ := prev["labels"].([]any); len(pl) != len(labels) {
				t.Errorf("%s: previous has %d labels, current %d", name, len(pl), len(labels))
			}
			pd, ok := prev["datasets"].([]any)
			if !ok || pd == nil {
				t.Errorf("%s: previous datasets must be an array", name)
			}
		}
	}
	if withPrevious == 0 {
		t.Error("no preset produced a previous period: the compare path is untested")
	}
}
