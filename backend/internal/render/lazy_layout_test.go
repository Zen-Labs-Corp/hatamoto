package render

import (
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
)

const goodHome = `{{define "content"}}<i>home</i>{{end}}{{template "content" .}}`

// A layout nobody requests must still fail the theme check and SetTheme: the
// whole theme is validated even though layouts are built on demand.
func TestLazyLayoutsStillValidatedAtActivation(t *testing.T) {
	t.Parallel()
	for name, bad := range map[string]string{
		"unparsable":      `{{define "content"}}{{broken`,
		"without content": `<p>no content here</p>`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			good := t.TempDir()
			writeFile(t, good, "partials/scripts.html", `{{define "scripts"}}<i>good</i>{{end}}`)
			e := newTestEngine(t, Options{Layers: []fs.FS{os.DirFS(good)}})

			broken := t.TempDir()
			writeFile(t, broken, "layouts/archive-foo.html", bad)
			if err := e.CheckTheme(os.DirFS(broken)); err == nil {
				t.Fatal("CheckTheme must refuse an unused broken layout")
			}
			if err := e.SetTheme(os.DirFS(broken)); err == nil {
				t.Fatal("SetTheme must refuse an unused broken layout")
			}
			if out := renderToString(t, e, minimalView()); !strings.Contains(out, "<i>good</i>") {
				t.Errorf("previous theme should stay live:\n%s", out)
			}
		})
	}
}

// The same holds when the broken file is shadowed by a higher layer's copy and
// so can never be picked.
func TestLazyLayoutsValidateShadowedLayer(t *testing.T) {
	t.Parallel()
	for name, bad := range map[string]string{
		"unparsable":      `{{define "content"}}{{broken`,
		"without content": `<p>no content here</p>`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			top, lower := t.TempDir(), t.TempDir()
			writeFile(t, top, "layouts/home.html", goodHome)
			writeFile(t, lower, "layouts/home.html", bad)
			layers := []fs.FS{os.DirFS(top), os.DirFS(lower)}
			e := newTestEngine(t, Options{})
			if err := e.CheckTheme(layers...); err == nil {
				t.Fatal("CheckTheme must refuse a broken shadowed layout")
			}
			if err := e.SetTheme(layers...); err == nil {
				t.Fatal("SetTheme must refuse a broken shadowed layout")
			}
		})
	}
}

// Concurrent first requests for one layout build it exactly once; layouts are
// built independently and never execute the shared root.
func TestLazyLayoutBuildsOnce(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	c := e.current()
	k := layoutKey{0, "home"}
	if !c.has("home") {
		t.Fatal("default theme has no home layout")
	}
	before := c.builds.Load()
	const n = 32
	entries := make([]*layoutEntry, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			le, ok, err := c.layout(k)
			if !ok || err != nil {
				t.Errorf("layout: ok=%v err=%v", ok, err)
				return
			}
			entries[i] = le
		}()
	}
	wg.Wait()
	if got := c.builds.Load() - before; got != 1 {
		t.Fatalf("builds = %d, want 1", got)
	}
	for _, le := range entries {
		if le != entries[0] {
			t.Fatal("concurrent requests got different entries")
		}
	}
}

// Rendering different layouts back to back (and concurrently) only works if the
// root is cloned, never executed.
func TestLazyLayoutsRenderIndependently(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if out := renderToString(t, e, minimalView()); !strings.Contains(out, "<html") {
				t.Errorf("page not rendered:\n%s", out)
			}
			if out := renderToString(t, e, &PageView{Kind: KindNotFound, Site: minimalView().Site}); out == "" {
				t.Error("404 not rendered")
			}
		}()
	}
	wg.Wait()
	if err := e.smokeRender(e.chain); err != nil {
		t.Fatalf("smoke after lazy renders: %v", err)
	}
}
