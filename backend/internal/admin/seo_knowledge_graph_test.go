package admin

import (
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"strings"
	"testing"
)

func TestSEOKnowledgeGraphPreviewIsTheSiteGraph(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	graph := map[string]any{"@context": "https://schema.org", "@graph": []any{map[string]any{"@type": "Organization", "name": "Acme </script>"}}}
	var asked *http.Request
	s.a.deps.SiteGraph = func(r *http.Request) (map[string]any, error) { asked = r; return graph, nil }
	body := s.get(t, "/admin/settings/seo/knowledge-graph")
	want, _ := json.MarshalIndent(graph, "", "  ")
	if asked == nil || !strings.Contains(html.UnescapeString(body), string(want)) {
		t.Errorf("preview is not the marshalled site graph: %s", body)
	}
	if strings.Contains(body, "Acme </script>") {
		t.Error("the preview is not escaped")
	}
	for _, link := range []string{`href="/admin/settings/general"`, `href="/admin/settings/locales/business"`} {
		if !strings.Contains(body, link) {
			t.Errorf("no link %s", link)
		}
	}
	for _, label := range []string{"Organization name", "Logo", "Profile links", "Locales with their own business details"} {
		if !strings.Contains(body, label) {
			t.Errorf("no row %q", label)
		}
	}
	if strings.Contains(body, `action="/admin/settings/seo/knowledge-graph"`) {
		t.Error("the tab has a form")
	}
}

func TestSEOKnowledgeGraphWithoutPreview(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	body := s.get(t, "/admin/settings/seo/knowledge-graph")
	if !strings.Contains(body, "No preview is available.") {
		t.Error("a nil SiteGraph should hide the preview")
	}
	s.a.deps.SiteGraph = func(*http.Request) (map[string]any, error) { return nil, errors.New("boom") }
	if body := s.get(t, "/admin/settings/seo/knowledge-graph"); !strings.Contains(body, "could not be built") {
		t.Error("a failing graph should say so")
	}
}

func TestSEOKnowledgeGraphOwnerOnlyAndReadOnly(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	if rec := s.do("GET", "/admin/settings/seo/knowledge-graph", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d, want 403", rec.Code)
	}
	if rec := s.do("POST", "/admin/settings/seo/knowledge-graph", "owner-token", nil, nil); rec.Code == 200 || rec.Code == 303 {
		t.Errorf("POST = %d: the tab has no save", rec.Code)
	}
}

// The profile count is the sameAs union of the Organization node: theme social
// links, business profile links and the Google Business Profile URL, each once,
// http(s) only; the row links to General.
func TestSEOKnowledgeGraphProfileLinksCount(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	cfg := &s.a.deps.Site.(*fakeSite).cfg
	cfg.ThemeExtra = []byte(`{"socialLinks":{"x":"https://x.example/a","bad":"javascript:alert(1)","dup":"https://dup.example"}}`)
	cfg.BusinessProfile = []byte(`{"sameas":["https://dup.example","https://b.example"],"address":{"gbp_url":"https://maps.example/c"}}`)
	body := s.get(t, "/admin/settings/seo/knowledge-graph")
	i := strings.Index(body, "Profile links")
	row := body[i : i+strings.Index(body[i:], "</tr>")]
	if !strings.Contains(row, ">4<") || !strings.Contains(row, `href="/admin/settings/general"`) {
		t.Errorf("profile row = %s, want 4 links and a General link", row)
	}
}
