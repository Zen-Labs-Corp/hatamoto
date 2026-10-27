package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

const (
	// two-column builder-only doc with a contact_form and an entity link
	builderOnlyAttrs = `{"page_doc":{"v":2,"nodes":[
	 {"id":"row","t":"container","p":{"tag":"div"},"c":[
	  {"id":"c1","t":"component","p":{"type":"contact_form","content":{"form_slug":"quote"}}},
	  {"id":"c2","t":"component","p":{"type":"content","content":{"content":"<a data-entity-id=\"%s\" href=\"/x\">x</a>"}}}]}]},
	 "page_sections":[{"id":"stale","type":"hero","content":{}}],"meta_title":"T"}`
	v1Attrs = `{"page_doc":{"v":2,"nodes":[{"id":"s1","t":"container","p":{"tag":"section","v1":true},"c":[
	  {"id":"k1","t":"component","p":{"type":"hero","title":"H","content":{"headline":"Hi"}}}]}]}}`
)

func docRoute(attrs string) *routeResult {
	return &routeResult{Page: &PublicPageResponse{Entity: &entity.Entity{ID: uuid.New(), Name: "P", Attributes: json.RawMessage(attrs)}}}
}

func TestPageViewPrefersBuilderDoc(t *testing.T) {
	target := uuid.New().String()
	attrs := fmt.Sprintf(builderOnlyAttrs, target)
	v := pageViewFromRoute(docRoute(attrs), "https://x.io", "/p")
	if v.Doc == nil || len(v.Sections) != 0 {
		t.Fatalf("builder-only: Doc=%v Sections=%v", v.Doc, v.Sections)
	}
	if _, ok := v.Attrs["page_doc"]; ok {
		t.Error("page_doc leaked into Attrs")
	}
	if _, ok := v.Attrs["page_sections"]; ok {
		t.Error("page_sections leaked into Attrs")
	}
	secs := viewSections(v.Sections, v.Doc)
	if len(secs) != 2 || secs[0].Type != "contact_form" || contactFormSlug(secs[0].Content) != "quote" {
		t.Fatalf("component sections = %+v", secs)
	}
	ids := linkIDs(secs[1].Content)
	if len(ids) != 1 || ids[0].String() != target {
		t.Errorf("link ids = %v", ids)
	}
}

func TestPageViewV1DocFeedsSections(t *testing.T) {
	v := pageViewFromRoute(docRoute(v1Attrs), "https://x.io", "/p")
	if v.Doc != nil || len(v.Sections) != 1 || v.Sections[0].Type != "hero" || v.Sections[0].ID != "k1" {
		t.Fatalf("v1 doc: Doc=%v Sections=%+v", v.Doc, v.Sections)
	}
}

func TestPageViewInvalidDocRendersEmpty(t *testing.T) {
	v := pageViewFromRoute(docRoute(`{"page_doc":{"v":9},"page_sections":[{"id":"a","type":"hero","content":{}}]}`), "", "/p")
	if v.Doc != nil || len(v.Sections) != 0 {
		t.Fatalf("frozen mirror must not leak: Doc=%v Sections=%+v", v.Doc, v.Sections)
	}
}

func TestAttrsForViewDropsDoc(t *testing.T) {
	m := attrsForView(json.RawMessage(`{"page_doc":{},"page_sections":[],"k":1}`))
	if len(m) != 1 || m["k"] == nil {
		t.Errorf("attrs = %v", m)
	}
}

func TestPageFilterSkippedForBuilderDoc(t *testing.T) {
	h := &renderHandlers{} // nil pageFilter would panic if the chain ran
	v := &render.PageView{Doc: pageViewFromRoute(docRoute(builderOnlyAttrs), "", "/p").Doc}
	if c := h.applyPageFilter(httptest.NewRequest("GET", "/p", nil), "/p", v); c != nil || v.Doc == nil {
		t.Errorf("filter ran: cookies=%v", c)
	}
}

func TestPreviewAttributesViewUsesDoc(t *testing.T) {
	h := &renderAdminHandlers{chrome: func(_ context.Context, secs []render.Section) (render.SiteView, map[string]render.FormView, error) {
		if len(secs) != 2 {
			t.Errorf("chrome got %d sections, want the 2 doc components", len(secs))
		}
		return render.SiteView{}, nil, nil
	}, baseURL: func(*http.Request) string { return "" }}
	req := &previewRequest{Attributes: json.RawMessage(fmt.Sprintf(builderOnlyAttrs, uuid.New().String())), Path: "/p"}
	v, err := h.attributesView(httptest.NewRequest("POST", "/", nil), req)
	if err != nil || v.Doc == nil || len(v.Sections) != 0 {
		t.Fatalf("v=%+v err=%v", v, err)
	}
}

// render.page hands plugins the page's locale: public pages are always in the
// site language, which the view carries.
func TestPageFilterFillsEntityLocale(t *testing.T) {
	engine := hooks.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	var got string
	hooks.OnFilter(engine.For("spy"), sdk.RenderPage, 10, func(_ context.Context, in sdk.PageContext) (sdk.PageContext, error) {
		got = in.Entity.Locale
		return in, nil
	})
	h := &renderHandlers{pageFilter: pluginhost.NewPageFilter(engine)}
	v := &render.PageView{Entity: &render.EntityView{ID: "e1", Slug: "s", Title: "T", Schema: "page"}, Site: render.SiteView{Language: "fr"}}
	h.applyPageFilter(httptest.NewRequest("GET", "/s", nil), "/s", v)
	if got != "fr" {
		t.Fatalf("render.page entity locale = %q, want fr", got)
	}
}
