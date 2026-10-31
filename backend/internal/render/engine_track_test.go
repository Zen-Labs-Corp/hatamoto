package render

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const trackTagStr = `<script src="/theme/assets/hm-track.js" defer></script>`

// themeWithJS is the default theme with its hm.js replaced by js ("" = no JS).
func themeWithJS(t *testing.T, js string) *Engine {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "assets/js/hm.js", js)
	return newTestEngine(t, Options{Layers: []fs.FS{os.DirFS(dir)}})
}

func TestEngineTrackTagOncePerPublicPage(t *testing.T) {
	t.Parallel()
	engines := map[string]*Engine{
		"no-js theme": themeWithJS(t, ""),
		"theme js":    themeWithJS(t, "console.log(1)"),
		"default":     newTestEngine(t, Options{}),
	}
	for name, e := range engines {
		if n := strings.Count(renderToString(t, e, minimalView()), trackTagStr); n != 1 {
			t.Errorf("%s: beacon tag count = %d, want 1", name, n)
		}
	}
}

func TestEngineTrackTagSkippedInEditorFrames(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	for name, mod := range map[string]func(*PageView){
		"preview": func(v *PageView) { v.Preview = true },
		"canvas":  func(v *PageView) { v.Canvas = true },
	} {
		v := minimalView()
		mod(v)
		if out := renderToString(t, e, v); strings.Contains(out, "hm-track.js") {
			t.Errorf("%s render carries the beacon", name)
		}
	}
}

func TestEngineTrackTagSkippedForLegacyBeaconTheme(t *testing.T) {
	t.Parallel()
	e := themeWithJS(t, "navigator.sendBeacon('/api/v1/track', b)")
	if out := renderToString(t, e, minimalView()); strings.Contains(out, "hm-track.js") {
		t.Fatalf("legacy theme must keep its own beacon, not get a second sender")
	}
	SetPublicAPIPrefix("/x/api")
	defer SetPublicAPIPrefix("")
	e = themeWithJS(t, "navigator.sendBeacon('/api/v1/track', b)")
	if out := renderToString(t, e, minimalView()); strings.Contains(out, "hm-track.js") {
		t.Fatalf("legacy detection must survive the API prefix rewrite")
	}
}

// A theme that never prints plugin-body still gets the tag, spliced before </body>.
func TestEngineTrackTagSplicedWhenThemeOmitsPluginBody(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "partials/plugin-assets.html", `{{define "plugin-head"}}{{end}}{{define "plugin-body"}}{{end}}`)
	e := newTestEngine(t, Options{Layers: []fs.FS{os.DirFS(dir)}})
	out := renderToString(t, e, minimalView())
	if n := strings.Count(out, trackTagStr); n != 1 || strings.Index(out, trackTagStr) > strings.LastIndex(out, "</body>") {
		t.Fatalf("beacon tag count = %d (want 1, before </body>):\n%s", n, out)
	}
}

func TestTrackScriptServed(t *testing.T) {
	e := newTestEngine(t, Options{})
	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		e.AssetHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AssetPrefix+TrackScriptName, nil))
		return rec
	}
	SetPublicAPIPrefix("/x/api")
	rec := get()
	SetPublicAPIPrefix("")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "'/x/api/track'") || !strings.Contains(body, "var ENGAGE = true;") {
		t.Fatalf("status %d, body:\n%s", rec.Code, body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != cacheShort {
		t.Errorf("cache-control %q", cc)
	}
	SetTrackEngagement(func() bool { return false })
	defer SetTrackEngagement(nil)
	if body := get().Body.String(); !strings.Contains(body, "var ENGAGE = false;") {
		t.Error("engagement opt-out not applied to the served script")
	}
}
