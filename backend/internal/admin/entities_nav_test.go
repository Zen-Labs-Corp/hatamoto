package admin

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

// subnavOf returns the Entities subnav <nav>, or "".
func subnavOf(body string) string {
	i := strings.Index(body, `<nav class="hm-subnav hm-subnav--entities"`)
	if i < 0 {
		return ""
	}
	nav := body[i:]
	return nav[:strings.Index(nav, "</nav>")]
}

var thCell = regexp.MustCompile(`<th[ >]`)

// tableShape returns the header cell count and every body row's cell count.
func tableShape(body string) (head int, rows []int) {
	thead := body[strings.Index(body, "<thead"):]
	thead = thead[:strings.Index(thead, "</thead>")]
	head = len(thCell.FindAllString(thead, -1))
	for _, tr := range regexp.MustCompile(`(?s)<tr id="row-.*?</tr>`).FindAllString(body, -1) {
		rows = append(rows, len(thCell.FindAllString(tr, -1))+strings.Count(tr, "<td"))
	}
	return head, rows
}

// newNavRig is the content rig plus a Post type with two posts.
func newNavRig(t *testing.T) (*contentRig, *entity.EntitySchema) {
	c := newContentRig(t)
	post := c.ents.addSchema(40, "Post", "post", `{"type":"object"}`, `{}`, false)
	c.ents.addEntity(21, post, "First post", "first-post", entity.StatusPublished, `{}`, t0)
	c.ents.addEntity(22, post, "Second post", "second-post", entity.StatusDraft, `{}`, t0)
	return c, post
}

func TestEntitiesSubnavAllView(t *testing.T) {
	t.Parallel()

	c, _ := newNavRig(t)
	body := c.get(t, "owner-token", "/admin/entities", "")
	nav := subnavOf(body)
	has(t, nav, `aria-label="Entities"`, `<h2 class="hm-subnav__title hm-visually-hidden">Entities</h2>`)
	all := subnavLink(nav, "/admin/entities")
	has(t, all, `aria-current="page"`, ">All entities<", `hm-subnav__marker">6<`) // 3 services (archived counts) + page + 2 posts
	if l := subnavLink(nav, "/admin/entities?type=post"); !strings.Contains(l, `hm-subnav__marker">2<`) || strings.Contains(l, "aria-current") {
		t.Errorf("post entry = %q", l)
	}
	if l := subnavLink(nav, "/admin/entities?type=page"); !strings.Contains(l, `hm-subnav__marker">1<`) {
		t.Errorf("page entry = %q", l)
	}
	if n := strings.Count(nav, `aria-current="page"`); n != 1 {
		t.Errorf("%d current entries, want 1", n)
	}
	has(t, nav, `<span class="hm-subnav__text">Posts</span>`, `<span class="hm-subnav__text">Pages</span>`) // plural labels, as the h1
	// the separator precedes the owner's manage link
	sep, manage := strings.Index(nav, `role="separator"`), strings.Index(nav, `href="/admin/settings/types"`)
	if sep < 0 || manage < sep {
		t.Errorf("separator %d must precede the manage link %d", sep, manage)
	}
	has(t, nav, ">Manage entity types<")
	lacks(t, nav, "<svg") // no lone icon: every entry is text, as in the Plugins subnav
	// the old tab row is gone; the header and Create menu stay
	lacks(t, body, "entity-types-more", "Find a type")
	has(t, body, "<h1>Entities</h1>", "Pages, posts and other content on your site.", `popovertarget="create-menu"`, `<th scope="col"`, ">Type<")
	if c.ents.counts != 1 {
		t.Errorf("count queries = %d, want 1", c.ents.counts)
	}
}

func TestEntitiesSubnavTypeView(t *testing.T) {
	t.Parallel()

	c, _ := newNavRig(t)
	body := c.get(t, "owner-token", "/admin/entities?type=post&q=x&status=draft", "")
	nav := subnavOf(body)
	has(t, subnavLink(nav, "/admin/entities?type=post"), `aria-current="page"`)
	if strings.Contains(subnavLink(nav, "/admin/entities"), "aria-current") || strings.Count(nav, `aria-current="page"`) != 1 {
		t.Error("only Posts is current")
	}
	// filters keep the type in the forms
	has(t, body, `name="type" value="post"`)
	// header: plural title, one primary button, no Create menu, no description
	has(t, body, "<h1>Posts</h1>", `<li><a href="/admin/entities">Entities</a></li><li aria-current="page">Posts</li>`)
	lacks(t, body, `popovertarget="create-menu"`, "Pages, posts and other content")
	if n := strings.Count(body, "New post"); n != 1 {
		t.Errorf("New post appears %d times, want 1", n)
	}
	has(t, body, `<form method="post" action="/admin/entities/drafts">`, `name="type" value="post"`, `name="_csrf"`)
}

func TestEntitiesPerTypeNewButton(t *testing.T) {
	t.Parallel()

	c, _ := newNavRig(t)
	page := c.get(t, "owner-token", "/admin/entities?type=page", "")
	has(t, page, "<h1>Pages</h1>", `<form method="post" action="/admin/entities/drafts">`, `name="type" value="page"`, "New page")
	has(t, page, `<span>New page</span></button></form>`) // through hm/button, label span like every primary
	// a type without a visual editor is created through the form: a link, not a POST
	svc := c.get(t, "owner-token", "/admin/entities?type=service", "")
	has(t, svc, `<a class="hm-btn hm-btn--primary" href="/admin/entities/new?type=service"`, "New service", `<span>New service</span></a>`)
	lacks(t, svc, `action="/admin/entities/drafts"`)
}

func TestEntitiesPerTypeEmptyStateHasNoGenericCreate(t *testing.T) {
	t.Parallel()

	c, _ := newNavRig(t)
	empty := c.get(t, "owner-token", "/admin/entities?type=recipe", "")
	has(t, empty, "No entities match", "New recipe")
	lacks(t, empty, "Create entity", `href="/admin/entities/new"`)
	has(t, c.get(t, "owner-token", "/admin/entities?q=zzzz-nothing", ""), "Create entity") // the unfiltered list keeps it
}

func TestEntitiesTypeColumnOnlyOnAll(t *testing.T) {
	t.Parallel()

	c, _ := newNavRig(t)
	allHead, allRows := tableShape(c.get(t, "owner-token", "/admin/entities", ""))
	typeBody := c.get(t, "owner-token", "/admin/entities?type=post", "")
	head, rows := tableShape(typeBody)
	if len(rows) != 2 || len(allRows) != 6 {
		t.Fatalf("rows = %d per type, %d all", len(rows), len(allRows))
	}
	if head >= allHead {
		t.Errorf("per-type header cells = %d, all view %d: the Type column belongs to All only", head, allHead)
	}
	for i, n := range rows {
		if n != head {
			t.Errorf("row %d has %d cells, header %d", i, n, head)
		}
	}
	for i, n := range allRows {
		if n != allHead {
			t.Errorf("all row %d has %d cells, header %d", i, n, allHead)
		}
	}
	lacks(t, typeBody, `Type</th>`, "<td>Post</td>", `Author</th>`) // nobody wrote these posts: no Author column
}

func TestEntitiesUnknownTypeIsAll(t *testing.T) {
	t.Parallel()

	c, _ := newNavRig(t)
	body := c.get(t, "owner-token", "/admin/entities?type=zzz", "")
	has(t, subnavLink(subnavOf(body), "/admin/entities"), `aria-current="page"`)
	if strings.Count(subnavOf(body), `aria-current="page"`) != 1 {
		t.Error("exactly one current entry")
	}
	has(t, body, "<h1>Entities</h1>", `popovertarget="create-menu"`, ">Type<")
}

func TestEntitiesSubnavEditorHasNoManageLink(t *testing.T) {
	t.Parallel()

	c, _ := newNavRig(t)
	for _, path := range []string{"/admin/entities", "/admin/entities?type=post"} {
		body := c.get(t, "editor-token", path, "")
		has(t, subnavOf(body), `href="/admin/entities?type=post"`)
		lacks(t, body, "Manage entity types", "/admin/settings/types")
	}
}

// There is no "Go" form any more: phones pick the type in the filter bar (live), the status is a tab row.
func TestEntitiesNoTypeSwitcherForm(t *testing.T) {
	t.Parallel()

	c, _ := newNavRig(t)
	body := c.get(t, "owner-token", "/admin/entities?type=post&status=draft&q=x", "")
	lacks(t, body, "hm-subnav-select", ">Go<", "hm-entities-type")
	has(t, body, `name="type"`, `<option value="post" selected>Posts</option>`, `name="status" value="draft" checked`)
	if n := strings.Count(body, "Apply filters"); n > 1 {
		t.Errorf("Apply filters appears %d times", n)
	}
}

// The status filter is one segmented control inside the toolbar form (so the other filters
// ride along) and shows the counts in its labels.
func TestEntitiesStatusSegmentedKeepsFilters(t *testing.T) {
	t.Parallel()

	c, _ := newNavRig(t)
	body := c.get(t, "owner-token", "/admin/entities?type=post&q=first&status=draft", "")
	i := strings.Index(body, `<fieldset class="hm-segmented"`)
	if i < 0 {
		t.Fatal("no status control")
	}
	seg := body[i : i+strings.Index(body[i:], "</fieldset>")]
	has(t, seg, `<legend class="hm-visually-hidden">Status</legend>`, `name="status" value="draft" checked id="entities-seg-draft"`, `>Draft `, `>All `)
	if n := strings.Count(seg, `type="radio"`); n != 5 {
		t.Errorf("%d status options, want 5", n)
	}
	if n := strings.Count(seg, ` checked`); n != 1 {
		t.Errorf("%d checked options, want 1", n)
	}
	lacks(t, body, `class="hm-tabs`)
}

// The filter form swaps only the results; the header (title, count) and the status control's
// labels ride along out of band, so a live update re-renders them with the current filters.
func TestEntitiesLiveFilterCarriesHeaderAndCounts(t *testing.T) {
	t.Parallel()

	c, _ := newNavRig(t)
	rec := c.do("GET", "/admin/entities?type=post&q=first", "owner-token", nil, map[string]string{"HX-Request": "true"})
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	lacks(t, body, `#entities-seg:innerHTML`) // the radios are never swapped: a queued choice must not be undone
	has(t, body, `hx-select-oob="#entities-count,#entities-status:innerHTML,#entities-head,#entities-seg-all-n,#entities-seg-published-n,#entities-seg-draft-n,#entities-seg-scheduled-n,#entities-seg-archived-n"`, `<span class="hm-segmented__count" id="entities-seg-all-n">`, `<div id="entities-head" class="hm-entities-head">`, `<h1>Posts</h1>`, `<p id="entities-status"`, `id="entities-results"`)
}

// The Create menu lists entity types only; managing types lives in the subnav.
func TestEntitiesCreateMenuHasTypesOnly(t *testing.T) {
	t.Parallel()

	c, _ := newNavRig(t)
	body := c.get(t, "owner-token", "/admin/entities", "")
	i := strings.Index(body, `id="create-menu"`)
	if i < 0 {
		t.Fatal("no create menu")
	}
	menu := body[i:]
	menu = menu[:strings.Index(menu, "</div>")]
	lacks(t, menu, "Manage entity types")
	has(t, body, "Manage entity types")
}

func TestEntitiesDescriptionCountsResults(t *testing.T) {
	t.Parallel()

	c, _ := newNavRig(t)
	has(t, c.get(t, "owner-token", "/admin/entities", ""), "6 entities. Pages, posts and other content on your site.")
	has(t, c.get(t, "owner-token", "/admin/entities?type=post", ""), "2 posts")
}

func TestEntitiesSubnavOnlyOnTheList(t *testing.T) {
	t.Parallel()

	c, _ := newNavRig(t)
	for _, path := range []string{"/admin/entities/new", "/admin/entities/" + fid(11).String(), "/admin/entities/" + fid(11).String() + "/versions"} {
		rec := c.do("GET", path, "owner-token", nil, nil)
		if rec.Code != 200 {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		lacks(t, rec.Body.String(), "hm-subnav")
	}
	// Settings keeps its own subnav
	has(t, c.get(t, "owner-token", "/admin/settings/types", ""), `aria-label="Settings"`)
}

func TestEntitiesSubnavCountFailureAndNoTypes(t *testing.T) {
	t.Parallel()

	c, _ := newNavRig(t)
	c.ents.countErr = errors.New("boom")
	body := c.get(t, "owner-token", "/admin/entities", "")
	has(t, subnavLink(subnavOf(body), "/admin/entities"), `hm-subnav__marker">0<`)
	has(t, subnavLink(subnavOf(body), "/admin/entities?type=post"), `hm-subnav__marker">0<`)

	c.ents.countErr, c.ents.schemas = nil, nil
	body = c.get(t, "owner-token", "/admin/entities", "")
	nav := subnavOf(body)
	has(t, subnavLink(nav, "/admin/entities"), `hm-subnav__marker">0<`)
	has(t, nav, ">Manage entity types<")
	has(t, body, "Choose a type")
}

func TestTypePlural(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"Post": "Posts", "Page": "Pages", "Service": "Services", "Box": "Boxes", "Church": "Churches",
		"Dish": "Dishes", "Bus": "Buses", "Category": "Categories", "Day": "Days", "Boy": "Boys", "Y": "Ys", "News": "Newses",
	} {
		if got := typePlural(in); got != want {
			t.Errorf("typePlural(%q) = %q, want %q", in, got, want)
		}
	}
}

// The person type's subnav link is the People list, not the generic filter.
func TestEntitiesNavPersonLinksToPeople(t *testing.T) {
	t.Parallel()

	nav := entitiesNavFor([]typeTab{{Slug: "post", Name: "Post", Count: 1}, {Slug: "person", Name: "Person", Count: 1}}, "person")
	if nav[1].Href != "/admin/entities?type=post" || nav[2].Href != "/admin/people" || !nav[2].Current {
		t.Fatalf("nav = %+v", nav)
	}
	if nav[1].Label != "Posts" || nav[2].Label != "People" {
		t.Fatalf("labels = %q, %q; want Posts, People", nav[1].Label, nav[2].Label)
	}
}

// Details has the same save indicator as the other two editors, and loads the script that drives it.
func TestEntityFormHasSaveIndicator(t *testing.T) {
	t.Parallel()

	c, _ := newNavRig(t)
	body := c.get(t, "owner-token", "/admin/entities/"+fid(11).String(), "")
	has(t, body, `data-hm-ef-status`, `class="hm-savestate"`, `data-state="saved"`, `Saved `, `entityform.`)
	// a rejected save re-renders unsaved edits: the indicator says so
	rec := c.post("/admin/entities/"+fid(11).String(), url.Values{"name": {""}, "slug": {"x"}, "status": {"published"}})
	if rec.Code != 422 && rec.Code != 200 {
		t.Fatalf("invalid save = %d", rec.Code)
	}
	has(t, rec.Body.String(), `data-state="unsaved"`, `Unsaved changes`)
}
