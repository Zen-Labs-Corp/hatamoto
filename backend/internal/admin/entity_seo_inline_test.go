package admin

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func seoEditOf(orig, next seoFields, fields ...string) seoEdit {
	has := map[string]bool{}
	for _, f := range fields {
		has[f] = true
	}
	return seoEdit{New: next, Orig: orig, Has: has}
}

func storedSEO() map[string]any {
	return map[string]any{
		"meta_title": "Old", "meta_description": "Desc", "canonical_url": "https://example.com/a", "og_image": "/i.png", "og_title": "OG",
		"schema_type": "Service", "hide_from_sitemap": true, "content": "body",
		"seo": map[string]any{"robots": "nofollow, max-snippet:50", "focus_keyword": "kw", "x": 1.0},
	}
}

func TestSEOEditKeepsEverythingElse(t *testing.T) {
	t.Parallel()

	attrs := storedSEO()
	orig := seoFieldsOf(attrs)
	res, err := applySEOEdit(attrs, seoEditOf(orig, seoFields{Title: " New ", Description: "Desc", Keyword: "kw"}, seoFieldTitle), true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Changed, []string{seoFieldTitle}) {
		t.Fatalf("changed = %v", res.Changed)
	}
	want := storedSEO()
	want["meta_title"] = "New"
	if !reflect.DeepEqual(attrs, want) {
		t.Errorf("attrs = %v\nwant %v", attrs, want)
	}
}

func TestSEOEditNoIndexRoundTrip(t *testing.T) {
	t.Parallel()

	attrs := storedSEO()
	orig := seoFieldsOf(attrs)
	on := orig
	on.NoIndex = true
	if _, err := applySEOEdit(attrs, seoEditOf(orig, on, seoFieldNoIndex), true); err != nil || attrs["no_index"] != true {
		t.Fatalf("noindex on: %v %v", err, attrs["no_index"])
	}
	if got := str(attrs["seo"].(map[string]any), "robots"); got != "nofollow, max-snippet:50" {
		t.Errorf("robots = %q", got)
	}
	// A robots-only noindex stays in robots when the switch is untouched.
	robotsOnly := map[string]any{"seo": map[string]any{"robots": "noindex, follow"}}
	o := seoFieldsOf(robotsOnly)
	if _, err := applySEOEdit(robotsOnly, seoEditOf(o, seoFields{Title: "T", NoIndex: true}, seoFieldTitle, seoFieldNoIndex), true); err != nil {
		t.Fatal(err)
	}
	if _, ok := robotsOnly["no_index"]; ok {
		t.Error("an untouched noindex gained a no_index key")
	}
	// Switching it off drops the noindex token only.
	off := seoFieldsOf(attrs)
	nf := off
	nf.NoIndex = false
	if _, err := applySEOEdit(attrs, seoEditOf(off, nf, seoFieldNoIndex), true); err != nil {
		t.Fatal(err)
	}
	if _, ok := attrs["no_index"]; ok {
		t.Error("no_index kept")
	}
	if got := str(attrs["seo"].(map[string]any), "robots"); got != "nofollow, max-snippet:50" {
		t.Errorf("robots = %q", got)
	}
}

func TestSEOEditConflictAndUnchanged(t *testing.T) {
	t.Parallel()

	attrs := storedSEO()
	loaded := seoFieldsOf(attrs)
	loaded.Title = "Stale title" // the row was loaded before someone changed the title
	next := loaded
	next.Title = "Mine"
	next.Description = "New desc"
	res, err := applySEOEdit(attrs, seoEditOf(seoFields{Title: "Stale title", Description: "Desc", Keyword: "kw"}, next, seoFieldTitle, seoFieldDescription), true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Conflicts[seoFieldTitle] != "Old" || len(res.Conflicts) != 1 {
		t.Errorf("conflicts = %v", res.Conflicts)
	}
	if attrs["meta_title"] != "Old" || attrs["meta_description"] != "New desc" {
		t.Errorf("attrs = %v", attrs)
	}
	if !reflect.DeepEqual(res.Changed, []string{seoFieldDescription}) {
		t.Errorf("changed = %v", res.Changed)
	}

	// Nothing changed: nothing written.
	same := storedSEO()
	cur := seoFieldsOf(same)
	res, err = applySEOEdit(same, seoEditOf(cur, cur, seoFieldTitle, seoFieldDescription, seoFieldKeyword, seoFieldNoIndex), true)
	if err != nil || len(res.Changed) != 0 || !reflect.DeepEqual(same, storedSEO()) {
		t.Errorf("unchanged post wrote: %v %v", err, res.Changed)
	}
}

func TestSEOEditKeyword(t *testing.T) {
	t.Parallel()

	attrs := map[string]any{"seo": map[string]any{"focus_keyword": "kw"}}
	cur := seoFieldsOf(attrs)
	if _, err := applySEOEdit(attrs, seoEditOf(cur, seoFields{}, seoFieldKeyword), true); err != nil {
		t.Fatal(err)
	}
	if _, ok := attrs["seo"]; ok {
		t.Errorf("an emptied seo map stayed: %v", attrs)
	}
	// A type without the keyword field never writes it.
	attrs = map[string]any{"seo": map[string]any{"focus_keyword": "kw"}}
	if res, _ := applySEOEdit(attrs, seoEditOf(cur, seoFields{}, seoFieldKeyword), false); len(res.Changed) != 0 || attrs["seo"] == nil {
		t.Errorf("keyword written for a type without it: %v", attrs)
	}
	// The shared helper is what the blog editor uses.
	a := map[string]any{}
	blogBody{Keyword: " go ", InLLMs: true}.mergeSEO(a)
	b := map[string]any{}
	mergeFocusKeyword(b, "go")
	if !reflect.DeepEqual(a, b) {
		t.Errorf("mergeSEO %v vs mergeFocusKeyword %v", a, b)
	}
}

func TestSEOEditLimits(t *testing.T) {
	t.Parallel()

	attrs := storedSEO()
	cur := seoFieldsOf(attrs)
	_, err := applySEOEdit(attrs, seoEditOf(cur, seoFields{Title: strings.Repeat("x", builderSEOTitleMax+1)}, seoFieldTitle), true)
	var le *seoLengthError
	if !errors.As(err, &le) || le.Field != "title" {
		t.Fatalf("err = %v", err)
	}
	if attrs["meta_title"] != "Old" {
		t.Error("a refused edit changed attrs")
	}
}
