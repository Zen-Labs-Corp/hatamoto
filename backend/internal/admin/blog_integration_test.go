//go:build integration

package admin

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
)

// TestBlogEditorWritesSchedulesPublishesAndRestores is the writing checklist of
// docs/QA.md on the real services: create a post, write it (the server
// sanitiser has the last word), autosave the draft, schedule it, publish it,
// update it and restore an earlier revision.
func TestBlogEditorWritesSchedulesPublishesAndRestores(t *testing.T) {
	p := newPGRig(t)
	ctx := context.Background()
	schemas, err := p.ents.ListSchemas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var post *entity.EntitySchema
	for _, s := range schemas {
		if s.Slug == "post" {
			post = s
		}
	}
	if post == nil {
		t.Skip("no post type in this database")
	}

	// create: the generic form, which sends a post to the writing view
	rec := p.post("/admin/entities", url.Values{"type": {"post"}, "name": {"Itest post " + p.suffix}})
	id := p.locationID(rec)
	p.cleanups = append(p.cleanups, func() { _ = p.ents.DeleteEntity(ctx, id) })
	if loc := rec.Header().Get("Location"); loc != "/admin/entities/"+id.String()+"/edit" {
		t.Fatalf("a new post opens in the writing view, got %q", loc)
	}
	edit := "/admin/entities/" + id.String() + "/edit"
	stored := func() (*entity.Entity, map[string]any) {
		e, err := p.ents.GetEntity(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return e, attrsMap(e.Attributes)
	}
	form := func() url.Values { return parseForm(t, p.get(edit)) }

	// write: unsafe markup is stripped by the server whatever the browser sent
	f := form()
	f.Set("content", `<p>First draft</p><p onclick="x()">kept</p><script>alert(1)</script><div class="faq"><div class="faq-item"><h3 class="faq-q">Why?</h3><div class="faq-a"><p>Because.</p></div></div></div>`)
	f.Set("excerpt", "Short")
	f.Set("seo_keyword", "first draft")
	f.Set("_op", "save")
	if rec := p.post(edit, f); rec.Code != 303 {
		t.Fatalf("write: %d\n%s", rec.Code, rec.Body.String())
	}
	e, attrs := stored()
	content, _ := attrs["content"].(string)
	if strings.Contains(content, "<script") || strings.Contains(content, "onclick") || !strings.Contains(content, `class="faq-item"`) || !strings.Contains(content, "First draft") {
		t.Errorf("stored content: %s", content)
	}
	if seo, _ := attrs["seo"].(map[string]any); seo["focus_keyword"] != "first draft" || e.Status != entity.StatusDraft {
		t.Errorf("stored: %v %s", attrs, e.Status)
	}
	v1 := e.Version

	// autosave the draft, version-checked
	rec = p.post(edit+"/autosave", url.Values{"version": {itoa(e.Version)}, "name": {e.Name}, "content": {"<p>Second draft</p>"}, "excerpt": {"Short"}})
	var out autosaveReply
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 || !out.OK || out.Version != v1+1 {
		t.Fatalf("autosave: %d %s", rec.Code, rec.Body.String())
	}
	rec = p.post(edit+"/autosave", url.Values{"version": {itoa(v1)}, "name": {e.Name}, "content": {"<p>From an old tab</p>"}})
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), `"conflict":true`) {
		t.Errorf("stale autosave: %d %s", rec.Code, rec.Body.String())
	}
	if _, a := stored(); a["content"] != "<p>Second draft</p>" {
		t.Errorf("a stale autosave overwrote the draft: %v", a["content"])
	}

	// the panel and the preview endpoint answer on the real page
	body := p.get(edit)
	for _, want := range []string{"Second draft", "Search result preview", `name="version" value="` + itoa(v1+1) + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("editor page lacks %q", want)
		}
	}

	// schedule: a future time makes it scheduled, not published
	f = form()
	f.Set("status", "scheduled")
	f.Set("schedule", time.Now().UTC().Add(72*time.Hour).Format(scheduleLayout))
	f.Set("_op", "save")
	if rec := p.post(edit, f); rec.Code != 303 {
		t.Fatalf("schedule: %d\n%s", rec.Code, rec.Body.String())
	}
	if e, _ := stored(); e.Status != entity.StatusScheduled || e.PublishedAt == nil || !e.PublishedAt.After(time.Now()) {
		t.Fatalf("scheduled: %s %v", e.Status, e.PublishedAt)
	}
	if body := p.get(edit); !strings.Contains(body, `<option value="scheduled" selected>`) {
		t.Error("the schedule is not shown")
	}

	// publish
	f = form()
	f.Set("content", "<p>Published text</p>")
	f.Set("_op", "publish")
	if rec := p.post(edit, f); rec.Code != 303 {
		t.Fatalf("publish: %d\n%s", rec.Code, rec.Body.String())
	}
	if e, a := stored(); e.Status != entity.StatusPublished || a["content"] != "<p>Published text</p>" {
		t.Fatalf("published: %s %v", e.Status, a["content"])
	}
	p.a.deps.Content.Crawlers = agents.NewStatsRepository(p.db)
	body = p.get(edit)
	if !strings.Contains(body, `id="be-crawlers"`) || !strings.Contains(body, "fetches in 7 days") || strings.Contains(body, "could not be loaded") {
		t.Error("the AI crawler card does not read the analytics tables")
	}
	if !strings.Contains(body, ">Update</span></button>") || strings.Contains(body, ">Publish</span></button>") {
		t.Error("a published post offers Update")
	}
	// autosave refuses a published post
	e, _ = stored()
	if rec := p.post(edit+"/autosave", url.Values{"version": {itoa(e.Version)}, "content": {"<p>nope</p>"}}); rec.Code != 409 {
		t.Errorf("autosave on a published post: %d", rec.Code)
	}

	// update: changes only on Update, and every save is a revision
	f = form()
	f.Set("content", "<p>Updated text</p>")
	f.Set("_op", "save")
	if rec := p.post(edit, f); rec.Code != 303 {
		t.Fatalf("update: %d", rec.Code)
	}
	if e, a := stored(); a["content"] != "<p>Updated text</p>" || e.Status != entity.StatusPublished {
		t.Fatalf("updated: %v", a["content"])
	}
	vs, err := p.ents.ListVersions(ctx, id, 50)
	if err != nil || len(vs) < 4 {
		t.Fatalf("revisions: %d %v", len(vs), err)
	}
	vb := p.get("/admin/entities/" + id.String() + "/versions")
	if !strings.Contains(vb, "blog editor") {
		t.Error("the versions page does not name the blog editor")
	}

	// restore the first draft revision (the one with the FAQ block)
	var first *entity.EntityVersion
	for _, v := range vs {
		if strings.Contains(string(v.Attributes), "faq-item") {
			if first == nil || v.Version < first.Version {
				first = v
			}
		}
	}
	if first == nil {
		t.Fatal("no revision with the first draft")
	}
	if rec := p.post("/admin/entities/"+id.String()+"/versions/"+itoa(first.Version)+"/restore", nil); rec.Code != 303 {
		t.Fatalf("restore: %d\n%s", rec.Code, rec.Body.String())
	}
	if _, a := stored(); !strings.Contains(a["content"].(string), "First draft") {
		t.Errorf("restored content: %v", a["content"])
	}
	if body := p.get(edit); !strings.Contains(body, "First draft") {
		t.Error("the writing view does not show the restored text")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// TestBlogAutosaveCoalescesVersions: a run of one person's autosaves keeps one
// version, an explicit save always adds one, an autosave keeps the slug
// following the title, a request without excerpt keeps the stored excerpt, and
// pruning drops autosave versions before any explicit one.
func TestBlogAutosaveCoalescesVersions(t *testing.T) {
	p := newPGRig(t)
	ctx := context.Background()
	rec := p.post("/admin/entities", url.Values{"type": {"post"}, "name": {"Coalesce " + p.suffix}})
	id := p.locationID(rec)
	p.cleanups = append(p.cleanups, func() { _ = p.ents.DeleteEntity(ctx, id) })
	edit := "/admin/entities/" + id.String() + "/edit"
	cur := func() *entity.Entity {
		e, err := p.ents.GetEntity(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	versions := func() (auto, explicit int) {
		vs, err := p.ents.ListVersions(ctx, id, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range vs {
			if v.ChangeSummary != nil && *v.ChangeSummary == entity.AutosaveSummary {
				auto++
			} else {
				explicit++
			}
		}
		return
	}
	autosave := func(content string, extra url.Values) autosaveReply {
		v := url.Values{"version": {itoa(cur().Version)}, "content": {content}}
		for k, vs := range extra {
			v[k] = vs
		}
		rec := p.post(edit+"/autosave", v)
		var out autosaveReply
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 || !out.OK {
			t.Fatalf("autosave: %d %s", rec.Code, rec.Body.String())
		}
		return out
	}

	autosave("<p>one</p>", url.Values{"excerpt": {"Kept"}, "name": {"Fresh Title"}, "slug_auto": {"1"}})
	for _, s := range []string{"<p>two</p>", "<p>three</p>", "<p>four</p>"} {
		autosave(s, nil) // no excerpt sent: the stored one stays
	}
	e := cur()
	if a := attrsMap(e.Attributes); a["excerpt"] != "Kept" || a["content"] != "<p>four</p>" {
		t.Errorf("stored: %v", a)
	}
	if e.Slug != "fresh-title" {
		t.Errorf("a slug that follows the title moved with it, got %q", e.Slug)
	}
	if auto, explicit := versions(); auto != 1 || explicit != 0 {
		t.Errorf("four autosaves in a row left %d autosave and %d explicit versions, want 1 and 0", auto, explicit)
	}

	// an explicit save always adds a version; the next autosave starts a new run
	f := parseForm(t, p.get(edit))
	f.Set("content", "<p>saved</p>")
	f.Set("_op", "save")
	if rec := p.post(edit, f); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	autosave("<p>after</p>", nil)
	autosave("<p>after more</p>", nil)
	if auto, explicit := versions(); auto != 2 || explicit != 1 {
		t.Errorf("got %d autosave and %d explicit versions, want 2 and 1", auto, explicit)
	}

	// pruning keeps the explicit version when the autosaves would exceed the limit
	if _, err := entity.NewPostgresEntityRepository(p.db).PruneVersions(database.WithSuperadmin(ctx), 1); err != nil {
		t.Fatal(err)
	}
	if _, explicit := versions(); explicit != 1 {
		t.Errorf("pruning dropped the explicit version")
	}
}
