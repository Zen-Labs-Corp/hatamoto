package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
)

const outlineDoc = `{"v":2,"nodes":[
 {"id":"c1","t":"container","p":{"tag":"div"},"c":[
  {"id":"h1","t":"heading","p":{"text":"Hi","level":2}},
  {"id":"t1","t":"text","p":{"text":"Body"}}]},
 {"id":"s1","t":"component","p":{"type":"hero","content":{"heading":"Hello","faqs":[{"question":"Why?","answer":"<p>Because</p>"}]}}},
 {"id":"s2","t":"component","p":{"type":"hero","content":{"heading":"Other"}}},
 {"id":"s3","t":"component","p":{"type":"html","content":{"html":"<p>two</p>"}}}
]}`

type outlineRig struct {
	*saveRig
}

func newOutlineRig(t *testing.T) *outlineRig {
	r := &outlineRig{newSaveRig(t)}
	r.setDoc(outlineDoc)
	return r
}

func (r *outlineRig) setDoc(doc string) {
	r.ents.ents[r.page.ID].Attributes = json.RawMessage(`{"title":"T","page_doc":` + doc + `}`)
}

func (r *outlineRig) entity() *entity.Entity { return r.ents.ents[r.page.ID] }

func (r *outlineRig) doc(t *testing.T) *builder.Doc {
	t.Helper()
	raw, _ := storedDoc(t, r.entity())
	d, err := builder.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

var outlineFormRx = regexp.MustCompile(`(?s)<form method="post" action="/admin/entities/[^"]+/edit" id="hm-bo".*?</form>`)

// outlineForm reads the outline's controls out of a page, as a browser posts them.
func outlineForm(t *testing.T, body string) url.Values {
	t.Helper()
	f := outlineFormRx.FindString(body)
	if f == "" {
		t.Fatalf("no outline form in: %.300s", body)
	}
	return fragmentValues(f)
}

func (r *outlineRig) get(t *testing.T, query string) (int, string) {
	t.Helper()
	rec := r.do("GET", r.url(r.page, "/edit")+query, "owner-token", nil, nil)
	return rec.Code, rec.Body.String()
}

func (r *outlineRig) form(t *testing.T) url.Values {
	t.Helper()
	code, body := r.get(t, "?outline=1")
	if code != 200 {
		t.Fatalf("outline: %d", code)
	}
	return outlineForm(t, body)
}

func (r *outlineRig) post(f url.Values, op string) *httptest.ResponseRecorder {
	if op != "" {
		f.Set("_op", op)
	}
	return r.do("POST", r.url(r.page, "/edit"), "owner-token", f, nil)
}

func nodeOrder(d *builder.Doc) string {
	var out []string
	walk(d.Nodes, func(n, _ *builder.Node) { out = append(out, n.ID) })
	return strings.Join(out, ",")
}

func TestOutlineRenders(t *testing.T) {
	t.Parallel()

	r := newOutlineRig(t)
	code, body := r.get(t, "?outline=1")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	has(t, body, `<legend class="hm-bo__legend">Container</legend>`, `<legend class="hm-bo__legend">Heading “Hi”</legend>`,
		`name="n.h1.p.text"`, `name="n.h1.p.level"`, `type="number"`, `name="n.c1.p.tag"`, `name="n.s1.c.heading"`, `name="n.s2.c.heading"`,
		`name="_op" value="up.t1"`, `name="_op" value="into.c1"`, `name="_op" value="publish"`, `name="doc"`, `name="outline" value="1"`,
		`aria-label="Remove Heading “Hi”"`)
	if strings.Contains(body, "data-hm-builder") || strings.Contains(body, `style="`) {
		t.Error("outline mode must be the plain page without the shell or inline styles")
	}
	// Every control has a label and every fieldset a legend.
	for _, m := range regexp.MustCompile(`<(input|select|textarea)[^>]*\sid="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		if !strings.Contains(body, `for="`+m[2]+`"`) && !strings.Contains(m[0], `type="hidden"`) {
			t.Errorf("control %s has no label", m[2])
		}
	}
	if n, l := strings.Count(body, "<fieldset"), strings.Count(body, "<legend"); n != l {
		t.Errorf("%d fieldsets, %d legends", n, l)
	}

	// Without ?outline=1 the shell is served and the outline sits inside noscript.
	_, body = r.get(t, "")
	has(t, body, `data-hm-bs-dock`, `id="hm-bs-canvas"`)
	i := strings.Index(body, "<noscript>")
	if !strings.Contains(body, "data-hm-builder") || i < 0 || !strings.Contains(body[i:], `name="n.h1.p.text"`) || strings.Contains(body[:i], `name="n.h1.p.text"`) {
		t.Error("the outline belongs inside <noscript> beside the shell")
	}
}

func TestOutlineRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("props and component content", func(t *testing.T) {
		r := newOutlineRig(t)
		f := r.form(t)
		f.Set("n.h1.p.text", "Changed")
		f.Set("n.h1.p.level", "3")
		f.Set("n.c1.p.tag", "section")
		f.Set("n.s1.c.heading", "New hero")
		f.Set("n.s1.c.faqs.0.question", "Why not?")
		f.Set("name", "Renamed")
		rec := r.post(f, "save")
		if rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
			t.Fatalf("save: %d %s", rec.Code, rec.Body)
		}
		d := r.doc(t)
		h, _ := d.Find("h1")
		if h.P["text"] != "Changed" || h.P["level"] != 3.0 {
			t.Errorf("heading: %v", h.P)
		}
		if c, _ := d.Find("c1"); c.P["tag"] != "section" {
			t.Errorf("container: %v", c.P)
		}
		s, _ := d.Find("s1")
		content := s.P["content"].(map[string]any)
		faq := content["faqs"].([]any)[0].(map[string]any)
		if content["heading"] != "New hero" || faq["question"] != "Why not?" || faq["answer"] != "<p>Because</p>" {
			t.Errorf("component content: %v", content)
		}
		if o, _ := d.Find("s2"); o.P["content"].(map[string]any)["heading"] != "Other" {
			t.Error("another section changed")
		}
		if r.entity().Name != "Renamed" || r.entity().Version != 2 {
			t.Errorf("name %q version %d", r.entity().Name, r.entity().Version)
		}
		if loc := rec.Header().Get("Location"); loc != "/admin/entities/"+r.page.ID.String()+"/edit?outline=1" { // the form came from ?outline=1
			t.Errorf("redirect %q", loc)
		}
	})

	t.Run("an unchanged form changes nothing", func(t *testing.T) {
		r := newOutlineRig(t)
		before := string(r.entity().Attributes)
		if rec := r.post(r.form(t), "save"); rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
			t.Fatalf("save: %d", rec.Code)
		}
		d := r.doc(t)
		want, _ := builder.Parse([]byte(outlineDoc))
		a, _ := json.Marshal(d)
		b, _ := json.Marshal(want)
		if string(a) != string(b) {
			t.Errorf("doc changed:\n%s\n%s\nbefore %s", a, b, before)
		}
	})

	t.Run("up and down re-render without saving, and carry on to a save", func(t *testing.T) {
		r := newOutlineRig(t)
		rec := r.post(r.form(t), "up.t1")
		if rec.Code != 200 || r.entity().Version != 1 {
			t.Fatalf("up: %d, version %d", rec.Code, r.entity().Version)
		}
		f := outlineForm(t, rec.Body.String())
		if f.Get("version") != "1" {
			t.Errorf("version %q", f.Get("version"))
		}
		rec = r.post(f, "down.s1")
		if rec.Code != 200 {
			t.Fatalf("down: %d", rec.Code)
		}
		rec = r.post(outlineForm(t, rec.Body.String()), "save")
		if rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
			t.Fatalf("save: %d %s", rec.Code, rec.Body)
		}
		if got := nodeOrder(r.doc(t)); got != "c1,t1,h1,s2,s1,s3" {
			t.Errorf("order %s", got)
		}
		// The edge buttons are disabled.
		_, body := r.get(t, "?outline=1")
		if !regexp.MustCompile(`<button[^>]* disabled[^>]* value="up\.t1"`).MatchString(body) {
			t.Error("the first sibling can still move up")
		}
	})

	t.Run("add after, add inside, add at the end, delete", func(t *testing.T) {
		r := newOutlineRig(t)
		f := r.form(t)
		f.Set("kind.h1", "text")
		rec := r.post(f, "after.h1")
		if rec.Code != 200 {
			t.Fatalf("after: %d %s", rec.Code, rec.Body)
		}
		f = outlineForm(t, rec.Body.String())
		f.Set("kind.c1", "component:html")
		rec = r.post(f, "into.c1")
		f = outlineForm(t, rec.Body.String())
		f.Set("add_kind", "divider")
		rec = r.post(f, "add")
		f = outlineForm(t, rec.Body.String())
		rec = r.post(f, "del.t1")
		if rec.Code != 200 {
			t.Fatalf("del: %d", rec.Code)
		}
		if rec = r.post(outlineForm(t, rec.Body.String()), "save"); rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
			t.Fatalf("save: %d %s", rec.Code, rec.Body)
		}
		d := r.doc(t)
		c, _ := d.Find("c1")
		if len(c.C) != 3 || c.C[0].ID != "h1" || c.C[1].T != "text" || c.C[2].T != "component" || c.C[2].P["type"] != "html" {
			t.Errorf("container children: %s", nodeOrder(d))
		}
		if last := d.Nodes[len(d.Nodes)-1]; last.T != "divider" {
			t.Errorf("last node %s", last.T)
		}
		if n, _ := d.Find("t1"); n != nil {
			t.Error("t1 was not removed")
		}
		if err := d.Validate(); err != nil {
			t.Error(err)
		}
	})

	t.Run("a structural widget is added with its seeded children and nests", func(t *testing.T) {
		r := newOutlineRig(t)
		f := r.form(t)
		f.Set("add_kind", "tabs")
		rec := r.post(f, "add")
		if rec.Code != 200 {
			t.Fatalf("add tabs: %d %s", rec.Code, rec.Body)
		}
		f = outlineForm(t, rec.Body.String())
		posted, err := builder.Parse([]byte(f.Get("doc")))
		if err != nil {
			t.Fatal(err)
		}
		panel := posted.Nodes[len(posted.Nodes)-1].C[0].ID // the new tabs' first panel
		f.Set("kind."+panel, "divider")
		if rec = r.post(f, "into."+panel); rec.Code != 200 || strings.Contains(rec.Body.String(), "Choose where to add") {
			t.Fatalf("into a panel refused: %d %s", rec.Code, regexp.MustCompile(`(?s)role="alert".{0,400}`).FindString(rec.Body.String()))
		}
		if rec = r.post(outlineForm(t, rec.Body.String()), "save"); rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
			t.Fatalf("save: %d %s", rec.Code, rec.Body)
		}
		d := r.doc(t)
		tabs := d.Nodes[len(d.Nodes)-1]
		if tabs.T != "tabs" || len(tabs.C) < 2 || tabs.C[0].T != "tabpanel" {
			t.Fatalf("tabs not seeded: %+v", tabs)
		}
		var got int
		for _, p := range tabs.C {
			got += len(p.C)
		}
		if got != 1 {
			t.Errorf("want 1 divider inside the panels, got %d: %s", got, func() string { b, _ := json.Marshal(tabs); return string(b) }())
		}
		if err := d.Validate(); err != nil {
			t.Error(err)
		}
	})

	t.Run("publish", func(t *testing.T) {
		r := newOutlineRig(t)
		if rec := r.post(r.form(t), "publish"); rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
			t.Fatalf("publish: %d %s", rec.Code, rec.Body)
		}
		if r.entity().Status != entity.StatusPublished {
			t.Error("not published")
		}
	})
}

func TestOutlineMatchesJSONPath(t *testing.T) {
	t.Parallel()

	// The outline form.
	a := newOutlineRig(t)
	f := a.form(t)
	f.Set("n.h1.p.text", "Same edit")
	f.Set("n.h1.p.level", "4")
	f.Set("n.s1.c.heading", "Same hero")
	if rec := a.post(f, "save"); rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		t.Fatalf("outline save: %d", rec.Code)
	}

	// The JSON path: ops through Doc.Apply, component content through the endpoint.
	b := newOutlineRig(t)
	prev := `{"heading":"Hello","faqs":[{"question":"Why?","answer":"<p>Because</p>"}]}`
	_, form := b.component(t, url.Values{"type": {"hero"}, "node": {"s1"}, "mode": {"form"}, "prev": {prev}})
	cf := fragmentValues(form.HTML)
	cf.Set("n.s1.c.heading", "Same hero")
	cf.Set("type", "hero")
	cf.Set("node", "s1")
	cf.Set("mode", "decode")
	cf.Set("prev", prev)
	_, dec := b.component(t, cf)
	doc := b.doc(t)
	content, _ := json.Marshal(dec.Content)
	for _, op := range []builder.Op{
		{Op: "setProp", ID: "h1", Key: "text", Value: json.RawMessage(`"Same edit"`)},
		{Op: "setProp", ID: "h1", Key: "level", Value: json.RawMessage(`4`)},
		{Op: "setProp", ID: "s1", Key: "content", Value: content},
	} {
		if _, err := doc.Apply(op); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(doc)
	if code, out := b.save(t, b.page, saveBody(1, string(raw), "save", "")); code != 200 || !out.OK {
		t.Fatalf("json save: %d %+v", code, out)
	}

	got, _ := json.Marshal(a.doc(t))
	want, _ := json.Marshal(b.doc(t))
	if string(got) != string(want) {
		t.Errorf("outline and JSON paths differ:\n%s\n%s", got, want)
	}
}

func TestOutlineStale(t *testing.T) {
	t.Parallel()

	r := newOutlineRig(t)
	f := r.form(t)
	r.entity().Version = 5 // somebody saved meanwhile
	f.Set("n.h1.p.text", "Mine")
	rec := r.post(f, "save")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "saved by someone else") {
		t.Fatalf("stale: %d", rec.Code)
	}
	if len(r.rec.inputs) != 0 {
		t.Error("a stale save wrote")
	}
	// The page shows the typed text and the current version, so saving again is the overwrite.
	f = outlineForm(t, rec.Body.String())
	if f.Get("version") != "5" || !strings.Contains(f.Get("doc"), `"Mine"`) {
		t.Errorf("conflict page: version %q", f.Get("version"))
	}
	// A missing version is stale too.
	f.Del("version")
	if rec = r.post(f, "save"); rec.Code != http.StatusConflict {
		t.Errorf("no version: %d", rec.Code)
	}
}

func TestOutlineInvalidValues(t *testing.T) {
	t.Parallel()

	r := newOutlineRig(t)
	f := r.form(t)
	f.Set("n.h1.p.level", "nine")
	f.Set("n.s1.c.heading", strings.Repeat("x", 61))
	rec := r.post(f, "save")
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, "problems") || !strings.Contains(body, `value="nine"`) || !strings.Contains(body, `aria-invalid="true"`) {
		t.Fatalf("invalid: %d", rec.Code)
	}
	if len(r.rec.inputs) != 0 {
		t.Error("an invalid save wrote")
	}
	// Out of range goes through the schema, not a client check.
	f = r.form(t)
	f.Set("n.h1.p.level", "9")
	if rec = r.post(f, "save"); rec.Code != 422 || len(r.rec.inputs) != 0 {
		t.Errorf("level 9: %d", rec.Code)
	}
	// An untitled page is not published.
	f = r.form(t)
	f.Set("name", untitledPage)
	r.entity().Name, r.entity().Slug = untitledPage, "untitled-1a2b3c4d"
	if rec = r.post(f, "publish"); rec.Code != 422 || r.entity().Status == entity.StatusPublished {
		t.Errorf("untitled publish: %d", rec.Code)
	}
}

func TestOutlinePrefixIsolation(t *testing.T) {
	t.Parallel()

	r := newOutlineRig(t)
	f := r.form(t)
	f.Set("n.s2.c.heading", "Second only")
	if rec := r.post(f, "save"); rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		t.Fatalf("save: %d", rec.Code)
	}
	d := r.doc(t)
	s1, _ := d.Find("s1")
	s2, _ := d.Find("s2")
	if s1.P["content"].(map[string]any)["heading"] != "Hello" || s2.P["content"].(map[string]any)["heading"] != "Second only" {
		t.Errorf("s1 %v s2 %v", s1.P["content"], s2.P["content"])
	}
}

func TestOutlineIgnoresUnknownNodes(t *testing.T) {
	t.Parallel()

	r := newOutlineRig(t)
	f := r.form(t)
	f.Set("n.ghost.p.text", "boo")
	f.Set("n.ghost.c.heading", "boo")
	f.Set("_op", "up.ghost")
	if rec := r.post(f, ""); rec.Code != 200 {
		t.Fatalf("op on a missing node: %d", rec.Code)
	}
	f.Set("kind.ghost", "text")
	if rec := r.post(f, "after.ghost"); rec.Code != 200 || r.entity().Version != 1 {
		t.Errorf("add after a missing node: %d", rec.Code)
	}
	f = r.form(t)
	if rec := r.post(f, "save"); rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		t.Fatal(rec.Code)
	}
	if strings.Contains(string(r.entity().Attributes), "ghost") || strings.Contains(string(r.entity().Attributes), "boo") {
		t.Error("a node outside the doc was written")
	}
}

func TestOutlineLimits(t *testing.T) {
	t.Parallel()

	// Depth: eight nested containers cannot take a ninth level.
	deep := `{"id":"d8","t":"container"}`
	for i := 7; i >= 1; i-- {
		deep = `{"id":"d` + string(rune('0'+i)) + `","t":"container","c":[` + deep + `]}`
	}
	r := newOutlineRig(t)
	r.setDoc(`{"v":2,"nodes":[` + deep + `]}`)
	f := r.form(t)
	f.Set("kind.d8", "heading")
	rec := r.post(f, "into.d8")
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "problem") {
		t.Errorf("depth: %d", rec.Code)
	}

	// Nodes: a page of 1000 nodes takes no more.
	var b strings.Builder
	b.WriteString(`{"v":2,"nodes":[`)
	for i := 0; i < 1000; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"id":"s` + strings.Repeat("a", 1) + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i/676)) + `","t":"spacer"}`)
	}
	b.WriteString(`]}`)
	r.setDoc(b.String())
	f = r.form(t)
	f.Set("add_kind", "divider")
	if rec = r.post(f, "add"); rec.Code != 422 {
		t.Errorf("nodes: %d", rec.Code)
	}
}

func TestOutlineGuards(t *testing.T) {
	t.Parallel()

	r := newOutlineRig(t)
	f := r.form(t)
	f.Del("_csrf") // the header is the proof under test
	path := r.url(r.page, "/edit")
	if rec := r.do("POST", path, "owner-token", f, map[string]string{"X-CSRF-Token": ""}); rec.Code != http.StatusForbidden {
		t.Errorf("no csrf: %d", rec.Code)
	}
	if rec := r.do("POST", path, "", f, nil); rec.Code == 200 {
		t.Error("posted without a session")
	}
	if len(r.rec.inputs) != 0 {
		t.Error("a refused post wrote")
	}
}

const bindDoc = `{"v":2,"nodes":[
 {"id":"h1","t":"heading","p":{"text":"Hi","level":2}},
 {"id":"x1","t":"html","p":{"html":"<p>raw</p>"}},
 {"id":"lp","t":"loop","p":{"schema":"post"},"c":[{"id":"it","t":"text","b":{"text":"loop.item.title"}}]}]}`

func TestOutlineBindControl(t *testing.T) {
	t.Parallel()

	r := newOutlineRig(t)
	r.setDoc(bindDoc)
	_, body := r.get(t, "?outline=1")
	has(t, body, `name="n.h1.b.text"`, `name="n.it.b.text"`, `<option value="entity.name"`, `<option value="entity.attributes."`, `Literal value`)
	if strings.Contains(body, `name="n.x1.b.html"`) || strings.Contains(body, `name="n.h1.b.level"`) {
		t.Error("a non-bindable prop got a bind control")
	}
	// loop item sources are offered inside the loop only
	if i := strings.Index(body, `name="n.h1.b.text"`); strings.Contains(body[i:strings.Index(body[i:], "</select>")+i], "loop.item.title") {
		t.Error("loop.item sources offered outside a loop")
	}
	j := strings.Index(body, `name="n.it.b.text"`)
	if !strings.Contains(body[j:strings.Index(body[j:], "</select>")+j], `<option value="loop.item.title" selected>`) {
		t.Error("the bound source is not selected inside the loop")
	}
}

func TestOutlineBindRoundTrip(t *testing.T) {
	t.Parallel()

	r := newOutlineRig(t)
	r.setDoc(bindDoc)
	f := r.form(t)
	f.Set("n.h1.b.text", "entity.name")
	if rec := r.post(f, "save"); rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	h, _ := r.doc(t).Find("h1")
	if h.B["text"] != "entity.name" {
		t.Fatalf("b = %v", h.B)
	}
	if _, has := h.P["text"]; has || h.P["level"] != 2.0 {
		t.Errorf("a bound prop must lose its literal, keep the others: %v", h.P)
	}
	// the stored doc has no page_sections mirror
	if strings.Contains(string(r.entity().Attributes), `"page_sections":[{`) {
		t.Errorf("mirror written: %s", r.entity().Attributes)
	}
	// the form of the bound page round-trips unchanged
	before := string(r.entity().Attributes)
	if rec := r.post(r.form(t), "save"); rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		t.Fatalf("resave: %d", rec.Code)
	}
	if string(r.entity().Attributes) != before {
		t.Errorf("an unchanged bound form changed the doc:\n%s\n%s", before, r.entity().Attributes)
	}
	// an attribute source takes its path
	f = r.form(t)
	f.Set("n.h1.b.text", "entity.attributes.")
	f.Set("n.h1.bp.text", "deep.sku")
	if rec := r.post(f, "save"); rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		t.Fatalf("path save: %d %s", rec.Code, rec.Body)
	}
	if h, _ = r.doc(t).Find("h1"); h.B["text"] != "entity.attributes.deep.sku" {
		t.Errorf("b = %v", h.B)
	}
	_, body := r.get(t, "?outline=1")
	has(t, body, `name="n.h1.bp.text"`, `value="deep.sku"`)
	// back to a literal
	f = r.form(t)
	f.Set("n.h1.b.text", "")
	f.Set("n.h1.p.text", "Plain again")
	if rec := r.post(f, "save"); rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		t.Fatalf("unbind: %d %s", rec.Code, rec.Body)
	}
	if h, _ = r.doc(t).Find("h1"); len(h.B) != 0 || h.P["text"] != "Plain again" {
		t.Errorf("unbound heading: b=%v p=%v", h.B, h.P)
	}
}

func TestOutlineBindInvalidSourceIs422(t *testing.T) {
	t.Parallel()

	r := newOutlineRig(t)
	r.setDoc(bindDoc)
	for name, set := range map[string]func(url.Values){
		"unknown source":    func(f url.Values) { f.Set("n.h1.b.text", "entity.nope") },
		"body attribute":    func(f url.Values) { f.Set("n.h1.b.text", "entity.attributes."); f.Set("n.h1.bp.text", "page_doc") },
		"item outside loop": func(f url.Values) { f.Set("n.h1.b.text", "loop.item.title") },
		"empty path":        func(f url.Values) { f.Set("n.h1.b.text", "site.settings.") },
	} {
		f := r.form(t)
		set(f)
		rec := r.post(f, "save")
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), "There is a problem") {
			t.Errorf("%s: %d", name, rec.Code)
		}
		if h, _ := r.doc(t).Find("h1"); len(h.B) != 0 {
			t.Errorf("%s: stored %v", name, h.B)
		}
	}
	if len(r.rec.inputs) != 0 {
		t.Error("an invalid bind wrote")
	}
}

func TestOutlineInlineHTML(t *testing.T) {
	r := newOutlineRig(t)
	_, body := r.get(t, "?outline=1")
	has(t, body, `<textarea class="hm-field__control" id="`+propFieldID("h1", "html")+`" name="n.h1.p.html"`, "Overrides Text when set")

	// An untouched form leaves the plain heading plain.
	f := r.form(t)
	if rec := r.post(f, "save"); rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	if h, _ := r.doc(t).Find("h1"); h.P["html"] != nil {
		t.Errorf("empty html post wrote %v", h.P["html"])
	}

	f = r.form(t)
	f.Set("n.h1.p.html", `Hi <strong>you</strong><script>alert(1)</script>`)
	if rec := r.post(f, "save"); rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	if h, _ := r.doc(t).Find("h1"); h.P["html"] != "Hi <strong>you</strong>" {
		t.Errorf("html = %v, want it sanitised", h.P["html"])
	}
}

// js/builder/kindnames.js (FIX) is the builder's copy of builderKindNames; the two must say the same.
func TestOutlineKindNamesMirrorJS(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile(filepath.Join("..", "..", "web", "admin", "js", "builder", "kindnames.js"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range regexp.MustCompile(`(\w+): "([^"]+)"`).FindAllStringSubmatch(string(regexp.MustCompile(`(?m)^const FIX = \{.*\};`).Find(src)), -1) {
		got[m[1]] = m[2]
	}
	if len(got) != len(builderKindNames) {
		t.Fatalf("kindnames.js has %v, Go has %v", got, builderKindNames)
	}
	for k, v := range builderKindNames {
		if got[k] != v {
			t.Errorf("%s: js %q, go %q", k, got[k], v)
		}
	}
}

func TestOutlineSaysPlainKindNames(t *testing.T) {
	t.Parallel()

	info := map[string]builder.KindInfo{"tabs": {Kind: "tabs", Label: "Tabs"}}
	for kind, want := range map[string]string{"richtext": "Rich text", "accorditem": "Accordion item", "tabpanel": "Tab panel", "loopempty": "Empty state", "tabs": "Tabs", "container": "Container"} {
		if got := nodeLabel(&builder.Node{T: kind}, nil, info); got != want {
			t.Errorf("%s: %q, want %q", kind, got, want)
		}
	}
}
