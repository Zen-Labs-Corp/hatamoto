package admin

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
	linkingv2 "hatamoto.dev/core/internal/linking/v2"
)

// A site with dozens of entity types lists every one in the subnav (no cap, no
// "More" menu), and the current type is the only entry marked current.
func TestEntitySubnavListsEveryType(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	for i := 0; i < 12; i++ {
		sc := c.ents.addSchema(100+i, fmt.Sprintf("City %02d", i), fmt.Sprintf("city-%02d", i), `{"type":"object"}`, `{}`, false)
		c.ents.addEntity(300+i, sc, fmt.Sprintf("Spot %02d", i), fmt.Sprintf("spot-%02d", i), entity.StatusPublished, `{}`, t0)
	}
	body := c.get(t, "owner-token", "/admin/entities", "")
	has(t, body, `aria-label="Entities"`)
	for i := 0; i < 12; i++ {
		has(t, body, fmt.Sprintf(`href="/admin/entities?type=city-%02d"`, i))
	}
	lacks(t, body, "hm-tabs__more", "entity-types-more", "Find a type")
	body = c.get(t, "owner-token", "/admin/entities?type=city-11", "")
	has(t, body, "Spot 11")
	lacks(t, body, "Spot 10")
	nav := body[strings.Index(body, `<nav class="hm-subnav hm-subnav--entities"`):]
	nav = nav[:strings.Index(nav, "</nav>")]
	if n := strings.Count(nav, `aria-current="page"`); n != 1 {
		t.Errorf("subnav aria-current count = %d, want 1", n)
	}
	has(t, body, `href="/admin/entities?type=city-11" aria-current="page"`)
}

// Entities under a parent (region home pages, hubs) are filterable by it: the
// options are the parents, labelled by public path, and the filter keeps the
// whole subtree.
func TestEntityListParentFilter(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	lacks(t, c.get(t, "owner-token", "/admin/entities", ""), `name="under"`) // no parents yet
	root := c.ents.addEntity(60, c.page, "Region home", "region", entity.StatusPublished, `{}`, t0)
	hub := c.ents.addEntity(61, c.page, "Melbourne", "melbourne", entity.StatusPublished, `{}`, t0)
	hub.ParentID = &root.ID
	leaf := c.ents.addEntity(62, c.service, "Melbourne SEO", "melbourne-seo", entity.StatusPublished, `{"summary":"x"}`, t0)
	leaf.ParentID = &hub.ID
	c.ents.ents[fid(11)].ParentID = &root.ID
	c.ents.parentCounts = 0
	body := c.get(t, "owner-token", "/admin/entities", "")
	has(t, body, `name="under"`, `>Any parent</option>`, `/services/region (2)`, `/services/melbourne (1)`)
	if c.ents.parentCounts != 1 {
		t.Errorf("parent count queries = %d, want 1", c.ents.parentCounts)
	}
	body = c.get(t, "owner-token", "/admin/entities?under="+root.ID.String(), "")
	if f := c.ents.lastList; f.Under == nil || *f.Under != root.ID {
		t.Fatalf("under filter not applied: %+v", f)
	}
	has(t, body, "Alpha service", "Melbourne SEO", ">Melbourne<")
	lacks(t, body, "Beta service", "About us")
	// the parent choice stays on the type's list: the filter form keeps the type
	body = c.get(t, "owner-token", "/admin/entities?type=service&under="+root.ID.String(), "")
	has(t, body, `<option value="service" selected>`, "Melbourne SEO")
}

// A type with no entities is left out of the subnav (it stays in the Create
// menu), unless it is the type being listed.
func TestEntitySubnavHidesEmptyTypes(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	c.ents.addSchema(120, "Empty thing", "empty-thing", `{"type":"object"}`, `{}`, false)
	body := c.get(t, "owner-token", "/admin/entities", "")
	nav := subnavOf(body)
	lacks(t, nav, `href="/admin/entities?type=empty-thing"`)
	has(t, nav, `href="/admin/entities?type=service"`)
	has(t, body, `/admin/entities/new?type=empty-thing`) // still creatable
	nav = subnavOf(c.get(t, "owner-token", "/admin/entities?type=empty-thing", ""))
	has(t, nav, `href="/admin/entities?type=empty-thing" aria-current="page"`)
}

// Rows with entities under them get a Children count linking to the list
// filtered to that parent; the column is absent when no row on the page has any.
func TestEntityListChildrenColumn(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	lacks(t, c.get(t, "owner-token", "/admin/entities", ""), `>Children<`)
	hub := c.ents.addEntity(70, c.page, "Sydney hub", "sydney", entity.StatusPublished, `{}`, t0)
	for i := 0; i < 3; i++ {
		e := c.ents.addEntity(71+i, c.service, fmt.Sprintf("Sydney svc %d", i), fmt.Sprintf("sydney-svc-%d", i), entity.StatusPublished, `{"summary":"x"}`, t0)
		e.ParentID = &hub.ID
	}
	body := c.get(t, "owner-token", "/admin/entities?type=page", "")
	has(t, body, `>Children<`, `href="/admin/entities?under=`+hub.ID.String()+`"`)
	// a parent outside the biggest maxParentOptions still shows as the selected Parent
	body = c.get(t, "owner-token", "/admin/entities?under="+hub.ID.String(), "")
	has(t, body, `value="`+hub.ID.String()+`" selected`, "Sydney svc 2")
	lacks(t, c.get(t, "owner-token", "/admin/entities?type=service&q=Alpha", ""), `>Children<`)
}

// The editor's "Suggest links" works on the live (unsaved) body and inserts into
// it: nothing is stored until the post is saved.
func TestBlogEditorLinkSuggestions(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	fl := newFakeLinking()
	r.a.deps.Growth.Linking = fl
	target := fid(11)
	fl.names[r.ent.ID] = "Go servers"
	fl.sugg = []linkingv2.Suggestion{
		{TargetEntityID: target, TargetName: "Alpha service", AnchorPhrase: "already", URL: "/x", Status: "skipped", SkipReason: "already_linked"},
		{TargetEntityID: target, TargetName: "Alpha service", AnchorPhrase: "missing", Status: "skipped", SkipReason: "no_match"},
		{TargetEntityID: target, TargetName: "Alpha service", AnchorPhrase: "Go", URL: "/x", Status: "insertable"},
		{TargetEntityID: r.ent.ID, TargetName: "Go servers", AnchorPhrase: "servers", Status: "insertable"},
	}
	stored := r.attrs()["content"]
	rec := r.post(r.path()+"/links/suggest", url.Values{"content": {"<p>Go is live</p>"}})
	var got struct {
		Suggestions []linkSuggestionJSON `json:"suggestions"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("suggest: %d %s", rec.Code, rec.Body.String())
	}
	if len(fl.liveIn) != 1 || fl.liveIn[0] != "<p>Go is live</p>" {
		t.Errorf("suggest did not scan the live body: %q", fl.liveIn)
	}
	// no_match and self links are dropped, insertable first, the path is the public one
	if len(got.Suggestions) != 2 || !got.Suggestions[0].Insertable || got.Suggestions[0].Anchor != "Go" || got.Suggestions[0].URL != "/blog/alpha" || got.Suggestions[1].Reason != "Already linked" {
		t.Errorf("suggestions = %+v", got.Suggestions)
	}

	rec = r.post(r.path()+"/links/insert", url.Values{"content": {"<p>Go is live</p>"}, "target": {target.String()}, "anchor": {"Go"}})
	var ins struct {
		Inserted bool   `json:"inserted"`
		HTML     string `json:"html"`
		Reason   string `json:"reason"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &ins) != nil || !ins.Inserted || !strings.Contains(ins.HTML, `data-entity-id="`+target.String()+`">Go</a>`) {
		t.Fatalf("insert: %d %s", rec.Code, rec.Body.String())
	}
	if r.attrs()["content"] != stored {
		t.Error("insert wrote the stored post; it must only change the editor")
	}
	rec = r.post(r.path()+"/links/insert", url.Values{"content": {"<p>nothing here</p>"}, "target": {target.String()}, "anchor": {"Go"}})
	if json.Unmarshal(rec.Body.Bytes(), &ins) != nil || ins.Inserted || ins.Reason != "Phrase not found" {
		t.Errorf("no match: %s", rec.Body.String())
	}
	if rec = r.post(r.path()+"/links/insert", url.Values{"content": {"x"}, "target": {"nope"}}); rec.Code != 400 {
		t.Errorf("bad target: %d", rec.Code)
	}
	// CSRF: a cross-site post is refused like every admin write
	if rec = r.do("POST", r.path()+"/links/suggest", "owner-token", url.Values{"content": {"x"}}, map[string]string{"X-CSRF-Token": "wrong"}); rec.Code == 200 {
		t.Errorf("suggest without a valid CSRF token = %d", rec.Code)
	}
	// without the linking service the panel says so
	r.a.deps.Growth.Linking = nil
	if rec = r.post(r.path()+"/links/suggest", url.Values{"content": {"x"}}); rec.Code != 503 {
		t.Errorf("no linking: %d", rec.Code)
	}
}
