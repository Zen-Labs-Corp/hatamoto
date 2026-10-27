package render

import (
	"strings"
	"testing"
)

func searchView(typ string) *PageView {
	v := hierarchyView()
	v.Kind, v.Path, v.Query, v.SearchType = KindSearch, "/search", "theme", typ
	v.Items = []RelatedItem{{Title: "Layouts", URL: "/docs/themes/layouts", Excerpt: "How the layout works."}}
	return v
}

// The search form carries the type only when the view has one, so a plain
// search renders exactly as before.
func TestSearchFormHiddenType(t *testing.T) {
	t.Parallel()
	e := hierarchyEngine(t)
	hidden := `<input type="hidden" name="type" value="doc">`
	if out := renderToString(t, e, searchView("doc")); !strings.Contains(out, hidden) {
		t.Errorf("type missing from the form:\n%s", mainInner(t, out))
	}
	if out := renderToString(t, e, searchView("")); strings.Contains(out, `name="type"`) {
		t.Errorf("type emitted without SearchType:\n%s", mainInner(t, out))
	}
}

// A result without a date or author (a doc page) shows title and excerpt only,
// no empty meta line.
func TestSearchResultWithoutBlogChrome(t *testing.T) {
	t.Parallel()
	out := mainInner(t, renderToString(t, hierarchyEngine(t), searchView("doc")))
	if !strings.Contains(out, "How the layout works.") || strings.Contains(out, `class="hm-meta"`) {
		t.Errorf("unexpected result card:\n%s", out)
	}
}
