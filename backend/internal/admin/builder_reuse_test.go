package admin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

const (
	blockSchema = `{"type":"object","properties":{"description":{"type":"string"}}}`
	partSchema  = `{"type":"object","required":["slot"],"properties":{"slot":{"type":"string","enum":["header","footer","single","archive"]}}}`
	blockDoc    = `{"v":2,"nodes":[{"id":"b1","t":"heading","p":{"text":"Block","level":2}}]}`
)

// reuseRig is a save rig with the block and theme_part types and one block.
type reuseRig struct {
	*saveRig
	blockSc, partSc *entity.EntitySchema
	block           *entity.Entity
}

func newReuseRig(t *testing.T) *reuseRig {
	r := &reuseRig{saveRig: newSaveRig(t)}
	r.blockSc = r.ents.addSchema(8, "Block", "block", blockSchema, `{}`, true)
	r.partSc = r.ents.addSchema(9, "Theme part", "theme_part", partSchema, `{}`, true)
	r.block = r.ents.addEntity(80, r.blockSc, "Footer CTA", "footer-cta", entity.StatusDraft, `{"page_doc":`+blockDoc+`}`, t0)
	return r
}

func (r *reuseRig) part(n int, slot, doc string) *entity.Entity {
	return r.ents.addEntity(n, r.partSc, "Part "+slot, "part-"+slot, entity.StatusDraft, `{"slot":"`+slot+`","page_doc":`+doc+`}`, t0)
}

func (r *reuseRig) getJSON(t *testing.T, token, path string, into any) int {
	t.Helper()
	rec := r.do("GET", path, token, nil, nil)
	if into != nil && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
			t.Fatalf("%s: %v: %q", path, err, rec.Body)
		}
	}
	return rec.Code
}

func refDoc(id string) string {
	return `{"v":2,"nodes":[{"id":"r1","t":"ref","p":{"block":"` + id + `"}}]}`
}

func TestDraftRouteCreatesBlocksAndParts(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	rec := r.do("POST", "/admin/entities/drafts", "owner-token", url.Values{"type": {"block"}}, nil)
	if rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		t.Fatalf("block draft: %d %s", rec.Code, rec.Body)
	}
	for _, slot := range []string{"header", "footer", "single", "archive"} {
		rec := r.do("POST", "/admin/entities/drafts", "editor-token", url.Values{"type": {"theme_part"}, "slot": {slot}}, nil)
		if rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
			t.Fatalf("%s part draft: %d %s", slot, rec.Code, rec.Body)
		}
	}
	var single, header *entity.Entity
	for _, e := range r.ents.ents {
		switch e.Name {
		case "Untitled single part":
			single = e
		case "Untitled header part":
			header = e
		}
	}
	if single == nil || header == nil {
		t.Fatal("part drafts not created")
	}
	if !strings.Contains(string(single.Attributes), `"t":"slot"`) || strings.Contains(string(header.Attributes), `"t":"slot"`) {
		t.Errorf("slot seeding: single %s header %s", single.Attributes, header.Attributes)
	}
	// A second click opens the same untouched draft.
	n := len(r.ents.ents)
	r.do("POST", "/admin/entities/drafts", "owner-token", url.Values{"type": {"block"}}, nil)
	r.do("POST", "/admin/entities/drafts", "editor-token", url.Values{"type": {"theme_part"}, "slot": {"header"}}, nil)
	if len(r.ents.ents) != n {
		t.Errorf("untouched drafts piled up: %d -> %d", n, len(r.ents.ents))
	}
	// A part needs a real slot; a block-type slug owned by a site type is refused.
	if rec := r.do("POST", "/admin/entities/drafts", "owner-token", url.Values{"type": {"theme_part"}, "slot": {"nope"}}, nil); rec.Code != 400 {
		t.Errorf("bad slot: %d", rec.Code)
	}
	r.blockSc.IsBuiltin = false
	if rec := r.do("POST", "/admin/entities/drafts", "owner-token", url.Values{"type": {"block"}}, nil); rec.Code != 400 {
		t.Errorf("site-owned block type: %d", rec.Code)
	}
}

func TestBlockAndPartOpenTheBuilderWithoutPublish(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	body := r.get(t, "owner-token", r.url(r.block, "/edit"), "")
	has(t, body, `data-hm-builder`, "Save block", `data-hm-bs-usage`, "No page uses this block yet", `aria-label="Block name"`)
	lacks(t, body, ">Publish<", "Update page", "Delete page", "This entity", "Delete entity")
	has(t, body, `data-hm-confirm="This block and its version history will be deleted. This cannot be undone."`, `data-hm-confirm-label="Delete block"`, `data-hm-danger>Delete block</a>`)
	if i := strings.Index(body, `"reuse":`); i < 0 || !strings.Contains(body[i:], `"role":"block"`) {
		t.Errorf("boot reuse missing: %.300s", body)
	}
	part := r.part(81, "header", `{"v":2,"nodes":[]}`)
	body = r.get(t, "owner-token", r.url(part, "/edit"), "")
	has(t, body, "Save part", `"role":"part"`, `"slot":"header"`, `data-hm-confirm-label="Delete part"`, `data-hm-danger>Delete part</a>`)
	lacks(t, body, ">Publish<", "data-hm-bs-usage", "Delete page", "This entity", "Delete entity")
}

func TestBlockShellListsUsage(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	r.page.Attributes = json.RawMessage(`{"page_doc":` + refDoc(r.block.ID.String()) + `}`)
	body := r.get(t, "owner-token", r.url(r.block, "/edit"), "")
	has(t, body, "Used on 1 page", `<a href="/admin/entities/`+r.page.ID.String()+`/edit">Builder only</a>`, "Missing block")
}

func TestBlockAndPartSaveRules(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	for _, op := range []string{"autosave", "publish"} {
		code, out := r.save(t, r.block, saveBody(1, blockDoc, op, ""))
		if code != 409 || out.Code != "not_savable_op" {
			t.Errorf("block %s: %d %+v", op, code, out)
		}
	}
	if code, out := r.save(t, r.block, saveBody(1, `{"v":2,"nodes":[{"id":"b1","t":"heading","p":{"text":"New","level":2}}]}`, "save", "")); code != 200 || !out.OK || out.Status != "draft" {
		t.Fatalf("block save: %d %+v", code, out)
	}

	slot := `{"v":2,"nodes":[{"id":"s1","t":"slot"}]}`
	two := `{"v":2,"nodes":[{"id":"s1","t":"slot"},{"id":"s2","t":"slot"}]}`
	empty := `{"v":2,"nodes":[]}`
	for _, tc := range []struct {
		slot, doc string
		code      int
	}{
		{"header", slot, 422}, {"header", empty, 200}, {"single", empty, 422}, {"single", two, 422}, {"single", slot, 200}, {"archive", slot, 200},
	} {
		p := r.part(82, tc.slot, `{"v":2,"nodes":[]}`)
		code, out := r.save(t, p, saveBody(1, tc.doc, "save", ""))
		if code != tc.code || (tc.code == 422 && out.Code != "part_slot") {
			t.Errorf("%s part %s: %d %+v", tc.slot, tc.doc, code, out)
		}
		delete(r.ents.ents, p.ID)
	}
	// replace is only for archive parts.
	repl := `{"v":2,"nodes":[{"id":"s1","t":"slot","p":{"replace":true}}]}`
	for _, tc := range []struct {
		slot string
		code int
	}{{"archive", 200}, {"single", 422}} {
		p := r.part(83, tc.slot, `{"v":2,"nodes":[]}`)
		code, out := r.save(t, p, saveBody(1, repl, "save", ""))
		if code != tc.code || (tc.code == 422 && out.Code != "part_slot") {
			t.Errorf("%s part with replace: %d %+v", tc.slot, code, out)
		}
		delete(r.ents.ents, p.ID)
	}
	// A page may not hold a slot.
	if code, out := r.save(t, r.page, saveBody(1, slot, "save", "")); code != 422 || out.Code != "part_slot" {
		t.Errorf("page with slot: %d %+v", code, out)
	}
}

func TestBlockSaveRefusesCycles(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	other := r.ents.addEntity(83, r.blockSc, "Other", "other", entity.StatusDraft, `{"page_doc":`+refDoc(r.block.ID.String())+`}`, t0)
	// block -> other -> block
	code, out := r.save(t, r.block, saveBody(1, refDoc(other.ID.String()), "save", ""))
	if code != 422 || out.Code != "ref_cycle" || !strings.Contains(out.Error, r.block.ID.String()) || !strings.Contains(out.Error, other.ID.String()) {
		t.Fatalf("cycle: %d %+v", code, out)
	}
	// Itself.
	if code, out := r.save(t, r.block, saveBody(1, refDoc(r.block.ID.String()), "save", "")); code != 422 || out.Code != "ref_cycle" {
		t.Errorf("self: %d %+v", code, out)
	}
	// A page may reference a block; a missing block is fine.
	if code, out := r.save(t, r.page, saveBody(1, refDoc(r.block.ID.String()), "save", "")); code != 200 {
		t.Errorf("page ref: %d %+v", code, out)
	}
	if code, _ := r.save(t, r.page, saveBody(2, refDoc("00000000-0000-0000-0000-00000000dead"), "save", "")); code != 200 {
		t.Errorf("missing block ref refused: %d", code)
	}
}

func TestMakeBlock(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	doc := `{"v":2,"nodes":[{"id":"c1","t":"container","c":[{"id":"h1","t":"heading","p":{"text":"Hi","level":2}}]},{"id":"c2","t":"container"}]}`
	post := func(token, body string) (int, map[string]string) {
		rec := r.postJSON(r.url(r.page, "/builder/block"), token, body, nil)
		var m map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return rec.Code, m
	}
	before := string(r.page.Attributes)
	code, m := post("owner-token", `{"name":"Hero","nodeId":"c1","doc":`+doc+`}`)
	if code != 201 || m["id"] == "" || m["name"] != "Hero" {
		t.Fatalf("make: %d %v", code, m)
	}
	var made *entity.Entity
	for _, e := range r.ents.ents {
		if e.Name == "Hero" {
			made = e
		}
	}
	if made == nil || made.SchemaID != r.blockSc.ID || made.Status != entity.StatusDraft {
		t.Fatalf("created: %+v", made)
	}
	stored := string(made.Attributes)
	if !strings.Contains(stored, `"Hi"`) || strings.Contains(stored, `"c1"`) || strings.Contains(stored, `"h1"`) || strings.Contains(stored, `"c2"`) {
		t.Errorf("block doc is not a fresh copy of the subtree: %s", stored)
	}
	if string(r.ents.ents[r.page.ID].Attributes) != before {
		t.Error("the page was changed")
	}
	if code, m := post("editor-token", `{"name":"Whole","doc":`+doc+`}`); code != 201 {
		t.Errorf("whole page as editor: %d %v", code, m)
	}
	for name, body := range map[string]string{
		"no name":      `{"name":" ","nodeId":"c1","doc":` + doc + `}`,
		"unknown node": `{"name":"X","nodeId":"zz","doc":` + doc + `}`,
		"slot":         `{"name":"X","doc":{"v":2,"nodes":[{"id":"s","t":"slot"}]}}`,
		"empty":        `{"name":"X","doc":{"v":2,"nodes":[]}}`,
		"bad doc":      `{"name":"X","doc":{"v":2,"nodes":[{"id":"a","t":"nope"}]}}`,
	} {
		if code, m := post("owner-token", body); code != 422 {
			t.Errorf("%s: %d %v", name, code, m)
		}
	}
	if rec := r.postJSON(r.url(r.page, "/builder/block"), "owner-token", `{"name":"x"}`, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
		t.Errorf("csrf: %d", rec.Code)
	}
	if rec := r.postJSON(r.url(r.page, "/builder/block"), "", `{"name":"x"}`, nil); rec.Code == 201 {
		t.Error("anonymous created a block")
	}
}

func TestBlockListNodesAndUsage(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	r.ents.addEntity(84, r.blockSc, "Alpha", "alpha-b", entity.StatusDraft, `{"page_doc":{"v":2,"nodes":[]}}`, t0)
	var list []blockItem
	if code := r.getJSON(t, "editor-token", "/admin/builder/blocks", &list); code != 200 || len(list) != 2 || list[0].Name != "Alpha" {
		t.Fatalf("list: %d %+v", code, list)
	}
	if code := r.getJSON(t, "owner-token", "/admin/builder/blocks?q=footer", &list); code != 200 || len(list) != 1 || list[0].Name != "Footer CTA" {
		t.Fatalf("search: %d %+v", code, list)
	}

	var a, b struct {
		Name  string `json:"name"`
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	}
	path := "/admin/builder/blocks/" + r.block.ID.String()
	if code := r.getJSON(t, "owner-token", path, &a); code != 200 || a.Name != "Footer CTA" || len(a.Nodes) != 1 || a.Nodes[0].ID == "b1" {
		t.Fatalf("nodes: %d %+v", code, a)
	}
	r.getJSON(t, "owner-token", path, &b)
	if a.Nodes[0].ID == b.Nodes[0].ID {
		t.Error("ids repeat between fetches")
	}
	if code := r.getJSON(t, "owner-token", "/admin/builder/blocks/"+r.page.ID.String(), nil); code != 404 {
		t.Errorf("a page is not a block: %d", code)
	}
	if code := r.getJSON(t, "owner-token", "/admin/builder/blocks/nope/usage", nil); code != 404 {
		t.Errorf("bad id: %d", code)
	}

	r.page.Attributes = json.RawMessage(`{"page_doc":` + refDoc(r.block.ID.String()) + `}`)
	r.ents.addEntity(85, r.ents.schemaOf(fid(2)), "Text only", "text-only", entity.StatusDraft, `{"body":"`+r.block.ID.String()+`"}`, t0)
	var use struct {
		Users  []usageItem `json:"users"`
		Capped bool        `json:"capped"`
	}
	if code := r.getJSON(t, "owner-token", path+"/usage", &use); code != 200 || len(use.Users) != 1 || use.Users[0].ID != r.page.ID.String() ||
		use.Users[0].Href != "/admin/entities/"+r.page.ID.String()+"/edit" || use.Capped {
		t.Fatalf("usage: %d %+v", code, use)
	}
}
