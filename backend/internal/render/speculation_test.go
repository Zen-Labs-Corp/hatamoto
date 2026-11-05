package render

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"hatamoto.dev/core/internal/adminpath"
)

type rulesDoc struct {
	Prefetch  []rulesEntry `json:"prefetch"`
	Prerender any          `json:"prerender"`
}

type rulesEntry struct {
	Where struct {
		And []struct {
			HrefMatches any `json:"href_matches"`
			Not         *struct {
				HrefMatches []string `json:"href_matches"`
			} `json:"not"`
		} `json:"and"`
	} `json:"where"`
	Eagerness string `json:"eagerness"`
}

func parseRules(t *testing.T, tag string) (rulesEntry, []string) {
	t.Helper()
	const open, closing = `<script type="speculationrules">`, `</script>`
	if !strings.HasPrefix(tag, open) || !strings.HasSuffix(tag, closing) || strings.Count(tag, "</script>") != 1 {
		t.Fatalf("bad block: %s", tag)
	}
	var d rulesDoc
	if err := json.Unmarshal([]byte(tag[len(open):len(tag)-len(closing)]), &d); err != nil {
		t.Fatal(err)
	}
	if d.Prerender != nil || len(d.Prefetch) != 1 {
		t.Fatalf("want one prefetch rule and no prerender: %s", tag)
	}
	e := d.Prefetch[0]
	return e, e.Where.And[1].Not.HrefMatches
}

func TestSpeculationRulesJSON(t *testing.T) {
	for _, prefix := range []string{"/admin", "/ctl-x9"} {
		e, excl := parseRules(t, string(speculationRules(prefix)))
		if e.Eagerness != "moderate" || e.Where.And[0].HrefMatches != "/*" {
			t.Errorf("%s: %+v", prefix, e)
		}
		have := map[string]bool{}
		for _, p := range excl {
			have[p] = true
		}
		for _, want := range []string{prefix + "/*", "/p/*", "/api/*", "/plugin-ui/*", "/forms/*", "/auth/*", "/preview/*", "/media/*", "/theme/*", "/*logout*", "/*\\?*", "/*.xml", "/*.pdf", "/*.zip"} {
			if !have[want] {
				t.Errorf("%s: %q not excluded", prefix, want)
			}
		}
	}
}

// globMatch is the subset of URLPattern the exclusions use: "*" matches any
// run, "\?" a literal question mark.
func globMatch(pat, s string) bool {
	pat = strings.ReplaceAll(pat, `\?`, "?")
	if !strings.Contains(pat, "*") {
		return pat == s
	}
	parts := strings.Split(pat, "*")
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

func TestSpeculationRulesExcludeReservedPrefixes(t *testing.T) {
	_, excl := parseRules(t, string(speculationRules("/admin")))
	excluded := func(path string) bool {
		for _, p := range excl {
			if globMatch(p, path) {
				return true
			}
		}
		return false
	}
	for _, r := range adminpath.ReservedPrefixes {
		if !excluded("/"+r) && !excluded("/"+r+"/x") {
			t.Errorf("reserved prefix %q is not excluded from prefetch", r)
		}
	}
	for _, page := range []string{"/", "/about", "/blog/hello-world", "/category/news"} {
		if excluded(page) {
			t.Errorf("page %q must stay prefetchable", page)
		}
	}
	for _, bad := range []string{"/about?x=1", "/logout", "/admin/entities", "/a/b.pdf"} {
		if !excluded(bad) {
			t.Errorf("%q must be excluded", bad)
		}
	}
}

func TestSpeculationAdminPrefixHook(t *testing.T) {
	if adminPrefix() != "/admin" {
		t.Fatal("default prefix")
	}
	SetAdminPrefix(func() string { return "/ctl-x9" })
	defer SetAdminPrefix(nil)
	if adminPrefix() != "/ctl-x9" {
		t.Fatal("hook ignored")
	}
}

func renderSpeculation(t *testing.T, manifest string, v *PageView) string {
	t.Helper()
	var layers []fs.FS
	if manifest != "" {
		layers = append(layers, fstest.MapFS{"theme.json": {Data: []byte(manifest)}})
	}
	e, err := New(Options{Layers: layers})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := e.RenderPage(&b, v); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestSpeculationRenderedOnPublicPage(t *testing.T) {
	SetAdminPrefix(func() string { return "/ctl-x9" })
	defer SetAdminPrefix(nil)
	out := renderSpeculation(t, "", &PageView{Path: "/", BaseURL: "https://x.io"})
	if n := strings.Count(out, `type="speculationrules"`); n != 1 {
		t.Fatalf("want one block, got %d", n)
	}
	if !strings.Contains(out, `"/ctl-x9/*"`) || strings.Index(out, "speculationrules") > strings.Index(out, "</head>") {
		t.Error("block missing the custom admin prefix or not in head")
	}
}

func TestSpeculationNotInEditorFrames(t *testing.T) {
	for name, v := range map[string]*PageView{
		"preview": {Path: "/", BaseURL: "https://x.io", Preview: true},
		"canvas":  {Path: "/", BaseURL: "https://x.io", Canvas: true},
	} {
		if out := renderSpeculation(t, "", v); strings.Contains(out, "speculationrules") {
			t.Errorf("%s carries the block", name)
		}
	}
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := e.RenderPage(&b, &PageView{Path: "/", BaseURL: "https://x.io", Access: &AccessView{}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "speculationrules") {
		t.Error("access notice carries the block")
	}
}

func TestLoadThemePrefetchFlag(t *testing.T) {
	for manifest, want := range map[string]bool{
		`{"name":"a","prefetch":false}`: false,
		`{"name":"a","prefetch":true}`:  true,
		`{"name":"a"}`:                  true,
	} {
		out := renderSpeculation(t, manifest, &PageView{Path: "/", BaseURL: "https://x.io"})
		if got := strings.Contains(out, "speculationrules"); got != want {
			t.Errorf("%s: block=%v want %v", manifest, got, want)
		}
		e, err := New(Options{Layers: []fs.FS{fstest.MapFS{"theme.json": {Data: []byte(manifest)}}}})
		if err != nil {
			t.Fatal(err)
		}
		if e.Prefetch() != want {
			t.Errorf("%s: Prefetch()=%v", manifest, e.Prefetch())
		}
	}
}

func TestSpeculationSpliceNoHead(t *testing.T) {
	if got := string(spliceHead([]byte("<p>x</p>"), speculationRules("/admin"))); got != "<p>x</p>" {
		t.Errorf("headless output changed: %s", got)
	}
}
