package wpimport

import (
	"os"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/render"
)

func TestWXRCustomCSS(t *testing.T) {
	f, err := os.Open("testdata/custom_css.wxr.xml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := ParseWXR(f, WXROptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := ".hero > a { color: red; }\n@media (width < 600px) { .hero { margin: 0; } }"
	if d.CustomCSS != want {
		t.Errorf("CustomCSS = %q, want the newest published item", d.CustomCSS)
	}
	if len(d.Items) != 1 || d.Items[0].Slug != "services" {
		t.Errorf("custom_css and other skipped types must not become items: %+v", d.Items)
	}
	rep := Scan(d, "test.xml")
	if rep.CustomCSS != "found (0.1 KB)" || !strings.Contains(rep.Summary(), "Custom CSS: found (0.1 KB)") {
		t.Errorf("report line %q", rep.CustomCSS)
	}
	store, status := planCustomCSS(d.CustomCSS)
	if store != want || status != "imported (0.1 KB) into Theme > Additional CSS" {
		t.Errorf("plan = %q, %q", store, status)
	}
}

func TestPlanCustomCSSRefused(t *testing.T) {
	css := "@import url(https://fonts.example/css?family=X);\r\n.a { color: red; } /* note */"
	store, status := planCustomCSS(css)
	if status != "refused: line 1: @import is not allowed; kept as a comment in Theme > Additional CSS" {
		t.Errorf("status %q", status)
	}
	if !strings.HasPrefix(store, "/* Skipped by the WordPress import (line 1: @import is not allowed): @import url(") || !strings.Contains(store, ".a { color: red; }") {
		t.Errorf("stored %q", store)
	}
	if strings.Count(store, "*/") != 1 {
		t.Errorf("the original comment must not close ours early: %q", store)
	}
	if issue := render.ValidateCustomCSS(store); issue != nil {
		t.Errorf("the commented copy must validate: %v", issue)
	}
}

func TestPlanCustomCSSTooLargeToKeep(t *testing.T) {
	store, status := planCustomCSS("@import 'x';" + strings.Repeat("a{}", render.MaxCustomCSS/3))
	if store != "" || !strings.HasPrefix(status, "refused: ") || !strings.Contains(status, "too large to keep as a comment") {
		t.Errorf("store %d bytes, status %q", len(store), status)
	}
}

func TestSkipTypeStillSkipsOthers(t *testing.T) {
	for _, n := range []string{"customize_changeset", "revision", "nav_menu_item", "oembed_cache"} {
		if !skipType(n) {
			t.Errorf("%s must stay skipped", n)
		}
	}
}
