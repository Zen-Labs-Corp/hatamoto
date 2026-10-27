package main

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

var testSections = []SectionSpec{
	{Slug: "a", Pages: []PageRef{{Slug: "one"}, {Slug: "two"}}},
	{Slug: "b", Pages: []PageRef{{Slug: "three"}}},
}

func testPages(hash string) []Page {
	mk := func(sec, slug string) Page {
		return Page{Section: sec, Slug: slug, Title: "T " + sec + "/" + slug, NavTitle: "N", Description: "D", Path: sec + "/" + slug + ".md", Hash: hash + sec + slug, Body: "## H\n\ntext\n"}
	}
	return []Page{
		{Section: "", Slug: IndexSlug, Hash: "landing"}, // root entity "docs"
		mk("a", IndexSlug), mk("a", "one"), mk("a", "two"),
		mk("b", IndexSlug), mk("b", "three"),
	}
}

func render(p Page) (string, error) { return "<p>" + p.Slug + "</p>", nil }

func kinds(plan []Action) []string {
	var k []string
	for _, a := range plan {
		k = append(k, a.Kind+":"+Key(*a.Page))
	}
	return k
}

func TestBuildPlanEmptySiteCreatesParentsFirst(t *testing.T) {
	t.Parallel()
	got := kinds(BuildPlan(testPages("v1"), testSections, nil))
	want := []string{"create:docs", "create:a", "create:b", "create:a/one", "create:a/two", "create:b/three"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestBuildPlanStates(t *testing.T) {
	t.Parallel()
	pages := testPages("v1")
	existing := []Existing{
		{ID: "L", Slug: "docs", Status: "published", SourceHash: "landing"},
		{ID: "A", Slug: "a", Status: "published", SourceHash: "v1a" + IndexSlug},
		{ID: "B", Slug: "b", Status: "draft", SourceHash: "v1b" + IndexSlug},
		{ID: "1", ParentID: "A", Slug: "one", Status: "published", SourceHash: "stale"},
		{ID: "2", ParentID: "A", Slug: "two", Status: "published", SourceHash: "v1atwo"},
		// "three" lives under another parent with the same slug: not a match.
		{ID: "9", ParentID: "A", Slug: "three", Status: "published", SourceHash: "v1bthree"},
	}
	got := kinds(BuildPlan(pages, testSections, existing))
	want := []string{"skip:docs", "skip:a", "publish:b", "update:a/one", "skip:a/two", "create:b/three"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestApplyEmptyThenIdempotent(t *testing.T) {
	t.Parallel()
	f := newFakeSite(t)
	c := f.client()
	ctx := context.Background()
	pages := testPages("v1")

	list := func() []Action {
		es, err := c.ListEntities(ctx, fakeSchema, "")
		if err != nil {
			t.Fatal(err)
		}
		return BuildPlan(pages, testSections, ToExisting(es))
	}

	ids, err := Apply(ctx, c, fakeSchema, list(), render)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 6 || len(f.ents) != 6 {
		t.Fatalf("ids=%v entities=%d", ids, len(f.ents))
	}
	for _, e := range f.ents {
		if e.Status != "published" {
			t.Errorf("%s not published", e.Slug)
		}
		if strings.Contains(string(e.Attributes), "source_hash") == false {
			t.Errorf("%s lacks source_hash", e.Slug)
		}
	}
	one := ids["a/one"]
	for _, e := range f.ents {
		if e.ID == one && e.ParentID != ids["a"] {
			t.Errorf("a/one parent = %q, want %q", e.ParentID, ids["a"])
		}
		if e.ID == ids["a"] && e.ParentID != "" {
			t.Errorf("section has parent %q", e.ParentID)
		}
	}
	// creates are POST then PATCH status, sections before pages
	w := f.writes()
	if w[0] != "POST /api/v1/entities" || w[1] != "PATCH /api/v1/entities/e001/status" {
		t.Fatalf("write order: %v", w[:3])
	}

	// second run: zero writes
	before := len(f.writes())
	plan := list()
	if c := Counts(plan); c[KindSkip] != 6 {
		t.Fatalf("second plan: %v", c)
	}
	ids2, err := Apply(ctx, c, fakeSchema, plan, render)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.writes()) != before {
		t.Fatalf("re-run wrote: %v", f.writes()[before:])
	}
	if !reflect.DeepEqual(ids, ids2) {
		t.Fatalf("ids changed: %v vs %v", ids, ids2)
	}
}

func TestApplyChangedBodyUpdatesOne(t *testing.T) {
	t.Parallel()
	f := newFakeSite(t)
	c := f.client()
	ctx := context.Background()
	if _, err := Apply(ctx, c, fakeSchema, BuildPlan(testPages("v1"), testSections, nil), render); err != nil {
		t.Fatal(err)
	}
	pages := testPages("v1")
	pages[2].Hash = "changed" // a/one
	es, _ := c.ListEntities(ctx, fakeSchema, "")
	before := len(f.writes())
	if _, err := Apply(ctx, c, fakeSchema, BuildPlan(pages, testSections, ToExisting(es)), render); err != nil {
		t.Fatal(err)
	}
	if w := f.writes()[before:]; len(w) != 1 || !strings.HasPrefix(w[0], "PATCH /api/v1/entities/") || strings.HasSuffix(w[0], "/status") {
		t.Fatalf("writes = %v", w)
	}
}

func TestApplyDraftOnlyPublishes(t *testing.T) {
	t.Parallel()
	f := newFakeSite(t)
	c := f.client()
	ctx := context.Background()
	if _, err := Apply(ctx, c, fakeSchema, BuildPlan(testPages("v1"), testSections, nil), render); err != nil {
		t.Fatal(err)
	}
	for _, e := range f.ents {
		if e.Slug == "two" {
			e.Status = "draft"
		}
	}
	es, _ := c.ListEntities(ctx, fakeSchema, "")
	plan := BuildPlan(testPages("v1"), testSections, ToExisting(es))
	if got := Counts(plan); got[KindPublish] != 1 || got[KindSkip] != 5 {
		t.Fatalf("counts %v", got)
	}
	before := len(f.writes())
	if _, err := Apply(ctx, c, fakeSchema, plan, render); err != nil {
		t.Fatal(err)
	}
	if w := f.writes()[before:]; len(w) != 1 || !strings.HasSuffix(w[0], "/status") {
		t.Fatalf("writes = %v", w)
	}
}

func TestApplyConflictSurfaces(t *testing.T) {
	t.Parallel()
	f := newFakeSite(t)
	c := f.client()
	f.ents = append(f.ents, &Entity{ID: "x1", SchemaID: fakeSchema, Slug: "a", Status: "published"})
	// The plan was built from an empty listing (a racing writer), so the create clashes.
	_, err := Apply(context.Background(), c, fakeSchema, BuildPlan(testPages("v1"), testSections, nil), render)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 409 || ae.Code != "slug_exists" {
		t.Fatalf("err = %v", err)
	}
}

func TestClientRejectsMissingOrWrongKey(t *testing.T) {
	t.Parallel()
	f := newFakeSite(t)
	c := NewClient(Config{URL: f.srv.URL, APIKey: "wrong"})
	_, err := c.ListEntities(context.Background(), fakeSchema, "")
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Fatalf("error mentions the key: %v", err)
	}
}

func TestClientRetriesRateLimit(t *testing.T) {
	t.Parallel()
	f := newFakeSite(t)
	f.throttle = 2
	c := f.client()
	var slept []time.Duration
	c.Sleep = func(d time.Duration) { slept = append(slept, d) }
	if _, err := c.ListEntities(context.Background(), fakeSchema, ""); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 2 || slept[0] != 2*time.Second {
		t.Fatalf("slept %v", slept)
	}
}

func TestClientGivesUpOnRateLimit(t *testing.T) {
	t.Parallel()
	f := newFakeSite(t)
	f.throttle = 100
	_, err := f.client().ListEntities(context.Background(), fakeSchema, "")
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 429 {
		t.Fatalf("err = %v", err)
	}
}

func TestListPaginates(t *testing.T) {
	t.Parallel()
	f := newFakeSite(t)
	f.pageSize = 2
	for i := 0; i < 5; i++ {
		f.ents = append(f.ents, &Entity{ID: string(rune('a' + i)), SchemaID: fakeSchema, Slug: string(rune('a' + i))})
	}
	f.ents = append(f.ents, &Entity{ID: "z", SchemaID: "other", Slug: "z"})
	es, err := f.client().ListEntities(context.Background(), fakeSchema, "")
	if err != nil || len(es) != 5 {
		t.Fatalf("%d entities, err %v", len(es), err)
	}
}

func TestPrintPlan(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	PrintPlan(&b, BuildPlan(testPages("v1"), testSections, nil))
	if !strings.Contains(b.String(), "create   a/one") || !strings.Contains(b.String(), "6 create, 0 update") {
		t.Fatalf("%s", b.String())
	}
}

func TestOrphansListsPublishedUnplannedEntities(t *testing.T) {
	t.Parallel()
	docs := []Entity{
		{ID: "A", Slug: "a", Status: "published"},
		{ID: "1", ParentID: "A", Slug: "one", Status: "published"},
		{ID: "2", ParentID: "A", Slug: "gone", Status: "published"},
		{ID: "3", ParentID: "A", Slug: "draft", Status: "draft"},
		{ID: "Z", Slug: "old", Status: "published"},
	}
	plan := []Action{{Kind: KindSkip, ID: "A"}, {Kind: KindSkip, ID: "1"}}
	got := Orphans(plan, docs, "/docs/")
	want := []string{"/docs/a/gone", "/docs/old"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	var b strings.Builder
	PrintOrphans(&b, got)
	if !strings.Contains(b.String(), "warning: 2 published") || !strings.Contains(b.String(), "/docs/old") {
		t.Fatalf("output %q", b.String())
	}
}
