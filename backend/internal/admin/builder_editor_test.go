package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/entity"
)

const (
	builderOnlyDoc  = `{"page_doc":{"v":2,"nodes":[{"id":"c1","t":"container","c":[{"id":"h1","t":"heading","p":{"text":"Hi","level":2}}]}]}}`
	builderMarker   = `data-hm-builder`
	classicMarker   = `data-hm-page-editor`
	brokenDocAttrs  = `{"page_doc":{"v":9,"nodes":"nope"},"page_sections":[{"id":"html_1","type":"html","content":{"html":"<p>x</p>"},"sort_order":0}]}`
	validDocSection = `{"page_doc":{"v":2,"nodes":[{"id":"s1","t":"component","p":{"type":"html","content":{"html":"<p>x</p>"}}}]}}`
)

func TestBuilderDocStates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		attrs string
		want  docState
	}{
		{`{}`, docNone},
		{`{"page_sections":[]}`, docNone},
		{`{"page_doc":null}`, docNone},
		{builderOnlyDoc, docOK},
		{`{"page_doc":{"v":2,"nodes":[]}}`, docOK},
		{brokenDocAttrs, docBroken},
		{`{"page_doc":"x"}`, docBroken},
		{`{"page_sections":[{"id":"a"},{"id":"a"}]}`, docLegacy},
		{`{"page_sections":[{"id":"a"},{"id":"a"}],"page_doc":null}`, docLegacy},
		{`{"page_sections":[{"id":"a","type":"html","content":{}}]}`, docNone},
	}
	for _, c := range cases {
		if _, got := builderDoc(&entity.Entity{Attributes: json.RawMessage(c.attrs)}); got != c.want {
			t.Errorf("builderDoc(%s) = %d, want %d", c.attrs, got, c.want)
		}
	}
}

func TestEditorChoiceByPageDoc(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	get := func(attrs string) (int, string) {
		r.ent.Attributes = json.RawMessage(attrs)
		rec := r.do("GET", r.path(), "owner-token", nil, nil)
		return rec.Code, rec.Body.String()
	}

	for name, attrs := range map[string]string{"builder-only": builderOnlyDoc, "v1-expressible": validDocSection} {
		code, body := get(attrs)
		if code != http.StatusOK || !strings.Contains(body, builderMarker) || strings.Contains(body, classicMarker) {
			t.Errorf("%s doc: %d, builder=%v classic=%v", name, code, strings.Contains(body, builderMarker), strings.Contains(body, classicMarker))
		}
	}

	// No page_doc: the builder opens on the in-memory conversion.
	code, body := get(`{"page_sections":[{"id":"html_1","type":"html","content":{"html":"<p>x</p>"},"sort_order":0}]}`)
	if code != http.StatusOK || !strings.Contains(body, builderMarker) || strings.Contains(body, classicMarker) || !strings.Contains(body, "html_1") {
		t.Errorf("no page_doc: %d, want the builder on the converted sections", code)
	}

	// An unreadable page_doc: the recovery screen, never a form.
	code, body = get(brokenDocAttrs)
	if code != http.StatusUnprocessableEntity || strings.Contains(body, builderMarker) || strings.Contains(body, classicMarker) || !strings.Contains(body, "cannot be read") {
		t.Errorf("broken doc: %d, want the recovery screen", code)
	}
}

func TestBuilderBarSaveStateAndMenu(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	r.ent.Attributes = json.RawMessage(builderOnlyDoc)
	body := r.do("GET", r.path(), "owner-token", nil, nil).Body.String()
	has(t, body, `data-hm-bs-state`, `class="hm-savestate" data-state="saved"`, `<span class="hm-savestate__text">Saved</span>`,
		`popovertarget="bs-more"`, `data-hm-post="/admin/entities/`+r.ent.ID.String()+`/delete"`,
		`aria-label="Undo"`,
		`data-hm-bs-device-chip`, `data-hm-bs-sitestyles`, `>Keyboard shortcuts</button>`, `data-hm-danger>Delete page</a>`, `data-hm-confirm="This page and its version history will be deleted. This cannot be undone."`, `data-hm-confirm-label="Delete page"`, `<span>Navigator</span>`, `<span>Inspector</span>`,
		`data-flip=""`)
	lacks(t, body, "Update page", "data-hm-bs-device-item")
	// Undo and Redo share one arrow icon: Undo alone is mirrored (css .hm-bs__tools [data-flip]), so the two differ.
	if n := strings.Count(body, "data-flip"); n != 1 {
		t.Errorf("data-flip appears %d times, want 1 (Undo)", n)
	}
}

// A published page says "Update" in the bar and in the no-JS outline: one label for one action.
// (The draft fixture shows neither, so this runs on a published entity.)
func TestBuilderUpdateLabelMatchesBarAndOutline(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	r.ent.Status = entity.StatusPublished
	r.ent.Attributes = json.RawMessage(builderOnlyDoc)
	body := r.do("GET", r.path(), "owner-token", nil, nil).Body.String()
	if n := strings.Count(body, "<span>Update</span>"); n != 2 {
		t.Errorf("%d Update buttons, want 2 (bar and outline)", n)
	}
	lacks(t, body, "Update page")
}

func TestPostForDocPageReachesBuilderHandler(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	r.ent.Attributes = json.RawMessage(builderOnlyDoc)
	rec := r.post(r.path(), url.Values{"version": {"1"}, "_op": {"save"}})
	if rec.Code != http.StatusFound && rec.Code != http.StatusSeeOther {
		t.Fatalf("doc page POST = %d, want the builder handler to save and redirect", rec.Code)
	}
	if rec.Header().Get("Location") != r.path() {
		t.Errorf("redirect to %q", rec.Header().Get("Location"))
	}

	// A page with only sections is saved by the same handler, on its conversion.
	r.ent.Attributes = json.RawMessage(`{"page_sections":[{"id":"a","type":"html","content":{"html":"<p>x</p>"},"sort_order":0}]}`)
	if rec := r.post(r.path(), url.Values{"version": {"1"}, "_op": {"save"}}); rec.Code != http.StatusFound && rec.Code != http.StatusSeeOther {
		t.Fatalf("sections-only page POST = %d, want the builder handler to save and redirect", rec.Code)
	}
}

func TestNewDraftAttributes(t *testing.T) {
	t.Parallel()

	r := newDraftRig(t)
	for typ, want := range map[string]string{"page": `{"page_doc":{"v":2,"nodes":[]}}`, "post": `{}`} {
		rec := r.do("POST", "/admin/entities/drafts", "owner-token", url.Values{"type": {typ}}, nil)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%s draft: %d", typ, rec.Code)
		}
		var got json.RawMessage
		for _, e := range r.ents.ents {
			if strings.HasPrefix(e.Name, "Untitled "+typ) {
				got = e.Attributes
			}
		}
		if string(got) != want {
			t.Errorf("%s draft attributes = %s, want %s", typ, got, want)
		}
	}

	// The first save of the empty doc keeps the page_sections mirror empty.
	stored := json.RawMessage(`{"page_doc":{"v":2,"nodes":[]}}`)
	out, err := entity.NormalizeAttributes(stored, stored)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if s := strings.TrimSpace(string(m["page_sections"])); s != "" && s != "[]" && s != "null" {
		t.Errorf("page_sections mirror = %s, want empty or absent", s)
	}
}

// The boot JSON sits in a <script> element as template.HTML: json.Marshal's HTML
// escaping is what keeps a title or text from closing it.
func TestBuilderBootJSONCannotCloseItsScript(t *testing.T) {
	t.Parallel()

	const evil = `</script><script>alert(1)</script><!--`
	r := newEditorRig(t)
	doc, _ := json.Marshal(map[string]any{"page_doc": map[string]any{"v": 2, "nodes": []any{
		map[string]any{"id": "c1", "t": "container", "c": []any{
			map[string]any{"id": "h1", "t": "heading", "p": map[string]any{"text": evil, "level": 2}},
		}},
	}}})
	r.ent.Attributes = doc
	r.ent.Name = evil
	rec := r.do("GET", r.path(), "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, builderMarker) {
		t.Fatalf("builder screen: %d", rec.Code)
	}
	const open = `id="hm-builder-boot">`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatal("no boot element")
	}
	rest := body[i+len(open):]
	end := strings.Index(rest, "</script>")
	if end < 0 {
		t.Fatal("boot element is not closed")
	}
	payload := rest[:end]
	if strings.ContainsAny(payload, "<>&") {
		t.Errorf("boot JSON carries raw HTML characters: %.200s", payload)
	}
	var got struct {
		Name string `json:"name"`
		Doc  struct {
			Nodes []struct {
				C []struct {
					P map[string]any `json:"p"`
				} `json:"c"`
			} `json:"nodes"`
		} `json:"doc"`
	}
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("boot JSON: %v", err)
	}
	if got.Name != evil || got.Doc.Nodes[0].C[0].P["text"] != evil {
		t.Errorf("payload does not round-trip: %q / %v", got.Name, got.Doc.Nodes[0].C[0].P)
	}
	if strings.Contains(body, "<script>alert(1)") {
		t.Error("the page carries the injected script element")
	}
}

func TestBuilderBootOptionsListSchemasAndTerms(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	r.terms.add(601, "category", "News", "news", nil)
	r.terms.add(602, "tag", "Go", "go", nil)
	r.ent.Attributes = json.RawMessage(builderOnlyDoc)
	rec := r.do("GET", r.path(), "owner-token", nil, nil)
	body := rec.Body.String()
	const open = `id="hm-builder-boot">`
	i := strings.Index(body, open)
	if rec.Code != http.StatusOK || i < 0 {
		t.Fatalf("builder screen: %d", rec.Code)
	}
	rest := body[i+len(open):]
	var boot struct {
		Options map[string][]string `json:"options"`
	}
	if err := json.Unmarshal([]byte(rest[:strings.Index(rest, "</script>")]), &boot); err != nil {
		t.Fatal(err)
	}
	for kind, want := range map[string][]string{"terms": {"category:news", "tag:go"}, "schemas": {"service", "page", "recipe"}} {
		got := boot.Options[kind]
		for _, w := range want {
			if !slices.Contains(got, w) {
				t.Errorf("options[%s] = %v, missing %s", kind, got, w)
			}
		}
	}
	for _, slug := range boot.Options["schemas"] {
		if slug == "block" || slug == "theme_part" {
			t.Errorf("reuse schema %s offered to loops", slug)
		}
	}
	if boot.Options["forms"] == nil || boot.Options["menus"] == nil {
		t.Error("forms/menus options must stay present")
	}
}

// The boot carries the per-kind universal keys (u) and the published pages the link field offers.
func TestBuilderBootCarriesUniversalKeysAndPages(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	r.ents.addEntity(71, r.page, "Pricing", "pricing", entity.StatusPublished, `{"body":"x"}`, t0)
	r.ents.addEntity(73, r.recipe, "Newer recipe", "newer", entity.StatusPublished, `{}`, t0.Add(time.Hour))
	r.ents.addEntity(72, r.page, "Secret", "secret", entity.StatusDraft, `{"body":"x"}`, t0)
	r.a.deps.Content.PublicPath = func(_ context.Context, e *entity.Entity) string { return "/" + e.Slug }
	r.ent.Attributes = json.RawMessage(builderOnlyDoc)
	body := r.do("GET", r.path(), "owner-token", nil, nil).Body.String()
	const open = `id="hm-builder-boot">`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatal("no boot element")
	}
	rest := body[i+len(open):]
	var boot struct {
		Options map[string][]string `json:"options"`
		Kinds   []struct {
			Kind string   `json:"kind"`
			U    []string `json:"u"`
			IA   bool     `json:"ia"`
		} `json:"kinds"`
	}
	if err := json.Unmarshal([]byte(rest[:strings.Index(rest, "</script>")]), &boot); err != nil {
		t.Fatal(err)
	}
	got := boot.Options["pages"]
	if !slices.Contains(got, "/pricing") || !slices.Contains(got, "/newer") || slices.Contains(got, "/secret") {
		t.Errorf("options.pages = %v, want the published entities only", got)
	}
	if slices.Index(got, "/pricing") > slices.Index(got, "/newer") {
		t.Errorf("options.pages = %v: pages come before posts", got)
	}
	if r.ents.maxLimit > 100 {
		t.Errorf("the page list asked for %d a page; the repository serves 100 at most and falls back to 20 above it", r.ents.maxLimit)
	}
	u := map[string][]string{}
	ia := map[string]bool{}
	for _, k := range boot.Kinds {
		u[k.Kind] = k.U
		ia[k.Kind] = k.IA
	}
	if !ia["button"] || !ia["menu"] || !ia["form"] || ia["text"] || ia["container"] || ia["image"] {
		t.Errorf("interactive kinds in the boot: button %v menu %v form %v text %v container %v", ia["button"], ia["menu"], ia["form"], ia["text"], ia["container"])
	}
	if !slices.Contains(boot.Options["reserved"], "main-content") || !slices.Contains(boot.Options["reserved"], "toc") {
		t.Errorf("options.reserved = %v", boot.Options["reserved"])
	}
	if !slices.Contains(u["container"], "anchor") || !slices.Contains(u["button"], "aria") || !slices.Contains(u["button"], "tip") {
		t.Errorf("u = container %v, button %v", u["container"], u["button"])
	}
	if slices.Contains(u["component"], "anchor") || slices.Contains(u["spacer"], "anchor") {
		t.Errorf("component/spacer must not take anchor: %v %v", u["component"], u["spacer"])
	}
}

// A page whose page_sections cannot become a doc (duplicate or unusable ids)
// opens the recovery screen with the cause and the CLI hint, and every builder
// write route refuses it: nothing is stored.
func TestUnconvertibleLegacyPageIsRefused(t *testing.T) {
	t.Parallel()

	r := newBuilderRenderRig(t)
	const legacy = `{"page_sections":[{"id":"a","type":"html","content":{"html":"<p>x</p>"},"sort_order":0},{"id":"a","type":"html","content":{"html":"<p>y</p>"},"sort_order":1}]}`
	r.page.Attributes = json.RawMessage(legacy)
	version := r.page.Version

	code, body := func() (int, string) {
		rec := r.do("GET", r.url(r.page, "/edit"), "owner-token", nil, nil)
		return rec.Code, rec.Body.String()
	}()
	if code != http.StatusConflict || strings.Contains(body, builderMarker) ||
		!strings.Contains(body, "legacy layout") || !strings.Contains(body, "Entity "+r.page.ID.String()) || !strings.Contains(body, "page_doc through the API or MCP") || !strings.Contains(body, "section ids must be unique") {
		t.Errorf("GET: %d, want the legacy recovery screen:\n%s", code, body)
	}

	doc := `{"v":2,"nodes":[]}`
	for _, p := range []struct{ name, path, body string }{
		{"save", r.url(r.page, "/builder/save"), saveBody(version, doc, "save", "")},
		{"autosave", r.url(r.page, "/builder/save"), saveBody(version, doc, "autosave", "")},
		{"stage", r.url(r.page, "/builder/stage"), `{"doc":` + doc + `}`},
		{"component", r.url(r.page, "/builder/component"), `{"doc":` + doc + `}`},
		{"render", r.url(r.page, "/edit/render"), `{"doc":` + doc + `}`},
	} {
		if rec := r.postJSON(p.path, "owner-token", p.body, nil); rec.Code != http.StatusConflict {
			t.Errorf("%s: %d, want 409: %s", p.name, rec.Code, rec.Body)
		}
	}
	if rec := r.post(r.url(r.page, "/edit"), url.Values{"version": {"1"}, "_op": {"save"}}); rec.Code != http.StatusConflict {
		t.Errorf("outline POST: %d, want 409", rec.Code)
	}
	if rec := r.post(r.url(r.page, "/versions/1/nodes/x/restore"), url.Values{}); rec.Code != http.StatusConflict {
		t.Errorf("node restore: %d, want 409", rec.Code)
	}
	if string(r.page.Attributes) != legacy || r.page.Version != version {
		t.Errorf("a refused write stored something: v%d %s", r.page.Version, r.page.Attributes)
	}
}

// The link field's page list costs a fixed number of queries: one list per
// schema page and one batch resolve per list, never one per entity, and a
// schema without a public URL is not listed at all.
func TestBuilderPagesAreResolvedInBatches(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	for i := range 30 {
		slug := fmt.Sprintf("p%02d", i)
		r.ents.addEntity(100+i, r.page, slug, slug, entity.StatusPublished, `{"body":"x"}`, t0)
	}
	r.ents.addEntity(200, r.recipe, "Soup", "soup", entity.StatusPublished, `{}`, t0)
	var batches, singles int
	var asked []string
	c := &r.a.deps.Content
	c.PublicPath = func(_ context.Context, e *entity.Entity) string { singles++; return "/" + e.Slug }
	c.PublicPaths = func(_ context.Context, sc *entity.EntitySchema, rows []*entity.Entity) ([]string, bool) {
		if sc.Slug == r.recipe.Slug {
			return nil, false // no public URL
		}
		if len(rows) == 0 {
			return nil, true
		}
		batches++
		asked = append(asked, sc.Slug)
		out := make([]string, len(rows))
		for i, e := range rows {
			out[i] = "/" + e.Slug
		}
		return out, true
	}
	r.ent.Attributes = json.RawMessage(builderOnlyDoc)
	before := r.ents.lists
	got := r.a.builderPages(httptest.NewRequest("GET", "/", nil))
	if singles != 0 || batches != len(asked) || slices.Contains(asked, r.recipe.Slug) || !slices.Contains(asked, r.page.Slug) {
		t.Errorf("single lookups %d, batches %d for %v; want 0 and one batch per routed schema", singles, batches, asked)
	}
	if slices.Contains(got, "/soup") || !slices.Contains(got, "/p00") || !slices.Contains(got, "/p29") {
		t.Errorf("pages = %v", got)
	}
	if n := r.ents.lists - before; n > len(asked) {
		t.Errorf("%d entity list queries for 30 entities, want one per routed schema (%d)", n, len(asked))
	}
}
