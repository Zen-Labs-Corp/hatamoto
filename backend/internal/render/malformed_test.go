package render

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func renderSections(t *testing.T, secs ...Section) string {
	t.Helper()
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.Sections = secs
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatalf("page failed: %v", err)
	}
	return buf.String()
}

// Stored section content is not schema-validated; none of these may fail the page.
func TestMalformedSectionContentDoesNotFailPage(t *testing.T) {
	for name, s := range map[string]Section{
		"hero cta without url": {ID: "a", Type: "hero", Content: map[string]any{"headline": "H", "primaryCta": "Go"}},
		"hero ctas without url": {ID: "a", Type: "hero", Content: map[string]any{"headline": "H",
			"ctas": []any{map[string]any{"text": "Go"}}}},
		"cta numeric url":   {ID: "a", Type: "cta", Content: map[string]any{"buttonText": "Go", "buttonUrl": 42.0}},
		"cta without url":   {ID: "a", Type: "cta", Content: map[string]any{"buttonText": "Go"}},
		"faqs of strings":   {ID: "a", Type: "faq", Content: map[string]any{"faqs": []any{"q"}}},
		"hero image object": {ID: "a", Type: "hero", Content: map[string]any{"headline": "H", "heroImage": map[string]any{"url": "x"}}},
	} {
		html := renderSections(t, s)
		if !strings.Contains(html, "data-hm-contract") {
			t.Errorf("%s: no page", name)
		}
	}
}

func TestFailingSectionRendersAsCommentOnly(t *testing.T) {
	good := Section{ID: "g", Type: "content", Content: map[string]any{"body": "<p>still here</p>"}}
	// A scalar where the template ranges over a list cannot be coerced away.
	bad := Section{ID: "b", Type: "faq", Content: map[string]any{"faqs": "not a list"}}
	html := renderSections(t, bad, good)
	if !strings.Contains(html, "still here") {
		t.Error("healthy section lost")
	}
	if !strings.Contains(html, "<!-- section render failed: faq -->") {
		t.Errorf("no failure comment in %s", mainInner(t, html))
	}
	if !strings.Contains(html, SectionFailedMarker) {
		t.Error("SectionFailedMarker no longer matches the failure comment; degraded pages would be cached")
	}
	if strings.Contains(html, `data-hm-section="b"`) {
		t.Error("failed section still emitted markup")
	}
}

func TestHelpersTolerateNonStrings(t *testing.T) {
	for _, v := range []any{nil, 3.0, map[string]any{"a": 1}, []any{"x"}, true} {
		if got := safeHref(v); got != "#" {
			t.Errorf("safeHref(%v)=%q", v, got)
		}
		if got := imgSrc(v); got != "" {
			t.Errorf("imgSrc(%v)=%q", v, got)
		}
		if embedAllowed(v) {
			t.Errorf("embedAllowed(%v)", v)
		}
		if got := telHref(v); got != "#" {
			t.Errorf("telHref(%v)=%q", v, got)
		}
	}
}

func TestPipelineDropsNonObjectItems(t *testing.T) {
	th := pipelineTheme(t)
	got := prepareSections(th, []Section{{ID: "a", Type: "faq", Content: map[string]any{
		"faqs": []any{"q", map[string]any{"question": "ok"}, 3.0},
	}}}, false)
	faqs, _ := got[0].Content["faqs"].([]any)
	if len(faqs) != 1 {
		t.Fatalf("faqs = %v, want only the object item", faqs)
	}
}

// A code panel that is still being filled in (missing language and/or code)
// must render, not fail the whole section.
func TestCodePanelPartialContentRenders(t *testing.T) {
	for name, p := range map[string]any{
		"title only":   map[string]any{"title": "main.go"},
		"no language":  map[string]any{"code": "x := 1"},
		"no code":      map[string]any{"language": "go"},
		"numeric code": map[string]any{"language": "go", "code": 42.0},
	} {
		for _, s := range []Section{
			{ID: "a", Type: "split", Content: map[string]any{"heading": "H", "panel": p}},
			{ID: "a", Type: "hero", Content: map[string]any{"headline": "H", "layout": "split", "panel": p}},
		} {
			if html := renderSections(t, s); strings.Contains(html, "section render failed") {
				t.Errorf("%s/%s: section failed to render", s.Type, name)
			}
		}
	}
}

type ctxLog struct {
	slog.Handler
	got *[]context.Context
}

func (h ctxLog) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		*h.got = append(*h.got, ctx)
	}
	return nil
}

// A section that fails is logged with the request's context, so the smoke
// check can tell its own failures from a visitor's.
func TestSectionFailureIsLoggedWithTheRequestContext(t *testing.T) {
	type key struct{}
	dir := t.TempDir()
	writeFile(t, dir, "sections/boom.html", `{{define "section-boom"}}{{call .Content.nope}}{{end}}`)
	var got []context.Context
	log := slog.New(ctxLog{slog.NewTextHandler(io.Discard, nil), &got})
	e := newTestEngine(t, Options{Layers: []fs.FS{os.DirFS(dir)}, Logger: log})
	v := minimalView()
	v.Sections = []Section{{ID: "a", Type: "boom", Content: map[string]any{}}}
	v.Ctx = context.WithValue(context.Background(), key{}, true)
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Value(key{}) == nil {
		t.Fatalf("section failure logged with ctx %v (output: %s)", got, buf.String())
	}
}
