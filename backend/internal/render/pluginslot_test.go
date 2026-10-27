package render

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

// The plugin public-asset slots are empty until a plugin package ships assets and render the host-provided
// markup once something fills them (see SetPluginAssets).
func TestPluginAssetSlots(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	page, ok := e.current().pick("page")
	if !ok {
		t.Fatal("no page layout")
	}
	for _, tc := range []struct {
		slot string
		data pageData
		want string
	}{
		{"plugin-head", pageData{}, ""},
		{"plugin-body", pageData{}, ""},
		{"plugin-head", pageData{PluginHead: template.HTML(`<link rel="stylesheet" href="/p/x.css">`)}, `<link rel="stylesheet" href="/p/x.css">` + "\n"},
		{"plugin-body", pageData{PluginBody: template.HTML(`<script src="/p/x.js"></script>`)}, `<script src="/p/x.js"></script>` + "\n"},
	} {
		var b bytes.Buffer
		if err := page.ExecuteTemplate(&b, tc.slot, tc.data); err != nil {
			t.Fatalf("%s: %v", tc.slot, err)
		}
		if got := strings.TrimLeft(b.String(), "\n"); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.slot, got, tc.want)
		}
	}
	out := renderToString(t, e, minimalView())
	if !strings.Contains(out, "</body>") || strings.Contains(out, "plugin") {
		t.Errorf("default render must not mention plugin assets:\n%s", out)
	}
}
