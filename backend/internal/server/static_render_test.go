package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/statichost"
)

// staticPageEngine renders one fixed document for pages, 404s and errors.
type staticPageEngine struct{ doc string }

func (e staticPageEngine) RenderPage(w io.Writer, _ *render.PageView) error {
	_, err := io.WriteString(w, e.doc)
	return err
}
func (e staticPageEngine) RenderNotFound(w io.Writer, v *render.PageView) error {
	return e.RenderPage(w, v)
}
func (e staticPageEngine) RenderError(w io.Writer, v *render.PageView, _ int) error {
	return e.RenderPage(w, v)
}
func (e staticPageEngine) Version() string { return "static-test" }

const staticRenderDoc = `<!doctype html><html><head><title>t</title></head><body>` +
	`<a href="/about">about</a><form action="/forms/contact/submit"></form><form action="/search"></form>` +
	`<script type="application/ld+json">{"urlTemplate":"https://static.example.net/search?q={search_term_string}"}</script>` +
	`<form action="/-/comments/` + testEntityID + `" data-hm-cform><input value="__hmcf_tok__"></form></body></html>`

func newStaticRenderFixture(t *testing.T, static bool) *renderFixture {
	t.Helper()
	f := newRenderFixture(t)
	f.h.engine = staticPageEngine{doc: staticRenderDoc}
	f.h.hosting = &statichost.Holder{}
	if static {
		f.h.hosting.Store(statichost.StaticHosting{Static: true, Live: testStaticLive, Site: testStaticSite})
	}
	f.next = okPage("About")
	return f
}

func TestStaticRenderPageRewritten(t *testing.T) {
	f := newStaticRenderFixture(t, true)
	first := f.get("/about")
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d", first.Code)
	}
	body := first.Body.String()
	for _, want := range []string{
		`<head><meta name="hm-live-origin" content="` + testStaticLive + `">`,
		`action="` + testStaticLive + `/forms/contact/submit"`,
		`action="` + testStaticLive + `/search"`,
		`<a href="` + testStaticSite + `/about">`,
		`"` + testStaticLive + `/search?q={search_term_string}"`,
		`data-hm-live-form="` + testStaticLive + commentFormLivePrefix + testEntityID + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{render.CommentFormMarker, "__hmcf_tok__"} {
		if strings.Contains(body, bad) {
			t.Errorf("body contains %q", bad)
		}
	}
	hd := first.Header()
	if !strings.HasPrefix(hd.Get("Cache-Control"), "public") {
		t.Errorf("Cache-Control = %q", hd.Get("Cache-Control"))
	}
	if hd.Get("ETag") != "" || hd.Get("Last-Modified") != "" {
		t.Errorf("validators set: ETag=%q Last-Modified=%q", hd.Get("ETag"), hd.Get("Last-Modified"))
	}
	if len(f.cache.sets) != 1 {
		t.Fatalf("cache sets = %v", f.cache.sets)
	}
	// The cache keeps the template bytes, so a mode switch needs no purge.
	if string(f.cache.data[f.cache.sets[0]]) != staticRenderDoc {
		t.Errorf("cache holds rewritten bytes")
	}
	second := f.get("/about")
	if f.resolves != 1 || second.Body.String() != body {
		t.Errorf("cache hit differs (resolves=%d)", f.resolves)
	}
	if second.Header().Get("ETag") != "" {
		t.Errorf("cache hit carries ETag")
	}
}

func TestStaticRenderServedModeUnchanged(t *testing.T) {
	zero := newStaticRenderFixture(t, false)
	nilHolder := newStaticRenderFixture(t, false)
	nilHolder.h.hosting = nil
	a, b := zero.get("/about"), nilHolder.get("/about")
	if a.Body.String() != staticRenderDoc || b.Body.String() != staticRenderDoc {
		t.Errorf("served body changed:\n%s", a.Body)
	}
	if a.Header().Get("Cache-Control") != b.Header().Get("Cache-Control") || a.Header().Get("ETag") != b.Header().Get("ETag") {
		t.Errorf("headers differ: %v vs %v", a.Header(), b.Header())
	}
	if a.Header().Get("ETag") == "" {
		t.Errorf("served mode lost its validators")
	}
}

func TestStaticRenderNotFoundRewritten(t *testing.T) {
	f := newStaticRenderFixture(t, true)
	f.next = func() (*routeResult, pageOutcome, error) {
		return &routeResult{NotFound: true}, pageOutcome{Status: 404}, nil
	}
	w := f.get("/nope")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `name="hm-live-origin"`) || !strings.Contains(w.Body.String(), `action="`+testStaticLive+`/search"`) {
		t.Errorf("404 not rewritten: %s", w.Body)
	}
}

func TestStaticRenderExportKeepsRelative(t *testing.T) {
	f := newStaticRenderFixture(t, true)
	export := func(r *http.Request) { *r = *r.WithContext(statichost.WithExport(context.Background())) }
	if body := f.get("/about", export).Body.String(); !strings.Contains(body, `<a href="/about">`) {
		t.Errorf("export lost relative link: %s", body)
	}
	if body := f.get("/about").Body.String(); !strings.Contains(body, `<a href="`+testStaticSite+`/about">`) {
		t.Errorf("non-export link not absolute: %s", body)
	}
}

func TestStaticRenderHeadAndPostUntouched(t *testing.T) {
	f := newStaticRenderFixture(t, true)
	r := httptest.NewRequest(http.MethodPost, "/about", nil)
	if _, ok := f.h.staticPage(r, []byte(staticRenderDoc)); ok {
		t.Errorf("POST rewritten")
	}
	r = httptest.NewRequest(http.MethodHead, "/about", nil)
	if _, ok := f.h.staticPage(r, []byte(staticRenderDoc)); !ok {
		t.Errorf("HEAD not rewritten")
	}
}
