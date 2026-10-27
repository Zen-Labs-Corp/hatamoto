package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

func newPreviewHandlers(t testing.TB) *renderAdminHandlers {
	t.Helper()
	engine, err := render.New(render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return &renderAdminHandlers{
		engine: engine,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		chrome: func(context.Context, []render.Section) (render.SiteView, map[string]render.FormView, error) {
			return render.SiteView{Name: "Draft Site"}, nil, nil
		},
		baseURL: func(*http.Request) string { return "https://x.io" },
	}
}

func preview(t *testing.T, h *renderAdminHandlers, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.handleRenderPreview(w, httptest.NewRequest(http.MethodPost, "/api/v1/render/preview", strings.NewReader(body)))
	return w
}

func TestRenderPreviewRendersPostedSections(t *testing.T) {
	h := newPreviewHandlers(t)
	w := preview(t, h, `{"schema":"page","attributes":{"meta_title":"Draft","page_sections":[
		{"id":"a","type":"html","sort_order":1,"content":{"body":"<p>Unsaved words</p><script>alert(1)</script>"}}]}}`)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, want := range []string{"Unsaved words", "data-hm-section", "data-hm-preview", "<title>Draft"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "alert(1)") {
		t.Error("script survived sanitising")
	}
	hd := w.Header()
	if hd.Get("Cache-Control") != "no-store" || hd.Get("X-Frame-Options") != "SAMEORIGIN" ||
		!strings.Contains(hd.Get("Content-Security-Policy"), "frame-ancestors 'self'") ||
		hd.Get("X-Robots-Tag") != "noindex" {
		t.Errorf("headers: %v", hd)
	}
}

var stampRe = regexp.MustCompile(` data-hm-text="[^"]*"`)

func TestRenderPreviewCanvasFlag(t *testing.T) {
	h := newPreviewHandlers(t)
	const attrs = `"schema":"page","attributes":{"page_sections":[{"id":"a","type":"html","sort_order":1,"content":{"body":"<p>Hi</p>"}}]}`
	on := preview(t, h, `{`+attrs+`,"canvas":true}`)
	off := preview(t, h, `{`+attrs+`}`)
	if on.Code != 200 || off.Code != 200 {
		t.Fatalf("status %d/%d", on.Code, off.Code)
	}
	if !strings.Contains(on.Body.String(), `id="hm-canvas-bridge"`) {
		t.Error("canvas:true did not inject the bridge")
	}
	if strings.Contains(off.Body.String(), "hm-canvas-bridge") {
		t.Error("bridge injected without canvas flag")
	}
	stripped := on.Body.String()
	i := strings.Index(stripped, "<script id=\"hm-canvas-bridge\">")
	// The bridge is injected at the top of <head>, so its end is the first
	// closing tag after its start, not the last one in the document.
	j := i + strings.Index(stripped[max(i, 0):], "</script>\n") + len("</script>\n")
	if !strings.Contains(on.Body.String(), `<p data-hm-text="body#0">Hi</p>`) {
		t.Error("canvas:true did not stamp html text")
	}
	if strings.Contains(off.Body.String(), "data-hm-text") {
		t.Error("data-hm-text emitted without canvas flag")
	}
	if i < 0 || stampRe.ReplaceAllString(stripped[:i]+stripped[j:], "") != off.Body.String() {
		t.Error("canvas render differs from plain render by more than the bridge and text stamps")
	}
}

func TestRenderPreviewCanvasNoSections(t *testing.T) {
	h := newPreviewHandlers(t)
	w := preview(t, h, `{"schema":"blog","attributes":{"content":"<p>Body only</p>"},"canvas":true}`)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `id="hm-canvas-bridge"`) || strings.Contains(w.Body.String(), "<section") {
		t.Error("expected a canvas document with the bridge and no sections")
	}
}

func TestRenderPreviewOversizeBody(t *testing.T) {
	w := preview(t, newPreviewHandlers(t), `{"attributes":"`+strings.Repeat("a", maxPreviewBodySize)+`"}`)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", w.Code)
	}
}

func TestRenderPreviewBadJSON(t *testing.T) {
	if w := preview(t, newPreviewHandlers(t), `{`); w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}

func TestRenderThemeInfo(t *testing.T) {
	h := newPreviewHandlers(t)
	w := httptest.NewRecorder()
	h.handleRenderTheme(w, httptest.NewRequest(http.MethodGet, "/api/v1/render/theme", nil))
	var got render.ThemeInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Name == "" || len(got.Sections) == 0 || got.Version == "" {
		t.Fatalf("bad theme info: %v %s", err, w.Body)
	}
}

// --- route-context preview -------------------------------------------------

// routeWorld is a DB-free stand-in for the public loaders: one stored page at
// /own (with a foreign page at /other). Its resolve mirrors the real
// loaders' order: load entity, applyPreviewAttributes, then derive meta,
// JSON-LD and the route result from the (possibly overridden) attributes.
type routeWorld struct {
	own, other *entity.Entity
}

const storedAttrs = `{"meta_title":"Stored title","meta_description":"stored desc","page_sections":[
	{"id":"a","type":"html","sort_order":1,"content":{"body":"<p>Stored body</p>"}}]}`

func newRouteWorld() *routeWorld {
	schema := &entity.EntitySchema{Slug: "page"}
	return &routeWorld{
		own:   &entity.Entity{ID: uuid.New(), Name: "Own", Slug: "own", Schema: schema, Attributes: json.RawMessage(storedAttrs)},
		other: &entity.Entity{ID: uuid.New(), Name: "Other", Slug: "other", Schema: schema, Attributes: json.RawMessage(`{"meta_title":"Foreign secret","page_sections":[{"id":"x","type":"html","sort_order":1,"content":{"body":"<p>FOREIGN-DATA</p>"}}]}`)},
	}
}

func (w *routeWorld) resolve(ctx context.Context, path string, _ *http.Request) (*routeResult, pageOutcome, error) {
	var src *entity.Entity
	switch path {
	case "/own":
		src = w.own
	case "/other":
		src = w.other
	default:
		return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, nil
	}
	ent := *src // the loader's own copy of the row
	applyPreviewAttributes(ctx, ent.ID, &ent.Attributes)
	var a struct {
		Title string `json:"meta_title"`
		Desc  string `json:"meta_description"`
	}
	_ = json.Unmarshal(ent.Attributes, &a)
	jsonLD, _ := entity.GenerateJSONLD(&ent, "https://x.io")
	canonical := "https://x.io" + path
	return &routeResult{
		Page: &PublicPageResponse{
			Entity: &ent, JSONLD: jsonLD,
			Meta:       PageMeta{Title: a.Title, Description: a.Desc, CanonicalURL: canonical},
			Breadcrumb: []BreadcrumbItem{{Label: "Home", Href: "/"}, {Label: "Sec", Href: "/sec"}, {Label: ent.Name, Href: path}},
		},
		Kind: resolveKindEntity, CanonicalURL: canonical,
	}, pageOutcome{Status: 200}, nil
}

func (w *routeWorld) buildView(_ context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
	v := pageViewFromRoute(rr, "https://x.io", requestPath(r))
	v.Site = render.SiteView{Name: "Route Site"}
	return v, nil
}

func (w *routeWorld) previewHandlers(t *testing.T) *renderAdminHandlers {
	h := newPreviewHandlers(t)
	h.resolve = w.resolve
	h.buildView = w.buildView
	h.loadHome = func(*http.Request) (*PublicPageResponse, pageOutcome, error) {
		return nil, pageOutcome{NotFound: true}, nil
	}
	return h
}

// publicHTML is what the public catch-all serves for path.
func (w *routeWorld) publicHTML(t *testing.T, path string) string {
	t.Helper()
	engine, err := render.New(render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h := &renderHandlers{
		engine: engine, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		resolve: w.resolve, buildView: w.buildView,
	}
	rec := httptest.NewRecorder()
	h.handleRenderCatchAll(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != 200 {
		t.Fatalf("public %s: %d", path, rec.Code)
	}
	return rec.Body.String()
}

func previewBody(w *routeWorld, e *entity.Entity, attrs, path string, canvas bool) string {
	return fmt.Sprintf(`{"entity_id":%q,"schema":"page","attributes":%s,"path":%q,"canvas":%t}`, e.ID, attrs, path, canvas)
}

// normalizePreview strips the complete list of differences between a preview and
// the public render of the same page and attributes: the noindex robots meta, and
// the data-hm-preview attribute on <html>. (Headers differ too: no-store,
// X-Robots-Tag: noindex.) The public side also carries the theme script tag,
// which editor renders omit: dropThemeScript removes it from the public html.
func normalizePreview(html string) string {
	html = strings.Replace(html, ` data-hm-preview="true"`, "", 1)
	return robotsMetaRe.ReplaceAllString(html, "")
}

func dropThemeScript(html string) string {
	return strings.Replace(html, `<script src="`+render.AssetPrefix+`hm.js" defer></script>`+"\n", "", 1)
}

var robotsMetaRe = regexp.MustCompile(`\s*<meta name="robots" content="noindex,follow">`)

func TestRenderPreviewOwnRouteEqualsPublicRender(t *testing.T) { // A1
	w := newRouteWorld()
	pub := w.publicHTML(t, "/own")
	for _, want := range []string{"Stored body", `rel="canonical"`, "BreadcrumbList", "Sec"} {
		if !strings.Contains(pub, want) {
			t.Fatalf("public render lacks %q; test premise broken", want)
		}
	}
	rec := preview(t, w.previewHandlers(t), previewBody(w, w.own, storedAttrs, "/own", false))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	got := rec.Body.String()
	if got == pub {
		t.Fatal("preview carries no preview markers")
	}
	if !strings.Contains(pub, render.AssetPrefix+"hm.js") {
		t.Fatal("public render lacks hm.js; dropThemeScript would be a no-op")
	}
	if n := normalizePreview(got); n != dropThemeScript(pub) {
		t.Errorf("preview differs from public beyond the documented markers:\npreview: %s\npublic:  %s", n, pub)
	}
}

func TestRenderPreviewEditedAttributesKeepRouteContext(t *testing.T) { // A2
	w := newRouteWorld()
	edited := `{"meta_title":"Edited title","meta_description":"edited desc","page_sections":[
		{"id":"a","type":"html","sort_order":1,"content":{"body":"<p>Edited body</p>"}}]}`
	rec := preview(t, w.previewHandlers(t), previewBody(w, w.own, edited, "/own", false))
	body := rec.Body.String()
	for _, want := range []string{"Edited body", "<title>Edited title", "edited desc", `rel="canonical"`, "BreadcrumbList", "Route Site", "data-hm-preview"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{"Stored body", "Stored title", "stored desc"} {
		if strings.Contains(body, bad) {
			t.Errorf("stale stored value %q", bad)
		}
	}
	if !strings.Contains(body, `content="noindex`) {
		t.Error("no noindex")
	}
	if w.own.Attributes == nil || !strings.Contains(string(w.own.Attributes), "Stored body") {
		t.Error("preview mutated the stored entity")
	}
}

func TestRenderPreviewForeignOrUnresolvablePathFallsBack(t *testing.T) { // A3
	w := newRouteWorld()
	draft := `{"meta_title":"Draft","page_sections":[{"id":"a","type":"html","sort_order":1,"content":{"body":"<p>Draft body</p>"}}]}`
	for _, path := range []string{"/other", "/missing", "/", "//evil.example", ""} {
		rec := preview(t, w.previewHandlers(t), previewBody(w, w.own, draft, path, false))
		body := rec.Body.String()
		if rec.Code != 200 || !strings.Contains(body, "Draft body") || !strings.Contains(body, "<title>Draft") {
			t.Errorf("%q: not the attributes-only view (%d)", path, rec.Code)
		}
		for _, bad := range []string{"FOREIGN-DATA", "Foreign secret", "BreadcrumbList", "hreflang", "Route Site"} {
			if strings.Contains(body, bad) {
				t.Errorf("%q: fallback leaked %q", path, bad)
			}
		}
	}
}

func TestRenderPreviewRouteViewCanvasKeepsBridgeAndStamps(t *testing.T) {
	w := newRouteWorld()
	rec := preview(t, w.previewHandlers(t), previewBody(w, w.own, storedAttrs, "/own", true))
	body := rec.Body.String()
	if !strings.Contains(body, `id="hm-canvas-bridge"`) || !strings.Contains(body, `<p data-hm-text="body#0">Stored body</p>`) {
		t.Error("canvas route preview lacks bridge or text stamps")
	}
	if !strings.Contains(body, "BreadcrumbList") {
		t.Error("canvas route preview lacks route context")
	}
}

func TestRenderPreviewRouteRequestIsAnonymous(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/render/preview?x=1", nil)
	r.Header.Set("Cookie", "sid=abc")
	r.Header.Set("Authorization", "Bearer k")
	r.Header.Set("Accept", "application/ld+json")
	pr := previewRouteRequest(r, "/own", uuid.New(), json.RawMessage(`{}`))
	if pr.Method != http.MethodGet || pr.URL.Path != "/own" || pr.URL.RawQuery != "" ||
		pr.Header.Get("Cookie") != "" || pr.Header.Get("Authorization") != "" || pr.Header.Get("Accept") != "" {
		t.Errorf("not an anonymous GET: %+v %v", pr.URL, pr.Header)
	}
	if r.Header.Get("Cookie") == "" {
		t.Error("original request mutated")
	}
}

func TestRenderPreviewContextHeaderAndFallbackWarning(t *testing.T) {
	w := newRouteWorld()
	draft := `{"meta_title":"Draft","page_sections":[{"id":"a","type":"html","sort_order":1,"content":{"body":"<p>Draft</p>"}}]}`
	failing := func(context.Context, string, *http.Request) (*routeResult, pageOutcome, error) {
		return nil, pageOutcome{}, errors.New("db blip")
	}
	cases := []struct {
		name, path, reason string
		resolve            func(context.Context, string, *http.Request) (*routeResult, pageOutcome, error)
		want               string
	}{
		{"own route", "/own", "", nil, "route"},
		{"no path", "", "", nil, "attributes"},
		{"resolve error", "/own", "resolve or load error", failing, "attributes"},
		{"id mismatch", "/other", "path does not resolve to the entity", nil, "attributes"},
	}
	for _, c := range cases {
		var logs bytes.Buffer
		h := w.previewHandlers(t)
		h.logger = slog.New(slog.NewTextHandler(&logs, nil))
		if c.resolve != nil {
			h.resolve = c.resolve
		}
		rec := preview(t, h, previewBody(w, w.own, draft, c.path, false))
		if got := rec.Header().Get("X-Hatamoto-Preview-Context"); got != c.want {
			t.Errorf("%s: header %q, want %q", c.name, got, c.want)
		}
		out := logs.String()
		if c.reason == "" {
			if strings.Contains(out, "WARN") {
				t.Errorf("%s: unexpected warning: %s", c.name, out)
			}
			continue
		}
		for _, f := range []string{"level=WARN", c.reason, "path=" + c.path, "entity_id=" + w.own.ID.String()} {
			if !strings.Contains(out, f) {
				t.Errorf("%s: log lacks %q: %s", c.name, f, out)
			}
		}
	}
}

func TestRenderPreviewCSPCaptchaProvider(t *testing.T) {
	const body = `{"schema":"page","attributes":{"page_sections":[]}}`
	h := newPreviewHandlers(t)
	if got := preview(t, h, body).Header().Get("Content-Security-Policy"); got != renderCSP {
		t.Errorf("no provider: CSP = %q, want renderCSP", got)
	}
	h.captchaProvider = func(context.Context) string { return "hcaptcha" }
	got := preview(t, h, body).Header().Get("Content-Security-Policy")
	if !strings.Contains(got, "script-src 'self' 'unsafe-inline' https://hcaptcha.com https://*.hcaptcha.com;") {
		t.Errorf("hcaptcha CSP = %q", got)
	}
}

// Preview and canvas responses never run page scripts and are sandboxed by
// header too: a direct navigation can not run a plugin script in the admin
// origin. The sandbox rides as a second CSP header (all of them are enforced);
// the first stays renderCSP.
func TestRenderPreviewSandboxAndNoPluginScript(t *testing.T) { //nolint:paralleltest // sets the global plugin assets and script gate
	render.SetPluginAssets("atk", "/p/atk/assets/plugin.css", "/p/atk/assets/plugin.js")
	render.SetPluginScriptGate(func(string) bool { return true })
	t.Cleanup(func() {
		render.SetPluginAssets("atk", "", "")
		render.SetPluginScriptGate(nil)
	})
	h := newPreviewHandlers(t)
	const attrs = `"schema":"page","attributes":{"page_sections":[{"id":"a","type":"html","sort_order":1,"content":{"body":"<p>Hi</p>"}}]}`
	for name, body := range map[string]string{"plain": `{` + attrs + `}`, "canvas": `{` + attrs + `,"canvas":true}`} {
		w := preview(t, h, body)
		if w.Code != 200 {
			t.Fatalf("%s: status %d: %s", name, w.Code, w.Body)
		}
		wantSrc := "'none'"
		if name == "canvas" {
			wantSrc = render.CanvasBridgeScriptHash()
		}
		want := "sandbox allow-scripts; script-src " + wantSrc
		var sandboxes int
		for _, v := range w.Header().Values("Content-Security-Policy") {
			if v == want {
				sandboxes++
			}
		}
		if sandboxes != 1 {
			t.Errorf("%s: CSP headers %q lack one exact %q", name, w.Header().Values("Content-Security-Policy"), want)
		}
		if strings.Contains(w.Body.String(), `<script src="/p/atk/assets/plugin.js"`) {
			t.Errorf("%s: preview carries the plugin script", name)
		}
		if !strings.Contains(w.Body.String(), `href="/p/atk/assets/plugin.css"`) {
			t.Errorf("%s: plugin stylesheet missing", name)
		}
	}
}

// A page whose content carries an inline script and an on* handler still
// renders; the enforced CSP (no unsafe-inline, no unsafe-hashes) blocks both.
// A Go test can not execute them, so the header is the assertion.
func TestRenderPreviewInlineScriptBlockedByCSP(t *testing.T) {
	h := newPreviewHandlers(t)
	const attrs = `"schema":"page","attributes":{"page_sections":[{"id":"a","type":"html","sort_order":1,"content":{"body":"<p>Hi</p><img src=x onerror=alert(1)>"}}]}`
	for name, body := range map[string]string{"plain": `{` + attrs + `}`, "canvas": `{` + attrs + `,"canvas":true}`} {
		w := preview(t, h, body)
		if w.Code != 200 {
			t.Fatalf("%s: status %d: %s", name, w.Code, w.Body)
		}
		if !strings.Contains(w.Body.String(), "Hi") {
			t.Errorf("%s: page did not render", name)
		}
		sandboxed := 0
		for _, v := range w.Header().Values("Content-Security-Policy") {
			sandbox := strings.HasPrefix(v, "sandbox allow-scripts; script-src ")
			if sandbox {
				sandboxed++
			}
			if strings.Contains(v, "unsafe-hashes") || (sandbox && strings.Contains(v, "unsafe-inline")) {
				t.Errorf("%s: CSP %q allows inline script", name, v)
			}
		}
		if sandboxed != 1 {
			t.Errorf("%s: want exactly one sandbox script-src CSP, got %d: %q", name, sandboxed, w.Header().Values("Content-Security-Policy"))
		}
	}
}
