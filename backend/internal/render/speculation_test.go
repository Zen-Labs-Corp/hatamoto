package render

import (
	"encoding/json"
	"io/fs"
	"regexp"
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
				HrefMatches []any `json:"href_matches"`
			} `json:"not"`
		} `json:"and"`
	} `json:"where"`
	Eagerness string `json:"eagerness"`
}

func parseRules(t *testing.T, tag string) (rulesEntry, []any) {
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
	e, excl := parseRules(t, string(speculationRules()))
	if e.Eagerness != "moderate" || e.Where.And[0].HrefMatches != "/*" {
		t.Errorf("%+v", e)
	}
	have := map[string]bool{}
	for _, p := range excl {
		if str, ok := p.(string); ok {
			have[str] = true
		} else if m, _ := p.(map[string]any); m["pathname"] == "/*" && m["search"] == "(.+)" {
			have["query"] = true
		}
	}
	for _, want := range []string{"/admin/*", "/p/*", "/api/*", "/plugin-ui/*", "/forms/*", "/auth/*", "/preview/*", "/media/*", "/theme/*", "/*logout*", "query", "/*.xml", "/*.pdf", "/*.zip"} {
		if !have[want] {
			t.Errorf("%q not excluded", want)
		}
	}
}

// globMatch is the subset of URLPattern the string exclusions use: "*" matches any run.
func globMatch(pat, s string) bool {
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
	_, excl := parseRules(t, string(speculationRules()))
	excluded := func(path string) bool {
		for _, p := range excl {
			if str, ok := p.(string); ok && globMatch(str, path) {
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
	for _, page := range []string{"/", "/about", "/blog/hello-world", "/category/news", "/feedback", "/healthy-recipes", "/ready-meals", "/sitemaps-explained"} {
		if excluded(page) {
			t.Errorf("page %q must stay prefetchable", page)
		}
	}
	for _, bad := range []string{"/logout", "/admin/entities", "/a/b.pdf", "/health", "/ready", "/feed", "/feed/x", "/sitemap", "/sitemap.xsl"} {
		if !excluded(bad) {
			t.Errorf("%q must be excluded", bad)
		}
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
	out := renderSpeculation(t, "", &PageView{Path: "/", BaseURL: "https://x.io"})
	if n := strings.Count(out, `type="speculationrules"`); n != 1 {
		t.Fatalf("want one block, got %d", n)
	}
	if strings.Index(out, "speculationrules") > strings.Index(out, "</head>") {
		t.Error("block not in head")
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
	if got := string(spliceHead([]byte("<p>x</p>"), speculationRules())); got != "<p>x</p>" {
		t.Errorf("headless output changed: %s", got)
	}
}

// withoutSpeculation drops the prefetch block, whose exclusion patterns name
// routes such as /fonts/ and /plugin-ui/, from a page a test greps for markup.
func withoutSpeculation(page string) string {
	return speculationBlockRe.ReplaceAllString(page, "")
}

var speculationBlockRe = regexp.MustCompile(`<script type="speculationrules">.*?</script>`)

func TestSpliceHeadInsertsBeforeLastHeadClose(t *testing.T) {
	const page = `<head><title>a</title></head><body><p>&lt;/head&gt;</p></body>`
	want := `<head><title>a</title><b>x</b></head><body><p>&lt;/head&gt;</p></body>`
	for _, spare := range []int{0, 64} { // grown copy, and shifted inside the spare capacity
		in := append(make([]byte, 0, len(page)+spare), page...)
		if got := string(spliceHead(in, "<b>x</b>")); got != want {
			t.Errorf("spare %d: %s", spare, got)
		}
	}
	if got := string(spliceHead([]byte("<p>x</p>"), "<b>x</b>")); got != "<p>x</p>" {
		t.Errorf("no head: %s", got)
	}
}
