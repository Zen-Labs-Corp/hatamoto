package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
)

// renderRig is the editor rig with a RenderNodes dep that records the JSON it
// is asked for, and a converted page (page_doc next to page_sections).
type renderRig struct {
	*editorRig
	conv  *entity.Entity
	asked []byte
}

func newRenderRig(t *testing.T) *renderRig {
	r := &renderRig{editorRig: newEditorRig(t)}
	r.a.deps.Content.RenderNodes = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.asked, _ = io.ReadAll(req.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"fragments":[],"css":{},"order":[]}`))
	})
	secs := []map[string]any{
		{"id": "one", "type": "html", "content": map[string]any{"html": "<p>one</p>"}},
		{"id": "two", "type": "html", "content": map[string]any{"html": "<p>two</p>"}},
	}
	r.conv = r.ents.addEntity(72, r.page, "Converted", "converted", entity.StatusDraft, convertedAttrs(t, secs), t0)
	return r
}

func convertedAttrs(t *testing.T, secs []map[string]any) string {
	t.Helper()
	in := make([]builder.Section, len(secs))
	for i, s := range secs {
		in[i] = builder.Section{ID: s["id"].(string), Type: s["type"].(string), Content: s["content"].(map[string]any), SortOrder: i}
	}
	doc, _ := json.Marshal(builder.FromSections(in))
	b, _ := json.Marshal(map[string]any{"page_sections": secs, "page_doc": json.RawMessage(doc)})
	return string(b)
}

func (r *renderRig) editPath(e *entity.Entity) string {
	return "/admin/entities/" + e.ID.String() + "/edit"
}

// A page with a page_doc opens the builder; so does one that has only
// page_sections (its in-memory conversion), and opening it writes nothing.
func TestConvertedPagesOpenTheBuilder(t *testing.T) {
	t.Parallel()

	r := newRenderRig(t)
	body := r.get(t, "owner-token", r.editPath(r.conv), "")
	has(t, body, `data-hm-builder`)
	lacks(t, body, `data-hm-render`, `data-hm-page-editor`)

	before := string(r.ents.ents[r.ent.ID].Attributes)
	versions := r.ents.ents[r.ent.ID].Version
	body = r.get(t, "owner-token", r.path(), "") // sections only
	has(t, body, `data-hm-builder`, `hm-builder-boot`, `hero_1`)
	lacks(t, body, `data-hm-page-editor`)
	if got := r.ents.ents[r.ent.ID]; string(got.Attributes) != before || got.Version != versions {
		t.Errorf("opening an unconverted page wrote: version %d -> %d", versions, got.Version)
	}
}

// A stored page_doc that does not parse shows the recovery screen, not a form.
func TestBrokenDocShowsRecoveryScreen(t *testing.T) {
	t.Parallel()

	r := newRenderRig(t)
	broken := r.ents.addEntity(73, r.page, "Broken", "broken", entity.StatusDraft, `{"page_doc":{"v":9},"page_sections":[{"id":"a","type":"html","content":{}}]}`, t0)
	rec := r.do("GET", r.editPath(broken), "owner-token", nil, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", rec.Code)
	}
	has(t, rec.Body.String(), "cannot be read", "/admin/entities/"+broken.ID.String()+"/versions")
	lacks(t, rec.Body.String(), `data-hm-builder`, `data-hm-page-editor`)
}

// The canvas render takes the builder's JSON body; a form post (the removed
// classic editor's) is refused, and the builder's guards hold.
func TestPageRenderGuards(t *testing.T) {
	t.Parallel()

	r := newRenderRig(t)
	id := r.editPath(r.conv)
	if rec := r.do("POST", id+"/render", "owner-token", url.Values{"junk": {"x"}}, nil); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("form body: %d", rec.Code)
	}
	if rec := r.do("POST", id+"/render", "", url.Values{"junk": {"x"}}, nil); rec.Code == 200 {
		t.Error("rendered without a session")
	}
	if rec := r.do("GET", id+"/render", "owner-token", nil, nil); rec.Code == 200 {
		t.Error("GET rendered")
	}
	if rec := r.do("POST", "/admin/entities/"+fid(999).String()+"/edit/render", "owner-token", url.Values{}, nil); rec.Code != 404 {
		t.Errorf("wrong id: %d", rec.Code)
	}
}

func TestStyleSpecEndpoint(t *testing.T) {
	t.Parallel()

	r := newRenderRig(t)
	rec := r.do("GET", "/admin/builder/stylespec", "owner-token", nil, nil)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d headers %v", rec.Code, rec.Header())
	}
	var table []builder.StyleSpecEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &table); err != nil || len(table) != len(builder.StyleSpecTable()) {
		t.Fatalf("table: %v (%d entries)", err, len(table))
	}
	keys := map[string]bool{}
	for _, e := range table {
		keys[e.Key] = true
	}
	if !keys["pt"] || !keys["ta"] {
		t.Errorf("keys: %v", keys)
	}
	tag := rec.Header().Get("ETag")
	if tag == "" {
		t.Fatal("no ETag")
	}
	if rec := r.do("GET", "/admin/builder/stylespec", "owner-token", nil, map[string]string{"If-None-Match": tag}); rec.Code != http.StatusNotModified {
		t.Errorf("etag: %d", rec.Code)
	}
	if rec := r.do("GET", "/admin/builder/stylespec", "", nil, nil); rec.Code == 200 {
		t.Error("served without a session")
	}
}
