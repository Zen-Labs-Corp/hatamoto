package admin

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/entity"
)

// parentRig is a content rig with a child whose parent is a recipe (another
// type) that sorts after more entities than the picker lists.
func parentRig(t *testing.T) (*contentRig, *entity.Entity, *entity.Entity) {
	c := newContentRig(t)
	parent := c.ents.addEntity(90, c.recipe, "Zzz guide", "zzz-guide", entity.StatusPublished, `{}`, t0)
	for i := 0; i < parentSearchLimit+5; i++ {
		c.ents.addEntity(100+i, c.service, fmt.Sprintf("Filler %02d", i), fmt.Sprintf("filler-%02d", i), entity.StatusDraft, `{"summary":"x"}`, t0)
	}
	child := c.ents.addEntity(91, c.service, "Child", "child", entity.StatusPublished, `{"summary":"x"}`, t0)
	child.ParentID = &parent.ID
	return c, parent, child
}

func TestSavingAnEntityKeepsAParentThePickerDoesNotList(t *testing.T) {
	t.Parallel()

	c, parent, child := parentRig(t)
	id := child.ID.String()
	body := c.get(t, "owner-token", "/admin/entities/"+id, "")
	// the cross-type parent beyond the search limit is the selected option
	has(t, body, `<option value="`+parent.ID.String()+`" selected>Zzz guide (/zzz-guide) · Recipe</option>`, `name="parent_orig" value="`+parent.ID.String()+`"`)
	if n := strings.Count(body, "<option value=\""+fid(100).String()); n != 1 {
		t.Errorf("fillers listed %d times", n)
	}
	lacks(t, body, `value="`+fid(100+parentSearchLimit+2).String()+`"`)

	// a form that posts the parent it was opened with leaves the parent alone,
	// even when the browser sent "no parent" (an option the page did not offer)
	for _, posted := range []string{parent.ID.String(), ""} {
		form := url.Values{"name": {"Child fixed"}, "slug": {"child"}, "status": {"published"}, "attr.summary": {"x"}, "parent": {posted}, "parent_orig": {parent.ID.String()}}
		if posted == "" {
			// the browser's value when nothing is selected is the first option: the stale form
			form.Set("parent", parent.ID.String())
		}
		if rec := c.post("/admin/entities/"+id, form); rec.Code != 303 {
			t.Fatalf("save: %d\n%s", rec.Code, rec.Body.String())
		}
		if got := c.ents.ents[child.ID]; got.ParentID == nil || *got.ParentID != parent.ID || got.Name != "Child fixed" {
			t.Fatalf("parent changed by an unrelated edit: %+v", got.ParentID)
		}
	}
	// choosing another parent changes it
	other := fid(101).String()
	c.post("/admin/entities/"+id, url.Values{"name": {"Child"}, "slug": {"child"}, "status": {"published"}, "attr.summary": {"x"}, "parent": {other}, "parent_orig": {parent.ID.String()}})
	if got := c.ents.ents[child.ID].ParentID; got == nil || got.String() != other {
		t.Fatalf("parent not changed: %v", got)
	}
	// and an explicit "no parent" clears it
	c.post("/admin/entities/"+id, url.Values{"name": {"Child"}, "slug": {"child"}, "status": {"published"}, "attr.summary": {"x"}, "parent": {""}, "parent_orig": {other}})
	if c.ents.ents[child.ID].ParentID != nil {
		t.Fatal("parent not cleared")
	}
}

func TestParentSearchFindsAnyEntityAndKeepsTheChoice(t *testing.T) {
	t.Parallel()

	c, parent, child := parentRig(t)
	q := url.Values{"parent_q": {"zzz"}, "parent": {fid(100).String()}, "exclude": {child.ID.String()}}
	rec := c.do("GET", "/admin/entities/parent-options?"+q.Encode(), "owner-token", nil, map[string]string{"HX-Request": "true", "HX-Target": "parent-select"})
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	body := rec.Body.String()
	has(t, body, `<select class="hm-field__control" id="parent" name="parent">`, `value="`+parent.ID.String()+`">Zzz guide`, `value="`+fid(100).String()+`" selected>Filler 00`, "No parent (top level)")
	lacks(t, body, "<html", "Filler 01")
	// the entity being edited is never offered as its own parent
	q.Set("parent_q", "child")
	rec = c.do("GET", "/admin/entities/parent-options?"+q.Encode(), "owner-token", nil, map[string]string{"HX-Request": "true", "HX-Target": "parent-select"})
	lacks(t, rec.Body.String(), `value="`+child.ID.String()+`"`)
	if rec := c.do("GET", "/admin/entities/parent-options", "", nil, nil); rec.Code == 200 {
		t.Error("parent search answered without a session")
	}
}

func TestFieldBuilderRoundTripsWhatItDoesNotEdit(t *testing.T) {
	t.Parallel()

	for name, schema := range map[string]string{
		"date and email": `{"type":"object","properties":{"when":{"type":"string","format":"date-time","title":"When"},"day":{"type":"string","format":"date"},"mail":{"type":"string","format":"email","maxLength":80,"pattern":"^.+@.+$"}},"required":["mail"]}`,
		"tags":           `{"type":"object","properties":{"links":{"type":"array","items":{"type":"string","format":"uri","maxLength":300},"maxItems":5}}}`,
		"reference":      `{"type":"object","properties":{"refs":{"type":"array","x-entity-schema":"page","items":{"type":"string","format":"uuid"}}}}`,
		"repeater":       `{"type":"object","properties":{"steps":{"type":"array","minItems":1,"items":{"type":"string","format":"textarea","maxLength":500,"x-hm-note":"keep"}}}}`,
		"select":         `{"type":"object","properties":{"level":{"type":"integer","enum":[1,2,3],"default":2,"x-hm-field":"rating"},"size":{"type":"string","enum":["s","m"],"description":"Pick"}}}`,
		"numbers":        `{"type":"object","properties":{"big":{"type":"integer","maximum":9007199254740993,"minimum":0},"x":{"type":"number","multipleOf":0.5}},"additionalProperties":false,"x-hm-extra":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			rows := fieldRows(json.RawMessage(schema))
			got, ok := buildSchema(json.RawMessage(schema), rows)
			if !ok {
				t.Fatalf("rows rejected: %+v", rows)
			}
			var want, have map[string]any
			dec := func(s string, into *map[string]any) {
				d := json.NewDecoder(strings.NewReader(s))
				d.UseNumber()
				if err := d.Decode(into); err != nil {
					t.Fatal(err)
				}
			}
			dec(schema, &want)
			dec(string(got), &have)
			delete(have, "x-hm-order") // the editor adds the field order
			if !reflect.DeepEqual(want, have) {
				t.Errorf("schema changed by an unedited save\n want %s\n  got %s", schema, got)
			}
		})
	}
}

func TestFieldBuilderEditsMergeIntoTheProperty(t *testing.T) {
	t.Parallel()

	schema := `{"type":"object","properties":{"mail":{"type":"string","format":"email","maxLength":80,"title":"Mail"},"lvl":{"type":"string","enum":["a","b"],"x-hm-field":"pill"}}}`
	rows := fieldRows(json.RawMessage(schema))
	rows[0].Title, rows[0].Help = "E-mail", "Where we write"
	rows[1].Options = "a\nb\nc"
	out, ok := buildSchema(json.RawMessage(schema), rows)
	if !ok {
		t.Fatal("rejected")
	}
	var s struct{ Properties map[string]map[string]any }
	_ = json.Unmarshal(out, &s)
	mail := s.Properties["mail"]
	if mail["title"] != "E-mail" || mail["description"] != "Where we write" || mail["format"] != "email" || mail["maxLength"] != float64(80) {
		t.Errorf("mail: %v", mail)
	}
	if lvl := s.Properties["lvl"]; !reflect.DeepEqual(lvl["enum"], []any{"a", "b", "c"}) || lvl["x-hm-field"] != "pill" {
		t.Errorf("lvl: %v", lvl)
	}
	// changing the kind does drop what no longer applies
	rows = fieldRows(json.RawMessage(schema))
	rows[0].Kind = "boolean"
	out, _ = buildSchema(json.RawMessage(schema), rows)
	_ = json.Unmarshal(out, &s)
	if m := s.Properties["mail"]; m["type"] != "boolean" || m["format"] != nil || m["maxLength"] != nil {
		t.Errorf("kind change kept stale keys: %v", m)
	}
}

func TestEntitiesWithPageSectionsOpenThePageEditor(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	sections := c.ents.addEntity(60, c.service, "Landing", "landing", entity.StatusDraft, `{"summary":"x","page_sections":[{"type":"hero","title":"Hi"}]}`, t0)
	empty := c.ents.addEntity(61, c.service, "Plain", "plain", entity.StatusDraft, `{"summary":"x","page_sections":[]}`, t0)
	withProp := c.ents.addSchema(9, "Landing page", "landing_page", `{"type":"object","properties":{"page_sections":{"type":"array"}}}`, `{}`, false)
	typed := c.ents.addEntity(62, withProp, "Typed", "typed", entity.StatusDraft, `{}`, t0)
	for _, e := range []*entity.Entity{sections, typed} {
		if rec := c.do("GET", "/admin/entities/"+e.ID.String()+"/edit", "owner-token", nil, nil); rec.Code != 200 {
			t.Errorf("%s: page editor not served: %d", e.Name, rec.Code)
		}
	}
	if rec := c.do("GET", "/admin/entities/"+empty.ID.String()+"/edit", "owner-token", nil, nil); rec.Code != 303 {
		t.Errorf("empty sections edit in the generic form: %d", rec.Code)
	}
	// the list links to the editor, and the form offers it
	list := c.get(t, "owner-token", "/admin/entities", "")
	has(t, list, `href="/admin/entities/`+sections.ID.String()+`/edit"`, `href="/admin/entities/`+empty.ID.String()+`"`)
	form := c.get(t, "owner-token", "/admin/entities/"+sections.ID.String(), "")
	has(t, form, `Visual editor`, `/admin/entities/`+sections.ID.String()+`/edit`)
}

func TestSEONoindexReadsAndWritesRobots(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	e := c.ents.addEntity(70, c.service, "Hidden", "hidden", entity.StatusDraft, `{"summary":"x","seo":{"robots":"noindex, follow","keep":"me"}}`, t0)
	id := e.ID.String()
	body := c.get(t, "owner-token", "/admin/entities/"+id, "")
	has(t, body, `name="seo_noindex" value="true" checked`)
	// unticking drops noindex from seo.robots and keeps the rest
	form := url.Values{"name": {"Hidden"}, "slug": {"hidden"}, "status": {"draft"}, "attr.summary": {"x"}}
	if rec := c.post("/admin/entities/"+id, form); rec.Code != 303 {
		t.Fatalf("%d", rec.Code)
	}
	var attrs map[string]any
	_ = json.Unmarshal(c.ents.ents[e.ID].Attributes, &attrs)
	seo, _ := attrs["seo"].(map[string]any)
	if seo["robots"] != "follow" || seo["keep"] != "me" || attrs["no_index"] != nil {
		t.Errorf("attrs after untick: %v", attrs)
	}
	if rec := c.get(t, "owner-token", "/admin/entities/"+id, ""); strings.Contains(rec, `value="true" checked`) {
		t.Error("still shown as noindex")
	}
	// ticking writes no_index
	form.Set("seo_noindex", "true")
	c.post("/admin/entities/"+id, form)
	_ = json.Unmarshal(c.ents.ents[e.ID].Attributes, &attrs)
	if attrs["no_index"] != true {
		t.Errorf("not ticked: %v", attrs)
	}
	// a protocol-relative canonical is refused
	form.Set("seo_canonical", "//evil.example/x")
	if rec := c.post("/admin/entities/"+id, form); rec.Code != 422 {
		t.Errorf("canonical //: %d", rec.Code)
	}
}

func TestScheduleErrorsAreFieldErrors(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := fid(12).String()
	post := func(at string) *httptest.ResponseRecorder {
		return c.post("/admin/entities/"+id, url.Values{"name": {"Beta service"}, "slug": {"beta"}, "status": {"scheduled"}, "schedule": {at}, "attr.summary": {"The second"}})
	}
	before := c.ents.ents[fid(12)].Version
	rec := post(time.Now().Add(-48 * time.Hour).Format(scheduleLayout))
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Pick a time in the future") {
		t.Errorf("past: %d", rec.Code)
	}
	if c.ents.ents[fid(12)].Version != before {
		t.Error("the content was saved although the schedule was refused")
	}
	// 2031-03-09 02:30 does not exist in New York (clocks go 02:00 -> 03:00)
	c.rig.a.deps.Site.(*fakeSite).cfg.Timezone = "America/New_York"
	rec = post("2031-03-09T02:30")
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "does not exist") {
		t.Errorf("dst gap: %d", rec.Code)
	}
}

func TestSchemaReorderKeepsFocusOnTheMovedRow(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := c.service.ID.String()
	form := url.Values{"name": {"Service"}, "op": {"down:r0"}, "field": {"r0", "r1"},
		"f.r0.name": {"a"}, "f.r0.kind": {"text"}, "f.r1.name": {"b"}, "f.r1.kind": {"text"}}
	rec := c.do("POST", "/admin/settings/types/"+id, "owner-token", form, map[string]string{"HX-Request": "true", "HX-Target": "fields"})
	body := rec.Body.String()
	has(t, body, `value="down:r0" formnovalidate formaction="/admin/settings/types/`+id+`#row-r0"`, ` autofocus`, `id="row-r0"`)
	if n := strings.Count(body, " autofocus"); n != 1 {
		t.Errorf("%d autofocus attributes", n)
	}
	// the moved row stays open, and the focused button is the one that moved it
	i := strings.Index(body, `id="row-r0"`)
	if !strings.Contains(body[i:i+400], "<details open>") && !strings.Contains(body[i:i+400], "<details open") {
		t.Errorf("moved row collapsed: %s", body[i:i+300])
	}
}

func TestBulkAndApplyWorkWithoutJavaScript(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	body := c.get(t, "owner-token", "/admin/entities", "")
	// the bulk buttons sit inside the form that posts the ticked rows
	i := strings.Index(body, `<form id="bulk-entities"`)
	j := strings.Index(body[i:], "</form>")
	if i < 0 || !strings.Contains(body[i:i+j], `name="action" value="publish"`) || strings.Contains(body[i:i+j], " hidden") {
		t.Errorf("bulk bar is not a visible form: %.300s", body[i:])
	}
	has(t, body, `data-hm-apply=""><span>Apply filters</span></button>`)
	lacks(t, body, `hm-visually-hidden-focusable" type="submit"`)
}

// F7: a bad editor value posted to the type form is refused on the editor field.
func TestSchemaFormRefusesABadEditorValue(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	path := "/admin/settings/types/" + c.service.ID.String()
	for _, tc := range []struct{ editor, want string }{
		{"tiptap", "Choose Form or Post editor"},
		{"post", "string property named content"}, // the service schema has no content
	} {
		rec := c.post(path, url.Values{"name": {"Service"}, "editor": {tc.editor}})
		body := rec.Body.String()
		if rec.Code != 422 || !strings.Contains(body, `id="editor-error"`) || !strings.Contains(body, tc.want) {
			t.Errorf("editor %q: %d, want 422 with %q on the editor field", tc.editor, rec.Code, tc.want)
		}
	}
}
