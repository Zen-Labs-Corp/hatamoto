package admin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/sectiontemplate"
)

const tplDoc = `{"v":2,"nodes":[{"id":"c1","t":"container","c":[{"id":"h1","t":"heading","p":{"text":"Hi","level":2}},{"id":"t1","t":"text","p":{"text":"Body"}}]},{"id":"c2","t":"container"}]}`

type tplOut struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Kind  string          `json:"kind"`
	Nodes []*builder.Node `json:"nodes"`
	Error string          `json:"error"`
	Code  string          `json:"code"`
}

func (r *reuseRig) saveTemplate(t *testing.T, token, body string) (int, tplOut) {
	t.Helper()
	rec := r.postJSON(r.url(r.page, "/builder/template"), token, body, nil)
	var out tplOut
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (r *reuseRig) fetchTemplate(t *testing.T, id string) (int, tplOut) {
	t.Helper()
	var out tplOut
	code := r.getJSON(t, "owner-token", "/admin/builder/templates/"+id, &out)
	return code, out
}

func collectIDs(ns []*builder.Node, into map[string]int) {
	for _, n := range ns {
		into[n.ID]++
		collectIDs(n.C, into)
	}
}

func TestSaveSelectionAndPageAsTemplate(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	code, out := r.saveTemplate(t, "owner-token", `{"name":"Hero","description":"d","nodeId":"c1","doc":`+tplDoc+`}`)
	if code != 201 || out.ID == "" || out.Name != "Hero" {
		t.Fatalf("selection: %d %+v", code, out)
	}
	stored := r.tpls.items[0]
	if stored.SectionType != treeTemplateType || stored.Description != "d" || !strings.Contains(string(stored.Content), `"v":1`) || strings.Contains(string(stored.Content), `"c2"`) {
		t.Fatalf("stored: %+v %s", stored, stored.Content)
	}
	if code, _ := r.saveTemplate(t, "editor-token", `{"name":"Whole","page":true,"doc":`+tplDoc+`}`); code != 201 {
		t.Errorf("page: %d", code)
	}
	if !strings.Contains(string(r.tpls.items[1].Content), `"c2"`) {
		t.Error("page template misses the second top node")
	}
	for name, body := range map[string]string{
		"no name":      `{"name":"","page":true,"doc":` + tplDoc + `}`,
		"long name":    `{"name":"` + strings.Repeat("n", 256) + `","page":true,"doc":` + tplDoc + `}`,
		"unknown node": `{"name":"X","nodeId":"zz","doc":` + tplDoc + `}`,
		"empty page":   `{"name":"X","page":true,"doc":{"v":2,"nodes":[]}}`,
		"invalid doc":  `{"name":"X","page":true,"doc":{"v":2,"nodes":[{"id":"a","t":"nope"}]}}`,
		"slot":         `{"name":"X","page":true,"doc":{"v":2,"nodes":[{"id":"s","t":"slot"}]}}`,
	} {
		if code, out := r.saveTemplate(t, "owner-token", body); code != 422 {
			t.Errorf("%s: %d %+v", name, code, out)
		}
	}
	if len(r.tpls.items) != 2 {
		t.Errorf("a refused save stored a template: %d", len(r.tpls.items))
	}
	if rec := r.postJSON(r.url(r.page, "/builder/template"), "owner-token", `{"name":"x","page":true}`, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
		t.Errorf("csrf: %d", rec.Code)
	}
}

func TestSaveTemplateRefusesOversize(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	// Valid nodes whose stored content passes the 512 KB limit: many long texts.
	var sb strings.Builder
	sb.WriteString(`{"v":2,"nodes":[`)
	for i := 0; i < 90; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"id":"t` + strings.Repeat("x", 3) + string(rune('a'+i/26)) + string(rune('a'+i%26)) + `","t":"text","p":{"text":"` + strings.Repeat("a", 5800) + `"}}`)
	}
	sb.WriteString(`]}`)
	code, out := r.saveTemplate(t, "owner-token", `{"name":"Big","page":true,"doc":`+sb.String()+`}`)
	if code != 422 || len(r.tpls.items) != 0 {
		t.Errorf("oversize: %d %+v (stored %d)", code, out, len(r.tpls.items))
	}
}

func TestInsertingATemplateGivesFreshIDs(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	_, saved := r.saveTemplate(t, "owner-token", `{"name":"Hero","page":true,"doc":`+tplDoc+`}`)
	code, a := r.fetchTemplate(t, saved.ID)
	_, b := r.fetchTemplate(t, saved.ID)
	if code != 200 || a.Kind != "tree" || len(a.Nodes) != 2 {
		t.Fatalf("fetch: %d %+v", code, a)
	}
	seen := map[string]int{}
	collectIDs(a.Nodes, seen)
	collectIDs(b.Nodes, seen)
	if len(seen) != 8 {
		t.Errorf("ids must be unique inside a doc and across inserts: %v", seen)
	}
	for id := range seen {
		if id == "c1" || id == "h1" || id == "t1" || id == "c2" || !builder.ValidID(id) {
			t.Errorf("id %q", id)
		}
	}
	if len(r.tpls.used) != 0 {
		t.Errorf("a GET must not change the usage counter, recorded %d times", len(r.tpls.used))
	}
	// A node this build cannot read is skipped, the rest inserts.
	future := sectiontemplate.SectionTemplate{ID: uuid.New(), Name: "Future", SectionType: treeTemplateType,
		Content: json.RawMessage(`{"v":1,"nodes":[{"id":"a","t":"hologram"},{"id":"b","t":"container"}]}`)}
	r.tpls.items = append(r.tpls.items, future)
	if code, out := r.fetchTemplate(t, future.ID.String()); code != 200 || len(out.Nodes) != 1 || out.Nodes[0].T != "container" {
		t.Errorf("unknown kind: %d %+v", code, out)
	}
	none := sectiontemplate.SectionTemplate{ID: uuid.New(), Name: "None", SectionType: treeTemplateType, Content: json.RawMessage(`{"v":1,"nodes":[{"id":"a","t":"hologram"}]}`)}
	r.tpls.items = append(r.tpls.items, none)
	if code, _ := r.fetchTemplate(t, none.ID.String()); code != 422 {
		t.Errorf("nothing readable: %d", code)
	}
	if code, _ := r.fetchTemplate(t, uuid.NewString()); code != 404 {
		t.Errorf("unknown template: %d", code)
	}
}

func TestLegacySectionTemplateInsertsAsAComponent(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	legacy := sectiontemplate.SectionTemplate{ID: uuid.New(), Name: "Old hero", SectionType: "hero", Content: json.RawMessage(`{"heading":"Hello"}`)}
	odd := sectiontemplate.SectionTemplate{ID: uuid.New(), Name: "Gone", SectionType: "not_in_theme", Content: json.RawMessage(`{}`)}
	r.tpls.items = append(r.tpls.items, legacy, odd)
	code, out := r.fetchTemplate(t, legacy.ID.String())
	if code != 200 || out.Kind != "section" || len(out.Nodes) != 1 || out.Nodes[0].T != "component" || out.Nodes[0].P["type"] != "hero" {
		t.Fatalf("legacy: %d %+v", code, out)
	}
	if c, _ := out.Nodes[0].P["content"].(map[string]any); c["heading"] != "Hello" {
		t.Errorf("content: %v", out.Nodes[0].P)
	}
	if code, out := r.fetchTemplate(t, odd.ID.String()); code != 422 || out.Code != "section_type" {
		t.Errorf("type not in the theme: %d %+v", code, out)
	}
}

func TestBootListsTemplatesOfBothKinds(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	r.saveTemplate(t, "owner-token", `{"name":"Hero tree","page":true,"doc":`+tplDoc+`}`)
	r.tpls.items = append(r.tpls.items,
		sectiontemplate.SectionTemplate{ID: uuid.New(), Name: "Old hero", SectionType: "hero", Content: json.RawMessage(`{}`)},
		sectiontemplate.SectionTemplate{ID: uuid.New(), Name: "Gone", SectionType: "not_in_theme", Content: json.RawMessage(`{}`)})
	body := r.get(t, "owner-token", r.url(r.page, "/edit"), "")
	has(t, body, `"name":"Hero tree"`, `"kind":"tree"`, `"name":"Old hero"`, `"kind":"section"`, `"saveTemplate":"/admin/entities/`)
	lacks(t, body, `"name":"Gone"`)
}
