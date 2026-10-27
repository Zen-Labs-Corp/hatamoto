package admin

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
)

const (
	nodesV2 = `{"v":2,"nodes":[
		{"id":"hero","t":"container","c":[
			{"id":"h1","t":"heading","p":{"text":"Welcome","level":2}},
			{"id":"t1","t":"text","p":{"text":"keep"}}]},
		{"id":"stats","t":"container","c":[
			{"id":"s1","t":"text","p":{"text":"a"}},
			{"id":"s2","t":"text","p":{"text":"b"}}]},
		{"id":"gallery","t":"text","p":{"text":"gal"}}]}`
	// nodesV3: h1 changed, gallery moved first, stats removed, pricing added.
	nodesV3 = `{"v":2,"nodes":[
		{"id":"gallery","t":"text","p":{"text":"gal"}},
		{"id":"hero","t":"container","c":[
			{"id":"h1","t":"heading","p":{"text":"Hello","level":2}},
			{"id":"t1","t":"text","p":{"text":"keep"}}]},
		{"id":"pricing","t":"text","p":{"text":"price"}}]}`
)

// versionsRig is a builder page at version 3: v1 is the seed doc, v2 holds
// nodesV2 and the current (v3) holds nodesV3.
type versionsRig struct {
	*saveRig
}

func newVersionsRig(t *testing.T) *versionsRig {
	r := &versionsRig{newSaveRig(t)}
	r.setDoc(t, nodesV2)
	r.setDoc(t, nodesV3)
	if v := r.ents.ents[r.page.ID].Version; v != 3 {
		t.Fatalf("version = %d, want 3", v)
	}
	return r
}

func (r *versionsRig) setDoc(t *testing.T, doc string) {
	t.Helper()
	attrs := json.RawMessage(`{"title":"T","page_doc":` + doc + `,"page_sections":[]}`)
	sum := "set"
	if _, err := r.ents.UpdateEntity(context.Background(), r.page.ID, nil, &entity.UpdateEntityInput{Attributes: &attrs, ChangeSummary: &sum}); err != nil {
		t.Fatal(err)
	}
}

func (r *versionsRig) compare(t *testing.T, from, to string) string {
	t.Helper()
	return r.get(t, "owner-token", r.url(r.page, "/versions/compare?from="+from+"&to="+to), "")
}

func (r *versionsRig) restore(token, version, node string, form url.Values, hdr map[string]string) (int, string) {
	rec := r.do("POST", r.url(r.page, "/versions/"+version+"/nodes/"+node+"/restore"), token, form, hdr)
	return rec.Code, rec.Body.String()
}

func (r *versionsRig) currentDoc(t *testing.T) *builder.Doc {
	t.Helper()
	d, st := builderDoc(r.ents.ents[r.page.ID])
	if st != docOK {
		t.Fatal("current doc unreadable")
	}
	return d
}

func baseForm(v string) url.Values { return url.Values{"base_version": {v}, "_csrf": {csrfValue}} }

func TestCompareShowsLayoutChanges(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t)
	body := r.get(t, "owner-token", r.url(r.page, "/versions/compare?from=2&to=0"), "entity-compare-layout")
	has(t, body, "Layout changes", "Welcome", "Hello", "Moved", "Removed", "Added", "(3 nodes)", "pricing"[:0]+"price",
		`action="/admin/entities/`+r.page.ID.String()+`/versions/2/nodes/stats/restore"`,
		`name="base_version" value="3"`, `name="_csrf"`, "Restore this node from v2")
	lacks(t, body, "<code>page_doc", "<code>page_sections")
	// An added node has nothing to restore from the older version.
	lacks(t, body, "/nodes/pricing/restore")
}

func TestCompareHidesRestoreForCurrentAsOlderSide(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t)
	body := r.compare(t, "0", "2")
	has(t, body, "Layout changes")
	lacks(t, body, "/restore\"")
}

func TestCompareHidesRestoreFromViewer(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t)
	r.editor.Role = "viewer"
	body := r.get(t, "editor-token", r.url(r.page, "/versions/compare?from=2&to=0"), "")
	has(t, body, "Layout changes")
	lacks(t, body, "Restore this node")
}

func TestCompareLegacyVersionsKeepTheFlatScreen(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t) // r.ent is a classic section page
	attrs := json.RawMessage(`{"body":"changed"}`)
	if _, err := r.ents.UpdateEntity(context.Background(), r.ent.ID, nil, &entity.UpdateEntityInput{Attributes: &attrs}); err != nil {
		t.Fatal(err)
	}
	body := r.get(t, "owner-token", "/admin/entities/"+r.ent.ID.String()+"/versions/compare?from=1&to=0", "")
	lacks(t, body, "Layout changes", "cannot be read")
	has(t, body, "<code>body</code>")
}

func TestCompareUnreadableLayoutFallsBackWithNote(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t)
	r.setDoc(t, `{"v":2,"nodes":[{"id":"bad id!","t":"container"}]}`)
	body := r.compare(t, "2", "0")
	has(t, body, layoutNote, "<code>page_doc")
	lacks(t, body, "Restore this node")
}

func TestCompareTruncatesLongLayoutLists(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t)
	var nodes []string
	for i := 0; i < 230; i++ {
		nodes = append(nodes, `{"id":"n-`+string(rune('a'+i/26))+string(rune('a'+i%26))+`","t":"text","p":{"text":"t"}}`)
	}
	r.setDoc(t, `{"v":2,"nodes":[`+strings.Join(nodes, ",")+`]}`)
	body := r.compare(t, "2", "0")
	if !regexp.MustCompile(`and \d+ more changes`).MatchString(body) {
		t.Error("no 'and N more' line")
	}
}

func TestRestoreNodeBringsBackARemovedSubtree(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t)
	code, body := r.restore("owner-token", "2", "stats", baseForm("3"), nil)
	if code != 303 {
		t.Fatalf("%d %s", code, body)
	}
	e := r.ents.ents[r.page.ID]
	if e.Version != 4 {
		t.Fatalf("version = %d, want one new version", e.Version)
	}
	vs, _ := r.ents.ListVersions(context.Background(), e.ID, 10)
	// The fake keeps each snapshot with the summary of the save that replaced it.
	if s := vs[0].ChangeSummary; s == nil || *s != "Restored node “stats” from version 2" {
		t.Errorf("summary = %v", deref(s))
	}
	d := r.currentDoc(t)
	n, p := d.Find("stats")
	if n == nil || p != nil || len(n.C) != 2 || n.C[1].ID != "s2" {
		t.Fatalf("stats = %+v", n)
	}
	if ids := topIDs(d); ids != "gallery,hero,stats,pricing" {
		t.Errorf("top level = %s (stats goes back after hero)", ids)
	}
	if h, _ := d.Find("h1"); h.P["text"] != "Hello" {
		t.Error("another node was reverted")
	}
	if !strings.Contains(string(e.Attributes), `"page_sections":[]`) {
		t.Errorf("a restore changed the frozen mirror: %s", e.Attributes)
	}
}

func TestRestoreNodeReplacesAChangedNodeInPlace(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t)
	if code, body := r.restore("owner-token", "2", "h1", baseForm("3"), nil); code != 303 {
		t.Fatalf("%d %s", code, body)
	}
	d := r.currentDoc(t)
	h, p := d.Find("h1")
	if h.P["text"] != "Welcome" || p == nil || p.ID != "hero" || p.C[0].ID != "h1" {
		t.Errorf("h1 = %+v parent %v", h, p)
	}
}

func TestRestoreNodeReturnsAMovedNode(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t)
	if code, body := r.restore("owner-token", "2", "gallery", baseForm("3"), nil); code != 303 {
		t.Fatalf("%d %s", code, body)
	}
	d := r.currentDoc(t)
	if ids := topIDs(d); ids != "hero,gallery,pricing" {
		t.Errorf("top level = %s", ids)
	}
}

func topIDs(d *builder.Doc) string {
	var ids []string
	for _, n := range d.Nodes {
		ids = append(ids, n.ID)
	}
	return strings.Join(ids, ",")
}

func TestRestoreNodeRefusesAStalePage(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t)
	before := string(r.ents.ents[r.page.ID].Attributes)
	for _, base := range []string{"2", "", "x"} {
		code, body := r.restore("owner-token", "2", "stats", baseForm(base), nil)
		if code != 409 || !strings.Contains(body, "This page changed since you opened the comparison") {
			t.Errorf("base %q: %d", base, code)
		}
	}
	e := r.ents.ents[r.page.ID]
	if e.Version != 3 || string(e.Attributes) != before {
		t.Error("a stale restore wrote")
	}
}

func TestRestoreNodeRefusesAnIDCollision(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t)
	// v2 has stats[s1,s2]; the current page now uses s2 for another node.
	r.setDoc(t, `{"v":2,"nodes":[{"id":"hero","t":"container","c":[{"id":"s2","t":"text","p":{"text":"other"}}]}]}`)
	rec := r.do("POST", r.url(r.page, "/versions/2/nodes/stats/restore"), "owner-token", baseForm("4"), nil)
	if rec.Code != 303 {
		t.Fatalf("%d", rec.Code)
	}
	if e := r.ents.ents[r.page.ID]; e.Version != 4 {
		t.Fatalf("version = %d: a colliding restore wrote", e.Version)
	}
	body := r.next(rec, r.url(r.page, "/versions"))
	if !strings.Contains(body, "cannot be restored") || !strings.Contains(body, "s2") {
		t.Error("no collision flash")
	}
}

func TestRestoreNodeNotFoundCases(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t)
	for name, c := range map[string][2]string{
		"missing node":     {"2", "nope"},
		"missing version":  {"9", "stats"},
		"current version":  {"3", "stats"},
		"version zero":     {"0", "stats"},
		"not a number":     {"abc", "stats"},
		"node not in that": {"1", "stats"},
	} {
		if code, _ := r.restore("owner-token", c[0], c[1], baseForm("3"), nil); code != 404 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if r.ents.ents[r.page.ID].Version != 3 {
		t.Error("a refused restore wrote")
	}
}

func TestRestoreNodeNeedsEditRoleAndCSRF(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t)
	r.editor.Role = "viewer"
	if code, _ := r.restore("editor-token", "2", "stats", baseForm("3"), nil); code != 403 {
		t.Errorf("viewer: %d", code)
	}
	noToken := url.Values{"base_version": {"3"}}
	if code, _ := r.restore("owner-token", "2", "stats", noToken, map[string]string{"X-CSRF-Token": ""}); code != 403 {
		t.Errorf("no csrf: %d", code)
	}
	rec := r.do("POST", r.url(r.page, "/versions/2/nodes/stats/restore"), "", baseForm("3"), nil)
	if loc := rec.Header().Get("Location"); rec.Code != 303 || strings.Contains(loc, "/versions") {
		t.Errorf("anonymous: %d %s", rec.Code, loc)
	}
	if r.ents.ents[r.page.ID].Version != 3 {
		t.Error("a refused restore wrote")
	}
}

func TestRestoreNodeNeedsABuilderPage(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t)
	rec := r.do("POST", "/admin/entities/"+r.ent.ID.String()+"/versions/1/nodes/x/restore", "owner-token", baseForm("1"), nil)
	if rec.Code != 404 {
		t.Errorf("classic page: %d", rec.Code)
	}
}

func parseDoc(t *testing.T, s string) *builder.Doc {
	t.Helper()
	d, err := builder.Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRestoreNodeParentGone(t *testing.T) {
	t.Parallel()

	old := parseDoc(t, `{"v":2,"nodes":[{"id":"p","t":"container","c":[{"id":"x","t":"text","p":{"text":"x"}}]}]}`)
	cur := parseDoc(t, `{"v":2,"nodes":[{"id":"a","t":"text","p":{"text":"a"}}]}`)
	note, err := restoreNode(cur, old, "x")
	if err != nil || !strings.Contains(note, "end of the top level") {
		t.Fatalf("note %q err %v", note, err)
	}
	if topIDs(cur) != "a,x" {
		t.Errorf("top = %s", topIDs(cur))
	}
}

func TestRestoreNodeRefusalLeavesTheDocAlone(t *testing.T) {
	t.Parallel()

	old := parseDoc(t, `{"v":2,"nodes":[{"id":"p","t":"container","c":[{"id":"x","t":"text"},{"id":"y","t":"text"}]}]}`)
	cur := parseDoc(t, `{"v":2,"nodes":[{"id":"y","t":"text"}]}`)
	before, _ := json.Marshal(cur)
	if _, err := restoreNode(cur, old, "p"); err == nil {
		t.Fatal("collision accepted")
	}
	if _, err := restoreNode(cur, old, "missing"); err == nil {
		t.Fatal("missing node accepted")
	}
	after, _ := json.Marshal(cur)
	if string(before) != string(after) {
		t.Errorf("doc changed: %s", after)
	}
}

func TestRestoreNodeOldIndexIsClamped(t *testing.T) {
	t.Parallel()

	old := parseDoc(t, `{"v":2,"nodes":[{"id":"a","t":"text"},{"id":"b","t":"text"},{"id":"c","t":"text"}]}`)
	cur := parseDoc(t, `{"v":2,"nodes":[{"id":"a","t":"text"}]}`)
	if _, err := restoreNode(cur, old, "c"); err != nil || topIDs(cur) != "a,c" {
		t.Errorf("top = %s err %v", topIDs(cur), err)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestRestoreNodeOfAChangedContainerKeepsItsCurrentChildren(t *testing.T) {
	t.Parallel()

	old := parseDoc(t, `{"v":2,"nodes":[{"id":"box","t":"container","p":{"tag":"div"},"c":[{"id":"a","t":"text","p":{"text":"a"}},{"id":"gone","t":"text","p":{"text":"g"}}]}]}`)
	cur := parseDoc(t, `{"v":2,"nodes":[{"id":"box","t":"container","p":{"tag":"section"},"c":[{"id":"a","t":"text","p":{"text":"A2"}},{"id":"new","t":"text","p":{"text":"n"}}]}]}`)
	if _, err := restoreNode(cur, old, "box"); err != nil {
		t.Fatal(err)
	}
	box, _ := cur.Find("box")
	if box.P["tag"] != "div" {
		t.Errorf("own props not restored: %v", box.P)
	}
	var ids []string
	for _, c := range box.C {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, ",") != "a,new" {
		t.Errorf("children = %v, want the current a,new", ids)
	}
	if a, _ := cur.Find("a"); a.P["text"] != "A2" {
		t.Error("a child was reverted")
	}
}

func TestRestoreNodeOfAMovedContainerKeepsItsCurrentChildren(t *testing.T) {
	t.Parallel()

	old := parseDoc(t, `{"v":2,"nodes":[{"id":"box","t":"container","c":[{"id":"a","t":"text"}]},{"id":"z","t":"text"},{"id":"y","t":"text"}]}`)
	cur := parseDoc(t, `{"v":2,"nodes":[{"id":"z","t":"text"},{"id":"y","t":"text"},{"id":"box","t":"container","c":[{"id":"new","t":"text"}]}]}`)
	if _, err := restoreNode(cur, old, "box"); err != nil {
		t.Fatal(err)
	}
	if topIDs(cur) != "box,z,y" {
		t.Errorf("top = %s", topIDs(cur))
	}
	if box, _ := cur.Find("box"); len(box.C) != 1 || box.C[0].ID != "new" {
		t.Errorf("children = %+v", box.C)
	}
}

func TestNodeNamePrefersNavlabel(t *testing.T) {
	t.Parallel()

	n := &builder.Node{ID: "h", T: "heading", P: map[string]any{"text": "Welcome", "navlabel": "Hero title"}}
	if got := nodeName(n); got != "Hero title" {
		t.Errorf("nodeName = %q, want the navlabel", got)
	}
}

func TestNodeNameFallsBackToInlineHTML(t *testing.T) {
	n := &builder.Node{ID: "h", T: "heading", P: map[string]any{"html": "Big <em>news</em> &amp; more"}}
	if got := nodeName(n); got != "Big news & more" {
		t.Errorf("nodeName = %q", got)
	}
	n.P["text"] = "Plain"
	if got := nodeName(n); got != "Plain" {
		t.Errorf("nodeName = %q, want text first", got)
	}
	if got := nodeName(&builder.Node{ID: "x", T: "heading", P: map[string]any{"html": "<br>"}}); got != "x" {
		t.Errorf("nodeName = %q, want the id", got)
	}
}

func TestFieldDetailsFormattingNote(t *testing.T) {
	got := fieldDetails(builder.NodeChange{Props: []builder.FieldChange{{Key: "html", Kind: builder.DiffChanged, Old: "a b", New: "a b", Note: "formatting changed"}}})
	if len(got) != 1 || got[0] != "html: “a b” → “a b” (formatting changed)" {
		t.Errorf("details = %q", got)
	}
}
