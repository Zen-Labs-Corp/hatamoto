package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/render"
)

// pagedData is a DataSource with 3 pages of 2 posts.
type pagedData struct{}

func (pagedData) Recent(string, int) []render.RelatedItem { return nil }
func (pagedData) Terms(string, int) []render.TermView     { return nil }
func (pagedData) Menu(string) []render.NavItem            { return nil }
func (pagedData) ImageVariants(string) []render.ImageVariant {
	return nil
}
func (pagedData) AttrValues(string, string, int) []render.AttrCount { return nil }
func (pagedData) Query(q render.QuerySpec) render.QueryResult {
	return render.QueryResult{
		Items: []render.RelatedItem{{Title: "A", URL: "/a"}, {Title: "B", URL: "/b"}},
		Total: 6, Page: min(q.Page, 3), Pages: 3, Limit: 2,
	}
}

func loopDoc(t *testing.T) *builder.Doc {
	t.Helper()
	d, err := builder.Parse([]byte(`{"v":2,"nodes":[{"id":"lp","t":"loop","p":{"schema":"post","limit":2,"paging":"pages"},"c":[
		{"id":"lh","t":"heading","b":{"text":"loop.item.title"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// pagedFixture serves every path as a page; withLoop picks a doc with a paged
// loop or one with a plain heading.
func pagedFixture(t *testing.T, withLoop bool) *renderFixture {
	t.Helper()
	engine, err := render.New(render.Options{Data: pagedData{}})
	if err != nil {
		t.Fatal(err)
	}
	f := newRenderFixture(t)
	f.h.engine = engine
	f.next = okPage("News")
	build := f.h.buildView
	f.h.buildView = func(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
		v, err := build(ctx, r, rr)
		if err != nil {
			return nil, err
		}
		v.PageNum = min(listingPage(r), render.MaxQueryPage)
		v.PagedLoop, v.LoopOutOfRange = new(bool), new(bool)
		v.Doc = loopDoc(t)
		if !withLoop {
			v.Doc = &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "h", T: "heading", P: map[string]any{"text": "Plain"}}}}
		}
		return v, nil
	}
	return f
}

func TestPagedLoopPageIsCachedOnSecondRequest(t *testing.T) {
	f := pagedFixture(t, true)
	w := f.get("/news?page=2")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `rel="prev"`) {
		t.Fatalf("status %d body %.300s", w.Code, w.Body.String())
	}
	if len(f.cache.sets) != 1 || !strings.HasSuffix(f.cache.sets[0], ":/news?page=2") {
		t.Fatalf("sets = %v", f.cache.sets)
	}
	w2 := f.get("/news?page=2")
	if f.resolves != 1 || w2.Body.String() != w.Body.String() {
		t.Errorf("second request must be a cache hit (resolves=%d)", f.resolves)
	}
	// page 1 of the same path is a different key
	f.get("/news")
	if len(f.cache.sets) != 2 || !strings.HasSuffix(f.cache.sets[1], ":/news") {
		t.Errorf("sets = %v", f.cache.sets)
	}
}

func TestPagedQueryWithoutLoopIsNeverStored(t *testing.T) {
	f := pagedFixture(t, false)
	if w := f.get("/about?page=2"); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if len(f.cache.sets) != 0 {
		t.Errorf("a page without a paged loop was stored: %v", f.cache.sets)
	}
}

func TestPagedLoopOutOfRangeIs404AndNotStored(t *testing.T) {
	f := pagedFixture(t, true)
	w := f.get("/news?page=99")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d", w.Code)
	}
	if len(f.cache.sets) != 0 || len(f.notFound) != 0 {
		t.Errorf("sets=%v logged 404s=%v (quiet 404 expected)", f.cache.sets, f.notFound)
	}
	if w := f.get("/news?page=3"); w.Code != 200 {
		t.Errorf("last page: status %d", w.Code)
	}
}

func TestPagedQueryOnlyCanonicalFormIsCached(t *testing.T) {
	for _, q := range []string{"page=2&x=1", "x=1&page=2", "page=02", "page=1", "page=0", "page=abc", "page=2&page=3", "page=1001"} {
		f := pagedFixture(t, true)
		f.get("/news?" + q)
		if len(f.cache.sets) != 0 {
			t.Errorf("?%s was cached: %v", q, f.cache.sets)
		}
	}
	for q, want := range map[string]int{"page=2": 2, "page=1000": 1000, "page=1": 0, "page=01": 0, "page=2&x=1": 0, "": 0, "page=": 0} {
		if got := pagedQuery(q); got != want {
			t.Errorf("pagedQuery(%q) = %d, want %d", q, got, want)
		}
	}
}

// failingData answers every query like renderData does on a DB error: no items, one page, a failure.
type failingData struct {
	pagedData
	failures *int64
}

func (d failingData) Query(render.QuerySpec) render.QueryResult {
	*d.failures++
	return render.QueryResult{Pages: 1, Page: 1}
}

func TestPagedLoopQueryFailureIsNotA404(t *testing.T) {
	f := pagedFixture(t, true)
	var failures int64
	engine, err := render.New(render.Options{Data: failingData{failures: &failures}})
	if err != nil {
		t.Fatal(err)
	}
	f.h.engine = engine
	f.h.dataFailures = func() int64 { return failures }
	w := f.get("/news?page=2")
	if w.Code != http.StatusOK || len(f.cache.sets) != 0 {
		t.Errorf("status %d sets=%v: a failed query must serve a degraded, uncached page", w.Code, f.cache.sets)
	}
}
