package admin

import (
	"net/url"
	"strings"
	"testing"
)

func TestTaxonomyCreateFlow(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)

	// The no-JS page preselects the kind from ?kind= and renders the same fields.
	rec := c.do("GET", "/admin/settings/taxonomies/new?kind=tag", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET new: %d", rec.Code)
	}
	has(t, rec.Body.String(), `name="name"`, `name="slug"`, `name="new_kind"`, `name="parent"`, `name="description"`, `value="tag" selected`)

	// The list: header opener is a link to /new with the dialog on the same page, table first.
	list := c.get(t, "owner-token", "/admin/settings/taxonomies?kind=category", "")
	has(t, list, `href="/admin/settings/taxonomies/new?kind=category"`, `data-hm-open="new-term"`, `id="new-term"`, `data-variant="sub"`)
	lacks(t, list, "Add a term")
	if strings.Count(list, "hm-btn--primary") != 2 { // header link + dialog primary
		t.Errorf("primaries = %d", strings.Count(list, "hm-btn--primary"))
	}
	if strings.Index(list, "<table") > strings.Index(list, `id="new-term"`) {
		t.Error("table must come before the dialog")
	}

	// Success: 303 to the kind with ?created=<id>, row pinned first and marked new.
	rec = c.post("/admin/settings/taxonomies/terms", url.Values{"kind": {"category"}, "name": {"Zebra"}, "parent": {fid(31).String()}})
	loc := rec.Header().Get("Location")
	if rec.Code != 303 || !strings.HasPrefix(loc, "/admin/settings/taxonomies?kind=category&created=") {
		t.Fatalf("create: %d %s", rec.Code, loc)
	}
	body := c.get(t, "owner-token", loc, "")
	first := strings.Index(body, `class="hm-row--new"`)
	if first < 0 {
		t.Fatal("no new-row class")
	}
	if rows := body[strings.Index(body, "<tbody>"):]; strings.Index(rows, `<tr id=`) != strings.Index(rows, `class="hm-row--new"`)-len(`<tr id="row-`+loc[strings.LastIndex(loc, "=")+1:]+`" `) {
		t.Error("created row is not first")
	}
	has(t, body, "Zebra", "— Zebra")

	// 422 re-renders the new page with the entered values kept.
	rec = c.post("/admin/settings/taxonomies/terms", url.Values{"kind": {"category"}, "name": {"News"}, "description": {"keep me"}})
	if rec.Code != 422 {
		t.Fatalf("dup: %d", rec.Code)
	}
	has(t, rec.Body.String(), "already uses that slug", `value="News"`, "keep me", "New term")
}

func TestKindLabelPluralises(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{"category": "Categories", "tag": "Tags", "series": "Series", "key": "Keys", "day": "Days", "": ""} {
		if got := kindLabel(in); got != want {
			t.Errorf("kindLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
