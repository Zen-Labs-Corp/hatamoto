package localeconv

import "testing"

// TestPlanSHA256Golden pins the plan hash byte for byte: it is shown in the
// review and compared on apply, so a change of the encoding would invalidate
// every report in flight.
func TestPlanSHA256Golden(t *testing.T) {
	yes := true
	cases := []struct {
		name string
		plan *Plan
		want string
	}{
		{"minimal nil slices", &Plan{Version: 1, RootSchema: "page"}, "885de6caea24888087d70ba117d824c4f51d2bbe4ee77db6cdb279bf265ffcb3"},
		{"map keys sorted", &Plan{
			Version:         1,
			Locales:         []LocaleSpec{{Tag: "en", Segment: "en", RootSlug: "en"}, {Tag: "de-AT", Segment: "aut", RootSlug: "aut"}},
			RootSchema:      "page",
			Default:         "en",
			XDefault:        "en",
			PrefixAll:       &yes,
			LocaleFrom:      LocaleFrom{Attribute: "lang", Map: map[string]string{"zz": "nl", "b": "fr", "a": "en", "m": "es"}, Fallback: "en"},
			GroupFrom:       &GroupFrom{Attribute: "group"},
			Shared:          "shared",
			Home:            HomeSpec{Slug: "home", OnConflict: "suffix"},
			StripAttributes: []string{"lang", "group"},
			SitemapNaming:   "group",
		}, "fce1374c4fee5c2e3998983526999609c70d4937d5ada5d046fd5b762d98c438"},
		{"html and line separators escaped", &Plan{
			Version:    2,
			RootSchema: "a<b>&c",
			Default:    "x\u2028y\u2029z",
			LocaleFrom: LocaleFrom{Attribute: "<lang>", Map: map[string]string{"<k>": "&v"}},
			Home:       HomeSpec{Slug: "h&m"},
		}, "e20b3a747b5943d9c29fe6c087e536ce4b308921dffb734d66f6134af5f37ab6"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PlanSHA256(c.plan); got != c.want {
				t.Errorf("PlanSHA256 = %q, want %q", got, c.want)
			}
		})
	}
}
