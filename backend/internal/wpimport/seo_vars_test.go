package wpimport

import (
	"reflect"
	"testing"

	"hatamoto.dev/core/internal/seo"
)

func TestSEOVarsTranslate(t *testing.T) {
	tests := []struct {
		name, plugin, in, want string
		notMapped, lossy       []string
	}{
		{name: "yoast basic", plugin: "yoast", in: "%%title%% %%sep%% %%sitename%%", want: "%title% %sep% %sitename%"},
		{name: "yoast tagline", plugin: "yoast", in: "%%sitename%% %%sep%% %%sitedesc%%", want: "%sitename% %sep% %tagline%"},
		{name: "yoast excerpt", plugin: "yoast", in: "%%excerpt%%", want: "%excerpt%"},
		{name: "yoast author", plugin: "yoast", in: "%%name%%, %%sitename%%", want: "%author%, %sitename%"},
		{name: "yoast year", plugin: "yoast", in: "(c) %%currentyear%% %%sitename%%", want: "(c) %currentyear% %sitename%"},
		{name: "yoast page lossy", plugin: "yoast", in: "%%title%% %%page%%", want: "%title% %page%", lossy: []string{"%%page%%"}},
		{name: "yoast pagetotal", plugin: "yoast", in: "%%pagenumber%% of %%pagetotal%%", want: "%page% of %pagetotal%", lossy: []string{"%%pagenumber%%"}},
		{name: "yoast modified lossy", plugin: "yoast", in: "%%modified%%", want: "%date%", lossy: []string{"%%modified%%"}},
		{name: "yoast term", plugin: "yoast", in: "%%term_title%% archives", want: "%term% archives"},
		{name: "yoast archive", plugin: "yoast", in: "%%archive_title%%", want: "%archive%"},
		{name: "yoast pt_plural lossy", plugin: "yoast", in: "%%pt_plural%% %%sep%% %%sitename%%", want: "%archive% %sep% %sitename%", lossy: []string{"%%pt_plural%%"}},
		{name: "yoast search", plugin: "yoast", in: "Search: %%searchphrase%%", want: "Search: %searchphrase%"},
		{name: "yoast unmapped dropped", plugin: "yoast", in: "%%title%% %%focuskw%% %%sep%%", want: "%title% %sep%", notMapped: []string{"%%focuskw%%"}},
		{name: "yoast unmapped middle", plugin: "yoast", in: "%%title%%  %%cf_color%%  %%sitename%%", want: "%title% %sitename%", notMapped: []string{"%%cf_color%%"}},
		{name: "yoast dangling sep kept", plugin: "yoast", in: "%%sep%% %%primary_category%%", want: "%sep%", notMapped: []string{"%%primary_category%%"}},
		{name: "yoast unknown only", plugin: "yoast", in: "%%focuskw%% %%id%%", want: "", notMapped: []string{"%%focuskw%%", "%%id%%"}},
		{name: "yoast dedup", plugin: "yoast", in: "%%id%% x %%id%%", want: "x", notMapped: []string{"%%id%%"}},
		{name: "yoast empty", plugin: "yoast", in: "", want: ""},
		{name: "yoast whitespace", plugin: "yoast", in: "   ", want: ""},
		{name: "yoast plain text", plugin: "yoast", in: "Welcome to my site", want: "Welcome to my site"},
		{name: "yoast literal percent", plugin: "yoast", in: "100% %%title%%", want: "100% %title%"},
		{name: "rankmath basic", plugin: "rankmath", in: "%title% %sep% %sitename% %customfield(x)%", want: "%title% %sep% %sitename%", notMapped: []string{"%customfield(x)%"}},
		{name: "rankmath site_title", plugin: "rankmath", in: "%site_title%", want: "%sitename%"},
		{name: "rankmath year", plugin: "rankmath", in: "%title% %year%", want: "%title% %currentyear%"},
		{name: "rankmath page", plugin: "rankmath", in: "%title% %page%", want: "%title% %page%", lossy: []string{"%page%"}},
		{name: "rankmath category lossy", plugin: "rankmath", in: "%category% %sep% %sitename%", want: "%term% %sep% %sitename%", lossy: []string{"%category%"}},
		{name: "rankmath search", plugin: "rankmath", in: "%search_query%", want: "%searchphrase%"},
		{name: "rankmath unmapped", plugin: "rankmath", in: "%title% %focuskw% %org_name%", want: "%title%", notMapped: []string{"%focuskw%", "%org_name%"}},
		{name: "rankmath count arg", plugin: "rankmath", in: "%title% %count(x)%", want: "%title%", notMapped: []string{"%count(x)%"}},
		{name: "rankmath excerpt", plugin: "rankmath", in: "%excerpt%", want: "%excerpt%"},
		{name: "tsf basic", plugin: "tsf", in: "{{title}} {{separator}} {{blogname}}", want: "%title% %sep% %sitename%"},
		{name: "tsf spaces", plugin: "tsf", in: "{{ title }} {{ sep }}", want: "%title% %sep%"},
		{name: "tsf description lossy", plugin: "tsf", in: "{{description}}", want: "%tagline%", lossy: []string{"{{description}}"}},
		{name: "tsf unknown", plugin: "tsf", in: "{{title}} {{nothing}}", want: "%title%", notMapped: []string{"{{nothing}}"}},
		{name: "tsf unknown only", plugin: "tsf", in: "{{nothing}}", want: "", notMapped: []string{"{{nothing}}"}},
		{name: "unknown plugin", plugin: "aioseo", in: "%title%", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, nm, lossy := TranslateTemplate(tc.plugin, tc.in)
			if got != tc.want {
				t.Errorf("out = %q, want %q", got, tc.want)
			}
			if !reflect.DeepEqual(nm, tc.notMapped) {
				t.Errorf("notMapped = %v, want %v", nm, tc.notMapped)
			}
			if !reflect.DeepEqual(lossy, tc.lossy) {
				t.Errorf("lossy = %v, want %v", lossy, tc.lossy)
			}
			if bad := (seo.Settings{}).UnknownVars(got); len(bad) > 0 {
				t.Errorf("out %q has unknown vars %v", got, bad)
			}
		})
	}
}

func TestSEOVarsTablesAreKnown(t *testing.T) {
	for name, table := range map[string]map[string]string{"yoast": yoastVars, "rankmath": rankMathVars, "tsf": tsfVars} {
		for from, to := range table {
			if bad := (seo.Settings{}).UnknownVars("%" + to + "%"); len(bad) > 0 {
				t.Errorf("%s: %s -> %s is not a Hatamoto variable", name, from, to)
			}
		}
	}
}
