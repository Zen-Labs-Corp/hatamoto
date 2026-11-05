package render

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const testCustomCSS = "a{color:red}\n"

func TestRenderCustomCSSLink(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	plain := renderToString(t, e, minimalView())
	if strings.Contains(plain, "custom.") {
		t.Fatal("a page without custom CSS links a custom stylesheet")
	}
	name := CustomCSSName(testCustomCSS)
	v := minimalView()
	v.Site.CustomCSSName = name
	out := renderToString(t, e, v)
	link := `<link rel="stylesheet" href="/theme/assets/` + name + `">`
	if strings.Count(out, link) != 1 {
		t.Fatalf("want the link once:\n%s", out)
	}
	if strings.Replace(out, link, "", 1) != plain {
		t.Error("custom CSS changed more than the link")
	}
	head := out[:strings.Index(out, "</head>")]
	if strings.Index(head, link) < strings.Index(head, "/theme/assets/theme.") {
		t.Errorf("custom link comes before the theme stylesheet:\n%s", head)
	}
	if strings.Count(out, "<style") != strings.Count(plain, "<style") || strings.Count(out, " style=") != strings.Count(plain, " style=") {
		t.Error("custom CSS added inline style")
	}
	compareGolden(t, "head-custom-css", strings.ReplaceAll(head, name, "custom.HASH.css")+"</head>\n")
}

func TestAssetHandlerCustomCSS(t *testing.T) {
	e := newTestEngine(t, Options{})
	h := e.AssetHandler()
	get := func(name string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, AssetPrefix+name, nil))
		return w
	}
	t.Cleanup(func() { SetCustomCSS(nil) })
	name := CustomCSSName(testCustomCSS)

	if w := get(name); w.Code != http.StatusNotFound {
		t.Errorf("none saved: %d, want 404", w.Code)
	}
	css := testCustomCSS
	SetCustomCSS(func(context.Context) string { return css })
	w := get(name)
	if w.Code != http.StatusOK || w.Body.String() != testCustomCSS {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control %q", cc)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
	if w := get("custom.000000000000.css"); w.Code != http.StatusNotFound {
		t.Errorf("unknown hash: %d, want 404", w.Code)
	}
	css = "a{color:blue}" // saved again: the old hash is stale
	if w := get(name); w.Code != http.StatusNotFound {
		t.Errorf("stale hash: %d, want 404", w.Code)
	}
	css = "@import 'x';" // refused at serve time too
	if w := get(CustomCSSName("@import 'x';")); w.Code != http.StatusNotFound || CustomCSSName(css) != "" {
		t.Errorf("refused CSS served: %d", w.Code)
	}
}

func TestAssetHandlerThemeCustomCSSNotShadowed(t *testing.T) {
	layer := fstest.MapFS{
		"assets/custom.css":      mapFile(".t{}"),
		"assets/custom.blog.css": mapFile(".b{}"),
	}
	h := newTestEngine(t, Options{Layers: []fs.FS{layer}}).AssetHandler()
	t.Cleanup(func() { SetCustomCSS(nil) })
	SetCustomCSS(func(context.Context) string { return testCustomCSS })
	for name, want := range map[string]string{"custom.css": ".t{}", "custom.blog.css": ".b{}"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, AssetPrefix+name, nil))
		if w.Code != http.StatusOK || w.Body.String() != want {
			t.Errorf("%s: %d %q, want 200 %q", name, w.Code, w.Body.String(), want)
		}
	}
}
