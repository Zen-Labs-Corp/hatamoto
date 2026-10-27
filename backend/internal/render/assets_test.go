package render

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

func assetTheme(t *testing.T, base string) *Theme {
	t.Helper()
	fsys := fstest.MapFS{
		"assets/css/base.css":           mapFile(base),
		"assets/css/sections/zeta.css":  mapFile(".zeta{}"),
		"assets/css/sections/alpha.css": mapFile(".alpha{}"),
		"assets/js/hm.js":               mapFile("console.log(1)"),
		"assets/img/logo.svg":           mapFile("<svg/>"),
	}
	th, err := LoadTheme(Chain{fsys})
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func TestAssetsBuildBundleOrderAndHash(t *testing.T) {
	t.Parallel()
	css, js, name := buildBundle(assetTheme(t, ".base{}"))
	got := string(css)
	if !(strings.Index(got, ".base") < strings.Index(got, ".alpha") && strings.Index(got, ".alpha") < strings.Index(got, ".zeta")) {
		t.Fatalf("order wrong: %q", got)
	}
	if string(js) != "console.log(1)" {
		t.Fatalf("js: %q", js)
	}
	if !regexp.MustCompile(`^theme\.[0-9a-f]{12}\.css$`).MatchString(name) {
		t.Fatalf("name %q", name)
	}
	_, _, again := buildBundle(assetTheme(t, ".base{}"))
	_, _, changed := buildBundle(assetTheme(t, ".base2{}"))
	if name != again || name == changed {
		t.Fatalf("hash stability: %s %s %s", name, again, changed)
	}
}

func TestAssetsHandler(t *testing.T) {
	t.Parallel()
	th := assetTheme(t, ".base{}")
	h := th.AssetHandler()
	_, _, name := buildBundle(th)

	get := func(p string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AssetPrefix+p, nil))
		return rec
	}

	rec := get(name)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("bundle: %d %v", rec.Code, rec.Header())
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("missing ACAO: %v", rec.Header())
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") || rec.Header().Get("ETag") == "" {
		t.Fatalf("headers: %v", rec.Header())
	}
	if !strings.Contains(rec.Body.String(), ".zeta{}") {
		t.Fatalf("body: %s", rec.Body.String())
	}

	// Conditional request hits 304.
	req := httptest.NewRequest(http.MethodGet, AssetPrefix+name, nil)
	req.Header.Set("If-None-Match", rec.Header().Get("ETag"))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("etag: %d", rec2.Code)
	}

	for _, p := range []string{"hm.js", "img/logo.svg"} {
		r := get(p)
		if r.Code != 200 || r.Header().Get("Cache-Control") != "public, max-age=300" {
			t.Errorf("%s: %d %v", p, r.Code, r.Header())
		}
	}
	for _, p := range []string{"nope.css", "img", "img/", "../theme.json", "", "css/../../x"} {
		if r := get(p); r.Code != http.StatusNotFound {
			t.Errorf("%q: want 404 got %d", p, r.Code)
		}
	}
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, httptest.NewRequest(http.MethodPost, AssetPrefix+name, nil))
	if rec3.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", rec3.Code)
	}
}

func TestAssetHandlerServesEmbeddedFonts(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Geist-Variable.woff2", "GeistMono-Variable.woff2"} {
		w := httptest.NewRecorder()
		e.AssetHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, AssetPrefix+"fonts/"+name, nil))
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "font/woff2" || w.Body.Len() < 10000 {
			t.Errorf("%s: %d %q len=%d", name, w.Code, w.Header().Get("Content-Type"), w.Body.Len())
		}
	}
}
