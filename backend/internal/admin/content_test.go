package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/person"
)

type contentRig struct {
	*rig
	ents    *fakeEntities
	terms   *fakeTerms
	media   *fakeMedia
	authors *fakeAuthors
	service *entity.EntitySchema
	page    *entity.EntitySchema
	recipe  *entity.EntitySchema
}

const serviceSchema = `{"type":"object","properties":{
 "summary":{"type":"string","title":"Summary","maxLength":120},
 "hero_image":{"type":"string","format":"uri","x-media":true,"title":"Hero image"},
 "featured":{"type":"boolean","title":"Featured"},
 "highlights":{"type":"array","title":"Highlights","items":{"type":"object","properties":{"label":{"type":"string","title":"Label"}}}},
 "meta_title":{"type":"string","title":"Meta title","maxLength":70}
},"required":["summary"]}`

func newContentRig(t *testing.T) *contentRig {
	t.Helper()
	c := &contentRig{ents: newFakeEntities(), terms: newFakeTerms(), media: newFakeMedia()}
	c.authors = &fakeAuthors{list: []*person.Person{{ID: fid(41), Name: "Ada Writer", Slug: "ada-writer"}}, org: "Acme Site"}
	c.service = c.ents.addSchema(1, "Service", "service", serviceSchema, `{"url_pattern":"/services/{path}"}`, false)
	c.page = c.ents.addSchema(2, "Page", "page", `{"type":"object","properties":{"body":{"type":"string","format":"richtext"}}}`, `{}`, true)
	c.recipe = c.ents.addSchema(3, "Recipe", "recipe", `{"type":"object","properties":{"minutes":{"type":"number","title":"Minutes"},"difficulty":{"type":"string","enum":["easy","hard"],"title":"Difficulty"}}}`, `{}`, false)
	a := c.ents.addEntity(11, c.service, "Alpha service", "alpha", entity.StatusPublished, `{"summary":"The first","featured":true}`, t0)
	a.BylinePersonID = ptr(fid(41))
	c.ents.addEntity(12, c.service, "Beta service", "beta", entity.StatusDraft, `{"summary":"The second"}`, t0.Add(time.Hour))
	c.ents.addEntity(13, c.service, "Gamma service", "gamma", entity.StatusArchived, `{"summary":"The third"}`, t0.Add(2*time.Hour))
	c.ents.addEntity(14, c.page, "About us", "about", entity.StatusPublished, `{"body":"<p>Hi</p>"}`, t0.Add(3*time.Hour))
	news := c.terms.add(31, "category", "News", "news", nil)
	c.terms.add(32, "category", "Press", "press", &news.ID)
	c.terms.add(33, "tag", "Go", "go", nil)
	c.terms.attach[fid(11)] = []uuid.UUID{fid(32), fid(33)}
	c.media.add(51, "team.png", "image/png", "The team")
	c.media.add(52, "logo.png", "image/png", "")
	c.media.add(53, "brochure.pdf", "application/pdf", "")
	c.media.usage[fid(51)] = []media.Usage{{Kind: "entity", ID: fid(11), Name: "Alpha service", SchemaSlug: "service"}}
	c.rig = newRig(t, func(d *Deps) {
		if os.Getenv("DUMP") != "" {
			d.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
		}
		d.Content = ContentDeps{Entities: c.ents, Terms: c.terms, Media: c.media, Authors: c.authors,
			PublicPath: func(_ context.Context, e *entity.Entity) string { return "/services/" + e.Slug }}
	})
	return c
}

// page fetches path as token and checks it: status, accessibility, and the
// golden (when golden is non-empty). It returns the body.
func (c *contentRig) get(t *testing.T, token, path, golden string) string {
	t.Helper()
	rec := c.do("GET", path, token, nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET %s = %d\n%s", path, rec.Code, rec.Body.String())
	}
	body := normalizePage(rec.Body.String())
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("%s a11y: %s", path, strings.Join(issues, "; "))
	}
	if golden != "" {
		compareGolden(t, "pages/"+golden+".html", body)
	}
	return body
}

// next fetches path as the owner, carrying the flash cookie that rec set, and
// returns the body: what the person sees after being redirected.
func (c *contentRig) next(rec *httptest.ResponseRecorder, path string) string {
	cookies := []string{"hatamoto_session=owner-token"}
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "hm_flash" {
			cookies = append(cookies, ck.Name+"="+ck.Value)
		}
	}
	return c.do("GET", path, "owner-token", nil, map[string]string{"Cookie": strings.Join(cookies, "; ")}).Body.String()
}

func (c *contentRig) post(path string, form url.Values) *httptest.ResponseRecorder {
	return c.do("POST", path, "owner-token", form, nil)
}

func has(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q (page is %d bytes; set DUMP=1 to print it)", w, len(body))
			if os.Getenv("DUMP") != "" {
				t.Log(body)
			}
		}
	}
}

func lacks(t *testing.T, body string, nots ...string) {
	t.Helper()
	for _, w := range nots {
		if strings.Contains(body, w) {
			t.Errorf("unexpected %q", w)
		}
	}
}

// The list needs two queries however many types and rows there are: one page
// of entities and one grouped count behind the type tabs and the status select.
func TestEntityListQueriesAreBounded(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	body := c.get(t, "owner-token", "/admin/entities", "")
	if c.ents.lists != 1 || c.ents.counts != 1 {
		t.Errorf("list queries = %d, count queries = %d, want 1 and 1", c.ents.lists, c.ents.counts)
	}
	has(t, body, ">Title</a>", `placeholder="Search titles and URLs"`, `aria-label="Status"`, `class="hm-tabs__count"`, ">All <", "Published ", `<span class="hm-table__sub">/services/alpha</span>`)
	lacks(t, body, "Search entities")
}

// TestFilterbarPopoverFieldsAreNotLive: a live request swaps the card, which
// closes the popover and drops focus, so only search and the primary selects
// may trigger one; popover fields apply through the form's submit (Apply, Enter).
func TestFilterbarPopoverFieldsAreNotLive(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	body := c.get(t, "owner-token", "/admin/entities", "")
	i := strings.Index(body, `hx-trigger="`)
	if i < 0 {
		t.Fatal("filterbar has no hx-trigger")
	}
	trig := body[i+len(`hx-trigger="`):]
	trig = trig[:strings.Index(trig, `"`)]
	if !strings.Contains(trig, "submit") || !strings.Contains(trig, "from:#") {
		t.Errorf("trigger must be explicit and include submit: %q", trig)
	}
	for _, part := range strings.Split(trig, ", ") {
		if strings.HasPrefix(part, "change") && (!strings.Contains(part, "from:#") || strings.Contains(part, "-f")) {
			t.Errorf("trigger %q listens to more than search and primary selects", part)
		}
	}
	has(t, body, `<button type="submit" class="hm-btn hm-btn--primary"><span>Apply</span></button>`)
}

func TestEntityListGoldenFiltersSortAndPaging(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	body := c.get(t, "owner-token", "/admin/entities", "entities-list")
	has(t, body, "Alpha service", "Beta service", "About us", `aria-label="Select all rows in Entities"`, "Ada Writer", `/services/alpha`)
	// the specialised editor stays where it is until v1-12
	has(t, body, `href="/admin/entities/`+fid(14).String()+`/edit"`, `href="/admin/entities/`+fid(11).String()+`"`)

	body = c.get(t, "owner-token", "/admin/entities?status=draft", "")
	has(t, body, "Beta service")
	lacks(t, body, "Alpha service")

	body = c.get(t, "owner-token", "/admin/entities?type=page", "")
	has(t, body, "About us")
	lacks(t, body, "Beta service")

	body = c.get(t, "owner-token", "/admin/entities?q=gam", "")
	has(t, body, "Gamma service")
	lacks(t, body, "Beta service")

	c.get(t, "owner-token", "/admin/entities?author="+fid(41).String(), "")
	if c.ents.lastList.PersonID == nil || *c.ents.lastList.PersonID != fid(41) {
		t.Errorf("author filter not applied: %+v", c.ents.lastList)
	}
	c.get(t, "owner-token", "/admin/entities?term="+fid(31).String()+"&from=2026-03-01&to=2026-03-02&sort=name&dir=asc", "")
	f := c.ents.lastList
	if f.TermID == nil || *f.TermID != fid(31) || f.UpdatedFrom == nil || f.UpdatedBefore == nil || f.Sort != "name" || !f.Asc {
		t.Errorf("filters not applied: %+v", f)
	}
	if got := f.UpdatedBefore.Sub(*f.UpdatedFrom); got != 48*time.Hour {
		t.Errorf("'to' day should be inclusive, window = %v", got)
	}
	// a dated filter is one removable chip that drops only its own param
	body = c.get(t, "owner-token", "/admin/entities?status=published&from=2026-03-01", "")
	has(t, body, `class="hm-btn-count"`, `Updated from 1 Mar 2026`, `aria-label="Remove filter: Updated from 1 Mar 2026"`, `href="/admin/entities?status=published"`, `name="from" value="2026-03-01"`, `<button type="submit" class="hm-btn hm-btn--primary"><span>Apply</span></button>`)
	lacks(t, c.get(t, "owner-token", "/admin/entities", ""), `hm-filter-chip`)
	// an unknown status or type is ignored, not an error
	c.get(t, "owner-token", "/admin/entities?status=bogus&type=nope&sort=drop+table", "")

	// pagination: 25 entities, 20 per page
	for i := 0; i < 25; i++ {
		c.ents.addEntity(200+i, c.recipe, fmt.Sprintf("Recipe %02d", i), fmt.Sprintf("recipe-%02d", i), entity.StatusDraft, `{}`, t0.Add(10*time.Hour))
	}
	body = c.get(t, "owner-token", "/admin/entities?type=recipe&page=2", "")
	has(t, body, "Showing 21–25 of 25")
	// fragment request for the table only
	rec := c.do("GET", "/admin/entities?q=alpha", "owner-token", nil, map[string]string{"HX-Request": "true", "HX-Target": "entities"})
	lacks(t, rec.Body.String(), "<!doctype")
	has(t, rec.Body.String(), `id="entities"`)
}

func TestEntityBulkActions(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	rec := c.post("/admin/entities/bulk", url.Values{"action": {"publish"}, "id": {fid(12).String(), fid(13).String()}, "back": {"/admin/entities?status=draft"}})
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/entities?status=draft" {
		t.Fatalf("bulk publish: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if c.ents.ents[fid(12)].Status != entity.StatusPublished || c.ents.ents[fid(13)].Status != entity.StatusPublished {
		t.Error("status not changed")
	}
	// nothing selected
	if rec := c.post("/admin/entities/bulk", url.Values{"action": {"publish"}}); rec.Code != 303 {
		t.Errorf("empty bulk: %d", rec.Code)
	}
	// open redirect through back is ignored
	rec = c.post("/admin/entities/bulk", url.Values{"action": {"archive"}, "id": {fid(11).String()}, "back": {"//evil.example"}})
	if rec.Header().Get("Location") != "/admin/entities" {
		t.Errorf("back not sanitised: %s", rec.Header().Get("Location"))
	}
	// delete asks first (no JavaScript path), then deletes
	rec = c.post("/admin/entities/bulk", url.Values{"action": {"delete"}, "id": {fid(12).String(), fid(13).String()}})
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Delete 2 entities?") || !strings.Contains(body, `name="confirmed" value="1"`) {
		t.Fatalf("delete confirmation: %d\n%s", rec.Code, body)
	}
	if len(c.ents.ents) != 4 {
		t.Fatal("deleted before confirming")
	}
	if issues := a11y.Check(normalizePage(body), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	rec = c.post("/admin/entities/bulk", url.Values{"action": {"delete"}, "confirmed": {"1"}, "id": {fid(12).String(), fid(13).String()}})
	if rec.Code != 303 || len(c.ents.ents) != 2 {
		t.Fatalf("confirmed delete: %d, %d left", rec.Code, len(c.ents.ents))
	}
	// the list offers the bulk buttons, with the dialog text for delete
	list := c.get(t, "owner-token", "/admin/entities", "")
	has(t, list, `data-hm-bulk-confirm="Delete {n} selected entities?`, `value="publish"`, `value="archive"`)
	// the dialog needs a way for a parent with children to say why it failed
	c.ents.ents[fid(14)].ParentID = ptr(fid(11))
	rec = c.post("/admin/entities/bulk", url.Values{"action": {"delete"}, "confirmed": {"1"}, "id": {fid(11).String()}})
	if rec.Code != 303 || c.ents.ents[fid(11)] == nil {
		t.Fatalf("parent with children must survive: %d", rec.Code)
	}
	if fl := c.next(rec, "/admin/entities"); !strings.Contains(fl, "child entities") {
		t.Error("flash with the reason is missing")
	}
	// anonymous and CSRF
	if rec := c.do("POST", "/admin/entities/bulk", "", url.Values{"action": {"delete"}}, nil); rec.Code != 303 {
		t.Errorf("anonymous: %d", rec.Code)
	}
}

func TestEntityRowStatusAndDelete(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	rec := c.post("/admin/entities/"+fid(12).String()+"/status", url.Values{"status": {"published"}, "back": {"/admin/entities?q=beta"}})
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/entities?q=beta" || c.ents.ents[fid(12)].Status != entity.StatusPublished {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := c.post("/admin/entities/"+fid(12).String()+"/status", url.Values{"status": {"scheduled"}}); rec.Code != 400 {
		t.Errorf("scheduled via the row action: %d", rec.Code)
	}
	body := c.get(t, "owner-token", "/admin/entities/"+fid(12).String()+"/delete", "")
	has(t, body, "Delete “Beta service”?")
	if len(c.ents.ents) != 4 {
		t.Fatal("deleted by the confirmation page")
	}
	if rec := c.post("/admin/entities/"+fid(12).String()+"/delete", nil); rec.Code != 303 || c.ents.ents[fid(12)] != nil {
		t.Errorf("delete: %d", rec.Code)
	}
	if rec := c.do("GET", "/admin/entities/"+fid(99).String(), "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("missing entity: %d", rec.Code)
	}
	if rec := c.do("GET", "/admin/entities/not-a-uuid", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("bad id: %d", rec.Code)
	}
}

func TestEntityEditGoldenSaveAndSEO(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := fid(11).String()
	body := c.get(t, "owner-token", "/admin/entities/"+id, "entity-edit")
	has(t, body, `value="Alpha service"`, `data-hm-media-picker="sf-attr-hero_image"`, `data-hm-seo-tabs`, `name="parent"`, `name="author"`,
		`name="term" value="`+fid(32).String()+`" checked`, `name="new_term_name"`)
	// SEO keys are edited in the panel, not duplicated by the attribute form
	lacks(t, body, `name="attr.meta_title"`)

	form := url.Values{
		"name": {"Alpha renamed"}, "slug": {"alpha-renamed"}, "status": {"published"}, "attr.summary": {"New summary"},
		"seo_title": {"Alpha | Acme"}, "seo_description": {"About alpha"}, "seo_canonical": {"https://acme.example/alpha"}, "seo_og_image": {"/media/x/file-51.png"}, "seo_noindex": {"true"},
		"author": {fid(41).String()}, "_terms": {"1"}, "term": {fid(31).String()}, "new_term_name": {"Fresh"}, "new_term_kind": {"tag"},
		"attr.highlights.0.label": {"Fast"}, "_present.attr.highlights": {"1"},
	}
	rec := c.post("/admin/entities/"+id, form)
	if rec.Code != 303 {
		t.Fatalf("save: %d\n%s", rec.Code, rec.Body.String())
	}
	e := c.ents.ents[fid(11)]
	if e.Name != "Alpha renamed" || e.Slug != "alpha-renamed" || e.Version != 2 {
		t.Errorf("entity after save: %+v", e)
	}
	var attrs map[string]any
	_ = json.Unmarshal(e.Attributes, &attrs)
	if attrs["summary"] != "New summary" || attrs["meta_title"] != "Alpha | Acme" || attrs["meta_description"] != "About alpha" ||
		attrs["canonical_url"] != "https://acme.example/alpha" || attrs["og_image"] != "/media/x/file-51.png" || attrs["no_index"] != true {
		t.Errorf("attributes: %v", attrs)
	}
	if hl, _ := attrs["highlights"].([]any); len(hl) != 1 {
		t.Errorf("repeater: %v", attrs["highlights"])
	}
	if got := len(c.terms.attach[fid(11)]); got != 2 {
		t.Errorf("terms attached: %d (want picked + the new one)", got)
	}
	foundNew := false
	for _, tm := range c.terms.terms {
		if tm.Name == "Fresh" && tm.Kind == "tag" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Error("inline term not created")
	}
	if len(c.ents.versions[fid(11)]) != 1 {
		t.Errorf("versions: %d", len(c.ents.versions[fid(11)]))
	}
	// clearing the SEO panel removes the keys
	rec = c.post("/admin/entities/"+id, url.Values{"name": {"Alpha renamed"}, "slug": {"alpha-renamed"}, "status": {"published"}, "attr.summary": {"New summary"}, "attr.highlights.0.label": {"Fast"}, "_present.attr.highlights": {"1"}})
	if rec.Code != 303 {
		t.Fatalf("second save: %d\n%s", rec.Code, rec.Body.String())
	}
	attrs = nil
	_ = json.Unmarshal(c.ents.ents[fid(11)].Attributes, &attrs)
	if _, has := attrs["meta_title"]; has || attrs["no_index"] != nil {
		t.Errorf("SEO not cleared: %v", attrs)
	}
}

func TestEntitySaveValidationErrors(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := fid(11).String()
	rec := c.post("/admin/entities/"+id, url.Values{"name": {""}, "slug": {"x"}, "status": {"published"}, "attr.summary": {strings.Repeat("a", 200)}, "seo_canonical": {"not a url"}, "seo_title": {strings.Repeat("t", 90)}})
	body := rec.Body.String()
	if rec.Code != 422 {
		t.Fatalf("%d", rec.Code)
	}
	has(t, body, "Enter a name", "Use a full address", `id="hm-form-errors"`, "problems with this form")
	if issues := a11y.Check(normalizePage(body), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y of the error page: %v", issues)
	}
	if c.ents.ents[fid(11)].Name != "Alpha service" {
		t.Error("saved despite errors")
	}
	// the typed values come back
	has(t, body, `value="x"`, "not a url")
	// slug taken by another entity
	rec = c.post("/admin/entities/"+id, url.Values{"name": {"Alpha"}, "slug": {"beta"}, "status": {"draft"}, "attr.summary": {"ok"}})
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "already used") {
		t.Errorf("slug conflict: %d", rec.Code)
	}
	// scheduled needs a time; a past time is refused with a message, content still saved
	rec = c.post("/admin/entities/"+id, url.Values{"name": {"Alpha"}, "slug": {"alpha"}, "status": {"scheduled"}, "attr.summary": {"ok"}})
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Pick the date and time") {
		t.Errorf("schedule required: %d", rec.Code)
	}
}

func TestEntitySchedule(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	c.rig.a.deps.Site.(*fakeSite).cfg.Timezone = "Australia/Sydney"
	id := fid(12).String()
	future := time.Now().Add(72 * time.Hour).In(time.FixedZone("x", 0))
	val := future.Format(scheduleLayout)
	rec := c.post("/admin/entities/"+id, url.Values{"name": {"Beta service"}, "slug": {"beta"}, "status": {"scheduled"}, "schedule": {val}, "attr.summary": {"The second"}})
	if rec.Code != 303 {
		t.Fatalf("%d\n%s", rec.Code, rec.Body.String())
	}
	e := c.ents.ents[fid(12)]
	if e.Status != entity.StatusScheduled || e.PublishedAt == nil {
		t.Fatalf("not scheduled: %+v", e)
	}
	// typed in the site's zone (Sydney), stored as an instant
	sydney, _ := time.LoadLocation("Australia/Sydney")
	want, _ := time.ParseInLocation(scheduleLayout, val, sydney)
	if !e.PublishedAt.Equal(want) {
		t.Errorf("published_at = %v, want %v", e.PublishedAt, want)
	}
	body := c.get(t, "owner-token", "/admin/entities/"+id, "")
	has(t, body, `value="`+val+`"`, "Australia/Sydney", `value="scheduled" selected`)
	// the list shows it as scheduled and offers no one-click publish
	list := c.get(t, "owner-token", "/admin/entities?status=scheduled", "")
	has(t, list, "Beta service", "Scheduled")
	// a time in the past
	past := time.Now().Add(-48 * time.Hour).Format(scheduleLayout)
	c.post("/admin/entities/"+id, url.Values{"name": {"Beta service"}, "slug": {"beta"}, "status": {"scheduled"}, "schedule": {past}, "attr.summary": {"The second"}})
	if c.ents.ents[fid(12)].PublishedAt.Before(time.Now()) {
		t.Error("a past time replaced the schedule")
	}
}

func TestEntityParentAuthorAndNew(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	// the service schema's pattern uses {path}: the parent picker shows
	body := c.get(t, "owner-token", "/admin/entities/new?type=service", "entity-new")
	has(t, body, `name="parent"`, "No parent (top level)", "Alpha service (/alpha)", `name="type" value="service"`)
	// recipes are not nested
	body = c.get(t, "owner-token", "/admin/entities/new?type=recipe", "")
	lacks(t, body, `name="parent"`)

	rec := c.post("/admin/entities", url.Values{"type": {"service"}, "name": {"Child service"}, "slug": {""}, "status": {"published"}, "attr.summary": {"Under alpha"},
		"parent": {fid(11).String()}, "new_author": {"Grace Hopper"}, "author": {""}})
	if rec.Code != 303 {
		t.Fatalf("create: %d\n%s", rec.Code, rec.Body.String())
	}
	var created *entity.Entity
	for _, e := range c.ents.ents {
		if e.Name == "Child service" {
			created = e
		}
	}
	if created == nil || created.ParentID == nil || *created.ParentID != fid(11) || created.BylinePersonID == nil || created.Status != entity.StatusPublished {
		t.Fatalf("created: %+v", created)
	}
	if len(c.authors.list) != 2 {
		t.Error("new author not created")
	}
	if rec.Header().Get("Location") != "/admin/entities/"+created.ID.String() {
		t.Errorf("redirect: %s", rec.Header().Get("Location"))
	}
	// the chooser lists the types; an unknown type is rejected
	body = c.get(t, "owner-token", "/admin/entities/new", "entity-new-choose")
	has(t, body, "Service", "Recipe", `class="hm-choice__icon"`)
	if rec := c.post("/admin/entities", url.Values{"type": {"nope"}, "name": {"x"}}); rec.Code != 400 {
		t.Errorf("unknown type: %d", rec.Code)
	}
	// a page opens the builder; its tests are in builder_*_test.go
	if rec := c.do("GET", "/admin/entities/"+fid(14).String()+"/edit", "owner-token", nil, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "data-hm-builder") {
		t.Errorf("page editor: %d", rec.Code)
	}
	if rec := c.do("GET", "/admin/entities/"+fid(11).String()+"/edit", "owner-token", nil, nil); rec.Code != 303 || rec.Header().Get("Location") != "/admin/entities/"+fid(11).String() {
		t.Errorf("generic types edit in Go: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestStructuralButtonsReRenderWithoutSaving(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := fid(11).String()
	rec := c.post("/admin/entities/"+id, url.Values{"name": {"Changed but unsaved"}, "slug": {"alpha"}, "status": {"published"}, "attr.summary": {"S"}, "_add.attr.highlights": {"1"}, "_present.attr.highlights": {"1"}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `value="Changed but unsaved"`) {
		t.Fatalf("%d", rec.Code)
	}
	if c.ents.ents[fid(11)].Name != "Alpha service" {
		t.Error("saved on Add item")
	}
}

// A page nobody has saved twice has nothing to compare: no Compare form, no history strip.
func TestVersionsWithoutEarlierVersions(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	body := c.get(t, "owner-token", "/admin/entities/"+fid(11).String()+"/versions", "")
	has(t, body, ">Current<")
	lacks(t, body, "Compare versions", "Versions, oldest first")
}

func TestVersionsCompareAndRestore(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := fid(11).String()
	for i, s := range []string{"Second summary", "Third summary"} {
		c.post("/admin/entities/"+id, url.Values{"name": {"Alpha service"}, "slug": {"alpha"}, "status": {"published"}, "attr.summary": {s}, "attr.featured": {"true"}, "_present.attr.featured": {"true"}})
		_ = i
	}
	if c.ents.ents[fid(11)].Version != 3 {
		t.Fatalf("version %d", c.ents.ents[fid(11)].Version)
	}
	body := c.get(t, "owner-token", "/admin/entities/"+id+"/versions", "entity-versions")
	has(t, body, "v3", "v2", "v1", ">Current<", ">Earlier<", "Compare versions", "Versions, oldest first", "Back to details", `data-hm-post="/admin/entities/`+id+`/versions/1/restore"`)
	body = c.get(t, "owner-token", "/admin/entities/"+id+"/versions/compare?from=1&to=0", "entity-compare")
	has(t, body, "summary", "The first", "Third summary", "Changed", `<code>summary</code>`, "Comparing v1 → v3 (current)", "Restore this version",
		`data-hm-post="/admin/entities/`+id+`/versions/1/restore"`, "hm-badge--warning", "Back to versions")
	body = c.get(t, "owner-token", "/admin/entities/"+id+"/versions/1", "entity-version")
	has(t, body, "The first", "<code>summary</code>", "Compare with current")
	if rec := c.do("GET", "/admin/entities/"+id+"/versions/42", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("view missing version: %d", rec.Code)
	}
	// identical versions
	body = c.get(t, "owner-token", "/admin/entities/"+id+"/versions/compare?from=0&to=3", "")
	has(t, body, "No differences")
	if rec := c.do("GET", "/admin/entities/"+id+"/versions/compare?from=77&to=0", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("missing version: %d", rec.Code)
	}
	// restore asks first
	body = c.get(t, "owner-token", "/admin/entities/"+id+"/versions/1/restore", "")
	has(t, body, "Restore version 1")
	if c.ents.ents[fid(11)].Version != 3 {
		t.Fatal("restored by the confirmation page")
	}
	rec := c.post("/admin/entities/"+id+"/versions/1/restore", nil)
	if rec.Code != 303 {
		t.Fatalf("restore: %d", rec.Code)
	}
	var attrs map[string]any
	_ = json.Unmarshal(c.ents.ents[fid(11)].Attributes, &attrs)
	if attrs["summary"] != "The first" || c.ents.ents[fid(11)].Version != 4 {
		t.Errorf("after restore: %v v%d", attrs, c.ents.ents[fid(11)].Version)
	}
	if rec := c.post("/admin/entities/"+id+"/versions/99/restore", nil); rec.Code != 404 {
		t.Errorf("restore missing: %d", rec.Code)
	}
}

func TestDiffAttributes(t *testing.T) {
	t.Parallel()

	rows, same := diffAttributes(json.RawMessage(`{"a":"1","b":{"c":[1,2]},"gone":"x"}`), json.RawMessage(`{"a":"1","b":{"c":[1,3,4]},"new":true}`))
	got := map[string]string{}
	for _, r := range rows {
		got[r.Path] = r.Kind
	}
	want := map[string]string{"b.c.1": "changed", "b.c.2": "added", "gone": "removed", "new": "added"}
	if fmt.Sprint(got) != fmt.Sprint(want) || same != 2 {
		t.Errorf("rows %v same %d", got, same)
	}
}

func TestSchemaScreens(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	body := c.get(t, "owner-token", "/admin/settings/types", "schemas-list")
	has(t, body, "Service", "/services/{path}", "page (built in)")
	id := fid(1).String()
	body = c.get(t, "owner-token", "/admin/settings/types/"+id, "schema-edit")
	has(t, body, `name="f.r0.name" value="summary"`, `data-hm-field-list`, "Move up", "preview-pattern", `data-hm-kind`)
	// a built-in type can be viewed, not saved
	body = c.get(t, "owner-token", "/admin/settings/types/"+fid(2).String(), "schema-builtin")
	has(t, body, "built-in entity type")
	lacks(t, body, "Add field")
	if rec := c.post("/admin/settings/types/"+fid(2).String(), url.Values{"name": {"x"}}); rec.Code != 403 {
		t.Errorf("builtin save: %d", rec.Code)
	}
	body = c.get(t, "owner-token", "/admin/settings/types/new", "schema-new")
	has(t, body, "Create from JSON", "data-hm-slug-from")
}

// schemaPost is the form of the service schema's editor with changes applied.
func schemaPost(c *contentRig, change func(url.Values)) url.Values {
	rows := fieldRows(c.service.Schema)
	f := url.Values{"name": {"Service"}, "description": {""}, "url_pattern": {"/services/{path}"}}
	for _, r := range rows {
		f.Add("field", r.Key)
		f.Set("f."+r.Key+".name", r.Name)
		f.Set("f."+r.Key+".kind", r.Kind)
		f.Set("f."+r.Key+".title", r.Title)
		f.Set("f."+r.Key+".raw", r.Raw)
		f.Set("f."+r.Key+".options", r.Options)
		if r.Required {
			f.Set("f."+r.Key+".required", "true")
		}
	}
	if change != nil {
		change(f)
	}
	return f
}

func TestSchemaEditAddReorderAndSave(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := fid(1).String()
	before := fieldRows(c.service.Schema)
	// Add field: re-renders with a new row, nothing saved yet
	rec := c.post("/admin/settings/types/"+id, schemaPost(c, func(f url.Values) { f.Set("op", "add"); f.Set("add_kind", "select") }))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `name="f.r5.name"`) {
		t.Fatalf("add: %d\n%s", rec.Code, rec.Body.String())
	}
	if len(fieldRows(c.service.Schema)) != len(before) {
		t.Fatal("saved on Add field")
	}
	// Move down / up and remove
	rec = c.post("/admin/settings/types/"+id, schemaPost(c, func(f url.Values) { f.Set("op", "down:r0") }))
	body := rec.Body.String()
	if i, j := strings.Index(body, `name="f.r1.name"`), strings.Index(body, `name="f.r0.name"`); rec.Code != 200 || i < 0 || j < i {
		t.Errorf("move down did not reorder: %d", rec.Code)
	}
	rec = c.post("/admin/settings/types/"+id, schemaPost(c, func(f url.Values) { f.Set("op", "remove:r1") }))
	if strings.Contains(rec.Body.String(), `name="f.r1.name"`) {
		t.Error("remove did not remove")
	}
	// Save: drag-reordered (field order = posted order), a new select field, URL pattern with date tokens
	rec = c.post("/admin/settings/types/"+id, schemaPost(c, func(f url.Values) {
		f["field"] = []string{"r2", "r0", "r1", "r3", "r4", "r5"}
		f.Set("f.r5.name", "level")
		f.Set("f.r5.kind", "select")
		f.Set("f.r5.title", "Level")
		f.Set("f.r5.options", "beginner\nadvanced\n\nbeginner")
		f.Set("f.r5.required", "true")
		f.Set("url_pattern", "/{year}/{month}/{slug}")
		f.Set("schema_org_type", "Service")
		f.Set("sitemap_exclude", "true")
		f.Set("priority", "0.4")
		f.Set("archive_path", "/services")
	}))
	if rec.Code != 303 {
		t.Fatalf("save: %d\n%s", rec.Code, rec.Body.String())
	}
	rows := fieldRows(c.service.Schema)
	if len(rows) != 6 || rows[0].Name != "featured" || rows[5].Name != "level" || rows[5].Kind != "select" || rows[5].Options != "beginner\nadvanced" {
		t.Errorf("rows after save: %+v", rows)
	}
	var schema struct {
		Required   []string
		Properties map[string]map[string]any
	}
	_ = json.Unmarshal(c.service.Schema, &schema)
	if len(schema.Required) != 2 {
		t.Errorf("required: %v", schema.Required)
	}
	if schema.Properties["hero_image"]["x-media"] != true || schema.Properties["summary"]["maxLength"] == nil {
		t.Errorf("unknown keys lost: %v", schema.Properties)
	}
	var st entity.SchemaURLSettings
	_ = json.Unmarshal(c.service.Settings, &st)
	if st.URLPattern != "/{year}/{month}/{slug}" || st.SchemaOrgType != "Service" || !st.Sitemap.Exclude || *st.Sitemap.Priority != 0.4 || st.ArchivePath != "/services" {
		t.Errorf("settings: %s", c.service.Settings)
	}
}

func TestSchemaValidation(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := fid(1).String()
	cases := map[string]struct {
		change func(url.Values)
		want   string
	}{
		"pattern without slug": {func(f url.Values) { f.Set("url_pattern", "/blog/{year}") }, "{slug}"},
		"pattern no slash":     {func(f url.Values) { f.Set("url_pattern", "blog/{slug}") }, "Must start with"},
		"bad type":             {func(f url.Values) { f.Set("schema_org_type", "Bogus") }, "Choose one of the listed types"},
		"bad priority":         {func(f url.Values) { f.Set("priority", "7") }, "0 to 1"},
		"archive path":         {func(f url.Values) { f.Set("archive_path", "blog") }, "Start with a slash"},
		"duplicate name":       {func(f url.Values) { f.Set("f.r1.name", "summary") }, "share a name"},
		"bad name":             {func(f url.Values) { f.Set("f.r1.name", "1 bad name") }, "starting with a letter"},
		"empty choices":        {func(f url.Values) { f.Set("f.r1.kind", "select"); f.Set("f.r1.options", " ") }, "at least one choice"},
		"no name":              {func(f url.Values) { f.Set("name", "") }, "Enter a name"},
	}
	for name, tc := range cases {
		rec := c.post("/admin/settings/types/"+id, schemaPost(c, tc.change))
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s: %d, want message %q", name, rec.Code, tc.want)
			continue
		}
		if name == "pattern without slug" {
			if issues := a11y.Check(normalizePage(rec.Body.String()), a11y.Options{Page: true}); len(issues) > 0 {
				t.Errorf("a11y of the error page: %v", issues)
			}
		}
	}
	if !strings.Contains(string(c.service.Schema), `"summary"`) || strings.Contains(string(c.service.Settings), "blog") {
		t.Errorf("invalid input was stored: %s %s", c.service.Schema, c.service.Settings)
	}
}

func TestSchemaJSONCreateAndDelete(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	rec := c.post("/admin/settings/types", url.Values{"name": {"Person"}, "slug": {"person"}, "op": {"save_json"}, "schema_json": {`{"type":"object","properties":{"nick":{"type":"string"}}}`}})
	if rec.Code != 303 {
		t.Fatalf("create from JSON: %d\n%s", rec.Code, rec.Body.String())
	}
	rec = c.post("/admin/settings/types", url.Values{"name": {"Broken"}, "op": {"save_json"}, "schema_json": {`{"type":`}})
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "not valid JSON") {
		t.Errorf("broken json: %d", rec.Code)
	}
	rec = c.post("/admin/settings/types", url.Values{"name": {"Another"}, "op": {"save_json"}, "schema_json": {`{"type":"object","properties":{"a":{"type":"mystery"}}}`}})
	if rec.Code != 422 {
		t.Errorf("unsupported type: %d", rec.Code)
	}
	// create from the builder, slug generated
	rec = c.post("/admin/settings/types", url.Values{"name": {"Testimonial"}, "field": {"r0"}, "f.r0.name": {"quote"}, "f.r0.kind": {"textarea"}, "f.r0.title": {"Quote"}})
	if rec.Code != 303 {
		t.Fatalf("builder create: %d\n%s", rec.Code, rec.Body.String())
	}
	if rec := c.post("/admin/settings/types", url.Values{"name": {"Testimonial"}, "slug": {"testimonial"}, "field": {"r0"}, "f.r0.name": {"quote"}, "f.r0.kind": {"text"}}); rec.Code != 422 || !strings.Contains(rec.Body.String(), "already uses that slug") {
		t.Errorf("duplicate slug: %d", rec.Code)
	}
	// delete: asks, refuses a type in use, refuses built-ins
	has(t, c.get(t, "owner-token", "/admin/settings/types/"+fid(3).String()+"/delete", ""), "Delete “Recipe”?")
	if rec := c.post("/admin/settings/types/"+fid(1).String()+"/delete", nil); rec.Code != 303 || !strings.Contains(c.next(rec, "/admin/settings/types/"+fid(1).String()), "still used") {
		t.Errorf("in-use delete: %d", rec.Code)
	}
	if rec := c.post("/admin/settings/types/"+fid(2).String()+"/delete", nil); rec.Code != 403 {
		t.Errorf("builtin delete: %d", rec.Code)
	}
	n := len(c.ents.schemas)
	if rec := c.post("/admin/settings/types/"+fid(3).String()+"/delete", nil); rec.Code != 303 || len(c.ents.schemas) != n-1 {
		t.Errorf("delete: %d", rec.Code)
	}
}

func TestSchemaPatternPreview(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	get := func(p string) string {
		return c.do("GET", "/admin/settings/types/preview-pattern?url_pattern="+url.QueryEscape(p), "owner-token", nil, nil).Body.String()
	}
	if got := get("/{year}/{month}/{slug}"); !strings.Contains(got, time.Now().Format("/2006/01/")+"example-page") {
		t.Errorf("date tokens: %s", got)
	}
	if got := get("/services/{path}"); !strings.Contains(got, "/services/section/example-page") {
		t.Errorf("path token: %s", got)
	}
	if got := get("/x/{year}"); !strings.Contains(got, "hm-field__error") || !strings.Contains(got, "slug") {
		t.Errorf("invalid: %s", got)
	}
	if got := get("<script>"); strings.Contains(got, "<script>") {
		t.Errorf("pattern echoed unescaped: %s", got)
	}
	if got := get(""); !strings.Contains(got, "Built-in address") {
		t.Errorf("empty: %s", got)
	}
}

func TestClassifyFieldKinds(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		`{"type":"string"}`: "text", `{"type":"string","format":"textarea"}`: "textarea", `{"type":"string","format":"richtext"}`: "richtext",
		`{"type":"string","x-hm-field":"html"}`: "html", `{"type":"string","x-media":true}`: "image", `{"type":"string","format":"uri"}`: "url",
		`{"type":"string","enum":["a"]}`: "select", `{"type":"number"}`: "number", `{"type":"integer"}`: "integer", `{"type":"boolean"}`: "boolean",
		`{"type":"array","items":{"type":"string"}}`: "tags", `{"type":"array","items":{"type":"string"},"x-entity-schema":"post"}`: "reference",
		`{"type":"array","items":{"type":"number"}}`: "repeater", `{"type":"array","items":{"type":"object","properties":{"a":{"type":"string"}}}}`: "custom",
		`{"type":"object","properties":{}}`: "custom", `{"type":"string","x-hm-field":"code"}`: "custom",
	}
	for raw, want := range cases {
		var p map[string]any
		_ = json.Unmarshal([]byte(raw), &p)
		if got := classify(p); got != want {
			t.Errorf("%s = %s, want %s", raw, got, want)
		}
		// every kind the editor builds reads back as itself
		if want != "custom" {
			prop, msg := buildProperty(schemaField{Name: "f", Kind: want, Options: "a", Ref: "post", Item: "number"})
			var back map[string]any
			_ = json.Unmarshal(prop, &back)
			if msg != "" || classify(back) != want {
				t.Errorf("build %s: %s -> %s %s", want, prop, classify(back), msg)
			}
		}
	}
}

func TestTaxonomyScreens(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	body := c.get(t, "owner-token", "/admin/settings/taxonomies?kind=category", "taxonomies-list")
	has(t, body, "News", "— Press", "New term", `href="/admin/settings/taxonomies?kind=tag"`)
	// create under a parent, with a slug made from the name
	rec := c.post("/admin/settings/taxonomies/terms", url.Values{"kind": {"category"}, "name": {"Releases"}, "parent": {fid(31).String()}})
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/settings/taxonomies?kind=category&created=") {
		t.Fatalf("create: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	var rel uuid.UUID
	for _, tm := range c.terms.terms {
		if tm.Name == "Releases" {
			rel = tm.ID
			if tm.Slug != "releases" || tm.ParentID == nil {
				t.Errorf("created %+v", tm)
			}
		}
	}
	// duplicate slug, invalid kind, empty name
	if rec := c.post("/admin/settings/taxonomies/terms", url.Values{"kind": {"category"}, "name": {"News"}}); rec.Code != 422 || !strings.Contains(rec.Body.String(), "already uses that slug") {
		t.Errorf("dup: %d", rec.Code)
	}
	if rec := c.post("/admin/settings/taxonomies/terms", url.Values{"kind": {"tag"}, "new_kind": {"admin"}, "name": {"x"}}); rec.Code != 422 {
		t.Errorf("reserved kind: %d", rec.Code)
	}
	if rec := c.post("/admin/settings/taxonomies/terms", url.Values{"kind": {"tag"}, "name": {""}}); rec.Code != 422 {
		t.Errorf("empty name: %d", rec.Code)
	}
	// a new kind appears as a tab
	if rec := c.post("/admin/settings/taxonomies/terms", url.Values{"kind": {"tag"}, "new_kind": {"genre"}, "name": {"Jazz"}}); rec.Code != 303 {
		t.Fatalf("new kind: %d", rec.Code)
	}
	has(t, c.get(t, "owner-token", "/admin/settings/taxonomies?kind=genre", ""), "Jazz", `href="/admin/settings/taxonomies?kind=genre"`)
	// edit
	body = c.get(t, "owner-token", "/admin/settings/taxonomies/terms/"+fid(32).String(), "taxonomy-term")
	has(t, body, `value="Press"`, "hm-seo", "seo_title")
	lacks(t, body, "Internal links")
	lacks(t, body, `value="`+fid(32).String()+`">Press`) // a term cannot be its own parent
	if rec := c.post("/admin/settings/taxonomies/terms/"+fid(32).String(), url.Values{"name": {"Press room"}, "slug": {"press-room"}, "parent": {""}}); rec.Code != 303 {
		t.Fatalf("edit: %d", rec.Code)
	}
	if tm := c.terms.terms[1]; tm.Name != "Press room" || tm.ParentID != nil {
		t.Errorf("after edit: %+v", tm)
	}
	// SEO values are stored; a bad canonical is refused
	if rec := c.post("/admin/settings/taxonomies/terms/"+fid(32).String(), url.Values{"name": {"Press room"}, "slug": {"press-room"}, "seo_title": {"Press"}, "seo_description": {"All press"}, "seo_noindex": {"true"}}); rec.Code != 303 {
		t.Fatalf("seo save: %d", rec.Code)
	}
	if sv := c.terms.terms[1].SEO; sv == nil || sv.MetaTitle != "Press" || sv.MetaDescription != "All press" || !sv.NoIndex {
		t.Errorf("seo stored: %+v", sv)
	}
	if rec := c.post("/admin/settings/taxonomies/terms/"+fid(32).String(), url.Values{"name": {"Press room"}, "slug": {"press-room"}, "seo_canonical": {"nope"}}); rec.Code != 422 || !strings.Contains(rec.Body.String(), "Use a full address") {
		t.Errorf("bad canonical: %d", rec.Code)
	}
	// a too-long title is reported on the title, not on the canonical
	rec = c.post("/admin/settings/taxonomies/terms/"+fid(32).String(), url.Values{"name": {"Press room"}, "slug": {"press-room"}, "seo_title": {strings.Repeat("a", 256)}})
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "SEO title") || strings.Contains(rec.Body.String(), "Use a full address") {
		t.Errorf("long title: %d", rec.Code)
	}
	// merge Releases into News (Releases has no children)
	body = c.get(t, "owner-token", "/admin/settings/taxonomies/terms/"+rel.String()+"/merge", "taxonomy-merge")
	has(t, body, "Merge into")
	c.terms.attach[fid(12)] = []uuid.UUID{rel}
	rec = c.post("/admin/settings/taxonomies/terms/"+rel.String()+"/merge", url.Values{"into": {fid(31).String()}})
	if rec.Code != 303 || c.terms.attach[fid(12)][0] != fid(31) {
		t.Fatalf("merge: %d %v", rec.Code, c.terms.attach[fid(12)])
	}
	// merging a parent is refused with a message
	c.post("/admin/settings/taxonomies/terms", url.Values{"kind": {"category"}, "name": {"Sub"}, "parent": {fid(31).String()}})
	rec = c.post("/admin/settings/taxonomies/terms/"+fid(31).String()+"/merge", url.Values{"into": {fid(33).String()}})
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "child terms") {
		t.Errorf("merge parent: %d", rec.Code)
	}
	if rec := c.post("/admin/settings/taxonomies/terms/"+fid(31).String()+"/merge", url.Values{}); rec.Code != 422 {
		t.Errorf("merge no target: %d", rec.Code)
	}
	// delete asks first
	has(t, c.get(t, "owner-token", "/admin/settings/taxonomies/terms/"+fid(33).String()+"/delete", ""), "Delete “Go”?")
	if rec := c.post("/admin/settings/taxonomies/terms/"+fid(33).String()+"/delete", nil); rec.Code != 303 {
		t.Errorf("delete: %d", rec.Code)
	}
}

func TestTermTreeOrder(t *testing.T) {
	t.Parallel()

	p, c1 := fid(1), fid(2)
	nodes := orderTerms([]termNode{
		{ID: fid(4), Kind: "tag", Name: "zeta"},
		{ID: c1, ParentID: &p, Kind: "category", Name: "Child"},
		{ID: p, Kind: "category", Name: "Parent"},
		{ID: fid(3), ParentID: &c1, Kind: "category", Name: "Grandchild"},
		{ID: fid(5), ParentID: ptr(fid(99)), Kind: "tag", Name: "orphan"},
	})
	var got []string
	for _, n := range nodes {
		got = append(got, fmt.Sprintf("%s:%d", n.Name, n.Depth))
	}
	if strings.Join(got, ",") != "Parent:0,Child:1,Grandchild:2,orphan:0,zeta:0" {
		t.Errorf("order %v", got)
	}
}

func TestMediaLibraryGridListAndFilters(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	body := c.get(t, "owner-token", "/admin/settings/media", "media-grid")
	has(t, body, `class="hm-media-grid"`, "team.png", "brochure.pdf", `data-hm-dropzone`, `enctype="multipart/form-data"`, `multiple`, "up to 10 MB", `href="/admin/media/`+fid(51).String()+`"`)
	body = c.get(t, "owner-token", "/admin/settings/media?view=list", "media-list")
	has(t, body, "<table", "Missing", "The team", "brochure.pdf")
	body = c.get(t, "owner-token", "/admin/settings/media?type=document", "")
	has(t, body, "brochure.pdf")
	lacks(t, body, "team.png")
	body = c.get(t, "owner-token", "/admin/settings/media?q=logo", "")
	has(t, body, "logo.png")
	lacks(t, body, "team.png")
	has(t, c.get(t, "owner-token", "/admin/settings/media?q=nothing-matches", ""), "No files match")
}

func multipartBody(t *testing.T, files map[string][]byte, withToken bool) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	// The token is the first part, where the CSRF check looks for it when no
	// X-CSRF-Token header is sent (the no-JS form post).
	if withToken {
		_ = w.WriteField("_csrf", csrfValue)
	}
	for name, data := range files {
		fw, err := w.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(data)
	}
	w.Close()
	return &buf, w.FormDataContentType()
}

var png1x1 = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xff\xff?\x00\x05\xfe\x02\xfe\xa75\x81\x84\x00\x00\x00\x00IEND\xaeB`\x82")

func (c *contentRig) upload(t *testing.T, target string, files map[string][]byte, csrfInBody bool) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := multipartBody(t, files, csrfInBody)
	req := httptest.NewRequest("POST", target, body)
	req.Header.Set("Content-Type", ct)
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	hdr := map[string]string{"X-CSRF-Token": ""} // the no-JavaScript form sends the token in the body only
	if !csrfInBody {
		hdr = map[string]string{"HX-Request": "true", "HX-Target": "hm-picker"}
	}
	c.addFetchMeta(req, "owner-token", hdr)
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	return rec
}

func TestMediaUpload(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	n := len(c.media.files)
	// the no-JavaScript form: token in the action's query string
	rec := c.upload(t, "/admin/media/upload", map[string][]byte{"new.png": png1x1}, true)
	if rec.Code != 303 || len(c.media.files) != n+1 {
		t.Fatalf("upload: %d, %d files\n%s", rec.Code, len(c.media.files), rec.Body.String())
	}
	// htmx upload: token in the header, answered with a redirect for htmx
	rec = c.upload(t, "/admin/media/upload", map[string][]byte{"two.png": png1x1}, false)
	if rec.Code != 303 && rec.Code != 204 && rec.Header().Get("HX-Redirect") == "" {
		t.Fatalf("header token: %d", rec.Code)
	}
	// a script renamed .png, and a .png with PDF bytes
	rec = c.upload(t, "/admin/media/upload", map[string][]byte{"evil.png": []byte("<script>alert(1)</script>")}, true)
	if rec.Code != 303 || len(c.media.files) != n+2 {
		t.Errorf("bad file stored: %d files", len(c.media.files))
	}
	if body := c.next(rec, "/admin/settings/media"); !strings.Contains(body, "evil.png") || !strings.Contains(body, "not allowed") {
		t.Errorf("rejection not explained")
	}
	// no token anywhere: blocked before the body is read
	body, ct := multipartBody(t, map[string][]byte{"x.png": png1x1}, false)
	req := httptest.NewRequest("POST", "/admin/media/upload", body)
	req.Header.Set("Content-Type", ct)
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	c.addFetchMeta(req, "owner-token", map[string]string{"X-CSRF-Token": ""}) // deliberately no token anywhere
	rec = httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("no csrf token: %d", rec.Code)
	}
	// no files
	rec = c.upload(t, "/admin/media/upload", nil, true)
	if rec.Code != 303 {
		t.Errorf("no files: %d", rec.Code)
	}
	// the picker's upload refreshes the picker
	rec = c.upload(t, "/admin/media/upload?picker=1", map[string][]byte{"in-picker.png": png1x1}, false)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `id="hm-picker"`) || strings.Contains(rec.Body.String(), "<!doctype") {
		t.Errorf("picker upload: %d\n%s", rec.Code, rec.Body.String())
	}
}

func TestMediaDetailAltCaptionUsageAndDelete(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := fid(51).String()
	body := c.get(t, "owner-token", "/admin/media/"+id, "media-detail")
	has(t, body, `value=""`, "The team", "Alpha service", "Where it is used", "800 × 600")
	body = c.get(t, "owner-token", "/admin/media/"+fid(52).String(), "")
	has(t, body, "Not used by any entity")
	rec := c.post("/admin/media/"+id, url.Values{"alt_text": {"The whole team"}, "caption": {"Spring offsite"}})
	if rec.Code != 303 || *c.media.files[0].AltText != "The whole team" || *c.media.files[0].Caption != "Spring offsite" {
		t.Fatalf("update: %d", rec.Code)
	}
	if rec := c.post("/admin/media/"+id, url.Values{"alt_text": {strings.Repeat("a", 600)}}); rec.Code != 422 {
		t.Errorf("long alt: %d", rec.Code)
	}
	// delete warns about usage
	body = c.get(t, "owner-token", "/admin/media/"+id+"/delete", "")
	has(t, body, "used in 1 place", "Alpha service", "broken image")
	if len(c.media.files) != 3 {
		t.Fatal("deleted by the confirmation page")
	}
	if rec := c.post("/admin/media/"+id+"/delete", nil); rec.Code != 303 || len(c.media.files) != 2 {
		t.Errorf("delete: %d", rec.Code)
	}
	if rec := c.do("GET", "/admin/media/"+id, "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("deleted file: %d", rec.Code)
	}
}

func TestMediaPickerFeedsImageFields(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	// the dialog fragment: images only, search, pick buttons carrying the URL
	rec := c.do("GET", "/admin/media/picker", "owner-token", nil, map[string]string{"HX-Request": "true", "HX-Target": "hm-media-picker-body"})
	body := rec.Body.String()
	if rec.Code != 200 || strings.Contains(body, "<!doctype") || !strings.Contains(body, `data-hm-pick="/media/x/file-51.png"`) || strings.Contains(body, "brochure.pdf") {
		t.Fatalf("picker: %d\n%s", rec.Code, body)
	}
	if issues := a11y.Check(body, a11y.Options{}); len(issues) > 0 {
		t.Errorf("picker fragment a11y: %v", issues)
	}
	rec = c.do("GET", "/admin/media/picker?q=logo", "owner-token", nil, map[string]string{"HX-Request": "true", "HX-Target": "hm-picker"})
	body = normalizePage(rec.Body.String())
	compareGolden(t, "pages/media-picker.html", body)
	has(t, body, "logo.png")
	lacks(t, body, "team.png")
	// every image field of the entity form carries the picker hook
	form := c.get(t, "owner-token", "/admin/entities/"+fid(11).String(), "")
	has(t, form, `data-hm-media-picker="sf-attr-hero_image"`, `data-hm-media-picker="seo_og_image"`)
	// and the JS that wires it exists
	as, _ := c.a.state()
	js := string(as.files[strings.TrimPrefix(as.jsURL["content"], "/admin/_/")].body)
	for _, want := range []string{"data-hm-media-picker", "data-hm-pick", "/media/picker"} {
		if !strings.Contains(js, want) {
			t.Errorf("content.js lacks %q", want)
		}
	}
}

func TestContentAccessControl(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := fid(11).String()
	for _, p := range []string{"/admin/settings/types", "/admin/settings/types/new", "/admin/settings/types/" + fid(1).String(), "/admin/settings/taxonomies", "/admin/settings/taxonomies/terms/" + fid(31).String()} {
		if rec := c.do("GET", p, "editor-token", nil, nil); rec.Code != 403 {
			t.Errorf("editor GET %s = %d, want 403", p, rec.Code)
		}
		if rec := c.do("GET", p, "", nil, nil); rec.Code != 303 {
			t.Errorf("anonymous GET %s = %d", p, rec.Code)
		}
	}
	for _, p := range []string{"/admin/settings/types", "/admin/settings/types/" + fid(1).String(), "/admin/settings/types/" + fid(1).String() + "/delete", "/admin/settings/taxonomies/terms", "/admin/settings/taxonomies/terms/" + fid(31).String() + "/delete"} {
		if rec := c.do("POST", p, "editor-token", url.Values{"name": {"x"}}, nil); rec.Code != 403 {
			t.Errorf("editor POST %s = %d, want 403", p, rec.Code)
		}
	}
	// editors work on content and media
	for _, p := range []string{"/admin/entities", "/admin/entities/" + id, "/admin/entities/" + id + "/versions", "/admin/settings/media", "/admin/media/picker", "/admin/media/" + fid(51).String()} {
		if rec := c.do("GET", p, "editor-token", nil, nil); rec.Code != 200 {
			t.Errorf("editor GET %s = %d", p, rec.Code)
		}
	}
	// editors cannot create terms from the entity form; the rest of the save still works
	body := c.do("GET", "/admin/entities/"+id, "editor-token", nil, nil).Body.String()
	lacks(t, body, `name="new_term_name"`)
	rec := c.do("POST", "/admin/entities/"+id, "editor-token", url.Values{"name": {"Alpha service"}, "slug": {"alpha"}, "status": {"published"}, "attr.summary": {"S"}, "_terms": {"1"}, "new_term_name": {"Sneaky"}}, nil)
	if rec.Code != 303 {
		t.Fatalf("editor save: %d", rec.Code)
	}
	for _, tm := range c.terms.terms {
		if tm.Name == "Sneaky" {
			t.Error("an editor created a term")
		}
	}
	// the sidebar offers each person what they can open
	nav := c.do("GET", "/admin/entities", "editor-token", nil, nil).Body.String()
	has(t, nav, `href="/admin/entities"`, `href="/admin/settings"`)
	lacks(t, nav, `href="/admin/settings/taxonomies"`, `href="/admin/settings/types"`)
	nav = c.do("GET", "/admin/entities", "owner-token", nil, nil).Body.String()
	has(t, nav, `href="/admin/entities"`, `href="/admin/settings"`)
}

func TestSlugHelpersInTemplates(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	body := c.get(t, "owner-token", "/admin/entities/new?type=recipe", "")
	has(t, body, `data-hm-slug-from="name"`, `data-hm-slug-auto="1"`)
}

func TestPersonFormHasNoAuthorPickerAndKeepsTheByline(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	sch := c.ents.addSchema(40, "Person", "person", `{"type":"object","properties":{"job_title":{"type":"string","title":"Job title"}}}`, `{"url_pattern":"/author/{slug}"}`, true)
	p := c.ents.addEntity(90, sch, "Grace Hopper", "grace", entity.StatusPublished, `{"job_title":"Admiral"}`, t0)
	p.BylinePersonID = ptr(fid(41))

	body := c.get(t, "owner-token", "/admin/entities/"+p.ID.String(), "")
	lacks(t, body, `name="author"`, `name="new_author"`, "Site organisation")
	has(t, body, `name="attr.job_title"`, "when Public is switched on")
	// A person's visibility is the Public switch: no status or schedule controls.
	lacks(t, body, `name="status"`, `name="schedule"`)

	// A posted author is ignored on a person: the byline is not theirs to edit.
	rec := c.post("/admin/entities/"+p.ID.String(), url.Values{"name": {"Grace Hopper"}, "slug": {"grace"}, "status": {"published"}, "attr.job_title": {"Rear Admiral"},
		"author": {""}, "new_author": {"Someone Else"}})
	if rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	got := c.ents.ents[p.ID]
	if got.BylinePersonID == nil || *got.BylinePersonID != fid(41) || len(c.authors.list) != 1 {
		t.Errorf("byline changed by saving a person: person=%v people=%d", got.BylinePersonID, len(c.authors.list))
	}
	if !strings.Contains(string(got.Attributes), "Rear Admiral") {
		t.Errorf("the person's own fields were not saved: %s", got.Attributes)
	}

	// Other types keep the picker, with the organisation option.
	body = c.get(t, "owner-token", "/admin/entities/"+fid(11).String(), "")
	has(t, body, `name="author"`, `<option value="org">Site organisation</option>`, `<option value="`+fid(41).String()+`" selected>Ada Writer</option>`)
}

// The CSRF token is accepted from a multipart form's header or first part,
// never from the query string.
func TestMultipartCSRFNeverFromQuery(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	post := func(target string, build func(w *multipart.Writer), header bool) int {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		build(w)
		w.Close()
		req := httptest.NewRequest("POST", target, &buf)
		req.Header.Set("Content-Type", w.FormDataContentType())
		req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
		hdr := map[string]string{"X-CSRF-Token": ""}
		if header {
			hdr = nil
		}
		c.addFetchMeta(req, "owner-token", hdr)
		rec := httptest.NewRecorder()
		c.h.ServeHTTP(rec, req)
		return rec.Code
	}
	file := func(w *multipart.Writer) {
		fw, _ := w.CreateFormFile("file", "q.png")
		fw.Write(png1x1)
	}
	tokenFirst := func(w *multipart.Writer) { _ = w.WriteField("_csrf", csrfValue); file(w) }
	tokenLast := func(w *multipart.Writer) { file(w); _ = w.WriteField("_csrf", csrfValue) }
	tokenFirstWrong := func(w *multipart.Writer) { _ = w.WriteField("_csrf", "nope"); file(w) }

	for _, picker := range []string{"/admin/media/upload?picker=1", "/admin/media/upload"} {
		if got := post(picker+"&_csrf="+csrfValue, file, false); got != http.StatusForbidden {
			t.Errorf("%s: token in the query string: %d, want 403", picker, got)
		}
		if got := post(picker, tokenLast, false); got != http.StatusForbidden {
			t.Errorf("%s: token as a late part: %d, want 403", picker, got)
		}
		if got := post(picker, tokenFirstWrong, false); got != http.StatusForbidden {
			t.Errorf("%s: wrong first-part token: %d, want 403", picker, got)
		}
		if got := post(picker, tokenFirst, false); got == http.StatusForbidden {
			t.Errorf("%s: token as the first part was refused", picker)
		}
		if got := post(picker, file, true); got == http.StatusForbidden {
			t.Errorf("%s: X-CSRF-Token header was refused", picker)
		}
	}
}
