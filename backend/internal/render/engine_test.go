package render

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestEngine(t *testing.T, o Options) *Engine {
	t.Helper()
	e, err := newEngine(o, newPluginRegistry())
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func renderToString(t *testing.T, e *Engine, v *PageView) string {
	t.Helper()
	var b bytes.Buffer
	if err := e.RenderPage(&b, v); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEngineShellWithoutSections(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	v := minimalView()
	v.EntityID = "ent-1"
	out := renderToString(t, e, v)

	for _, want := range []string{
		`<html lang="en" data-hm-contract="1">`,
		`<title>Fixture | Test Site</title>`,
		`<a class="hm-skip" href="#main-content">`,
		`<main id="main-content" tabindex="-1" data-hm-entity="ent-1">`,
		`<link rel="stylesheet" href="/theme/assets/theme.`,
		`&copy; ` + "",
		`<script src="/theme/assets/hm.js" defer></script>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "data-hm-preview") {
		t.Errorf("unexpected preview markup:\n%s", out)
	}
	if strings.Contains(out, "hm-header__cta") {
		t.Errorf("no CTA expected without Site.HeaderCTA")
	}
}

func TestEnginePreviewAndNotFoundAndError(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	v := minimalView()

	v.Preview = true
	out := renderToString(t, e, v)
	if !strings.Contains(out, `data-hm-preview="true"`) || !strings.Contains(out, `<meta name="robots" content="noindex,follow">`) {
		t.Errorf("preview markup missing:\n%s", out)
	}

	var b bytes.Buffer
	untitled := minimalView()
	untitled.Page.Title = ""
	if err := e.RenderNotFound(&b, untitled); err != nil {
		t.Fatal(err)
	}
	if o := b.String(); !strings.Contains(o, "<h1>Page not found</h1>") || !strings.Contains(o, "<title>Page not found | Test Site</title>") || !strings.Contains(o, "noindex") {
		t.Errorf("404:\n%s", o)
	}

	b.Reset()
	if err := e.RenderError(&b, minimalView(), http.StatusBadGateway); err != nil {
		t.Fatal(err)
	}
	if o := b.String(); !strings.Contains(o, "502") || !strings.Contains(o, "Bad Gateway") {
		t.Errorf("error page:\n%s", o)
	}
}

func TestEngineHeaderCTAOnlyWhenConfigured(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	v := minimalView()
	v.Site.HeaderCTA = &Link{Label: "Start", URL: "javascript:alert(1)"}
	out := renderToString(t, e, v)
	if !strings.Contains(out, `<a class="hm-btn hm-header__cta" href="#">Start</a>`) {
		t.Errorf("CTA missing or unsafe URL not neutralised:\n%s", out)
	}
}

func TestEngineThemeTokensInlineStyleIsFiltered(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	v := minimalView()
	v.Site.Tokens = ThemeTokens{Primary: "#0ea5e9", Font: "Inter, sans-serif", FontHeading: "red;}</style><script>alert(1)</script>"}
	out := renderToString(t, e, v)
	if !strings.Contains(out, "--hm-primary:#0ea5e9;") {
		t.Errorf("primary token missing:\n%s", out)
	}
	if strings.Contains(out, "<script>alert") || strings.Contains(out, "</style><script") {
		t.Errorf("CSS injection not blocked:\n%s", out)
	}
	if strings.Contains(out, "--hm-font-heading") || !strings.Contains(out, "--hm-font:Inter, sans-serif;") {
		t.Errorf("hostile token should be dropped, valid kept:\n%s", out)
	}
	v.Site.Tokens = ThemeTokens{}
	if strings.Contains(renderToString(t, e, v), ":root{") {
		t.Errorf("no inline style expected without tokens")
	}
}

func TestEngineUnknownSectionComment(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	v := minimalView()
	v.Sections = []Section{{ID: "x", Type: "nope"}}
	out := renderToString(t, e, v)
	if !strings.Contains(out, "<!-- unknown section type: nope -->") || strings.Contains(out, "data-hm-section") {
		t.Errorf("unknown type:\n%s", out)
	}
}

func TestEngineThemeLayersAndSetTheme(t *testing.T) {
	t.Parallel()
	themeDir, siteDir := t.TempDir(), t.TempDir()
	writeFile(t, themeDir, "sections/note.html", `{{define "section-note"}}<p {{hmf "text"}}>disk:{{.Content.text}}</p>{{end}}`)
	writeFile(t, themeDir, "schemas/note.json", `{"x-hm-section":{"title":"Note"},"type":"object","properties":{"text":{"type":"string","x-hm-field":"plaintext"}}}`)
	writeFile(t, siteDir, "partials/scripts.html", `{{define "scripts"}}<i>site-scripts</i>{{end}}`)

	e := newTestEngine(t, Options{Layers: []fs.FS{os.DirFS(siteDir), os.DirFS(themeDir)}})
	v := minimalView()
	v.Sections = []Section{{ID: "n1", Type: "note", Content: map[string]any{"text": "<b>hi</b>"}}}
	out := renderToString(t, e, v)
	for _, want := range []string{
		`<section id="n1" data-hm-section="n1" data-hm-type="note" class="hm-section hm-section--note">`,
		`<p data-hm-field="text">disk:&lt;b&gt;hi&lt;/b&gt;</p>`,
		`<i>site-scripts</i>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	// The first layer wins over the next for the same section file; SetTheme
	// swaps the layers on a running engine.
	writeFile(t, siteDir, "sections/note.html", `{{define "section-note"}}site:{{.Content.text}}{{end}}`)
	v1 := e.Version()
	if err := e.SetTheme(os.DirFS(siteDir), os.DirFS(themeDir)); err != nil {
		t.Fatal(err)
	}
	out = renderToString(t, e, v)
	if !strings.Contains(out, "site:&lt;b&gt;hi&lt;/b&gt;") {
		t.Errorf("SetTheme / layer precedence not applied:\n%s", out)
	}
	if e.Version() == v1 {
		t.Errorf("version must change after the theme changed")
	}
}

func TestEngineKeepsPreviousThemeOnBrokenSetTheme(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "partials/scripts.html", `{{define "scripts"}}<i>good</i>{{end}}`)
	e := newTestEngine(t, Options{Layers: []fs.FS{os.DirFS(dir)}})

	writeFile(t, dir, "partials/scripts.html", `{{define "scripts"}}{{broken`)
	if err := e.SetTheme(os.DirFS(dir)); err == nil {
		t.Fatal("a theme that does not parse must be refused")
	}
	if err := e.CheckTheme(os.DirFS(dir)); err == nil {
		t.Fatal("CheckTheme must report it too")
	}
	if out := renderToString(t, e, minimalView()); !strings.Contains(out, "<i>good</i>") {
		t.Errorf("previous theme should stay live:\n%s", out)
	}
}

func TestNewRejectsBrokenTheme(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "partials/footer.html", `{{define "other"}}x{{end}}`) // required "footer" is no longer defined
	if _, err := newEngine(Options{Layers: []fs.FS{os.DirFS(dir)}}, newPluginRegistry()); err == nil {
		t.Fatal("theme without a required template must fail")
	}
	dir2 := t.TempDir()
	writeFile(t, dir2, "sections/x.html", `no define here`)
	if _, err := newEngine(Options{Layers: []fs.FS{os.DirFS(dir2)}}, newPluginRegistry()); err == nil {
		t.Fatal("section template without section-x must fail")
	}
	dir3 := t.TempDir()
	writeFile(t, dir3, "sections/Bad-Type.html", `{{define "section-Bad-Type"}}x{{end}}`)
	if _, err := newEngine(Options{Layers: []fs.FS{os.DirFS(dir3)}}, newPluginRegistry()); err == nil {
		t.Fatal("invalid section type name must fail")
	}
}

func TestEnginePluginFuncsAndPartials(t *testing.T) {
	t.Parallel()
	reg := newPluginRegistry()
	dir := t.TempDir()
	writeFile(t, dir, "partials/scripts.html", `{{define "scripts"}}<p>{{plugin_t_x}}</p>{{template "plugin/t/card" .}}{{end}}`)
	if err := reg.registerFunc("t", "x", func() string { return `<script>alert(1)</script>` }); err != nil {
		t.Fatal(err)
	}
	if err := reg.registerPartial("t", "card", `<i>{{.Site.Name}}</i>`); err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(Options{Layers: []fs.FS{os.DirFS(dir)}}, reg)
	if err != nil {
		t.Fatal(err)
	}
	out := renderToString(t, e, minimalView())
	if !strings.Contains(out, `<p>&lt;script&gt;alert(1)&lt;/script&gt;</p><i>Test Site</i>`) {
		t.Errorf("plugin output not escaped / partial missing:\n%s", out)
	}

	// A func registered after New is picked up on the next render.
	writeFile(t, dir, "partials/scripts.html", `{{define "scripts"}}{{plugin_t_late}}{{end}}`)
	if err := reg.registerFunc("t", "late", func() string { return "late" }); err != nil {
		t.Fatal(err)
	}
	if out := renderToString(t, e, minimalView()); !strings.Contains(out, "late") {
		t.Errorf("late plugin func not visible:\n%s", out)
	}
}

func TestEngineHeadFilterApplied(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{HeadFilter: hostileFilter(HeadTag{Kind: "meta", Attrs: map[string]string{"name": "x-test", "content": "1"}})})
	if out := renderToString(t, e, minimalView()); !strings.Contains(out, `<meta name="x-test" content="1">`) {
		t.Errorf("head filter tag missing:\n%s", out)
	}
}

func TestEngineThemeInfoVersionAndAssets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "sections/b.html", `{{define "section-b"}}b{{end}}`)
	writeFile(t, dir, "sections/a.html", `{{define "section-a"}}a{{end}}`)
	writeFile(t, dir, "schemas/a.json", `{"x-hm-section":{"title":"Alpha","description":"d"},"type":"object","properties":{}}`)
	e := newTestEngine(t, Options{Layers: []fs.FS{os.DirFS(dir)}})

	info := e.ThemeInfo()
	b, _ := json.Marshal(info)
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"name", "version", "sections", "settings_schema"} {
		if _, ok := got[k]; !ok {
			t.Errorf("ThemeInfo JSON missing %q: %s", k, b)
		}
	}
	if info.Name != "default" || info.Version != e.Version() || info.Version == "" {
		t.Errorf("info: %+v", info)
	}
	// The embedded theme contributes its own section types; check only a and b.
	var fixture []SectionInfo
	for _, s := range info.Sections {
		if s.Type == "a" || s.Type == "b" {
			fixture = append(fixture, s)
		}
	}
	if len(fixture) != 2 || fixture[0].Type != "a" || fixture[0].Title != "Alpha" || fixture[1].Title != "b" {
		t.Errorf("sections: %+v", info.Sections)
	}

	// Asset handler serves the fingerprinted bundle for the current version.
	rec := httptest.NewRecorder()
	e.AssetHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AssetPrefix+e.current().cssName, nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), ".hm-html-body") ||
		rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("asset: %d %v", rec.Code, rec.Header())
	}
}

func TestEngineContactInfoLinksAndMap(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	render := func(mapURL string) string {
		v := minimalView()
		v.Sections = []Section{{ID: "c", Type: "contact_info", Content: map[string]any{
			"phone": "+1 555 0100", "email": "a@b.test", "showMap": true, "mapEmbedUrl": mapURL,
		}}}
		var b strings.Builder
		if err := e.RenderPage(&b, v); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	out := render("https://www.openstreetmap.org/export/embed.html?bbox=1")
	for _, want := range []string{`href="tel:&#43;15550100"`, `href="mailto:a@b.test"`, `<iframe src="https://www.openstreetmap.org/export/embed.html`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(out, "ZgotmplZ") {
		t.Error("href rewritten to #ZgotmplZ")
	}
	out = render("https://maps.example.test/place/1")
	if strings.Contains(out, "<iframe") || !strings.Contains(out, `<a href="https://maps.example.test/place/1" rel="noopener">View map</a>`) {
		t.Errorf("non-allowlisted map must be a plain link:\n%s", out)
	}
}

func TestEngineSchemaLayoutSelection(t *testing.T) {
	t.Parallel()
	blog := `{{define "content"}}<article>BLOG:{{.Page.Title}}</article>{{end}}`
	dir := t.TempDir()
	writeFile(t, dir, "layouts/single-blog.html", blog)
	e := newTestEngine(t, Options{Layers: []fs.FS{os.DirFS(dir)}})

	v := minimalView()
	v.EntityID = "e1"
	v.SchemaSlug = "blog"
	if out := renderToString(t, e, v); !strings.Contains(out, "<article>BLOG:Fixture</article>") {
		t.Errorf("blog schema should use layouts/single-blog.html:\n%s", out)
	}

	// An archive of the same schema (no entity) keeps layouts/page.html.
	av := minimalView()
	av.SchemaSlug = "blog"
	if out := renderToString(t, e, av); strings.Contains(out, "BLOG:") {
		t.Errorf("archive view must not use the schema layout:\n%s", out)
	}

	v.SchemaSlug = "news" // no layouts/news.html
	if out := renderToString(t, e, v); strings.Contains(out, "BLOG:") {
		t.Errorf("absent schema layout must fall back to page")
	}

	for _, slug := range []string{"../x", "base", "BLOG", ""} {
		v.SchemaSlug = slug
		if out := renderToString(t, e, v); strings.Contains(out, "BLOG:") {
			t.Errorf("slug %q must fall back to page", slug)
		}
	}

	bad := t.TempDir()
	writeFile(t, bad, "layouts/single-blog.html", `no content defined`)
	if _, err := newEngine(Options{Layers: []fs.FS{os.DirFS(bad)}}, newPluginRegistry()); err == nil {
		t.Fatal("schema layout without content must reject the theme")
	}
}

func TestEngineRelatedCallableOnNotFoundAndError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := `{{define "content"}}<p>REL:{{len (call .Related 3)}}</p>{{end}}`
	writeFile(t, dir, "layouts/404.html", body)
	writeFile(t, dir, "layouts/error.html", body)
	e := newTestEngine(t, Options{Layers: []fs.FS{os.DirFS(dir)}})
	v := minimalView() // Related is nil, as on the error-view fallback
	var b bytes.Buffer
	if err := e.RenderNotFound(&b, v); err != nil || !strings.Contains(b.String(), "REL:0") {
		t.Errorf("404: err=%v\n%s", err, b.String())
	}
	b.Reset()
	if err := e.RenderError(&b, v, http.StatusBadGateway); err != nil || !strings.Contains(b.String(), "REL:0") {
		t.Errorf("error: err=%v\n%s", err, b.String())
	}
}

func TestEngineThemeTokensRenderFunctionalColorsAndQuotedFonts(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	v := minimalView()
	v.Site.Tokens = ThemeTokens{
		Primary: "rgb(1, 2, 3)", Background: "hsl(1 2% 3%)",
		Font: `'Open Sans', sans-serif`, FontHeading: `"Merriweather", serif`,
	}
	out := renderToString(t, e, v)
	if strings.Contains(out, "ZgotmplZ") {
		t.Fatalf("validated tokens rendered as ZgotmplZ:\n%s", out)
	}
	for _, want := range []string{"--hm-primary:rgb(1, 2, 3);", "--hm-bg:hsl(1 2% 3%);", `--hm-font:'Open Sans', sans-serif;`, `--hm-font-heading:"Merriweather", serif;`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	v.Site.Tokens = ThemeTokens{Font: `"Inter`, Primary: "red"}
	out = renderToString(t, e, v)
	if strings.Contains(out, "--hm-font") || !strings.Contains(out, "--hm-primary:red;") {
		t.Errorf("unbalanced font should be dropped:\n%s", out)
	}
}

func TestValidFontRequiresBalancedQuotes(t *testing.T) {
	t.Parallel()
	for v, want := range map[string]bool{
		`Inter, sans-serif`: true, `"Open Sans", serif`: true, `'A', "B"`: true,
		`"Inter`: false, `Inter'`: false, `"a'b"`: false, `'a" b'`: false,
	} {
		if ValidFont(v) != want {
			t.Errorf("ValidFont(%q) = %v, want %v", v, !want, want)
		}
	}
}

func TestEngineSeriesAndPostsByAuthor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "layouts/page.html", `{{define "content"}}<p>S:{{with call .Series}}{{.Name}}/{{.Total}}{{range .Parts}}[{{.Part}}{{if .Published}}{{.URL}}{{end}}]{{end}}{{else}}none{{end}} A:{{with call .PostsByAuthor 5 .PageNum}}{{.Total}}/{{.Page}}/{{.Pages}}/{{len .Items}}{{end}} N:{{.PageNum}}</p>{{end}}`)
	e := newTestEngine(t, Options{Layers: []fs.FS{os.DirFS(dir)}})

	// Unset closures and PageNum are safe no-ops in a layout.
	out := renderToString(t, e, minimalView())
	if !strings.Contains(out, "S:none A:0/1/1/0 N:1") {
		t.Errorf("defaults:\n%s", out)
	}

	v := minimalView()
	v.PageNum = 3
	v.Series = func() *SeriesView {
		return &SeriesView{Name: "Audit", Total: 2, Parts: []SeriesPartView{{Part: 1, URL: "/a", Published: true}, {Part: 2}}}
	}
	v.PostsByAuthor = func(limit, page int) PostsPage {
		return PostsPage{Items: make([]RelatedItem, 2), Total: 12, Page: page, Pages: 3}
	}
	out = renderToString(t, e, v)
	if !strings.Contains(out, "S:Audit/2[1/a][2] A:12/3/3/2 N:3") {
		t.Errorf("set:\n%s", out)
	}
}
