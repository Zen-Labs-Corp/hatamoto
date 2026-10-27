package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/entity"
)

const postSchema = `{"type":"object","properties":{"content":{"type":"string"},"excerpt":{"type":"string","maxLength":300},"featured_image":{"type":"string"},"meta_title":{"type":"string","maxLength":70}}}`

const postBody = `<p>Go servers are easy to ship, test and reason about for a whole team.</p><h2>Why use Go?</h2><p>It compiles to one binary.</p>` +
	`<h2>Setup</h2><p>Install it. <a href="/docs">Docs</a> and <a href="https://go.dev/doc">the manual</a>.</p><img src="/m/a.png" alt="">` +
	`<div class="faq"><div class="faq-item"><h3 class="faq-q">Is it fast?</h3><div class="faq-a"><p>Yes.</p></div></div></div>`

type fakeCrawlers struct{ data agents.EntityActivityData }

func (f fakeCrawlers) EntityActivity(context.Context, uuid.UUID, []string, time.Time) (agents.EntityActivityData, error) {
	return f.data, nil
}

type blogRig struct {
	*contentRig
	postSch *entity.EntitySchema
	ent     *entity.Entity
	preview []byte
}

func newBlogRig(t *testing.T) *blogRig {
	c := newContentRig(t)
	r := &blogRig{contentRig: c}
	r.postSch = c.ents.addSchema(20, "Post", "post", postSchema, `{"url_pattern":"/blog/{slug}"}`, true)
	r.ent = c.ents.addEntity(80, r.postSch, "Go servers", "go-servers", entity.StatusDraft, `{"content":`+jsonString(postBody)+`,"excerpt":"Short","keep_me":1}`, t0)
	r.ent.BylinePersonID = ptr(fid(41))
	c.a.deps.BaseURL = func(*http.Request) string { return "https://example.test" }
	c.a.deps.Content.PublicPath = func(_ context.Context, e *entity.Entity) string { return "/blog/" + e.Slug }
	c.a.deps.Content.Preview = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.preview, _ = io.ReadAll(req.Body)
		_, _ = w.Write([]byte("<html><body>preview</body></html>"))
	})
	return r
}

func (r *blogRig) path() string { return "/admin/entities/" + r.ent.ID.String() + "/edit" }

func (r *blogRig) form(t *testing.T) url.Values {
	t.Helper()
	return parseForm(t, r.get(t, "owner-token", r.path(), ""))
}

func (r *blogRig) stored() *entity.Entity {
	e, _ := r.ents.GetEntity(context.Background(), r.ent.ID)
	return e
}

func (r *blogRig) attrs() map[string]any { return attrsMap(r.stored().Attributes) }

func TestBlogEditorRenders(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	body := r.get(t, "owner-token", r.path(), "blogedit")
	has(t, body,
		`data-hm-blog-editor`, `name="name"`, `name="content"`, `data-hm-editor="blog"`, `name="version" value="1"`,
		`<span>Save draft</span></button>`, `<span>Publish</span></button>`, `name="seo_keyword"`, `name="seo_llms"`, `name="featured_image"`, `name="excerpt"`,
		`name="slug"`, `data-hm-slug-auto="1"`, `Search and answer checks`, `hx-post="/admin/entities/`+r.ent.ID.String()+`/edit/analyze"`,
		`data-hm-preview="/admin/entities/`+r.ent.ID.String()+`/edit/preview"`, `Ada Writer`, `Categories and tags`, `Versions`, `data-hm-be-toolbar`)
	lacks(t, body, `id="be-crawlers"`, `<span>Update</span></button>`)
	// both sidebar panels are in the form, so a post saves them with or without JavaScript
	post, seo := strings.Index(body, `id="be-tabpanel-post"`), strings.Index(body, `id="be-tabpanel-seo"`)
	if post < 0 || seo < post {
		t.Fatalf("sidebar panels missing or out of order: post %d, seo %d", post, seo)
	}
	for name, at := range map[string]int{"seo_canonical": strings.Index(body, `name="seo_canonical"`), "seo_noindex": strings.Index(body, `name="seo_noindex"`)} {
		if at < post || at > seo {
			t.Errorf("%s is not in the Post panel", name)
		}
	}
	for _, name := range []string{"seo_keyword", "seo_title", "seo_description", "seo_llms"} {
		if at := strings.Index(body, `name="`+name+`"`); at < seo {
			t.Errorf("%s is not in the SEO and AEO panel", name)
		}
	}
	if at := strings.Index(body, `id="be-panel"`); at < seo {
		t.Error("the checks panel is not in the SEO and AEO panel")
	}
	// the plain textarea carries the body, so the form saves without JavaScript
	if f := parseForm(t, body); f.Get("content") != postBody || f.Get("name") != "Go servers" {
		t.Errorf("form lost the text: %q", f.Get("content"))
	}
}

func TestBlogEditorSaveKeepsWhatItDoesNotOwn(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	f := r.form(t)
	f.Set("name", "Go servers, shipped")
	f.Set("content", "<p>New body</p>")
	f.Set("excerpt", "")
	f.Set("featured_image", "/m/hero.png")
	f.Set("seo_keyword", "go servers")
	f.Set("seo_title", "Go servers, shipped")
	f.Set("seo_canonical", "https://example.test/original")
	f.Set("seo_noindex", "true")
	f.Del("seo_llms")
	f.Set("term", fid(33).String())
	f.Set("_op", "save")
	rec := r.post(r.path(), f)
	if rec.Code != 303 || rec.Header().Get("Location") != r.path() {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	got, e := r.attrs(), r.stored()
	seo, _ := got["seo"].(map[string]any)
	if e.Name != "Go servers, shipped" || got["content"] != "<p>New body</p>" || got["featured_image"] != "/m/hero.png" || got["excerpt"] != nil ||
		got["keep_me"] != float64(1) || got["no_index"] != true || got["canonical_url"] != "https://example.test/original" ||
		seo["focus_keyword"] != "go servers" || seo["include_in_llms_txt"] != false {
		t.Errorf("stored: %v %v", got, e.Name)
	}
	if ids := r.terms.attach[r.ent.ID]; len(ids) != 1 || ids[0] != fid(33) {
		t.Errorf("terms: %v", ids)
	}
	if e.BylinePersonID == nil || *e.BylinePersonID != fid(41) {
		t.Errorf("author: %v", e.BylinePersonID)
	}
	if vs, _ := r.ents.ListVersions(context.Background(), r.ent.ID, 10); len(vs) != 1 || vs[0].ChangeSummary == nil || *vs[0].ChangeSummary != "Updated in the blog editor" {
		t.Errorf("revision: %v", vs)
	}
	// llms.txt is on again when the box is ticked
	f = r.form(t)
	f.Set("seo_llms", "true")
	f.Set("_op", "save")
	r.post(r.path(), f)
	if seo, _ := r.attrs()["seo"].(map[string]any); seo["include_in_llms_txt"] != nil {
		t.Errorf("llms default not restored: %v", seo)
	}
}

func TestBlogEditorSaveIsVersionChecked(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	stale := r.form(t)
	other := r.form(t)
	other.Set("content", "<p>Theirs</p>")
	other.Set("_op", "save")
	if rec := r.post(r.path(), other); rec.Code != 303 {
		t.Fatal(rec.Code)
	}
	stale.Set("content", "<p>Mine</p>")
	stale.Set("_op", "save")
	rec := r.post(r.path(), stale)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale save: %d", rec.Code)
	}
	body := rec.Body.String()
	has(t, body, "saved by someone else", `name="version" value="2"`, "&lt;p&gt;Mine&lt;/p&gt;")
	if r.attrs()["content"] != "<p>Theirs</p>" {
		t.Error("a stale save overwrote the newer version")
	}
	// a structural error keeps the posted version, so a later save is still checked
	stale.Set("name", "")
	rec = r.post(r.path(), stale)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty title: %d", rec.Code)
	}
	has(t, rec.Body.String(), `name="version" value="1"`, "Enter a name")
}

func TestBlogEditorPublishScheduleAndUpdate(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	f := r.form(t)
	f.Set("_op", "publish")
	rec := r.post(r.path(), f)
	if rec.Code != 303 || r.stored().Status != entity.StatusPublished {
		t.Fatalf("publish: %d %s", rec.Code, r.stored().Status)
	}
	has(t, r.next(rec, r.path()), "Saved and published.", `<span>Update</span></button>`)
	// schedule from the status select
	r2 := newBlogRig(t)
	f = r2.form(t)
	f.Set("status", "scheduled")
	f.Set("schedule", time.Now().UTC().Add(48*time.Hour).Format(scheduleLayout))
	f.Set("_op", "save")
	if rec := r2.post(r2.path(), f); rec.Code != 303 || r2.stored().Status != entity.StatusScheduled {
		t.Fatalf("schedule: %d %s %s", rec.Code, r2.stored().Status, rec.Body.String())
	}
	f.Set("schedule", "")
	f.Set("version", "1")
	if rec := r.post(r.path(), f); rec.Code != 422 {
		t.Errorf("scheduled without a time: %d", rec.Code)
	}
	// Update on a published post keeps it published and says so
	f = r.form(t)
	f.Set("content", "<p>Edited</p>")
	f.Set("_op", "save")
	rec = r.post(r.path(), f)
	if rec.Code != 303 || r.stored().Status != entity.StatusPublished || r.attrs()["content"] != "<p>Edited</p>" {
		t.Fatalf("update: %d", rec.Code)
	}
	has(t, r.next(rec, r.path()), "Updated.")
}

func TestBlogAutosave(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	auto := func(f url.Values) (int, autosaveReply) {
		rec := r.post(r.path()+"/autosave", f)
		var out autosaveReply
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		return rec.Code, out
	}
	f := url.Values{"version": {"1"}, "name": {"Autosaved title"}, "content": {"<p>Typing…</p>"}, "excerpt": {"x"}, "status": {"published"}, "slug": {"hijack"}}
	code, out := auto(f)
	if code != 200 || !out.OK || out.Version != 2 {
		t.Fatalf("autosave: %d %+v", code, out)
	}
	e := r.stored()
	if e.Name != "Autosaved title" || r.attrs()["content"] != "<p>Typing…</p>" || e.Status != entity.StatusDraft || e.Slug != "go-servers" || r.attrs()["keep_me"] != float64(1) {
		t.Errorf("autosave wrote more than the text: %v %s %s", r.attrs(), e.Status, e.Slug)
	}
	// nothing changed: no new revision, same version
	if code, out = auto(url.Values{"version": {"2"}, "name": {"Autosaved title"}, "content": {"<p>Typing…</p>"}, "excerpt": {"x"}}); code != 200 || out.Version != 2 {
		t.Errorf("no-op autosave: %d %+v", code, out)
	}
	// a second tab with the old version conflicts and writes nothing
	code, out = auto(url.Values{"version": {"1"}, "content": {"<p>Old tab</p>"}})
	if code != http.StatusConflict || !out.Conflict || out.Version != 2 || r.attrs()["content"] != "<p>Typing…</p>" {
		t.Errorf("conflict: %d %+v", code, out)
	}
	// autosave never touches a published post
	r.ents.UpdateEntityStatus(context.Background(), r.ent.ID, nil, entity.StatusPublished)
	if code, out = auto(url.Values{"version": {"2"}, "content": {"<p>Live!</p>"}}); code != http.StatusConflict || out.Conflict || r.attrs()["content"] != "<p>Typing…</p>" {
		t.Errorf("published autosave: %d %+v", code, out)
	}
	// without a session
	if rec := r.do("POST", r.path()+"/autosave", "", url.Values{"version": {"1"}}, nil); rec.Code == 200 {
		t.Error("autosave without a session")
	}
}

func TestBlogAnalyzeAnswersTheFragment(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	f := r.form(t)
	f.Set("seo_keyword", "go servers")
	f.Set("seo_title", "Go servers")
	rec := r.do("POST", r.path()+"/analyze", "owner-token", f, map[string]string{"HX-Request": "true", "HX-Target": "be-panel"})
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<html") || strings.Contains(body, `class="hm-shell"`) {
		t.Error("the panel is a fragment")
	}
	lacks(t, body, "score", "Score")
	has(t, body, "checks pass", "Search result preview", "Keyword in title", "Question headings", "FAQPage", "Structured data:", "https://example.test/blog/go-servers")
}

func TestBlogPanelCountsMatchTheChecks(t *testing.T) {
	t.Parallel()

	for name, in := range map[string]seoInput{
		"weak": {Title: "Go", Slug: "go", Content: "<p>short</p>"},
		"good": {Title: "Go servers in production: a guide", MetaDescription: strings.Repeat("Ship Go servers with confidence. ", 4), Slug: "go-servers-guide", Keyword: "Go servers", InLLMs: true, Content: postBody + strings.Repeat("<p>More words to read here.</p>", 80)},
	} {
		p := buildPanel(in)
		all := append(append([]seoCheck{{Status: p.SERP.TitleStatus}, {Status: p.SERP.DescStatus}}, p.SEO...), p.Answer...)
		var pass, warn, fail int
		for _, c := range all {
			switch c.Status {
			case checkPass:
				pass++
			case checkWarn:
				warn++
			case checkFail:
				fail++
			}
		}
		if p.Pass != pass || p.Warn != warn || p.Fail != fail || p.Total != len(all) || p.ToFix() != warn+fail || p.Total == 0 {
			t.Errorf("%s: counts %d/%d/%d of %d, want %d/%d/%d of %d", name, p.Pass, p.Warn, p.Fail, p.Total, pass, warn, fail, len(all))
		}
	}
}

func TestBlogPanelFor(t *testing.T) {
	t.Parallel()

	p := buildPanel(seoInput{Title: "Go", Slug: "go", Content: "<p>short</p>", InLLMs: false})
	status := func(cs []seoCheck, id string) string {
		for _, c := range cs {
			if c.ID == id {
				return c.Status
			}
		}
		return "missing"
	}
	if p.SERP.TitleStatus != checkFail || p.SERP.DescStatus != checkFail || status(p.SEO, "keyword") != checkWarn || status(p.SEO, "length") != checkFail ||
		status(p.Answer, "question-headings") != checkWarn || status(p.Answer, "faq") != checkWarn || status(p.Answer, "llms") != checkWarn {
		t.Errorf("weak draft: %+v", p)
	}
	good := buildPanel(seoInput{
		Title: "Go servers in production: a guide", MetaDescription: strings.Repeat("Ship Go servers with confidence. ", 4), Slug: "go-servers-guide", Keyword: "Go servers", InLLMs: true, Published: true,
		BaseURL: "https://example.test", Path: "/blog/x",
		Content: postBody + strings.Repeat("<p>More words to read here.</p>", 80),
	})
	for id, want := range map[string]string{"keyword-title": checkPass, "keyword-intro": checkPass, "keyword-description": checkPass, "keyword-slug": checkPass, "headings": checkPass, "links-internal": checkPass, "links-external": checkPass, "image-alt": checkFail} {
		if got := status(good.SEO, id); got != want {
			t.Errorf("%s = %s, want %s", id, got, want)
		}
	}
	for id, want := range map[string]string{"question-headings": checkPass, "direct-answers": checkPass, "lead": checkPass, "faq": checkPass, "llms": checkPass} {
		if got := status(good.Answer, id); got != want {
			t.Errorf("answer %s = %s, want %s", id, got, want)
		}
	}
	if good.SERP.URL != "https://example.test/blog/x" || good.SERP.DescStatus != checkPass || good.FAQ != 1 {
		t.Errorf("serp: %+v", good.SERP)
	}
	long := buildPanel(seoInput{Content: "<h2>What is it?</h2><p>" + strings.Repeat("word ", 90) + "</p>"})
	if status(long.Answer, "direct-answers") != checkWarn {
		t.Error("a long paragraph under a question is not a direct answer")
	}
	faqOnly := buildPanel(seoInput{Content: `<p>Intro</p><div class="faq"><div class="faq-item"><h3 class="faq-q">Is it?</h3><div class="faq-a"><p>Yes.</p></div></div></div>`})
	if status(faqOnly.SEO, "headings") != checkPass || status(faqOnly.Answer, "question-headings") != checkWarn {
		t.Errorf("FAQ questions are not outline headings: %+v", faqOnly)
	}
	skip := buildPanel(seoInput{Content: "<h2>One</h2><h4>Skipped</h4>"})
	if status(skip.SEO, "headings") != checkWarn {
		t.Error("skipped heading level")
	}
}

func TestBlogCrawlerCardOnlyAfterPublishing(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	st := 200
	r.a.deps.Content.Crawlers = fakeCrawlers{agents.EntityActivityData{Exists: true, UserFetch7d: 3, UserFetch30d: 9, Last: map[string]agents.LastFetch{"gptbot": {At: t0, Status: &st}}}}
	lacks(t, r.get(t, "owner-token", r.path(), ""), `id="be-crawlers"`)
	r.ents.UpdateEntityStatus(context.Background(), r.ent.ID, nil, entity.StatusPublished)
	body := r.get(t, "owner-token", r.path(), "")
	has(t, body, `id="be-crawlers"`, "<strong>3</strong> fetches in 7 days", "<strong>9</strong> in 30 days")
}

func TestBlogPreviewStagesTheDraftForTheFrame(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	f := r.form(t)
	f.Set("name", "Unsaved title")
	f.Set("content", `<p>Unsaved</p><script>alert(1)</script>`)
	rec := r.do("POST", r.path()+"/preview", "owner-token", f, map[string]string{"X-HM-Stage": "1"})
	u := previewURL(t, rec)
	if !regexp.MustCompile(`/edit/preview/[A-Za-z0-9_-]{32}$`).MatchString(u) {
		t.Fatalf("ticket shape: %s", u)
	}
	iframe := map[string]string{"Sec-Fetch-Dest": "iframe"}
	frame := r.do("GET", u, "owner-token", nil, iframe)
	if frame.Code != 200 || !strings.Contains(frame.Body.String(), "preview") {
		t.Fatalf("frame: %d", frame.Code)
	}
	if rec := r.do("GET", u, "owner-token", nil, iframe); rec.Code != http.StatusNotFound {
		t.Errorf("a ticket loaded twice: %d", rec.Code)
	}
	var sent struct {
		Schema     string
		Attributes map[string]any
		Canvas     bool
	}
	if err := json.Unmarshal(r.preview, &sent); err != nil || sent.Schema != "post" || sent.Attributes["content"] != "<p>Unsaved</p>" /* the sanitiser dropped the script */ || sent.Attributes["name"] != "Unsaved title" || sent.Canvas {
		t.Errorf("preview request: %s", r.preview)
	}
	if strings.Contains(string(r.preview), "<script") || r.attrs()["content"] == "<p>Unsaved</p>" {
		t.Error("preview saved the draft")
	}
	// without JavaScript the button answers the rendered page itself
	if rec := r.do("POST", r.path()+"/preview", "owner-token", f, nil); !strings.Contains(rec.Body.String(), "preview") || rec.Code != 200 {
		t.Errorf("no-JS preview: %d", rec.Code)
	}
}

func TestBlogEditorIsForEditorsAndOwnersOnly(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	if rec := r.do("GET", r.path(), "", nil, nil); rec.Code == 200 {
		t.Error("an anonymous request reached the editor")
	}
}

func TestMediaUploadAnswersJSONForTheWritingView(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	n := len(r.media.files)
	rec := r.upload(t, "/admin/media/upload?json=1", map[string][]byte{"pasted.png": png1x1}, false)
	var out struct {
		Files  []uploadedFile
		Errors []string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 || len(out.Files) != 1 || out.Files[0].URL == "" || out.Files[0].Name != "pasted.png" || len(out.Errors) != 0 || len(r.media.files) != n+1 {
		t.Fatalf("json upload: %d %s", rec.Code, rec.Body.String())
	}
	rec = r.upload(t, "/admin/media/upload?json=1", map[string][]byte{"evil.png": []byte("<script>alert(1)</script>")}, false)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 422 || len(out.Errors) != 1 || !strings.Contains(out.Errors[0], "not allowed") {
		t.Errorf("rejected json upload: %d %s", rec.Code, rec.Body.String())
	}
	if rec = r.upload(t, "/admin/media/upload?json=1", nil, false); rec.Code != 422 || !strings.Contains(rec.Body.String(), "Choose at least one file") {
		t.Errorf("no files: %d %s", rec.Code, rec.Body.String())
	}
}

func TestBlogEditorSaveKeepsFieldsTheFormDidNotSend(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	rec := r.post(r.path(), url.Values{"name": {"Only the title"}, "version": {"1"}, "_op": {"save"}})
	if rec.Code != 303 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	got := r.attrs()
	if got["content"] != postBody || got["excerpt"] != "Short" || got["keep_me"] != float64(1) || r.stored().Name != "Only the title" {
		t.Errorf("a title-only post changed more: %v", got)
	}
}

func TestBlogEditorHasOneTopBarAndNoTabStrip(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	body := r.get(t, "owner-token", r.path(), "")
	for _, gone := range []string{"hm-tabs", "hm-pageheader", "Writing view"} {
		if strings.Contains(body, gone) {
			t.Errorf("editor still renders %q", gone)
		}
	}
	for _, want := range []string{
		`hm-editor-bar__crumbs`, `/admin/entities/` + r.ent.ID.String() + `/versions"`, `/admin/entities/` + r.ent.ID.String() + `"`,
		`form="hm-be"`, `popovertarget="be-more"`, `data-hm-be-status`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("top bar lacks %s", want)
		}
	}
	if strings.Contains(body, "View on site") {
		t.Error("a draft has no View on site")
	}
}

var submitRx = regexp.MustCompile(`<button[^>]*type="submit"[^>]*>`)

// firstSubmitter returns the tag of the form's default button: the first submit button
// in tree order whose form owner is the form (inside it, or pointing at it by form=).
func firstSubmitter(t *testing.T, body, id string) string {
	t.Helper()
	start := strings.Index(body, `id="`+id+`"`)
	end := start + strings.Index(body[start:], "</form>")
	for _, m := range submitRx.FindAllStringIndex(body, -1) {
		tag := body[m[0]:m[1]]
		inside := m[0] > start && m[0] < end
		if strings.Contains(tag, `form="`+id+`"`) || (inside && !strings.Contains(tag, ` form="`)) {
			return tag
		}
	}
	t.Fatalf("no submit button for form %s", id)
	return ""
}

// Pressing Enter in a sidebar field submits the form with its default button, which
// must save: a preview button ahead of it would open a tab and save nothing.
func TestBlogEditorEnterSavesNotPreviews(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	body := r.get(t, "owner-token", r.path(), "")
	if !strings.Contains(body, `formtarget="_blank"`) {
		t.Fatal("rig has no preview button to guard against")
	}
	if tag := firstSubmitter(t, body, "hm-be"); !strings.Contains(tag, `name="_op" value="save"`) {
		t.Errorf("default button of the blog form is %s, want _op=save", tag)
	}
}

// A draft's address follows its title on a full save too, until the writer edits the slug.
func TestBlogSaveKeepsTheSlugFollowingTheTitle(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	f := r.form(t)
	f.Set("name", "Why Go Wins")
	f.Set("slug", "untitled-1a2b3c4d") // what the form still shows after autosaves
	f.Set("slug_auto", "1")
	f.Set("_op", "save")
	if rec := r.post(r.path(), f); rec.Code != 303 {
		t.Fatalf("%d", rec.Code)
	}
	if got := r.stored().Slug; got != "why-go-wins" {
		t.Errorf("slug %q", got)
	}
	f.Set("name", "Another Title")
	f.Del("slug_auto")
	f.Set("slug", "why-go-wins")
	f.Set("version", "2")
	r.post(r.path(), f)
	if got := r.stored().Slug; got != "why-go-wins" {
		t.Errorf("a hand-set slug moved to %q", got)
	}
}

// Without JavaScript the form does not claim the slug follows the title, so a
// slug typed next to Publish is the one that goes live.
func TestBlogNoJSKeepsTheTypedSlug(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	r.ents.ents[r.ent.ID].Slug = "untitled-1a2b3c4d"
	f := r.form(t)
	if v, ok := f["slug_auto"]; !ok || v[0] != "" {
		t.Fatalf("rendered slug_auto = %v, want an empty field for JavaScript to switch on", v)
	}
	f.Set("name", "Hello")
	f.Set("slug", "my-custom-url")
	f.Set("_op", "publish")
	if rec := r.post(r.path(), f); rec.Code != 303 {
		t.Fatalf("%d", rec.Code)
	}
	if got := r.stored().Slug; got != "my-custom-url" {
		t.Errorf("slug %q, want the typed one", got)
	}
}

// A post that is or was live keeps its address when it is renamed, even if the client says "auto".
func TestBlogPublishedSlugNeverFollowsTheTitle(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	r.ents.ents[r.ent.ID].Status = entity.StatusPublished
	f := r.form(t)
	f.Set("name", "A New Title")
	f.Set("slug_auto", "1")
	f.Set("version", "2")
	f.Set("_op", "save")
	r.post(r.path(), f)
	if got := r.stored().Slug; got != "go-servers" {
		t.Errorf("a published post's slug moved to %q", got)
	}
	t0 := time.Now()
	e := r.stored()
	e.Status, e.PublishedAt = entity.StatusDraft, &t0
	f.Set("version", fmt.Sprint(e.Version))
	r.post(r.path(), f)
	if got := r.stored().Slug; got != "go-servers" {
		t.Errorf("an unpublished post's slug moved to %q", got)
	}
}

func TestBlogEditorAuthorPickerCreditsPeopleAndTheOrganisation(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	body := r.get(t, "owner-token", r.path(), "")
	has(t, body, `<option value="">No author</option>`, `<option value="org">Site organisation</option>`,
		`<option value="`+fid(41).String()+`" selected>Ada Writer</option>`)

	save := func(author string) {
		t.Helper()
		f := r.form(t)
		f.Set("author", author)
		f.Set("_op", "save")
		if rec := r.post(r.path(), f); rec.Code != 303 {
			t.Fatalf("save with author %q: %d %s", author, rec.Code, rec.Body.String())
		}
	}
	save("org")
	if e := r.stored(); !e.BylineOrg || e.BylinePersonID != nil {
		t.Errorf("after org: person=%v org=%v", e.BylinePersonID, e.BylineOrg)
	}
	has(t, r.get(t, "owner-token", r.path(), ""), `<option value="org" selected>Site organisation</option>`)

	save(fid(41).String())
	if e := r.stored(); e.BylineOrg || e.BylinePersonID == nil || *e.BylinePersonID != fid(41) {
		t.Errorf("after person: person=%v org=%v", e.BylinePersonID, e.BylineOrg)
	}
	save("")
	if e := r.stored(); e.BylineOrg || e.BylinePersonID != nil {
		t.Errorf("after no author: person=%v org=%v", e.BylinePersonID, e.BylineOrg)
	}

	// A new author named like the site organisation credits the organisation, not a person.
	f := r.form(t)
	f.Set("new_author", "acme site")
	f.Set("_op", "save")
	if rec := r.post(r.path(), f); rec.Code != 303 {
		t.Fatalf("new author: %d", rec.Code)
	}
	if e := r.stored(); !e.BylineOrg || e.BylinePersonID != nil || len(r.authors.list) != 1 {
		t.Errorf("organisation name: person=%v org=%v people=%d", e.BylinePersonID, e.BylineOrg, len(r.authors.list))
	}
}
