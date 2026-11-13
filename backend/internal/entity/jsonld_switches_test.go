package entity

import "testing"

func TestResolveNodeSwitches(t *testing.T) {
	str := func(v NodeSwitch) any { return string(v) }
	cases := []struct {
		name   string
		schema NodeSwitch
		entry  any
		want   string // "", "on", "off"
	}{
		{"nothing set", "", nil, ""},
		{"schema on", "on", nil, "on"},
		{"schema off", "off", nil, "off"},
		{"entry on beats schema off", "off", str("on"), "on"},
		{"entry off beats schema on", "on", str("off"), "off"},
		{"entry on, no schema", "", str("on"), "on"},
		{"entry off, no schema", "", str("off"), "off"},
		{"empty entry uses schema", "off", str(""), "off"},
		{"junk entry string uses schema", "on", str("maybe"), "on"},
		{"non-string entry uses schema", "off", true, "off"},
		{"junk entry, no schema", "", 7, ""},
	}
	show := func(b *bool) string {
		switch {
		case b == nil:
			return ""
		case *b:
			return "on"
		}
		return "off"
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attrs := map[string]any{}
			if tc.entry != nil {
				attrs[attrJSONLDWebPage] = tc.entry
				attrs[attrJSONLDBreadcrumb] = tc.entry
			}
			sw := ResolveNodeSwitches(SchemaURLSettings{EmitWebPage: tc.schema, EmitBreadcrumb: tc.schema}, attrs)
			if got := show(sw.WebPage); got != tc.want {
				t.Errorf("WebPage = %q, want %q", got, tc.want)
			}
			if got := show(sw.Breadcrumb); got != tc.want {
				t.Errorf("Breadcrumb = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNodeSwitchKeysAreStructural(t *testing.T) {
	for _, k := range SEOStructuredKeys {
		if !isStructuralKey(k) || !IsSEOAttr(k) {
			t.Errorf("%s must be a structural SEO attribute", k)
		}
	}
}
