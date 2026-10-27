package admin

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

const stageDoc = `{"v":2,"nodes":[{"id":"c1","t":"container","c":[{"id":"h1","t":"heading","p":{"text":"Hi","level":2}}]}]}`

// builderRenderRig is a render rig with a builder-only page.
type builderRenderRig struct {
	*renderRig
	page *entity.Entity
}

func newBuilderRenderRig(t *testing.T) *builderRenderRig {
	r := &builderRenderRig{renderRig: newRenderRig(t)}
	r.page = r.ents.addEntity(74, r.renderRig.page, "Builder only", "builder-only", entity.StatusDraft,
		`{"title":"T","page_doc":{"v":2,"nodes":[{"id":"old","t":"container"}]},"page_sections":[]}`, t0)
	return r
}

func (r *builderRenderRig) url(e *entity.Entity, tail string) string {
	return "/admin/entities/" + e.ID.String() + tail
}

func TestBuilderRenderForwardsTheDoc(t *testing.T) {
	t.Parallel()

	r := newBuilderRenderRig(t)
	body := `{"doc":` + stageDoc + `,"ops":[{"op":"remove","id":"h1"}],"render":["c1"],"css":["h1"],"seq":7,"entity_id":"evil"}`
	rec := r.postJSON(r.url(r.page, "/edit/render"), "owner-token", body, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"fragments"`) {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		EntityID   string                     `json:"entity_id"`
		Schema     string                     `json:"schema"`
		Canvas     bool                       `json:"canvas"`
		Doc        json.RawMessage            `json:"doc"`
		Ops        json.RawMessage            `json:"ops"`
		Render     []string                   `json:"render"`
		CSS        []string                   `json:"css"`
		Seq        int                        `json:"seq"`
		Attributes map[string]json.RawMessage `json:"attributes"`
	}
	if err := json.Unmarshal(r.asked, &got); err != nil {
		t.Fatalf("%v: %s", err, r.asked)
	}
	if got.EntityID != r.page.ID.String() || got.Schema != "page" || !got.Canvas || got.Seq != 7 ||
		len(got.Render) != 1 || got.Render[0] != "c1" || len(got.CSS) != 1 || !strings.Contains(string(got.Ops), `"remove"`) {
		t.Errorf("request: %s", r.asked)
	}
	if !strings.Contains(string(got.Doc), `"h1"`) || strings.Contains(string(got.Doc), `"old"`) {
		t.Errorf("doc is not the posted one: %s", got.Doc)
	}
	if _, ok := got.Attributes["page_doc"]; ok {
		t.Errorf("attributes carry page_doc: %s", r.asked)
	}
	if _, ok := got.Attributes["page_sections"]; ok {
		t.Errorf("attributes carry page_sections: %s", r.asked)
	}
	if string(got.Attributes["title"]) != `"T"` {
		t.Errorf("other attributes dropped: %s", r.asked)
	}
	if strings.Contains(string(r.ents.ents[r.page.ID].Attributes), `"h1"`) {
		t.Error("the draft was stored")
	}
}

func TestBuilderRenderGuards(t *testing.T) {
	t.Parallel()

	r := newBuilderRenderRig(t)
	path := r.url(r.page, "/edit/render")
	ok := `{"doc":` + stageDoc + `}`
	if rec := r.postJSON(path, "owner-token", ok, map[string]string{"X-CSRF-Token": ""}); rec.Code != http.StatusForbidden {
		t.Errorf("no csrf: %d", rec.Code)
	}
	if rec := r.postJSON(path, "", ok, nil); rec.Code == 200 {
		t.Error("rendered without a session")
	}
	if rec := r.postJSON(r.url(&entity.Entity{ID: fid(999)}, "/edit/render"), "owner-token", ok, nil); rec.Code != 404 {
		t.Errorf("wrong id: %d", rec.Code)
	}
	if rec := r.postJSON(path, "owner-token", `{"doc":{"v":2,"nodes":[{"id":"x","t":"nope"}]}}`, nil); rec.Code != 422 || !strings.Contains(rec.Body.String(), `"invalid_page_doc"`) {
		t.Errorf("bad doc: %d %s", rec.Code, rec.Body)
	}
	if rec := r.postJSON(path, "owner-token", `{nope`, nil); rec.Code != 400 {
		t.Errorf("bad json: %d", rec.Code)
	}
	if rec := r.postJSON(path, "owner-token", `{"doc":`+stageDoc+`,"pad":"`+strings.Repeat("a", previewMaxBody)+`"}`, nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize: %d", rec.Code)
	}
	// A page that has only sections renders its in-memory conversion.
	if rec := r.postJSON(r.url(r.ent, "/edit/render"), "owner-token", ok, nil); rec.Code != 200 {
		t.Errorf("sections-only page: %d", rec.Code)
	}
	r.a.deps.Content.RenderNodes = nil
	if rec := r.postJSON(path, "owner-token", ok, nil); rec.Code != 404 {
		t.Errorf("no render dep: %d", rec.Code)
	}
}

func TestBuilderStage(t *testing.T) {
	t.Parallel()

	r := newBuilderRenderRig(t)
	path := r.url(r.page, "/builder/stage")
	rec := r.postJSON(path, "owner-token", `{"doc":`+stageDoc+`}`, nil)
	var out struct {
		URL string `json:"url"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || !strings.HasPrefix(out.URL, r.url(r.page, "/edit/preview/")) {
		t.Fatalf("stage: %d %s", rec.Code, rec.Body)
	}

	if !regexp.MustCompile(`/edit/preview/[A-Za-z0-9_-]{32}$`).MatchString(out.URL) {
		t.Fatalf("ticket shape: %s", out.URL)
	}
	iframe := map[string]string{"Sec-Fetch-Dest": "iframe"}
	frame := r.do("GET", out.URL, "owner-token", nil, iframe)
	if frame.Code != 200 {
		t.Fatalf("frame: %d", frame.Code)
	}
	if rec := r.do("GET", out.URL, "owner-token", nil, iframe); rec.Code != http.StatusNotFound {
		t.Errorf("a ticket loaded twice: %d", rec.Code)
	}
	var got struct {
		Canvas     bool                       `json:"canvas"`
		Attributes map[string]json.RawMessage `json:"attributes"`
	}
	if err := json.Unmarshal(r.lastBody, &got); err != nil || !got.Canvas {
		t.Fatalf("preview request: %v %s", err, r.lastBody)
	}
	if !strings.Contains(string(got.Attributes["page_doc"]), `"h1"`) || got.Attributes["page_sections"] != nil {
		t.Errorf("staged attributes: %s", r.lastBody)
	}

	// The ticket belongs to its entity.
	rec = r.postJSON(path, "owner-token", `{"doc":`+stageDoc+`}`, nil)
	if json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("stage: %d %s", rec.Code, rec.Body)
	}
	other := strings.Replace(out.URL, r.page.ID.String(), r.ent.ID.String(), 1)
	if rec := r.do("GET", other, "owner-token", nil, iframe); rec.Code != http.StatusNotFound {
		t.Errorf("other entity: %d", rec.Code)
	}

	if rec := r.postJSON(path, "owner-token", `{"doc":`+stageDoc+`}`, map[string]string{"X-CSRF-Token": ""}); rec.Code != http.StatusForbidden {
		t.Errorf("no csrf: %d", rec.Code)
	}
	if rec := r.postJSON(path, "", `{"doc":`+stageDoc+`}`, nil); rec.Code == 200 {
		t.Error("staged without a session")
	}
	if rec := r.postJSON(path, "owner-token", `{"doc":{"v":1}}`, nil); rec.Code != 422 {
		t.Errorf("bad doc: %d", rec.Code)
	}
	if rec := r.postJSON(path, "owner-token", `{"doc":`+stageDoc+`,"pad":"`+strings.Repeat("a", previewMaxBody)+`"}`, nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize: %d", rec.Code)
	}
	if rec := r.postJSON(r.url(r.ent, "/builder/stage"), "owner-token", `{"doc":`+stageDoc+`}`, nil); rec.Code != 200 {
		t.Errorf("sections-only page: %d", rec.Code)
	}
}

func TestBuilderRenderNeedsAPageEditorEntity(t *testing.T) {
	t.Parallel()

	r := newBuilderRenderRig(t)
	post := r.ents.addSchema(21, "Post", "post", `{"type":"object","properties":{}}`, `{}`, true)
	e := r.ents.addEntity(75, post, "Not a page", "not-a-page", entity.StatusDraft, `{"page_doc":{"v":2,"nodes":[]}}`, t0)
	body := `{"doc":` + stageDoc + `,"render":["c1"]}`
	if rec := r.postJSON(r.url(e, "/edit/render"), "owner-token", body, nil); rec.Code != http.StatusNotFound {
		t.Errorf("render for a post with a page_doc = %d, want 404", rec.Code)
	}
	if rec := r.postJSON(r.url(r.page, "/edit/render"), "owner-token", body, nil); rec.Code != 200 {
		t.Errorf("render for a page = %d", rec.Code)
	}
}
