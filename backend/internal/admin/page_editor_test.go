package admin

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/sectiontemplate"
)

const heroSchema = `{"type":"object","x-hm-section":{"title":"Hero"},"properties":{
 "heading":{"type":"string","title":"Heading","maxLength":60,"x-hm-alias":["headline"]},
 "image":{"type":"string","format":"uri","x-hm-media":true,"title":"Image"},
 "faqs":{"type":"array","title":"Questions","items":{"type":"object","properties":{"question":{"type":"string","title":"Question"},"answer":{"type":"string","format":"richtext","title":"Answer"}}}}}}`

const htmlSchema = `{"type":"object","properties":{"html":{"type":"string","x-hm-field":"html","title":"HTML"}}}`

type fakeTemplates struct {
	mu    sync.Mutex
	items []sectiontemplate.SectionTemplate
	used  []uuid.UUID
}

func (f *fakeTemplates) List(_ context.Context, _ *sectiontemplate.ListFilter, _, _ int) (*sectiontemplate.ListResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &sectiontemplate.ListResult{Items: append([]sectiontemplate.SectionTemplate(nil), f.items...), Total: len(f.items)}, nil
}
func (f *fakeTemplates) Get(_ context.Context, id uuid.UUID) (*sectiontemplate.SectionTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.items {
		if t.ID == id {
			c := t
			return &c, nil
		}
	}
	return nil, sectiontemplate.ErrTemplateNotFound
}
func (f *fakeTemplates) Create(_ context.Context, _ *uuid.UUID, in *sectiontemplate.CreateInput) (*sectiontemplate.SectionTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := sectiontemplate.SectionTemplate{ID: uuid.New(), Name: in.Name, Description: in.Description, SectionType: in.SectionType, Content: in.Content}
	f.items = append(f.items, t)
	return &t, nil
}
func (f *fakeTemplates) RecordUsage(_ context.Context, id uuid.UUID) error {
	f.used = append(f.used, id)
	return nil
}

// editorRig is a content rig with a theme, section templates and a preview that
// echoes the JSON it was asked to render.
type editorRig struct {
	*contentRig
	tpls     *fakeTemplates
	ent      *entity.Entity
	lastBody []byte
}

func newEditorRig(t *testing.T) *editorRig {
	c := newContentRig(t)
	r := &editorRig{contentRig: c, tpls: &fakeTemplates{}}
	r.ent = c.ents.addEntity(70, c.page, "Landing", "landing", entity.StatusDraft, `{"body":"x","page_sections":[
	 {"id":"hero_1","type":"hero","title":"","content":{"headline":"Hello there","faqs":[{"question":"Why?","answer":"<p>Because</p>"}],"legacy":"keep"},"sort_order":0},
	 {"id":"html_1","type":"html","content":{"html":"<h2>Story</h2><p>Text</p>"},"sort_order":1},
	 {"id":"odd_1","type":"plugin_only","content":{"x":1},"sort_order":2}]}`, t0)
	c.a.deps.Content.Theme = func() []ThemeSection {
		return []ThemeSection{
			{Type: "hero", Title: "Hero", Description: "A big opening", Schema: json.RawMessage(heroSchema)},
			{Type: "html", Title: "HTML", Schema: json.RawMessage(htmlSchema)},
		}
	}
	c.a.deps.Content.Templates = r.tpls
	c.a.deps.Content.Preview = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.lastBody = b
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>preview</body></html>"))
	})
	return r
}

func (r *editorRig) path() string { return "/admin/entities/" + r.ent.ID.String() + "/edit" }

// parseForm reads the named controls of the page's editor form (hidden inputs,
// text inputs, textareas, selects' selected option and checked boxes).
func parseForm(t *testing.T, body string) url.Values {
	t.Helper()
	v := url.Values{}
	form := body[strings.Index(body, `<form method="post" action="/admin/entities/`):]
	form = form[:strings.Index(form, "</form>")]
	for _, m := range inputRx.FindAllStringSubmatch(form, -1) {
		tag := m[0]
		name := attr(tag, "name")
		if name == "" || strings.Contains(tag, " disabled") || attr(tag, "type") == "submit" || attr(tag, "type") == "checkbox" && !strings.Contains(tag, " checked") {
			continue
		}
		v.Add(name, unescape(attr(tag, "value")))
	}
	for _, m := range textareaRx.FindAllStringSubmatch(form, -1) {
		v.Add(attr(m[1], "name"), unescape(strings.TrimPrefix(m[2], "\n")))
	}
	for _, m := range selectRx.FindAllStringSubmatch(form, -1) {
		name, picked := attr(m[1], "name"), ""
		for i, o := range optionRx.FindAllStringSubmatch(m[2], -1) {
			if i == 0 || strings.Contains(o[1], " selected") {
				picked = unescape(attr(o[1], "value"))
			}
		}
		if name != "" {
			v.Add(name, picked)
		}
	}
	return v
}

func previewURL(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct{ URL string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.URL == "" {
		t.Fatalf("preview response: %v %s", err, rec.Body.String())
	}
	return out.URL
}

func TestPostsOpenTheBlogEditor(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	post := r.ents.addSchema(20, "Post", "post", `{"type":"object","properties":{"content":{"type":"string"}}}`, `{}`, true)
	p := r.ents.addEntity(71, post, "Hello", "hello", entity.StatusDraft, `{"content":"<p>x</p>"}`, t0)
	if rec := r.do("GET", "/admin/entities/"+p.ID.String()+"/edit", "owner-token", nil, nil); rec.Code != 200 {
		t.Errorf("post editor not served: %d", rec.Code)
	}
}

var (
	inputRx    = regexpMust(`<input[^>]*>`)
	textareaRx = regexpMust(`(?s)<textarea([^>]*)>(.*?)</textarea>`)
	selectRx   = regexpMust(`(?s)<select([^>]*)>(.*?)</select>`)
	optionRx   = regexpMust(`<option([^>]*)>`)
)

func regexpMust(p string) *regexp.Regexp { return regexp.MustCompile(p) }

func attr(tag, name string) string {
	m := regexp.MustCompile(`\s` + name + `="([^"]*)"`).FindStringSubmatch(tag)
	if m == nil {
		return ""
	}
	return m[1]
}

func unescape(s string) string { return html.UnescapeString(s) }

func TestEditorModulesAreFingerprintedAndMapped(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	as, _ := r.a.state()
	for _, name := range []string{"editors", "editor/kit", "editor/fieldsview", "editor/htmlblocks", "editor/links", "canvas/host", "canvas/protocol"} {
		u := as.JSURL(name)
		if u == "" || !strings.Contains(u, "/js/"+name+".") {
			t.Fatalf("%s: %q", name, u)
		}
		if !strings.Contains(string(as.Importmap), `"@hm/`+name+`":"`+u+`"`) {
			t.Errorf("%s missing from the import map", name)
		}
		rec := r.do("GET", u, "", nil, nil)
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
			t.Errorf("%s: %d %s", u, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	// every bare "@hm/" import inside a module resolves through the map
	for _, f := range as.files {
		for _, m := range regexp.MustCompile(`from "(@hm/[a-z/]+)"|import\("(@hm/[a-z/]+)"\)`).FindAllStringSubmatch(string(f.body), -1) {
			name := strings.TrimPrefix(m[1]+m[2], "@hm/")
			if as.JSURL(name) == "" {
				t.Errorf("an import of @hm/%s has no module", name)
			}
		}
	}
}

func TestBulkAndStatusRefuseToPublishAGeneratedSlug(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	e := r.ents.ents[r.ent.ID]
	e.Slug = "untitled-1a2b3c4d"
	rec := r.post("/admin/entities/bulk", url.Values{"action": {"publish"}, "id": {e.ID.String()}})
	if e.Status == entity.StatusPublished {
		t.Error("bulk publish made a generated address live")
	}
	if fl := r.next(rec, "/admin/entities"); !strings.Contains(fl, "generated address") {
		t.Error("bulk flash does not explain the refusal")
	}
	rec = r.post("/admin/entities/"+e.ID.String()+"/status", url.Values{"status": {"published"}})
	if e.Status == entity.StatusPublished {
		t.Error("the status field made a generated address live")
	}
	if fl := r.next(rec, "/admin/entities"); !strings.Contains(fl, "generated address") {
		t.Error("status flash does not explain the refusal")
	}
	e.Slug = "landing"
	r.post("/admin/entities/"+e.ID.String()+"/status", url.Values{"status": {"published"}})
	if e.Status != entity.StatusPublished {
		t.Error("a real address could not be published")
	}
}

func TestPageEditorForPersonAndOthers(t *testing.T) {
	t.Parallel()

	schema := func(slug, props string) *entity.EntitySchema {
		return &entity.EntitySchema{Slug: slug, Schema: json.RawMessage(`{"type":"object","properties":{` + props + `}}`)}
	}
	withSections := &entity.Entity{Attributes: json.RawMessage(`{"page_sections":[{"id":"a","type":"hero"}]}`)}
	none := &entity.Entity{Attributes: json.RawMessage(`{}`)}
	for _, tc := range []struct {
		name string
		sc   *entity.EntitySchema
		e    *entity.Entity
		want bool
	}{
		{"page", schema("page", ``), none, true},
		{"person with stored sections", schema("person", `"job_title":{"type":"string"}`), withSections, false},
		{"person declaring the property", schema("person", `"page_sections":{"type":"array"}`), withSections, false},
		{"custom type with stored sections keeps the editor", schema("team", `"x":{"type":"string"}`), withSections, true},
		{"schema declaring the property", schema("landing", `"page_sections":{"type":"array"}`), none, true},
		{"declaring schema with sections", schema("landing", `"page_sections":{"type":"array"}`), withSections, true},
		{"post stays in the blog editor", schema("post", `"page_sections":{"type":"array"}`), withSections, false},
	} {
		if got := pageEditorFor(tc.sc, tc.e); got != tc.want {
			t.Errorf("%s: pageEditorFor = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A person that has page_sections stored is edited in the generic form.
func TestPersonWithStoredSectionsOpensTheGenericForm(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	person := r.ents.addSchema(21, "Person", "person", `{"type":"object","properties":{"job_title":{"type":"string","title":"Job title"}}}`, `{}`, true)
	p := r.ents.addEntity(72, person, "Ada", "ada", entity.StatusDraft, `{"job_title":"Engineer","page_sections":[{"id":"h","type":"hero","content":{}}]}`, t0)
	rec := r.do("GET", "/admin/entities/"+p.ID.String()+"/edit", "owner-token", nil, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/entities/"+p.ID.String() {
		t.Fatalf("edit = %d -> %q, want a redirect to the generic form", rec.Code, rec.Header().Get("Location"))
	}
	body := r.get(t, "owner-token", "/admin/entities/"+p.ID.String(), "")
	has(t, body, "Job title")
	if strings.Contains(body, "Add section") {
		t.Error("the generic person form shows page builder controls")
	}
}
