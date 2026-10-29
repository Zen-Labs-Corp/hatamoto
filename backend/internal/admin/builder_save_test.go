package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

// recordingEntities records every update and runs the page_doc dual write the
// real service runs, so the stored attributes are what the database would hold.
type recordingEntities struct {
	*fakeEntities
	inputs []entity.UpdateEntityInput
}

func (f *recordingEntities) UpdateEntity(ctx context.Context, id uuid.UUID, by *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error) {
	f.inputs = append(f.inputs, *in)
	if in.Attributes != nil {
		f.mu.Lock()
		stored := f.ents[id].Attributes
		f.mu.Unlock()
		norm, err := entity.NormalizeAttributes(stored, *in.Attributes)
		if err != nil {
			return nil, err
		}
		in.Attributes = &norm
	}
	return f.fakeEntities.UpdateEntity(ctx, id, by, in)
}

type saveRig struct {
	*builderRenderRig
	rec *recordingEntities
}

func newSaveRig(t *testing.T) *saveRig {
	r := &saveRig{builderRenderRig: newBuilderRenderRig(t)}
	r.rec = &recordingEntities{fakeEntities: r.ents}
	r.a.deps.Content.Entities = r.rec
	return r
}

const (
	saveDoc    = `{"v":2,"nodes":[{"id":"c1","t":"container","c":[{"id":"h1","t":"heading","p":{"text":"Saved","level":2}}]}]}`
	mirrorDoc  = `{"v":2,"nodes":[{"id":"s1","t":"container","p":{"tag":"section","v1":true},"c":[{"id":"x1","t":"component","p":{"type":"html","content":{"html":"<p>x</p>"}}}]}]}`
	dirtyDoc   = `{"v":2,"nodes":[{"id":"s1","t":"container","p":{"tag":"section","v1":true},"c":[{"id":"x1","t":"component","p":{"type":"html","content":{"body":"<p>x</p><script>alert(1)</script>"}}}]}]}`
	saveAction = "/builder/save"
)

func saveBody(version int, doc, op string, extra string) string {
	b, _ := json.Marshal(version)
	return `{"version":` + string(b) + `,"doc":` + doc + `,"op":"` + op + `"` + extra + `}`
}

type saveOut struct {
	OK       bool            `json:"ok"`
	Version  int             `json:"version"`
	SavedAt  string          `json:"savedAt"`
	Status   string          `json:"status"`
	Conflict bool            `json:"conflict"`
	Code     string          `json:"code"`
	Error    string          `json:"error"`
	Note     string          `json:"note"`
	Doc      json.RawMessage `json:"doc"`
}

func (r *saveRig) save(t *testing.T, e *entity.Entity, body string) (int, saveOut) {
	t.Helper()
	rec := r.postJSON(r.url(e, saveAction), "owner-token", body, nil)
	var out saveOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("status %d body %q", rec.Code, rec.Body)
	}
	return rec.Code, out
}

func storedDoc(t *testing.T, e *entity.Entity) (doc json.RawMessage, hasSections bool) {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(e.Attributes, &m); err != nil {
		t.Fatal(err)
	}
	_, hasSections = m["page_sections"]
	return m["page_doc"], hasSections
}

func TestBuilderSaveWritesDocAndBumpsVersion(t *testing.T) {
	t.Parallel()

	r := newSaveRig(t)
	code, out := r.save(t, r.page, saveBody(1, saveDoc, "save", ""))
	if code != 200 || !out.OK || out.Version != 2 || out.Status != "draft" || out.SavedAt == "" || out.Doc != nil {
		t.Fatalf("save: %d %+v", code, out)
	}
	doc, _ := storedDoc(t, r.ents.ents[r.page.ID])
	if !strings.Contains(string(doc), `"Saved"`) {
		t.Errorf("stored doc: %s", doc)
	}
	in := r.rec.inputs[0]
	if in.Autosave || in.ChangeSummary == nil || *in.ChangeSummary != "Updated in the builder" {
		t.Errorf("update input: %+v", in)
	}
	if strings.Contains(string(*in.Attributes), `"title":"T"`) == false {
		t.Errorf("other attributes were dropped: %s", *in.Attributes)
	}
}

// The mirror is frozen: a builder save never writes page_sections, whatever the layout.
func TestBuilderSaveLeavesTheMirrorAlone(t *testing.T) {
	t.Parallel()

	r := newSaveRig(t)
	for i, doc := range []string{saveDoc, mirrorDoc} {
		if code, out := r.save(t, r.page, saveBody(i+1, doc, "save", "")); code != 200 || !out.OK {
			t.Fatalf("%d %+v", code, out)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(r.ents.ents[r.page.ID].Attributes, &m); err != nil {
			t.Fatal(err)
		}
		if string(m["page_sections"]) != "[]" { // the rig stores an empty mirror
			t.Errorf("save %d changed page_sections: %s", i+1, m["page_sections"])
		}
	}
}

func TestBuilderSaveStaleWritesNothing(t *testing.T) {
	t.Parallel()

	r := newSaveRig(t)
	before := string(r.ents.ents[r.page.ID].Attributes)
	for _, body := range []string{saveBody(0, saveDoc, "save", ""), saveBody(7, saveDoc, "autosave", ""), `{"doc":` + saveDoc + `,"op":"save"}`} {
		code, out := r.save(t, r.page, body)
		if code != http.StatusConflict || !out.Conflict || out.OK || out.Version != 1 || out.Error == "" {
			t.Errorf("%s: %d %+v", body[:20], code, out)
		}
	}
	if len(r.rec.inputs) != 0 || string(r.ents.ents[r.page.ID].Attributes) != before || r.ents.ents[r.page.ID].Version != 1 {
		t.Error("a stale save wrote")
	}
}

func TestBuilderSaveInvalid(t *testing.T) {
	t.Parallel()

	r := newSaveRig(t)
	for name, body := range map[string]string{
		"unknown kind": saveBody(1, `{"v":2,"nodes":[{"id":"x","t":"nope"}]}`, "save", ""),
		"wrong v":      saveBody(1, `{"v":1}`, "save", ""),
		"no doc":       `{"version":1,"op":"save"}`,
		"long title":   saveBody(1, saveDoc, "save", `,"name":"`+strings.Repeat("a", 256)+`"`),
	} {
		code, out := r.save(t, r.page, body)
		if code != 422 || out.OK || out.Error == "" {
			t.Errorf("%s: %d %+v", name, code, out)
		}
		if name != "long title" && out.Code != "invalid_page_doc" {
			t.Errorf("%s: code %q", name, out.Code)
		}
	}
	if code, out := r.save(t, r.page, saveBody(1, saveDoc, "frobnicate", "")); code != 400 || out.Code != "invalid_op" {
		t.Errorf("bad op: %d %+v", code, out)
	}
	if len(r.rec.inputs) != 0 {
		t.Error("an invalid save wrote")
	}
}

func TestBuilderSaveGuards(t *testing.T) {
	t.Parallel()

	r := newSaveRig(t)
	path := r.url(r.page, saveAction)
	body := saveBody(1, saveDoc, "save", "")
	if rec := r.postJSON(path, "owner-token", body, map[string]string{"X-CSRF-Token": ""}); rec.Code != http.StatusForbidden {
		t.Errorf("no csrf: %d", rec.Code)
	}
	if rec := r.postJSON(path, "", body, nil); rec.Code == 200 {
		t.Error("saved without a session")
	}
	if rec := r.postJSON(r.url(&entity.Entity{ID: fid(999)}, saveAction), "owner-token", body, nil); rec.Code != 404 {
		t.Errorf("wrong id: %d", rec.Code)
	}
	if rec := r.postJSON(path, "owner-token", `{nope`, nil); rec.Code != 400 {
		t.Errorf("bad json: %d", rec.Code)
	}
	if rec := r.postJSON(path, "owner-token", body[:len(body)-1]+`,"pad":"`+strings.Repeat("a", builderSaveMaxBody)+`"}`, nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize: %d", rec.Code)
	}
	if len(r.rec.inputs) != 0 {
		t.Error("a refused save wrote")
	}
}

func TestBuilderAutosave(t *testing.T) {
	t.Parallel()

	r := newSaveRig(t)
	code, out := r.save(t, r.page, saveBody(1, saveDoc, "autosave", ""))
	if code != 200 || !out.OK || out.Version != 2 {
		t.Fatalf("autosave: %d %+v", code, out)
	}
	in := r.rec.inputs[0]
	if !in.Autosave || in.ChangeSummary == nil || *in.ChangeSummary != entity.AutosaveSummary {
		t.Errorf("autosave input (the repository coalesces on these): %+v", in)
	}
	// A second autosave goes through the same input, so the repository can fold it.
	if code, out = r.save(t, r.page, saveBody(2, mirrorDoc, "autosave", "")); code != 200 || out.Version != 3 {
		t.Fatalf("second autosave: %d %+v", code, out)
	}
	if in := r.rec.inputs[1]; !in.Autosave || *in.ChangeSummary != entity.AutosaveSummary {
		t.Errorf("second autosave input: %+v", in)
	}

	// Published pages never autosave.
	r.ents.ents[r.page.ID].Status = entity.StatusPublished
	before := string(r.ents.ents[r.page.ID].Attributes)
	n := len(r.rec.inputs)
	code, out = r.save(t, r.page, saveBody(3, saveDoc, "autosave", ""))
	if code != http.StatusConflict || out.OK || out.Conflict || !strings.Contains(out.Error, "Only drafts autosave") {
		t.Errorf("published autosave: %d %+v", code, out)
	}
	if len(r.rec.inputs) != n || string(r.ents.ents[r.page.ID].Attributes) != before {
		t.Error("a refused autosave wrote")
	}
	// ... but Update saves them.
	if code, out = r.save(t, r.page, saveBody(3, saveDoc, "save", "")); code != 200 || out.Status != "published" || out.Version != 4 {
		t.Errorf("published save: %d %+v", code, out)
	}
}

func TestBuilderPublish(t *testing.T) {
	t.Parallel()

	r := newSaveRig(t)
	untitled := r.ents.addEntity(80, r.renderRig.page, untitledPage, "untitled-1a2b3c4d", entity.StatusDraft, `{"page_doc":{"v":2,"nodes":[]}}`, t0)

	code, out := r.save(t, untitled, saveBody(1, saveDoc, "publish", ""))
	if code != 422 || out.OK || out.Code != "untitled" {
		t.Fatalf("untitled publish: %d %+v", code, out)
	}
	if untitled.Status != entity.StatusDraft || len(r.rec.inputs) != 0 {
		t.Error("a refused publish wrote")
	}

	code, out = r.save(t, untitled, saveBody(1, saveDoc, "publish", `,"name":"Our story"`))
	if code != 200 || !out.OK || out.Status != "published" || out.Version != 2 {
		t.Fatalf("titled publish: %d %+v", code, out)
	}
	if got := r.ents.ents[untitled.ID]; got.Name != "Our story" || got.Slug != "our-story" {
		t.Errorf("name %q slug %q", got.Name, got.Slug)
	}
	// Only drafts publish.
	if code, out = r.save(t, untitled, saveBody(2, saveDoc, "publish", "")); code != http.StatusConflict || out.OK {
		t.Errorf("publish of a published page: %d %+v", code, out)
	}
}

func TestBuilderSaveAdoptsSanitisedDoc(t *testing.T) {
	t.Parallel()

	r := newSaveRig(t)
	code, out := r.save(t, r.page, saveBody(1, dirtyDoc, "save", ""))
	if code != 200 || !out.OK {
		t.Fatalf("%d %+v", code, out)
	}
	if out.Doc == nil || strings.Contains(string(out.Doc), "<script") {
		t.Errorf("the reply should carry the sanitised doc: %s", out.Doc)
	}
	// A doc the sanitiser leaves alone is not echoed.
	if code, out = r.save(t, r.page, saveBody(2, mirrorDoc, "save", "")); code != 200 || out.Doc != nil {
		t.Errorf("clean doc echoed: %d %+v", code, out)
	}
}

// Canonical inline html (apostrophes, quotes, br) is what the sanitiser writes, so the save reply does not
// differ and the client does not adopt; a value that grows past the limit once cleaned is refused, not stored.
func TestBuilderSaveInlineHTML(t *testing.T) {
	doc := func(html string) string {
		b, _ := json.Marshal(map[string]any{"v": 2, "nodes": []any{map[string]any{"id": "h", "t": "heading", "p": map[string]any{"text": "x", "html": html}}}})
		return string(b)
	}
	r := newSaveRig(t)
	canon := `Don't <strong>say "hi"</strong><br>a &amp; <a href="/x" target="_blank" rel="noopener noreferrer">b</a>`
	code, out := r.save(t, r.page, saveBody(1, doc(canon), "save", ""))
	if code != 200 || !out.OK || out.Doc != nil {
		t.Errorf("canonical html adopted: %d %+v", code, out)
	}
	code, out = r.save(t, r.page, saveBody(2, doc(strings.Repeat("<a href=/x>", 1818)), "save", ""))
	if code != 422 || out.Code != "invalid_page_doc" || !strings.Contains(out.Error, "too long") {
		t.Errorf("html that grows past the limit: %d %+v", code, out)
	}
}

// An image that opens in a lightbox with nothing to name its link is saved, with a note.
func TestBuilderSaveNotesUnnamedLightboxImages(t *testing.T) {
	t.Parallel()

	r := newSaveRig(t)
	const doc = `{"v":2,"nodes":[{"id":"g1","t":"gallery","p":{"lightbox":true},"c":[` +
		`{"id":"i1","t":"image","p":{"src":"/media/a.jpg","decorative":true}},` +
		`{"id":"i2","t":"image","p":{"src":"/media/b.jpg","alt":"Named"}}]}]}`
	code, out := r.save(t, r.page, saveBody(1, doc, "save", ""))
	if code != 200 || !out.OK || !strings.Contains(out.Note, "an image that opens in a lightbox has no alt text or caption") {
		t.Fatalf("save: %d %+v", code, out)
	}
	const named = `{"v":2,"nodes":[{"id":"i1","t":"image","p":{"src":"/media/a.jpg","decorative":true,"lightbox":true,"caption":"A cat"}}]}`
	if _, out := r.save(t, r.page, saveBody(2, named, "save", "")); out.Note != "" {
		t.Errorf("a captioned decorative lightbox image got a note: %q", out.Note)
	}
	const auto = `{"v":2,"nodes":[{"id":"i1","t":"image","p":{"src":"/media/a.jpg","lightbox":true,"alt":""}}]}`
	if _, out := r.save(t, r.page, saveBody(3, auto, "autosave", "")); out.Note != "" {
		t.Errorf("an autosave got a note: %q", out.Note)
	}
}

const seoStored = `{"title":"T","meta_title":"Old","meta_description":"Old d","no_index":true,"seo":{"robots":"noindex, follow","x":1.50},"page_doc":{"v":2,"nodes":[]},"page_sections":[]}`

func attrsOf(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBuilderSaveSEOWritesInOneUpdate(t *testing.T) {
	t.Parallel()

	r := newSaveRig(t)
	r.page.Attributes = json.RawMessage(`{"title":"T","price":1.50,"page_doc":{"v":2,"nodes":[]},"page_sections":[]}`)
	seo := `,"seo":{"title":" Hello ","description":"D","canonical":"/c","og_image":"/i.png","no_index":true}`
	if code, out := r.save(t, r.page, saveBody(1, saveDoc, "save", seo)); code != 200 || !out.OK || out.Version != 2 {
		t.Fatalf("save: %d %+v", code, out)
	}
	if len(r.rec.inputs) != 1 || r.rec.inputs[0].ExpectedVersion == nil || *r.rec.inputs[0].ExpectedVersion != 1 {
		t.Fatalf("want one update at version 1: %+v", r.rec.inputs)
	}
	m := attrsOf(t, *r.rec.inputs[0].Attributes)
	for k, want := range map[string]string{"meta_title": `"Hello"`, "meta_description": `"D"`, "canonical_url": `"/c"`, "og_image": `"/i.png"`, "no_index": `true`, "price": `1.50`, "title": `"T"`} {
		if string(m[k]) != want {
			t.Errorf("%s = %s, want %s", k, m[k], want)
		}
	}
}

func TestBuilderSaveWithoutSEOLeavesStoredValues(t *testing.T) {
	t.Parallel()

	for _, op := range []string{"autosave", "save"} {
		r := newSaveRig(t)
		r.page.Attributes = json.RawMessage(seoStored)
		if code, out := r.save(t, r.page, saveBody(1, saveDoc, op, "")); code != 200 || !out.OK {
			t.Fatalf("%s: %d %+v", op, code, out)
		}
		m := attrsOf(t, r.ents.ents[r.page.ID].Attributes)
		if string(m["meta_title"]) != `"Old"` || string(m["meta_description"]) != `"Old d"` || string(m["no_index"]) != `true` || !strings.Contains(string(m["seo"]), "noindex") {
			t.Errorf("%s changed stored SEO: %s", op, r.ents.ents[r.page.ID].Attributes)
		}
	}
}

func TestBuilderSaveSEOEmptyDeletesAndNoIndexOff(t *testing.T) {
	t.Parallel()

	r := newSaveRig(t)
	r.page.Attributes = json.RawMessage(seoStored)
	if code, out := r.save(t, r.page, saveBody(1, saveDoc, "save", `,"seo":{"title":"","description":" ","canonical":"","og_image":"","no_index":false}`)); code != 200 || !out.OK {
		t.Fatalf("save: %d %+v", code, out)
	}
	m := attrsOf(t, *r.rec.inputs[0].Attributes)
	for _, k := range []string{"meta_title", "meta_description", "canonical_url", "og_image", "no_index"} {
		if _, ok := m[k]; ok {
			t.Errorf("%s was kept: %s", k, m[k])
		}
	}
	if strings.Contains(string(m["seo"]), "robots") && strings.Contains(strings.ToLower(string(m["seo"])), "noindex") {
		t.Errorf("noindex robots kept: %s", m["seo"])
	}
	if !strings.Contains(string(m["seo"]), `"follow"`) {
		t.Errorf("other robots directives dropped: %s", m["seo"])
	}
}

func TestBuilderSaveSEOStaleVersionWritesNothing(t *testing.T) {
	t.Parallel()

	r := newSaveRig(t)
	r.page.Attributes = json.RawMessage(seoStored)
	before := string(r.page.Attributes)
	code, out := r.save(t, r.page, saveBody(7, saveDoc, "save", `,"seo":{"title":"New"}`))
	if code != http.StatusConflict || !out.Conflict {
		t.Fatalf("stale: %d %+v", code, out)
	}
	if len(r.rec.inputs) != 0 || string(r.ents.ents[r.page.ID].Attributes) != before {
		t.Error("a stale save wrote")
	}
}

func TestBuilderSaveSEOInvalidCanonical(t *testing.T) {
	t.Parallel()

	for _, c := range []string{"javascript:alert(1)", "//evil.example", "not a url"} {
		r := newSaveRig(t)
		code, out := r.save(t, r.page, saveBody(1, saveDoc, "save", `,"seo":{"canonical":"`+c+`"}`))
		if code != http.StatusUnprocessableEntity || out.Code != "invalid" {
			t.Errorf("%q: %d %+v", c, code, out)
		}
		if len(r.rec.inputs) != 0 {
			t.Errorf("%q: a refused save wrote", c)
		}
	}
}

func TestBuilderSaveSEOCapsCountCharacters(t *testing.T) {
	t.Parallel()

	r := newSaveRig(t)
	// 90 characters, 270 bytes: within the 255-character cap.
	seo := `,"seo":{"title":"` + strings.Repeat("日", 90) + `"}`
	if code, out := r.save(t, r.page, saveBody(1, saveDoc, "save", seo)); code != 200 || !out.OK {
		t.Fatalf("a 90-character title: %d %+v", code, out)
	}
	r = newSaveRig(t)
	seo = `,"seo":{"title":"` + strings.Repeat("日", 256) + `"}`
	if code, out := r.save(t, r.page, saveBody(1, saveDoc, "save", seo)); code != http.StatusUnprocessableEntity {
		t.Errorf("a 256-character title: %d %+v", code, out)
	}
}

func TestBuilderSaveSEOIgnoredForReuseBlocks(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	if code, out := r.save(t, r.block, saveBody(1, blockDoc, "save", `,"seo":{"title":"Nope"}`)); code != 200 || !out.OK {
		t.Fatalf("block save: %d %+v", code, out)
	}
	if _, ok := attrsOf(t, *r.rec.inputs[0].Attributes)["meta_title"]; ok {
		t.Error("a reuse block got SEO attributes")
	}
}
