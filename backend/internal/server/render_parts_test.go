package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/render"
)

const testHeaderPart = "11111111-0000-4000-8000-00000000000a"

type headerParts struct{}

func (headerParts) Block(context.Context, string) (*builder.Doc, bool) { return nil, false }
func (headerParts) Part(_ context.Context, slot, id string) (*builder.Doc, bool) {
	if slot != builder.SlotHeader || id != testHeaderPart {
		return nil, false
	}
	return &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "h", T: "text", P: map[string]any{"text": "ASSIGNED-HEADER"}}}}, true
}

// An opted-in theme draws the assigned header in every preview except the one
// of a theme_part itself, which would show the part twice.
func TestPreviewThemePartCanvasHasNoAssignedHeader(t *testing.T) {
	theme := fstest.MapFS{"theme.json": {Data: []byte(`{"name":"p","version":"1.0.0","builder":{"parts":["header"]}}`)}}
	engine, err := render.New(render.Options{Refs: headerParts{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.SetTheme(theme); err != nil {
		t.Fatal(err)
	}
	h := &renderAdminHandlers{
		engine: engine,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		chrome: func(context.Context, []render.Section) (render.SiteView, map[string]render.FormView, error) {
			sv := render.SiteView{Name: "Draft Site"}
			sv.Design.Parts = map[string]string{builder.SlotHeader: testHeaderPart}
			return sv, nil, nil
		},
		baseURL: func(*http.Request) string { return "https://x.io" },
	}
	const attrs = `"attributes":{"page_doc":{"v":2,"nodes":[{"id":"a","t":"text","p":{"text":"body"}}]}}`
	page := preview(t, h, `{"schema":"page",`+attrs+`}`)
	part := preview(t, h, `{"schema":"theme_part",`+attrs+`}`)
	if page.Code != 200 || part.Code != 200 {
		t.Fatalf("status %d/%d", page.Code, part.Code)
	}
	if !strings.Contains(page.Body.String(), "ASSIGNED-HEADER") {
		t.Error("a page preview must draw the assigned header")
	}
	if strings.Contains(part.Body.String(), "ASSIGNED-HEADER") || !strings.Contains(part.Body.String(), `class="hm-header"`) {
		t.Error("a theme_part preview must show the theme's own header")
	}
}
