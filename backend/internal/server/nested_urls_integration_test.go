//go:build integration

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/author"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/taxonomy"
)

type importSink struct {
	mu     sync.Mutex
	events []entity.Event
}

func (s *importSink) Emit(_ context.Context, ev entity.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

type nestedRig struct {
	t       *testing.T
	ctx     context.Context
	db      *database.DB
	srv     *Server
	svc     *entity.Service
	sink    *importSink
	pageSch uuid.UUID // pages routed at /{path}
	postSch uuid.UUID // posts routed at /{year}/{month}/{slug}
	noteSch uuid.UUID // authored posts with an archive
}

// newNestedRig builds a Server over the migrated DATABASE_URL with three
// scratch schemas and everything the nested URL features need wired like
// New() does: authors, the path observer and the redirect service.
func newNestedRig(t *testing.T) *nestedRig {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 8, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	r := &nestedRig{t: t, ctx: ctx, db: db, pageSch: uuid.New(), postSch: uuid.New(), noteSch: uuid.New(), sink: &importSink{}}
	r.exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'NPage','nested-page','{"type":"object"}','{"url_pattern":"/{path}"}')`, r.pageSch)
	r.exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'NPost','dated-post','{"type":"object"}','{"url_pattern":"/{year}/{month}/{slug}"}')`, r.postSch)
	r.exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'NNote','note','{"type":"object"}','{"url_pattern":"/notes/{slug}","archive_path":"/notes"}')`, r.noteSch)
	t.Cleanup(func() {
		ids := []uuid.UUID{r.pageSch, r.postSch, r.noteSch}
		for _, q := range []string{
			`DELETE FROM hatamoto_entities WHERE schema_id = ANY($1)`,
			`DELETE FROM hatamoto_entity_schemas WHERE id = ANY($1)`,
		} {
			_, _ = db.Pool().Exec(ctx, q, ids)
		}
		for _, q := range []string{
			`DELETE FROM hatamoto_redirects WHERE source_tag IN ('entity-move', 'review-it')`,
			`DELETE FROM hatamoto_authors`,
			`DELETE FROM hatamoto_terms`,
		} {
			_, _ = db.Pool().Exec(ctx, q)
		}
	})

	entityRepo := entity.NewPostgresEntityRepository(db)
	r.svc = entity.NewService(entity.NewPostgresSchemaRepository(db), entityRepo, logger)
	r.svc.SetEvents(r.sink)
	authors := author.NewStore(db)
	r.svc.SetBylines(person.NewByline(db.Pool()))
	r.srv = &Server{db: db, entityService: r.svc, entityRepo: entityRepo, authors: authors,
		redirectService: redirect.NewService(db, logger), logger: logger,
		siteConfig: siteconfig.NewStore(db, logger)}
	t.Cleanup(r.srv.redirectService.Close)
	r.svc.SetPathObserver(r.srv.onEntityMoved)
	r.svc.SetLiveObserver(r.srv.onEntitiesLive)
	return r
}

func (r *nestedRig) exec(q string, args ...any) {
	r.t.Helper()
	if _, err := r.db.Pool().Exec(r.ctx, q, args...); err != nil {
		r.t.Fatalf("%s: %v", q, err)
	}
}

// page creates and publishes a page under parent (nil for a root).
func (r *nestedRig) page(slug string, parent *entity.Entity) *entity.Entity {
	r.t.Helper()
	in := &entity.CreateEntityInput{SchemaID: r.pageSch, Name: slug, Slug: slug, Attributes: json.RawMessage(`{}`)}
	if parent != nil {
		in.ParentID = &parent.ID
	}
	e, err := r.svc.CreateEntity(r.ctx, nil, in)
	if err != nil {
		r.t.Fatalf("create %s: %v", slug, err)
	}
	if _, err := r.svc.UpdateEntityStatus(r.ctx, e.ID, nil, entity.StatusPublished); err != nil {
		r.t.Fatalf("publish %s: %v", slug, err)
	}
	return e
}

// get resolves a path like the renderer does.
func (r *nestedRig) get(path string) (int, *routeResult) {
	req := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	res, outcome, err := r.srv.resolveForRender(r.ctx, path, req)
	switch {
	case err != nil:
		return outcome.Status, nil
	case res.Redirect != nil:
		return res.Redirect.Status, res
	case res.NotFound:
		return http.StatusNotFound, res
	}
	return http.StatusOK, res
}

func (r *nestedRig) expect(path string, status int) *routeResult {
	r.t.Helper()
	code, res := r.get(path)
	if code != status {
		r.t.Fatalf("GET %s = %d, want %d (%+v)", path, code, status, res)
	}
	return res
}

// /services/seo/local routes by the parent chain; the same slug in another
// branch is another page; wrong chains are 404.
func TestNestedPathRouting_Integration(t *testing.T) {
	r := newNestedRig(t)
	services := r.page("services", nil)
	seo := r.page("seo", services)
	pt := r.page("pt", services)
	local := r.page("local", seo)
	localPT := r.page("local", pt)

	res := r.expect("/services/seo/local", 200)
	if res.Page.Entity.ID != local.ID || res.CanonicalURL != "/services/seo/local" {
		t.Errorf("/services/seo/local -> %v canonical %q", res.Page.Entity.ID, res.CanonicalURL)
	}
	if !strings.HasSuffix(res.Page.Meta.CanonicalURL, "/services/seo/local") {
		t.Errorf("meta canonical = %q", res.Page.Meta.CanonicalURL)
	}
	if got := r.expect("/services/pt/local", 200); got.Page.Entity.ID != localPT.ID {
		t.Errorf("same slug under another parent must be another page")
	}
	r.expect("/services", 200)
	r.expect("/services/seo", 200)
	r.expect("/services/seo/local/", 200) // trailing slash
	for _, p := range []string{"/seo/local", "/services/local", "/local", "/seo", "/pt/local", "/services/seo/pt/local"} {
		r.expect(p, 404)
	}

	// A draft parent still gives its children their URL; a draft child is not served.
	r.exec(`UPDATE hatamoto_entities SET status = 'draft' WHERE id = $1`, services.ID)
	r.expect("/services/seo/local", 200)
	r.exec(`UPDATE hatamoto_entities SET status = 'draft' WHERE id = $1`, local.ID)
	r.expect("/services/seo/local", 404)
}

// A parent cycle and a too-deep chain are refused on write.
func TestNestedParentGuards_Integration(t *testing.T) {
	r := newNestedRig(t)
	a := r.page("a", nil)
	b := r.page("b", a)
	c := r.page("c", b)
	for _, bad := range []*entity.Entity{a, b} { // a under c; b under c
		_, err := r.svc.UpdateEntity(r.ctx, bad.ID, nil, &entity.UpdateEntityInput{ParentID: &c.ID})
		if err != entity.ErrParentCycle {
			t.Errorf("moving %s under its descendant: %v", bad.Slug, err)
		}
	}
	if _, err := r.svc.UpdateEntity(r.ctx, a.ID, nil, &entity.UpdateEntityInput{ParentID: &a.ID}); err != entity.ErrParentCycle {
		t.Errorf("own parent: %v", err)
	}
	missing := uuid.New()
	if _, err := r.svc.UpdateEntity(r.ctx, a.ID, nil, &entity.UpdateEntityInput{ParentID: &missing}); err != entity.ErrParentNotFound {
		t.Errorf("missing parent: %v", err)
	}
	// Chain depth: 8 levels is the cap.
	cur := r.page("d1", nil)
	for i := 2; i <= 8; i++ {
		cur = r.page("d"+string(rune('0'+i)), cur)
	}
	_, err := r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: r.pageSch, Name: "d9", Slug: "d9", ParentID: &cur.ID, Attributes: json.RawMessage(`{}`)})
	if err != entity.ErrParentTooDeep {
		t.Errorf("ninth level: %v", err)
	}
}

// Posts at /{year}/{month}/{slug}: the date must match the publication date,
// read in the site time zone (UTC here).
func TestDatedRouting_Integration(t *testing.T) {
	r := newNestedRig(t)
	e, err := r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: r.postSch, Name: "Hello World", Slug: "hello-world", Attributes: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	r.exec(`UPDATE hatamoto_entities SET status='published', published_at='2024-05-31T23:30:00Z' WHERE id=$1`, e.ID)

	res := r.expect("/2024/05/hello-world", 200)
	if res.Page.Entity.ID != e.ID || res.CanonicalURL != "/2024/05/hello-world" {
		t.Errorf("dated post = %+v", res)
	}
	if !strings.HasSuffix(res.Page.Meta.CanonicalURL, "/2024/05/hello-world") {
		t.Errorf("canonical = %q", res.Page.Meta.CanonicalURL)
	}
	for _, p := range []string{"/2024/06/hello-world", "/2023/05/hello-world", "/hello-world", "/2024/5/hello-world", "/blog/hello-world"} {
		r.expect(p, 404)
	}

	// The site time zone moves the date: 23:30 UTC is already June in Sydney.
	r.svc.URLRegistry().SetTimezone(func(context.Context) string { return "Australia/Sydney" })
	t.Cleanup(func() { r.svc.URLRegistry().SetTimezone(nil) })
	r.expect("/2024/06/hello-world", 200)
	r.expect("/2024/05/hello-world", 404)
}

// The sitemap lists nested and dated URLs, no duplicates for one slug in two branches.
func TestNestedDatedSitemap_Integration(t *testing.T) {
	r := newNestedRig(t)
	services := r.page("services", nil)
	seo := r.page("seo", services)
	pt := r.page("pt", services)
	r.page("local", seo)
	r.page("local", pt)
	e, err := r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: r.postSch, Name: "Hello", Slug: "hello-world", Attributes: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	r.exec(`UPDATE hatamoto_entities SET status='published', published_at='2024-05-17T12:00:00Z' WHERE id=$1`, e.ID)

	req := httptest.NewRequest(http.MethodGet, "http://example.test/sitemap-misc.xml", nil)
	req.SetPathValue("file", "sitemap-misc.xml")
	rec := httptest.NewRecorder()
	r.srv.handlePublicSubSitemap(rec, req)
	body := rec.Body.String()
	for _, want := range []string{
		"<loc>http://example.test/services/seo/local</loc>", "<loc>http://example.test/services/pt/local</loc>",
		"<loc>http://example.test/services/seo</loc>", "<loc>http://example.test/2024/05/hello-world</loc>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("sitemap-misc lacks %s:\n%s", want, body)
		}
	}
	if n := strings.Count(body, "/services/seo/local</loc>"); n != 1 {
		t.Errorf("/services/seo/local listed %d times", n)
	}
}

func (r *nestedRig) redirectTarget(path string) string {
	r.t.Helper()
	if rd := r.srv.matchRedirect(r.ctx, path); rd != nil && rd.StatusCode == 301 {
		return rd.TargetURL
	}
	return ""
}

// Renaming a parent or moving a page answers 301 from every old path, to the
// new one, without chains; moving back drops the redirect away from a live URL.
func TestParentSlugChangeRedirects_Integration(t *testing.T) {
	r := newNestedRig(t)
	services := r.page("services", nil)
	seo := r.page("seo", services)
	local := r.page("local", seo)
	draft, err := r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: r.pageSch, Name: "wip", Slug: "wip", ParentID: &seo.ID, Attributes: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}

	offers := "offers"
	if _, err := r.svc.UpdateEntity(r.ctx, services.ID, nil, &entity.UpdateEntityInput{Slug: &offers}); err != nil {
		t.Fatal(err)
	}
	for old, want := range map[string]string{
		"/services": "/offers", "/services/seo": "/offers/seo", "/services/seo/local": "/offers/seo/local",
	} {
		if got := r.redirectTarget(old); got != want {
			t.Errorf("redirect %s -> %q, want %q", old, got, want)
		}
	}
	if got := r.redirectTarget("/services/seo/wip"); got != "" {
		t.Errorf("a draft has no public URL to redirect: %q", got)
	}
	r.expect("/offers/seo/local", 200)
	r.expect("/services/seo/local", 301)

	// A second rename does not leave a chain: the first redirects are repointed.
	deals := "deals"
	if _, err := r.svc.UpdateEntity(r.ctx, services.ID, nil, &entity.UpdateEntityInput{Slug: &deals}); err != nil {
		t.Fatal(err)
	}
	for old, want := range map[string]string{
		"/services/seo/local": "/deals/seo/local", "/offers/seo/local": "/deals/seo/local", "/offers": "/deals",
	} {
		if got := r.redirectTarget(old); got != want {
			t.Errorf("redirect %s -> %q, want %q", old, got, want)
		}
	}

	// Moving a page to the root: its subtree follows.
	if _, err := r.svc.UpdateEntity(r.ctx, seo.ID, nil, &entity.UpdateEntityInput{ClearParent: true}); err != nil {
		t.Fatal(err)
	}
	if got := r.redirectTarget("/deals/seo/local"); got != "/seo/local" {
		t.Errorf("moved to root: /deals/seo/local -> %q", got)
	}
	r.expect("/seo/local", 200)

	// Moving it back makes /deals/seo live again: the redirect that would send
	// it away is gone.
	if _, err := r.svc.UpdateEntity(r.ctx, seo.ID, nil, &entity.UpdateEntityInput{ParentID: &services.ID}); err != nil {
		t.Fatal(err)
	}
	if got := r.redirectTarget("/deals/seo"); got != "" {
		t.Errorf("/deals/seo is live again but redirects to %q", got)
	}
	r.expect("/deals/seo/local", 200)
	if got := r.redirectTarget("/seo/local"); got != "/deals/seo/local" {
		t.Errorf("/seo/local -> %q", got)
	}
	_ = local
	_ = draft

	// A plain slug change redirects too.
	renamed := "regional"
	if _, err := r.svc.UpdateEntity(r.ctx, local.ID, nil, &entity.UpdateEntityInput{Slug: &renamed}); err != nil {
		t.Fatal(err)
	}
	if got := r.redirectTarget("/deals/seo/local"); got != "/deals/seo/regional" {
		t.Errorf("slug change: %q", got)
	}
}

// Import: dates, status, parents and authors stored as given, re-runs update in
// place, one import.completed and no entity events, and the result routes.
func TestImport_Integration(t *testing.T) {
	r := newNestedRig(t)
	created := time.Date(2018, 2, 3, 4, 5, 6, 0, time.UTC)
	updated := time.Date(2019, 7, 8, 9, 10, 11, 0, time.UTC)
	published := time.Date(2018, 2, 10, 12, 0, 0, 0, time.UTC)
	attrs := func(title string) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"title": title, "content": "<p>Body</p><script>x()</script>"})
		return b
	}
	batch := entity.ImportBatch{Source: "it", Items: []entity.ImportItem{
		{Key: "wp:3", SchemaSlug: "nested-page", Name: "Local", Slug: "local", Status: entity.StatusPublished, ParentKey: "wp:2",
			Attributes: attrs("Local"), CreatedAt: created, UpdatedAt: updated, PublishedAt: published, AuthorName: "Jane Guest"},
		{Key: "wp:2", SchemaSlug: "nested-page", Name: "SEO", Slug: "seo", Status: entity.StatusPublished, ParentKey: "wp:1",
			Attributes: attrs("SEO"), CreatedAt: created, UpdatedAt: updated, PublishedAt: published},
		{Key: "wp:1", SchemaSlug: "nested-page", Name: "Services", Slug: "services", Status: entity.StatusPublished,
			Attributes: attrs("Services"), CreatedAt: created, UpdatedAt: updated, PublishedAt: published},
		{Key: "wp:4", SchemaSlug: "dated-post", Name: "Hello World", Slug: "hello-world", Status: entity.StatusPublished,
			Attributes: attrs("Hello"), CreatedAt: created, UpdatedAt: updated, PublishedAt: time.Date(2024, 5, 17, 10, 0, 0, 0, time.UTC)},
		{Key: "wp:5", SchemaSlug: "nested-page", Name: "Draft", Slug: "draft", Attributes: attrs("Draft")},
		{Key: "wp:6", SchemaSlug: "nested-page", Name: "Broken", Slug: "broken", ParentKey: "missing", Attributes: attrs("x")},
	}}
	res, err := r.svc.Import(r.ctx, nil, batch)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 5 || res.Updated != 0 || res.Failed != 1 {
		t.Fatalf("result = %+v", res)
	}
	if len(r.sink.events) != 1 || r.sink.events[0].Kind != entity.EventImportCompleted ||
		r.sink.events[0].Import.Created != 5 || r.sink.events[0].Import.Failed != 1 {
		t.Fatalf("events = %+v, want one import.completed", r.sink.events)
	}

	var gotC, gotU, gotP time.Time
	var status, content string
	var parent, authorID *uuid.UUID
	err = r.db.Pool().QueryRow(r.ctx, `SELECT created_at, updated_at, published_at, status, attributes->>'content', parent_id, author_id
		FROM hatamoto_entities WHERE metadata->>'import_key' = 'wp:3'`).Scan(&gotC, &gotU, &gotP, &status, &content, &parent, &authorID)
	if err != nil {
		t.Fatal(err)
	}
	if !gotC.Equal(created) || !gotU.Equal(updated) || !gotP.Equal(published) || status != "published" {
		t.Errorf("stored dates/status: %v %v %v %s", gotC, gotU, gotP, status)
	}
	if strings.Contains(content, "script") {
		t.Errorf("content not sanitised: %s", content)
	}
	if parent == nil || authorID == nil {
		t.Errorf("parent %v author %v", parent, authorID)
	}

	// Routing of what was imported, including the byline.
	r.expect("/services/seo/local", 200)
	r.expect("/2024/05/hello-world", 200)
	r.expect("/draft", 404)
	r.expect("/author/jane-guest", 404) // the person made by the byline is not public: no page

	// Re-run: same rows, nothing duplicated, updated_at kept, a second event.
	res, err = r.svc.Import(r.ctx, nil, batch)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 0 || res.Updated != 5 || res.Failed != 1 {
		t.Fatalf("re-run result = %+v", res)
	}
	var n int
	_ = r.db.Pool().QueryRow(r.ctx, `SELECT count(*) FROM hatamoto_entities WHERE metadata ? 'import_key'`).Scan(&n)
	if n != 5 {
		t.Errorf("%d imported rows after a re-run, want 5", n)
	}
	_ = r.db.Pool().QueryRow(r.ctx, `SELECT updated_at FROM hatamoto_entities WHERE metadata->>'import_key' = 'wp:3'`).Scan(&gotU)
	if !gotU.Equal(updated) {
		t.Errorf("a re-run moved updated_at to %v", gotU)
	}
	if len(r.sink.events) != 2 {
		t.Errorf("events after re-run = %d, want 2 (one per batch)", len(r.sink.events))
	}
	for _, ev := range r.sink.events {
		if ev.Kind != entity.EventImportCompleted {
			t.Errorf("unexpected per-entity event %s", ev.Kind)
		}
	}

	// A normal save afterwards stamps updated_at again (the trigger is back).
	title := "Services 2"
	var sid uuid.UUID
	_ = r.db.Pool().QueryRow(r.ctx, `SELECT id FROM hatamoto_entities WHERE metadata->>'import_key' = 'wp:1'`).Scan(&sid)
	if _, err := r.svc.UpdateEntity(r.ctx, sid, nil, &entity.UpdateEntityInput{Name: &title}); err != nil {
		t.Fatal(err)
	}
	_ = r.db.Pool().QueryRow(r.ctx, `SELECT updated_at FROM hatamoto_entities WHERE id = $1`, sid).Scan(&gotU)
	if time.Since(gotU) > time.Minute {
		t.Errorf("updated_at after a normal save = %v", gotU)
	}
}

// Bylines: a plain-text byline makes a person (not public, no page), the
// person page is the author listing once public, a rename follows the posts and
// JSON-LD, and deleting the person keeps the entity.
func TestAuthors_Integration(t *testing.T) {
	r := newNestedRig(t)
	post, err := r.svc.GetSchemaBySlug(r.ctx, "post")
	if err != nil {
		t.Fatal(err)
	}
	e, err := r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: post.ID, Name: "Note", Slug: "note-authors-it",
		Attributes: json.RawMessage(`{"author":"Jane Guest"}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = r.db.Pool().Exec(r.ctx, `DELETE FROM hatamoto_entities WHERE id = $1 OR slug = 'jane-guest'`, e.ID)
	})
	if e.BylinePersonID == nil || e.AuthorID == nil {
		t.Fatalf("a byline must resolve to a person and its mirror author: %+v", e)
	}
	if _, err := r.svc.UpdateEntityStatus(r.ctx, e.ID, nil, entity.StatusPublished); err != nil {
		t.Fatal(err)
	}
	jane, err := r.srv.people().Get(r.ctx, *e.BylinePersonID)
	if err != nil || jane.Slug != "jane-guest" || jane.Public {
		t.Fatalf("person = %+v %v, want jane-guest, not public", jane, err)
	}
	// Not public: no page, and the byline is text without a link.
	r.expect("/author/jane-guest", 404)
	if v := r.srv.bylinesOf(r.ctx, e.ID).view(r.ctx, e.ID, nil, "post", []string{"post"}); v == nil || v.Name != "Jane Guest" || v.URL != "" {
		t.Errorf("hidden person byline = %+v", v)
	}

	// Public with a bio: the person page is the author page.
	r.exec(`UPDATE hatamoto_entities SET status = 'published', attributes = '{"public":true,"short_bio":"Writes things."}' WHERE id = $1`, jane.ID)
	res := r.expect("/author/jane-guest", 200)
	req := httptest.NewRequest(http.MethodGet, "http://example.test/author/jane-guest", nil)
	v := pageViewFromRoute(res, "http://example.test", "/author/jane-guest")
	if err := r.srv.fillView(r.ctx, req, res, v); err != nil {
		t.Fatal(err)
	}
	if v.Kind != "author" || v.Author == nil || v.Author.Bio != "Writes things." || v.Author.URL != "/author/jane-guest" {
		t.Fatalf("person view = kind %q %+v", v.Kind, v.Author)
	}
	if len(v.Items) != 1 || v.Items[0].Author != "Jane Guest" {
		t.Errorf("person page items = %+v", v.Items)
	}
	r.expect("/author/nobody", 404)

	// A public person with no posts still has a page.
	personSch, err := r.svc.GetSchemaBySlug(r.ctx, "person")
	if err != nil {
		t.Fatal(err)
	}
	other, err := r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: personSch.ID, Name: "Quiet Person", Slug: "quiet-person-it",
		Attributes: json.RawMessage(`{"public":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = r.db.Pool().Exec(r.ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, other.ID) })
	r.expect("/author/quiet-person-it", 200) // public, so the service published it

	// A rename follows the plain byline of the post and the mirror author.
	renamed := "Jane Q. Guest"
	if _, err := r.svc.UpdateEntity(r.ctx, jane.ID, nil, &entity.UpdateEntityInput{Name: &renamed}); err != nil {
		t.Fatal(err)
	}
	var byline, mirror string
	_ = r.db.Pool().QueryRow(r.ctx, `SELECT attributes->>'author' FROM hatamoto_entities WHERE id=$1`, e.ID).Scan(&byline)
	_ = r.db.Pool().QueryRow(r.ctx, `SELECT a.name FROM hatamoto_entities x JOIN hatamoto_authors a ON a.id = x.author_id WHERE x.id=$1`, e.ID).Scan(&mirror)
	if byline != renamed || mirror != renamed {
		t.Errorf("after rename: byline %q, mirror %q", byline, mirror)
	}
	pub := entity.NewPostgresPublicRepository(r.db)
	ent, err := pub.GetPublishedByID(r.ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	ld, _ := entity.GenerateJSONLD(ent, "http://example.test")
	if got, _ := json.Marshal(ld["author"]); string(got) != `{"@type":"Person","name":"Jane Q. Guest"}` {
		t.Errorf("JSON-LD author = %s", got)
	}

	// An entity credited to a person but with no plain byline gets the name in JSON-LD.
	r.exec(`UPDATE hatamoto_entities SET attributes = '{}' WHERE id = $1`, e.ID)
	ent, _ = pub.GetPublishedByID(r.ctx, e.ID)
	r.srv.applyAuthorName(r.ctx, ent)
	ld, _ = entity.GenerateJSONLD(ent, "http://example.test")
	if got, _ := json.Marshal(ld["author"]); string(got) != `{"@type":"Person","name":"Jane Q. Guest"}` {
		t.Errorf("filled-in JSON-LD author = %s", got)
	}

	// Deleting the person keeps the entity and drops the reference.
	if err := r.svc.DeleteEntity(r.ctx, jane.ID); err != nil {
		t.Fatal(err)
	}
	r.expect("/author/jane-guest", 404)
	got, err := r.svc.GetEntity(r.ctx, e.ID)
	if err != nil || got.BylinePersonID != nil {
		t.Errorf("entity after person delete: %+v %v", got, err)
	}
	// An unknown person id is refused.
	missing := uuid.New()
	if _, err := r.svc.UpdateEntity(r.ctx, e.ID, nil, &entity.UpdateEntityInput{BylinePersonID: &missing}); err != entity.ErrAuthorNotFound {
		t.Errorf("unknown person id: %v", err)
	}
}

// Hierarchical terms and custom kinds: archives at /{kind}/{ancestors.../slug}.
func TestTermArchives_Integration(t *testing.T) {
	r := newNestedRig(t)
	tsvc := taxonomy.NewService(taxonomy.NewPostgresRepository(r.db), nil)
	mk := func(kind, name string, parent *taxonomy.Term) *taxonomy.Term {
		t.Helper()
		in := taxonomy.CreateTermInput{Kind: kind, Name: name, Description: name + " things"}
		if parent != nil {
			in.ParentID = &parent.ID
		}
		term, err := tsvc.CreateTerm(r.ctx, in)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return term
	}
	fiction := mk("genre", "Fiction", nil)
	noir := mk("genre", "Noir", fiction)
	neo := mk("genre", "Neo Noir", noir)
	news := mk("category", "News", nil)
	sub := mk("category", "Local News", news)

	e, err := r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: r.noteSch, Name: "N", Slug: "n", Attributes: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.UpdateEntityStatus(r.ctx, e.ID, nil, entity.StatusPublished); err != nil {
		t.Fatal(err)
	}
	if err := tsvc.ReplaceEntityTerms(r.ctx, e.ID, []uuid.UUID{neo.ID, sub.ID}); err != nil {
		t.Fatal(err)
	}

	for path, name := range map[string]string{
		"/genre/fiction":               "Fiction",
		"/genre/fiction/noir":          "Noir",
		"/genre/fiction/noir/neo-noir": "Neo Noir",
		"/category/news":               "News",
		"/category/news/local-news":    "Local News",
	} {
		res := r.expect(path, 200)
		if res.Listing == nil || res.Listing.Term.Name != name {
			t.Errorf("%s -> %+v", path, res.Listing)
		}
	}
	// Only the canonical path serves a term.
	for _, p := range []string{"/genre/noir", "/genre/neo-noir", "/genre/noir/neo-noir", "/category/local-news", "/genre", "/mood/noir", "/genre/fiction/noir/nope"} {
		r.expect(p, 404)
	}

	// Entity pages link the terms at their nested archive URL, with the parent.
	v := r.srv.entityTermViews(r.ctx, e.ID)
	got := map[string]string{}
	for _, tv := range v {
		got[tv.Slug] = tv.URL + "|" + tv.Parent + "|" + tv.Description
	}
	if got["neo-noir"] != "/genre/fiction/noir/neo-noir|noir|Neo Noir things" || got["local-news"] != "/category/news/local-news|news|Local News things" {
		t.Errorf("term views = %v", got)
	}

	// Deleting a parent leaves its children as roots.
	if err := tsvc.DeleteTerm(r.ctx, fiction.ID); err != nil {
		t.Fatal(err)
	}
	r.expect("/genre/fiction/noir", 404)
	r.expect("/genre/noir", 200)
}

// localePage creates and publishes a page in locale, under no parent.
func (r *nestedRig) localePage(slug, locale string) *entity.Entity {
	r.t.Helper()
	e, err := r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: r.pageSch, Name: slug + locale, Slug: slug, Locale: locale, Attributes: json.RawMessage(`{}`)})
	if err != nil {
		r.t.Fatalf("create %s %s: %v", slug, locale, err)
	}
	if _, err := r.svc.UpdateEntityStatus(r.ctx, e.ID, nil, entity.StatusPublished); err != nil {
		r.t.Fatalf("publish %s %s: %v", slug, locale, err)
	}
	return e
}

// A non-site-language entity shares its twin's path, so renaming it must not
// redirect the live page, and publishing it must not retire the twin's redirect.
func TestOtherLocaleNeverTouchesRedirects_Integration(t *testing.T) {
	r := newNestedRig(t)
	var lang string
	if err := r.db.Pool().QueryRow(r.ctx, `SELECT public.hatamoto_site_language()`).Scan(&lang); err != nil {
		t.Fatal(err)
	}
	if lang != "en" {
		t.Skipf("site language %q, test assumes en", lang)
	}
	r.localePage("about", "en")
	fr := r.localePage("about", "fr")

	apropos := "a-propos"
	if _, err := r.svc.UpdateEntity(r.ctx, fr.ID, nil, &entity.UpdateEntityInput{Slug: &apropos}); err != nil {
		t.Fatal(err)
	}
	if got := r.redirectTarget("/about"); got != "" {
		t.Errorf("renaming a fr entity redirected /about to %q", got)
	}
	r.expect("/about", 200)

	// An en rename leaves an entity-move redirect; a fr twin going live at the
	// old path must not retire it.
	r.localePage("team", "en")
	var team uuid.UUID
	if err := r.db.Pool().QueryRow(r.ctx, `SELECT id FROM hatamoto_entities WHERE schema_id=$1 AND slug='team' AND locale='en'`, r.pageSch).Scan(&team); err != nil {
		t.Fatal(err)
	}
	crew := "crew"
	if _, err := r.svc.UpdateEntity(r.ctx, team, nil, &entity.UpdateEntityInput{Slug: &crew}); err != nil {
		t.Fatal(err)
	}
	if got := r.redirectTarget("/team"); got != "/crew" {
		t.Fatalf("en rename redirect /team -> %q, want /crew", got)
	}
	r.localePage("team", "fr")
	if got := r.redirectTarget("/team"); got != "/crew" {
		t.Errorf("publishing a fr twin retired the en redirect: /team -> %q", got)
	}
}

// Promoting a twin into the site language and renaming it in one update must
// not shadow the live page at the old path, nor repoint redirects aimed at it.
func TestLocalePromoteWithRenameKeepsLiveTwin_Integration(t *testing.T) {
	r := newNestedRig(t)
	var lang string
	if err := r.db.Pool().QueryRow(r.ctx, `SELECT public.hatamoto_site_language()`).Scan(&lang); err != nil {
		t.Fatal(err)
	}
	if lang != "en" {
		t.Skipf("site language %q, test assumes en", lang)
	}
	r.localePage("about", "en")
	fr := r.localePage("about", "fr")
	r.exec(`INSERT INTO hatamoto_redirects (source_path, target_url, status_code, source_tag) VALUES ('/old-about', '/about', 301, 'entity-move')`)

	en, slug := "en", "about-fr"
	if _, err := r.svc.UpdateEntity(r.ctx, fr.ID, nil, &entity.UpdateEntityInput{Locale: &en, Slug: &slug}); err != nil {
		t.Fatal(err)
	}
	if got := r.redirectTarget("/about"); got != "" {
		t.Errorf("promoting a twin redirected /about to %q", got)
	}
	if got := r.redirectTarget("/old-about"); got != "/about" {
		t.Errorf("/old-about repointed to %q, want /about", got)
	}
	r.expect("/about", 200)
	r.expect("/about-fr", 200)
}

// A locale change into the site language retires the stale entity-move
// redirect at the page's path, as publishing does.
func TestLocalePromoteRetiresStaleRedirect_Integration(t *testing.T) {
	r := newNestedRig(t)
	var lang string
	if err := r.db.Pool().QueryRow(r.ctx, `SELECT public.hatamoto_site_language()`).Scan(&lang); err != nil {
		t.Fatal(err)
	}
	if lang != "en" {
		t.Skipf("site language %q, test assumes en", lang)
	}
	r.localePage("contact", "fr")
	r.exec(`INSERT INTO hatamoto_redirects (source_path, target_url, status_code, source_tag) VALUES ('/contact', '/contact-us', 301, 'entity-move')`)
	var id uuid.UUID
	if err := r.db.Pool().QueryRow(r.ctx, `SELECT id FROM hatamoto_entities WHERE schema_id=$1 AND slug='contact'`, r.pageSch).Scan(&id); err != nil {
		t.Fatal(err)
	}
	en := "en"
	if _, err := r.svc.UpdateEntity(r.ctx, id, nil, &entity.UpdateEntityInput{Locale: &en}); err != nil {
		t.Fatal(err)
	}
	if got := r.redirectTarget("/contact"); got != "" {
		t.Errorf("stale redirect kept: /contact -> %q", got)
	}
	r.expect("/contact", 200)
}

// A site-language child under a page in another locale is not reachable
// through that parent's chain, so renaming the parent must not redirect the
// child's path and shadow the live twin's child.
func TestOtherLocaleParentRenameKeepsLiveChild_Integration(t *testing.T) {
	r := newNestedRig(t)
	var lang string
	if err := r.db.Pool().QueryRow(r.ctx, `SELECT public.hatamoto_site_language()`).Scan(&lang); err != nil {
		t.Fatal(err)
	}
	if lang != "en" {
		t.Skipf("site language %q, test assumes en", lang)
	}
	en := r.localePage("about", "en")
	r.page("team", en)
	fr := r.localePage("about", "fr")
	r.page("team", fr)
	r.expect("/about/team", 200)

	apropos := "a-propos"
	if _, err := r.svc.UpdateEntity(r.ctx, fr.ID, nil, &entity.UpdateEntityInput{Slug: &apropos}); err != nil {
		t.Fatal(err)
	}
	if got := r.redirectTarget("/about/team"); got != "" {
		t.Errorf("/about/team redirected to %q", got)
	}
	r.expect("/about/team", 200)
	r.expect("/about", 200)
}

func (r *nestedRig) requireEnglishSite() {
	r.t.Helper()
	var lang string
	if err := r.db.Pool().QueryRow(r.ctx, `SELECT public.hatamoto_site_language()`).Scan(&lang); err != nil {
		r.t.Fatal(err)
	}
	if lang != "en" {
		r.t.Skipf("site language %q, test assumes en", lang)
	}
}

// A site-language child under a page in another locale is public through that
// parent's slug, so renaming the parent (no site-language twin) moves it.
func TestOtherLocaleParentRenameRedirectsChild_Integration(t *testing.T) {
	r := newNestedRig(t)
	r.requireEnglishSite()
	fr := r.localePage("a-propos", "fr")
	r.page("team", fr)
	r.expect("/a-propos/team", 200)

	slug := "qui-sommes-nous"
	if _, err := r.svc.UpdateEntity(r.ctx, fr.ID, nil, &entity.UpdateEntityInput{Slug: &slug}); err != nil {
		t.Fatal(err)
	}
	if got := r.redirectTarget("/a-propos/team"); got != "/qui-sommes-nous/team" {
		t.Errorf("/a-propos/team -> %q", got)
	}
}

// Demoting a page to another locale with a rename moves its site-language
// children to a path that is still public.
func TestLocaleDemoteWithRenameRedirectsChild_Integration(t *testing.T) {
	r := newNestedRig(t)
	r.requireEnglishSite()
	en := r.localePage("about", "en")
	r.page("team", en)
	r.expect("/about/team", 200)

	fr, slug := "fr", "a-propos"
	if _, err := r.svc.UpdateEntity(r.ctx, en.ID, nil, &entity.UpdateEntityInput{Locale: &fr, Slug: &slug}); err != nil {
		t.Fatal(err)
	}
	if got := r.redirectTarget("/about/team"); got != "/a-propos/team" {
		t.Errorf("/about/team -> %q", got)
	}
}

// The ownership check also covers a site-language grandchild below another-locale
// middle page: it moves when its old path is free, and stays when a twin owns it.
func TestGrandchildMoveOwnership_Integration(t *testing.T) {
	r := newNestedRig(t)
	r.requireEnglishSite()

	// Free old path: no twin chain.
	root := r.localePage("docs", "en")
	mid := r.localePage("mid", "fr")
	if _, err := r.svc.UpdateEntity(r.ctx, mid.ID, nil, &entity.UpdateEntityInput{ParentID: &root.ID}); err != nil {
		t.Fatal(err)
	}
	r.page("leaf", mid)
	r.expect("/docs/mid/leaf", 200)
	renamed := "manual"
	if _, err := r.svc.UpdateEntity(r.ctx, root.ID, nil, &entity.UpdateEntityInput{Slug: &renamed}); err != nil {
		t.Fatal(err)
	}
	if got := r.redirectTarget("/docs/mid/leaf"); got != "/manual/mid/leaf" {
		t.Errorf("/docs/mid/leaf -> %q", got)
	}

	// Owned old path: a live twin chain keeps /about/mid/leaf.
	e := r.localePage("about", "en")
	em := r.localePage("mid", "fr")
	if _, err := r.svc.UpdateEntity(r.ctx, em.ID, nil, &entity.UpdateEntityInput{ParentID: &e.ID}); err != nil {
		t.Fatal(err)
	}
	r.page("leaf", em)
	f := r.localePage("about", "fr")
	fm := r.page("mid", f)
	r.page("leaf", fm)
	r.expect("/about/mid/leaf", 200)
	other := "about2"
	if _, err := r.svc.UpdateEntity(r.ctx, e.ID, nil, &entity.UpdateEntityInput{Slug: &other}); err != nil {
		t.Fatal(err)
	}
	if got := r.redirectTarget("/about/mid/leaf"); got != "" {
		t.Errorf("/about/mid/leaf redirected to %q", got)
	}
	r.expect("/about/mid/leaf", 200)
}
