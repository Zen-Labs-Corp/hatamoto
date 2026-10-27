package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

func relEntity(slug string, status entity.EntityStatus, attrs string) *entity.Entity {
	pub := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	return &entity.Entity{ID: uuid.New(), Name: "Name " + slug, Slug: slug, Status: status,
		Attributes: json.RawMessage(attrs), PublishedAt: &pub}
}

func TestRelatedItems(t *testing.T) {
	self := relEntity("self", entity.StatusPublished, `{}`)
	a := relEntity("a", entity.StatusPublished, `{"title":"A","excerpt":"ex-a","featured_image":"/a.png","category":"News","secret":"x"}`)
	draft := relEntity("d", entity.StatusDraft, `{"title":"Draft"}`)
	noURL := relEntity("nourl", entity.StatusPublished, `{"title":"NoURL"}`)
	g := relEntity("g", entity.StatusPublished, `{"title":"G","excerpt":"ex-g"}`)
	b := relEntity("b", entity.StatusPublished, `{}`)
	c := relEntity("c", entity.StatusPublished, `{"title":"C"}`)
	items := []*entity.Entity{self, draft, a, noURL, g, b, c}

	locate := func(e *entity.Entity) string {
		if e.Slug == "nourl" {
			return ""
		}
		return "/blog/" + e.Slug
	}

	got := relatedItems(items, self.ID.String(), 3, locate)
	if len(got) != 3 {
		t.Fatalf("limit 3: got %d items %+v", len(got), got)
	}
	if got[0].Title != "A" || got[0].URL != "/blog/a" || got[0].Excerpt != "ex-a" || got[0].Image != "/a.png" || got[0].Category != "News" || got[0].PublishedAt.IsZero() {
		t.Errorf("item 0 = %+v", got[0])
	}
	if got[1].Title != "G" || got[1].Excerpt != "ex-g" {
		t.Errorf("item 1 = %+v", got[1])
	}
	if got[2].Title != "Name b" {
		t.Errorf("title falls back to entity name: %+v", got[2])
	}
	for _, it := range got {
		if it.Title == "Draft" || it.Title == "NoURL" || strings.Contains(it.URL, "self") {
			t.Errorf("must be excluded: %+v", it)
		}
	}
	if got := relatedItems(nil, self.ID.String(), 3, locate); got != nil {
		t.Errorf("empty = %v", got)
	}
}

func TestClampRelated(t *testing.T) {
	for in, want := range map[int]int{-5: 1, 0: 1, 3: 3, 12: 12, 99: 12} {
		if got := clampRelated(in); got != want {
			t.Errorf("clampRelated(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestRelatedNoopWithoutEntity(t *testing.T) {
	var s *Server
	if got := s.relatedFunc(t.Context(), "", "blog", nil)(3); got != nil {
		t.Errorf("no entity: %v", got)
	}
	if got := s.relatedFunc(t.Context(), "x", "", nil)(3); got != nil {
		t.Errorf("no schema: %v", got)
	}
}

func TestRelatedLocatorUsesRegistry(t *testing.T) {
	locate := relatedLocator(context.Background(), entity.DefaultURLSnapshot(), nil, "page")
	if got := locate(relEntity("other", entity.StatusPublished, `{}`)); got != "/other" {
		t.Errorf("related URL = %q, want /other", got)
	}
}

func TestMemoRelatedFetchesOnce(t *testing.T) {
	self := relEntity("self", entity.StatusPublished, `{}`)
	rows := []*entity.Entity{self}
	for _, s := range []string{"a", "b", "c", "d"} {
		rows = append(rows, relEntity(s, entity.StatusPublished, `{}`))
	}
	calls := 0
	fetch := func() ([]*entity.Entity, error) { calls++; return rows, nil }
	rel := memoRelated(self.ID.String(), fetch,
		func(e *entity.Entity) string { return "/blog/" + e.Slug }, func(error) { t.Error("unexpected error") })
	if n := len(rel(3)); n != 3 {
		t.Fatalf("limit 3 = %d items", n)
	}
	if n := len(rel(3)); n != 3 {
		t.Errorf("second call = %d items", n)
	}
	if n := len(rel(2)); n != 2 {
		t.Errorf("other limit = %d items", n)
	}
	if calls != 1 {
		t.Errorf("fetch ran %d times, want 1", calls)
	}
}

func TestMemoRelatedErrorReportedOnce(t *testing.T) {
	calls, errs := 0, 0
	rel := memoRelated("x", func() ([]*entity.Entity, error) { calls++; return nil, errors.New("db down") },
		func(*entity.Entity) string { return "/" }, func(error) { errs++ })
	if first, second := rel(3), rel(3); first != nil || second != nil {
		t.Error("failed query must yield no items")
	}
	if calls != 1 || errs != 1 {
		t.Errorf("fetch calls=%d onErr=%d, want 1 and 1", calls, errs)
	}
}

func TestItemExtras(t *testing.T) {
	words := strings.Repeat("word ", 250)
	e := relEntity("a", entity.StatusPublished, `{"title":"A","series_part":3,"content":"<p>`+words+`</p>","page_sections":[{"id":"s"}]}`)
	for name, it := range map[string]render.RelatedItem{
		"related": relatedItems([]*entity.Entity{e}, "", 1, func(*entity.Entity) string { return "/a" })[0],
		"listing": listItem(e, "/a"),
	} {
		if it.Name != "Name a" || it.WordCount != 250 || it.ReadingTime != 2 {
			t.Errorf("%s: name/words/time = %q %d %d", name, it.Name, it.WordCount, it.ReadingTime)
		}
		if it.Attrs["series_part"] != float64(3) || it.Attrs["title"] != "A" {
			t.Errorf("%s: attrs = %v", name, it.Attrs)
		}
		if _, ok := it.Attrs["content"]; ok {
			t.Errorf("%s: attrs must not carry the content body", name)
		}
		if _, ok := it.Attrs["page_sections"]; ok {
			t.Errorf("%s: attrs must not carry page_sections", name)
		}
	}
}

func seriesRow(slug string, status entity.EntityStatus, attrs string) entity.SeriesRow {
	pub := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	return entity.SeriesRow{ID: slug, Name: "Name " + slug, Slug: slug, Status: status, Attributes: []byte(attrs), PublishedAt: &pub}
}

func TestSeriesViewOrderingAndLinks(t *testing.T) {
	rows := []entity.SeriesRow{
		seriesRow("p2", entity.StatusScheduled, `{"title":"Two","series":"Audit","series_part":2,"series_total":2}`),
		seriesRow("p1", entity.StatusPublished, `{"title":"One","series":"Audit","series_part":1,"series_total":2}`),
		seriesRow("idx", entity.StatusPublished, `{"series":"Audit","series_part":0}`),
	}
	got := seriesView("audit", rows, func(r entity.SeriesRow) string { return "/blog/" + r.Slug })
	if got == nil || got.Name != "Audit" || got.Total != 2 || got.IndexURL != "/blog/idx" || got.Slug != "audit" {
		t.Fatalf("series = %+v", got)
	}
	if len(got.Parts) != 3 || got.Parts[0].Part != 0 || got.Parts[1].Part != 1 || got.Parts[2].Part != 2 {
		t.Fatalf("parts order = %+v", got.Parts)
	}
	if !got.Parts[0].Index || got.Parts[0].Title != "Name idx" || got.Parts[1].Title != "One" || got.Parts[1].URL != "/blog/p1" {
		t.Errorf("parts = %+v", got.Parts)
	}
	if got.Parts[2].Published || got.Parts[2].URL != "" || got.Parts[2].PublishDate.IsZero() {
		t.Errorf("scheduled part must not link: %+v", got.Parts[2])
	}
	if seriesView("x", nil, nil) != nil {
		t.Error("no rows must give nil")
	}
}

func TestMemoSeriesOnceAndError(t *testing.T) {
	calls := 0
	f := memoSeries("s", func() ([]entity.SeriesRow, error) {
		calls++
		return []entity.SeriesRow{seriesRow("a", entity.StatusPublished, `{"series_part":0}`)}, nil
	}, func(entity.SeriesRow) string { return "/a" }, func(error) { t.Error("unexpected") })
	first, second := f(), f()
	if first == nil || second == nil || calls != 1 {
		t.Errorf("calls = %d", calls)
	}
	errs := 0
	g := memoSeries("s", func() ([]entity.SeriesRow, error) { return nil, errors.New("down") },
		nil, func(error) { errs++ })
	first, second = g(), g()
	if first != nil || second != nil || errs != 1 {
		t.Errorf("errs = %d", errs)
	}
}

func TestSeriesFuncNoSeries(t *testing.T) {
	var s *Server
	if s.seriesFunc(t.Context(), "blog", map[string]any{}, nil)() != nil {
		t.Error("no series_slug must give nil")
	}
}

func TestMemoPostsByAuthor(t *testing.T) {
	a := relEntity("a", entity.StatusPublished, `{"title":"A"}`)
	d := relEntity("d", entity.StatusDraft, `{"title":"D"}`)
	calls := map[[2]int]int{}
	var marks []bool
	newMemo := func(pageNum int) func(limit, page int) render.PostsPage {
		marks = nil
		return memoPostsByAuthor(pageNum, func(limit, page int) (*entity.PublicPagedResult, error) {
			calls[[2]int{limit, page}]++
			if page == 9 {
				return nil, errors.New("down")
			}
			return &entity.PublicPagedResult{Items: []*entity.Entity{a, d}, Total: 25}, nil
		}, func(e *entity.Entity) string { return "/blog/" + e.Slug }, func(out bool) { marks = append(marks, out) }, func(error) {})
	}
	f := newMemo(1)
	got := f(200, 2)
	if got.Total != 25 || got.Page != 2 || got.Pages != 1 || len(got.Items) != 1 || got.Items[0].URL != "/blog/a" {
		t.Errorf("page = %+v", got)
	}
	f(200, 2)
	if calls[[2]int{render.MaxQueryLimit, 2}] != 1 {
		t.Errorf("limit clamps to MaxQueryLimit and memoises: %v", calls)
	}
	if p := f(10, 0); p.Page != 1 || p.Pages != 3 {
		t.Errorf("page<1 -> 1, pages = ceil(25/10): %+v", p)
	}
	if p := f(10, 9); p.Items != nil || p.Page != 9 || p.Pages != 1 {
		t.Errorf("error page = %+v", p)
	}
	if p := f(10, 5000); p.Page != render.MaxQueryPage {
		t.Errorf("page clamps to MaxQueryPage: %+v", p)
	}
	if len(marks) != 0 {
		t.Errorf("PageNum 1 must never mark a paged loop: %v", marks)
	}
}

func TestMemoPostsByAuthorPagedMarks(t *testing.T) {
	fetch := func(limit, page int) (*entity.PublicPagedResult, error) {
		return &entity.PublicPagedResult{Total: 25}, nil
	}
	run := func(pageNum, limit, page int) []bool {
		var marks []bool
		memoPostsByAuthor(pageNum, fetch, nil, func(out bool) { marks = append(marks, out) }, func(error) {})(limit, page)
		return marks
	}
	if m := run(3, 10, 1); len(m) != 0 {
		t.Errorf("constant-page sidebar must not mark paged: %v", m)
	}
	if m := run(2, 10, 2); len(m) != 1 || m[0] {
		t.Errorf("in-range own page: %v", m)
	}
	if m := run(4, 10, 4); len(m) != 1 || !m[0] {
		t.Errorf("out-of-range own page: %v", m)
	}
	failed := 0
	memoPostsByAuthor(2, func(int, int) (*entity.PublicPagedResult, error) { return nil, errors.New("down") },
		nil, func(bool) { t.Error("failed fetch must not mark") }, func(error) { failed++ })(10, 2)
	if failed != 1 {
		t.Errorf("failed = %d", failed)
	}
}

func TestSeriesViewStringPartAndEmptyURL(t *testing.T) {
	rows := []entity.SeriesRow{
		seriesRow("p2", entity.StatusPublished, `{"series_part":"2","series_total":"2"}`),
		seriesRow("p1", entity.StatusPublished, `{"series_part":" 1 "}`),
	}
	got := seriesView("s", rows, func(r entity.SeriesRow) string {
		if r.Slug == "p2" {
			return ""
		}
		return "/b/" + r.Slug
	})
	if got == nil || got.Total != 2 || got.Parts[0].Part != 1 || got.Parts[1].Part != 2 || got.IndexURL != "" {
		t.Fatalf("series = %+v", got)
	}
	if !got.Parts[0].Published || got.Parts[1].Published {
		t.Errorf("Published needs a URL: %+v", got.Parts)
	}
}
