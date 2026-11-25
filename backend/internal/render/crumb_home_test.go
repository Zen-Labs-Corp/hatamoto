package render

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// The home crumb with no owner label takes the theme string breadcrumb_home
// of the page's locale, in the BreadcrumbList JSON-LD and the visible trail
// alike; an English site keeps "Home" (#413).
func TestHomeCrumbThemeString(t *testing.T) {
	t.Parallel()
	view := func(def bool, ov map[string]string) *PageView {
		v := minimalView()
		v.Breadcrumb = []Crumb{{Name: "Home", URL: "/"}, {Name: "イベント", URL: "/events"}, {Name: "みなジャズ"}}
		v.HomeCrumbDefault = def
		v.StringOverrides = ov
		return v
	}
	ja := map[string]string{"breadcrumb_home": "ホーム"}
	e := newTestEngine(t, Options{})
	for _, tc := range []struct {
		name string
		v    *PageView
		want string
	}{
		{"translated", view(true, ja), `"name":"ホーム"`},
		{"default text", view(true, nil), `"name":"Home"`},
		{"owner label kept", view(false, ja), `"name":"Home"`},
	} {
		v := tc.v
		before := v.Breadcrumb
		out := renderToString(t, e, v)
		if !strings.Contains(out, tc.want) || strings.Count(out, `"BreadcrumbList"`) != 1 {
			t.Errorf("%s: JSON-LD lacks %s", tc.name, tc.want)
		}
		if before[0].Name != "Home" {
			t.Errorf("%s: the caller's trail was written into", tc.name)
		}
	}

	// A theme layered over the built-in one inherits the key; its visible
	// trail prints the same label.
	custom := fstest.MapFS{
		"theme.json":        mapFile(`{"name":"ct","version":"1.0.0"}`),
		"layouts/base.html": mapFile(`{{range .VisibleCrumbs}}{{.Name}};{{end}}`),
	}
	ce, err := New(Options{Layers: []fs.FS{custom, DefaultFS()}})
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := ce.RenderPage(&b, view(true, ja)); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "ホーム;イベント;みなジャズ;" {
		t.Errorf("visible trail = %q", got)
	}
}
