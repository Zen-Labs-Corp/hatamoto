package render

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/builder"
)

// loopData is a DataSource whose Query is scripted: it serves n stub items and
// records every call.
type loopData struct {
	fakeData
	n        int
	pages    int
	specs    []QuerySpec
	prefetch [][]string
}

func (d *loopData) Query(q QuerySpec) QueryResult {
	d.specs = append(d.specs, q)
	items := make([]RelatedItem, d.n)
	for i := range items {
		items[i] = RelatedItem{
			Title: fmt.Sprintf("Post %d", i+1), URL: fmt.Sprintf("/blog/p%d", i+1), Excerpt: "Intro <b>",
			Image:       fmt.Sprintf("/media/%d.jpg", i+1),
			PublishedAt: time.Date(2026, 1, i+1, 0, 0, 0, 0, time.UTC),
		}
	}
	pages := max(d.pages, 1)
	return QueryResult{Items: items, Total: d.n * pages, Page: min(q.Page, pages), Pages: pages, Limit: q.Limit}
}

func (d *loopData) PrefetchVariants(urls []string) { d.prefetch = append(d.prefetch, urls) }

func renderDocView(t *testing.T, v *PageView, d *builder.Doc, canvas bool) string {
	t.Helper()
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	v.Doc, v.Canvas = d, canvas
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// widgetFixtureView is the view the widget goldens render with: the loop
// fixture sits on an archive of 3 items and 2 pages; every other one on the
// bare view.
func widgetFixtureView(name string) *PageView {
	v := minimalView()
	if name == "loop" {
		v.Items = []RelatedItem{
			{Title: "First post", URL: "/blog/first", Excerpt: "A short intro."},
			{Title: "Second <post>", URL: "/blog/second", Excerpt: "More."},
			{Title: "Third", URL: "/blog/third", Excerpt: "Last."},
		}
		v.Path = "/blog"
		v.Pagination = NewPagination("/blog", nil, 2, 3, 7)
	}
	return v
}

func loopNode(p map[string]any, kids ...*builder.Node) *builder.Node {
	return &builder.Node{ID: "lp", T: "loop", P: p, C: kids}
}

func itemKids() []*builder.Node {
	return []*builder.Node{
		{ID: "lh", T: "heading", B: map[string]string{"text": "loop.item.title"}},
		{ID: "lb", T: "button", P: map[string]any{"text": "More"}, B: map[string]string{"href": "loop.item.url"}},
		{ID: "le", T: "loopempty", C: []*builder.Node{{ID: "let", T: "text", P: map[string]any{"text": "Nothing here"}}}},
	}
}

func renderLoop(t *testing.T, ds DataSource, v *PageView, n *builder.Node, canvas bool) string {
	t.Helper()
	e := newTestEngine(t, Options{Data: ds})
	v.Canvas = canvas
	var b bytes.Buffer
	if err := e.RenderNode(&b, v, n, canvas); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestLoopRendersItemsPerQuery(t *testing.T) {
	ds := &loopData{n: 3}
	v := minimalView()
	v.EntityID, v.SchemaSlug = "11111111-1111-1111-1111-111111111111", "page"
	got := renderLoop(t, ds, v, loopNode(map[string]any{
		"schema": "post", "term": "category:news", "author": "ada", "order": "updated", "limit": float64(3), "offset": float64(2),
		"exclude_current": true,
	}, itemKids()...), false)
	if len(ds.specs) != 1 {
		t.Fatalf("Query called %d times", len(ds.specs))
	}
	want := QuerySpec{Schema: "post", Term: "category:news", Author: "ada", Order: "-updated_at", Limit: 3, Page: 1, Offset: 2,
		ExcludeID: v.EntityID}
	if ds.specs[0] != want {
		t.Errorf("spec = %+v, want %+v", ds.specs[0], want)
	}
	if n := strings.Count(got, `class="hm-loop__item"`); n != 3 {
		t.Errorf("items = %d in %s", n, got)
	}
	for _, w := range []string{">Post 1<", ">Post 3<", `href="/blog/p2"`} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q", w)
		}
	}
	if strings.Contains(got, "Nothing here") || strings.Contains(got, "data-hm-") || strings.Contains(got, "hm-pager") {
		t.Errorf("unexpected markup: %s", got)
	}
	if len(ds.prefetch) != 1 || len(ds.prefetch[0]) != 3 {
		t.Errorf("prefetch = %v", ds.prefetch)
	}
	if v.scope != nil {
		t.Error("scope leaked")
	}
}

func TestLoopDefaultsAndContext(t *testing.T) {
	ds := &loopData{n: 1}
	v := minimalView()
	v.SchemaSlug = "event"
	renderLoop(t, ds, v, loopNode(map[string]any{"context": true}, itemKids()...), false)
	renderLoop(t, ds, v, loopNode(map[string]any{"schema": "post", "limit": float64(99)}, itemKids()...), false) // out of range: default
	if ds.specs[0].Schema != "event" || ds.specs[0].Limit != 12 || ds.specs[0].Order != "-published_at" || ds.specs[0].ExcludeID != "" {
		t.Errorf("context spec = %+v", ds.specs[0])
	}
	if ds.specs[1].Limit != 12 {
		t.Errorf("limit 99 → %d", ds.specs[1].Limit)
	}
	// no schema and no context: nothing to ask
	ds2 := &loopData{n: 1}
	got := renderLoop(t, ds2, minimalView(), loopNode(nil, itemKids()...), false)
	if len(ds2.specs) != 0 || !strings.Contains(got, "Nothing here") {
		t.Errorf("no schema: %d queries, %s", len(ds2.specs), got)
	}
}

func TestLoopEmptyShowsOnlyLoopempty(t *testing.T) {
	ds := &loopData{n: 0}
	got := renderLoop(t, ds, minimalView(), loopNode(map[string]any{"schema": "post"}, itemKids()...), false)
	if strings.Contains(got, "hm-loop__item") || !strings.Contains(got, `class="hm-n hm-loopempty hm-n-le"`) || !strings.Contains(got, "Nothing here") {
		t.Errorf("empty: %s", got)
	}
	// no Data source behaves like zero items
	e := newTestEngine(t, Options{})
	var b bytes.Buffer
	_ = e.RenderNode(&b, minimalView(), loopNode(map[string]any{"schema": "post"}, itemKids()...), false)
	if !strings.Contains(b.String(), "Nothing here") {
		t.Errorf("nil data: %s", b.String())
	}
}

func TestLoopCanvasMarkersOnFirstItemOnly(t *testing.T) {
	ds := &loopData{n: 6}
	got := renderLoop(t, ds, minimalView(), loopNode(map[string]any{"schema": "post"}, itemKids()...), true)
	if n := strings.Count(got, `class="hm-loop__item"`); n != 3 {
		t.Errorf("canvas items = %d, want 3", n)
	}
	if n := strings.Count(got, `data-hm-node="lh"`); n != 1 {
		t.Errorf("markers on heading = %d, want 1", n)
	}
	if !strings.Contains(got, `data-hm-node="lp"`) || strings.Contains(got, "No items match") {
		t.Errorf("loop marker / box: %s", got)
	}
	empty := renderLoop(t, &loopData{}, minimalView(), loopNode(map[string]any{"schema": "post"}, itemKids()...), true)
	if !strings.Contains(empty, "No items match") || !strings.Contains(empty, `data-hm-node="le"`) {
		t.Errorf("canvas empty: %s", empty)
	}
}

func TestLoopFromArchiveUsesViewItemsWithoutQuery(t *testing.T) {
	ds := &loopData{n: 9}
	v := widgetFixtureView("loop")
	got := renderLoop(t, ds, v, loopNode(map[string]any{"from": "archive", "schema": "post"}, itemKids()...), false)
	if len(ds.specs) != 0 {
		t.Errorf("archive loop queried: %v", ds.specs)
	}
	if !strings.Contains(got, ">First post<") || strings.Contains(got, "Post 1") || !strings.Contains(got, `aria-current="page"`) {
		t.Errorf("archive: %s", got)
	}
	if v.loopPaged != nil {
		t.Error("an archive loop must not own the page param")
	}
	// a single entity view has no items: empty state
	one := renderLoop(t, ds, minimalView(), loopNode(map[string]any{"from": "archive"}, itemKids()...), false)
	if !strings.Contains(one, "Nothing here") {
		t.Errorf("archive without items: %s", one)
	}
}

func TestLoopNestedRendersComment(t *testing.T) {
	ds := &loopData{n: 2}
	inner := loopNode(map[string]any{"schema": "post"}, itemKids()...)
	inner.ID = "inner"
	got := renderLoop(t, ds, minimalView(), loopNode(map[string]any{"schema": "post"}, inner), false)
	if strings.Count(got, "<!-- hm-loop: nested -->") != 2 || len(ds.specs) != 1 {
		t.Errorf("nested: %d queries\n%s", len(ds.specs), got)
	}
}

func TestLoopPagingOwnsPageParam(t *testing.T) {
	ds := &loopData{n: 4, pages: 3}
	v := minimalView()
	v.Path, v.PageNum = "/news", 2
	paged := loopNode(map[string]any{"schema": "post", "paging": "pages", "limit": float64(4)}, itemKids()...)
	second := loopNode(map[string]any{"schema": "post", "paging": "pages"}, itemKids()...)
	second.ID = "lp2"
	got := renderLoop(t, ds, v, paged, false)
	if ds.specs[0].Page != 2 {
		t.Errorf("page = %d", ds.specs[0].Page)
	}
	for _, w := range []string{`<nav class="hm-pager" aria-label="Pagination">`, `<a href="/news">Previous</a>`, `<a href="/news?page=3">Next</a>`, `<a href="/news?page=2" aria-current="page" aria-label="Page 2, current page">2</a>`} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in %s", w, got)
		}
	}
	lp := v.loopPaged
	if lp == nil || lp.Page != 2 || lp.Out || lp.Pagination.TotalPages != 3 {
		t.Fatalf("loopPaged = %+v", lp)
	}
	// the second paged loop renders page 1, without a pager, and keeps the first's record
	got2 := renderLoop(t, ds, v, second, false)
	if ds.specs[1].Page != 1 || strings.Contains(got2, "hm-pager") || v.loopPaged != lp {
		t.Errorf("second loop: page %d, %s", ds.specs[1].Page, got2)
	}
	// past the last page
	v2 := minimalView()
	v2.PageNum = 9
	renderLoop(t, ds, v2, paged, false)
	if !v2.loopPaged.Out || v2.loopPaged.Page != 3 {
		t.Errorf("out of range = %+v", v2.loopPaged)
	}
	// paging none never records
	v3 := minimalView()
	v3.PageNum = 2
	renderLoop(t, ds, v3, loopNode(map[string]any{"schema": "post"}, itemKids()...), false)
	if v3.loopPaged != nil || ds.specs[len(ds.specs)-1].Page != 1 {
		t.Error("paging none must stay on page 1")
	}
}

// ISC-30: a 12-item loop with 4 bound nodes per item renders in at most 5 ms.
func BenchmarkLoop12(b *testing.B) {
	e, err := New(Options{Data: &loopData{n: 12}})
	if err != nil {
		b.Fatal(err)
	}
	item := &builder.Node{ID: "it", T: "container", P: map[string]any{"tag": "article"}, C: []*builder.Node{
		{ID: "h", T: "heading", P: map[string]any{"level": float64(3)}, B: map[string]string{"text": "loop.item.title"}},
		{ID: "t", T: "text", B: map[string]string{"text": "loop.item.excerpt"}},
		{ID: "i", T: "image", B: map[string]string{"src": "loop.item.image", "alt": "loop.item.title"}},
		{ID: "b", T: "button", P: map[string]any{"text": "Read more"}, B: map[string]string{"href": "loop.item.url"}},
	}}
	n := loopNode(map[string]any{"schema": "post", "limit": float64(12)}, item)
	v := minimalView()
	var buf bytes.Buffer
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		buf.Reset()
		if err := e.RenderNode(&buf, v, n, false); err != nil {
			b.Fatal(err)
		}
	}
}

func TestLoop12RendersWithinBudget(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing is meaningless under -race and skipped under -short")
	}
	res := testing.Benchmark(BenchmarkLoop12)
	if res.N == 0 {
		t.Fatal("benchmark did not run")
	}
	per := time.Duration(res.NsPerOp())
	t.Logf("12-item loop: %v/op, %d allocs/op", per, res.AllocsPerOp())
	if per > 5*time.Millisecond {
		t.Errorf("12-item loop takes %v, budget 5ms", per)
	}
}

func pagedDoc() *builder.Doc {
	return &builder.Doc{V: 2, Nodes: []*builder.Node{loopNode(map[string]any{"schema": "post", "paging": "pages", "limit": float64(4)}, itemKids()...)}}
}

func pagedHeadView(page int, doc *builder.Doc) *PageView {
	v := minimalView()
	v.Path, v.PageNum, v.Doc = "/news", page, doc
	v.PagedLoop, v.LoopOutOfRange = new(bool), new(bool)
	return v
}

func TestPagedLoopHead(t *testing.T) {
	e := newTestEngine(t, Options{Data: &loopData{n: 4, pages: 3}})
	for _, tc := range []struct {
		page                  int
		canonical, prev, next string
	}{
		{1, `rel="canonical" href="https://example.test/news"`, "", `<link rel="next" href="https://example.test/news?page=2">`},
		{2, `rel="canonical" href="https://example.test/news?page=2"`, `<link rel="prev" href="https://example.test/news">`, `<link rel="next" href="https://example.test/news?page=3">`},
		{3, `rel="canonical" href="https://example.test/news?page=3"`, `<link rel="prev" href="https://example.test/news?page=2">`, ""},
	} {
		v := pagedHeadView(tc.page, pagedDoc())
		out := renderToString(t, e, v)
		if !strings.Contains(out, tc.canonical) {
			t.Errorf("page %d: canonical %q missing", tc.page, tc.canonical)
		}
		if has := strings.Contains(out, `rel="prev"`); has != (tc.prev != "") || (tc.prev != "" && !strings.Contains(out, tc.prev)) {
			t.Errorf("page %d: prev link wrong (want %q)", tc.page, tc.prev)
		}
		if has := strings.Contains(out, `rel="next"`); has != (tc.next != "") || (tc.next != "" && !strings.Contains(out, tc.next)) {
			t.Errorf("page %d: next link wrong (want %q)", tc.page, tc.next)
		}
		if !*v.PagedLoop || *v.LoopOutOfRange {
			t.Errorf("page %d: PagedLoop=%v out=%v", tc.page, *v.PagedLoop, *v.LoopOutOfRange)
		}
	}
	v := pagedHeadView(9, pagedDoc())
	renderToString(t, e, v)
	if !*v.LoopOutOfRange {
		t.Error("page 9 of 3 must report out of range")
	}
}

func TestPagedLoopHeadFilterRunsOncePerRender(t *testing.T) {
	calls := 0
	filter := func(_ context.Context, c HeadContext) HeadContext { calls++; return c }
	e := newTestEngine(t, Options{Data: &loopData{n: 4, pages: 3}, HeadFilter: filter})
	plain := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "h", T: "heading", P: map[string]any{"text": "x"}}}}
	renderToString(t, e, pagedHeadView(2, plain))
	if calls != 1 {
		t.Errorf("head filter ran %d times on a page without a paged loop", calls)
	}
	calls = 0
	v := pagedHeadView(2, pagedDoc())
	renderToString(t, e, v)
	if calls != 1 {
		t.Errorf("head filter ran %d times on a page with a paged loop, want 1", calls)
	}
	v = pagedHeadView(2, plain)
	renderToString(t, e, v)
	if *v.PagedLoop || *v.LoopOutOfRange {
		t.Error("a page without a loop must not report one")
	}
}

func TestLoopsPerRenderAreCapped(t *testing.T) {
	root := func() *builder.Node {
		r := &builder.Node{ID: "root", T: "container"}
		for i := range maxLoopsPerRender + 2 {
			l := loopNode(map[string]any{"schema": "post"}, itemKids()...)
			l.ID = fmt.Sprintf("l%d", i)
			for j, c := range l.C {
				c.ID = fmt.Sprintf("%s-%d", l.ID, j)
			}
			r.C = append(r.C, l)
		}
		return r
	}
	ds := &loopData{n: 1}
	got := renderLoop(t, ds, minimalView(), root(), false)
	if len(ds.specs) != maxLoopsPerRender || strings.Count(got, "<!-- hm-loop: limit -->") != 2 {
		t.Errorf("public: %d queries, %d placeholders", len(ds.specs), strings.Count(got, "<!-- hm-loop: limit -->"))
	}
	ds = &loopData{n: 1}
	got = renderLoop(t, ds, minimalView(), root(), true)
	if len(ds.specs) != maxLoopsPerRender || strings.Count(got, "Loop limit reached") != 2 {
		t.Errorf("canvas: %d queries, %d notices", len(ds.specs), strings.Count(got, "Loop limit reached"))
	}
}

func TestLoopPagerStopsAtMaxQueryPage(t *testing.T) {
	ds := &loopData{n: 1, pages: 5000}
	v := minimalView()
	v.Path = "/news"
	renderLoop(t, ds, v, loopNode(map[string]any{"schema": "post", "paging": "pages", "limit": float64(1)}, itemKids()...), false)
	if got := v.loopPaged.Pagination.TotalPages; got != MaxQueryPage {
		t.Errorf("pager pages = %d, want %d", got, MaxQueryPage)
	}
}

func TestLoopInListingPartDoesNotOwnPaging(t *testing.T) {
	ds := &loopData{n: 2, pages: 3}
	v := minimalView()
	v.Path, v.PageNum = "/blog", 2
	v.Pagination = NewPagination("/blog", nil, 2, 10, 30)
	renderLoop(t, ds, v, loopNode(map[string]any{"schema": "post", "paging": "pages"}, itemKids()...), false)
	if v.loopPaged != nil || ds.specs[0].Page != 1 {
		t.Errorf("a loop on a listing route took ?page=: %+v page=%d", v.loopPaged, ds.specs[0].Page)
	}
}
