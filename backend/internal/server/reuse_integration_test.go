//go:build integration

package server

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/sectiontemplate"
	"hatamoto.dev/core/internal/user"
)

// The reuse features (synced blocks, theme parts, templates) against a real
// database and the real page cache.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run Reuse

// reuseIT is a running server with an owner and an editor signed in.
type reuseIT struct {
	t      *testing.T
	srv    *Server
	db     *database.DB
	ctx    context.Context
	sfx    string
	owner  *user.User
	tokens map[string]string // role -> session token
	csrf   map[string]string // role -> session-bound admin token, fetched on first admin use

	pageSc, blockSc, partSc *entity.EntitySchema
}

func themeFixture(t *testing.T, name string) fs.FS {
	t.Helper()
	b, err := os.ReadFile("testdata/reuse/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return fstest.MapFS{"theme.json": {Data: b}}
}

func newReuseIT(t *testing.T) *reuseIT {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set: skipping (needs a migrated Postgres)")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "b6-it")
	t.Setenv("HATAMOTO_SITE_NAME", "B6 IT")
	t.Setenv("PUBLIC_BASE_URL", "https://b6-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("HATAMOTO_PAGE_CACHE_TTL", "24h")
	t.Setenv("MEDIA_UPLOAD_DIR", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	r := &reuseIT{t: t, ctx: context.Background(), sfx: uuid.NewString()[:6], tokens: map[string]string{}, csrf: map[string]string{}}
	if r.db, err = database.New(r.ctx, cfg.Database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.db.Close)
	r.srv = New(cfg, r.db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if r.srv.renderErr != nil {
		t.Fatal(r.srv.renderErr)
	}

	for _, role := range []user.Role{user.RoleOwner, user.RoleEditor} {
		hash, err := auth.HashPassword("a-long-enough-password")
		if err != nil {
			t.Fatal(err)
		}
		u, err := r.srv.userRepo.Create(r.ctx, &user.CreateUserInput{Email: "b6-" + string(role) + "-" + r.sfx + "@example.test", Role: role, Name: "B6 " + string(role)}, hash)
		if err != nil {
			t.Fatal(err)
		}
		completeProfile(t, r.srv, u)
		if role == user.RoleOwner {
			r.owner = u
		}
		if r.tokens[string(role)], err = r.srv.authService.IssueSession(r.ctx, u.ID, "it", "127.0.0.1"); err != nil {
			t.Fatal(err)
		}
		uid := u.ID
		t.Cleanup(func() { r.exec(`DELETE FROM hatamoto_users WHERE id=$1`, uid) })
	}

	hadSite := r.db.Pool().QueryRow(r.ctx, `SELECT 1 FROM hatamoto_site_config`).Scan(new(int)) == nil
	if !hadSite {
		r.exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('B6 IT')`)
	}
	var extra []byte
	_ = r.db.Pool().QueryRow(r.ctx, `SELECT theme_extra FROM hatamoto_site_config LIMIT 1`).Scan(&extra)
	t.Cleanup(func() {
		if !hadSite {
			r.exec(`DELETE FROM hatamoto_site_config`)
		} else if extra != nil {
			r.exec(`UPDATE hatamoto_site_config SET theme_extra=$1::jsonb`, string(extra))
		} else {
			r.exec(`UPDATE hatamoto_site_config SET theme_extra=NULL`)
		}
	})

	for slug, dst := range map[string]**entity.EntitySchema{"page": &r.pageSc, entity.BlockSchemaSlug: &r.blockSc, entity.PartSchemaSlug: &r.partSc} {
		if *dst, err = r.srv.entityService.GetSchemaBySlug(r.ctx, slug); err != nil || *dst == nil {
			t.Fatalf("schema %s: %v", slug, err)
		}
	}
	return r
}

func (r *reuseIT) exec(q string, args ...any) {
	r.t.Helper()
	if _, err := r.db.Pool().Exec(context.Background(), q, args...); err != nil {
		r.t.Errorf("%s: %v", q, err)
	}
}

// mk creates an entity through the service (so the real rules apply) and removes it afterwards.
func (r *reuseIT) mk(sc *entity.EntitySchema, name string, attrs any) *entity.Entity {
	r.t.Helper()
	raw, err := json.Marshal(attrs)
	if err != nil {
		r.t.Fatal(err)
	}
	e, err := r.srv.entityService.CreateEntity(r.ctx, &r.owner.ID, &entity.CreateEntityInput{
		SchemaID: sc.ID, Name: name, Slug: "b6-" + strings.ToLower(strings.ReplaceAll(name, " ", "-")) + "-" + r.sfx, Attributes: raw,
	})
	if err != nil {
		r.t.Fatalf("create %s: %v", name, err)
	}
	id := e.ID
	r.t.Cleanup(func() { r.exec(`DELETE FROM hatamoto_entities WHERE id=$1`, id) })
	return e
}

func (r *reuseIT) publish(e *entity.Entity) {
	r.t.Helper()
	if _, err := r.srv.entityService.UpdateEntityStatus(r.ctx, e.ID, &r.owner.ID, entity.StatusPublished); err != nil {
		r.t.Fatalf("publish %s: %v", e.Name, err)
	}
}

// setDoc stores a page_doc on e through the service, as an API or MCP write would.
func (r *reuseIT) setDoc(e *entity.Entity, doc any) {
	r.t.Helper()
	cur, err := r.srv.entityService.GetEntity(r.ctx, e.ID)
	if err != nil {
		r.t.Fatal(err)
	}
	attrs := map[string]any{}
	_ = json.Unmarshal(cur.Attributes, &attrs)
	attrs["page_doc"] = doc
	raw, _ := json.Marshal(attrs)
	rm := json.RawMessage(raw)
	if _, err := r.srv.entityService.UpdateEntity(r.ctx, e.ID, &r.owner.ID, &entity.UpdateEntityInput{Attributes: &rm}); err != nil {
		r.t.Fatalf("update %s: %v", e.Name, err)
	}
}

// do sends a request as role; a map body is JSON, url.Values a form, nil nothing.
func (r *reuseIT) do(role, method, path string, body any) *httptest.ResponseRecorder {
	r.t.Helper()
	var rd io.Reader
	ct := ""
	switch b := body.(type) {
	case url.Values:
		rd, ct = strings.NewReader(b.Encode()), "application/x-www-form-urlencoded"
	case nil:
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			r.t.Fatal(err)
		}
		rd, ct = strings.NewReader(string(raw)), "application/json"
	}
	req := httptest.NewRequest(method, path, rd)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if strings.HasPrefix(path, "/admin") {
		// Fetched lazily: the first admin page read warms route caches that fixtures built later must not meet.
		if role != "" && r.csrf[role] == "" {
			r.csrf[role] = adminToken(r.t, r.srv.router, r.tokens[role])
		}
		adminReq(req, r.tokens[role], r.csrf[role])
	}
	rec := httptest.NewRecorder()
	r.srv.router.ServeHTTP(rec, req)
	return rec
}

func (r *reuseIT) get(path string) *httptest.ResponseRecorder { return r.do("", "GET", path, nil) }

func (r *reuseIT) cacheKey(path string) string {
	return cache.RenderedPageKey(r.srv.renderEngine.Version(), path)
}

// eventually polls fn until it holds: entity events reach the cache purge on
// their own goroutine, so "purged" and "cached again" are eventual, not instant.
func (r *reuseIT) eventually(what string, fn func() bool) {
	r.t.Helper()
	for i := 0; i < 250; i++ {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	r.t.Fatalf("never happened: %s", what)
}

func (r *reuseIT) cached(path string) bool {
	_, err := r.srv.infra.Cache.Get(r.ctx, r.cacheKey(path))
	return err == nil
}

// canvas renders the page as the builder canvas does, from the doc it holds.
func (r *reuseIT) canvas(e *entity.Entity, doc any) string {
	r.t.Helper()
	body, _ := json.Marshal(map[string]any{"entity_id": e.ID.String(), "schema": "page", "canvas": true, "path": "/" + e.Slug,
		"attributes": map[string]any{"page_doc": doc}})
	req := httptest.NewRequest("POST", "/api/v1/render/preview", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	r.srv.newRenderAdminHandlers(r.srv.renderEngine).handleRenderPreview(rec, req)
	if rec.Code != 200 {
		r.t.Fatalf("canvas render: %d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func (r *reuseIT) entityRow(id uuid.UUID) (status string, version int, updated string) {
	r.t.Helper()
	if err := r.db.Pool().QueryRow(r.ctx, `SELECT status, version, updated_at::text FROM hatamoto_entities WHERE id=$1`, id).Scan(&status, &version, &updated); err != nil {
		r.t.Fatal(err)
	}
	return
}

func headingDoc(id, text string) map[string]any {
	return map[string]any{"v": 2, "nodes": []any{map[string]any{"id": id, "t": "heading", "p": map[string]any{"text": text, "level": 2}}}}
}

func refDoc(blocks ...uuid.UUID) map[string]any {
	nodes := []any{}
	for i, b := range blocks {
		nodes = append(nodes, map[string]any{"id": "r" + string(rune('a'+i)), "t": "ref", "p": map[string]any{"block": b.String()}})
	}
	return map[string]any{"v": 2, "nodes": nodes}
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// A1 + ISC-3/4/5: saving a block changes the next response of an unchanged,
// cached page and its canvas; the page entity is not touched.
func TestReuseBlockSaveReachesCachedPage_Integration(t *testing.T) {
	r := newReuseIT(t)
	block := r.mk(r.blockSc, "Footer CTA", map[string]any{"page_doc": headingDoc("h", "BLOCK-V1")})
	page := r.mk(r.pageSc, "Home B6", map[string]any{"page_doc": refDoc(block.ID)})
	r.publish(page)
	path := "/" + page.Slug

	rec := r.get(path)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "BLOCK-V1") {
		t.Fatalf("page: %d %.300s", rec.Code, rec.Body.String())
	}
	r.eventually("the page is cached", func() bool { r.get(path); return r.cached(path) })
	statusBefore, versionBefore, updatedBefore := r.entityRow(page.ID)

	res := r.do("owner", "POST", "/admin/entities/"+block.ID.String()+"/builder/save", map[string]any{"version": 1, "doc": headingDoc("h", "BLOCK-V2"), "op": "save"})
	if res.Code != 200 {
		t.Fatalf("save block: %d %s", res.Code, res.Body.String())
	}
	r.eventually("a block save purges the cached page", func() bool { return !r.cached(path) })
	body := r.get(path).Body.String()
	if !strings.Contains(body, "BLOCK-V2") || strings.Contains(body, "BLOCK-V1") {
		t.Fatalf("the page did not pick up the block:\n%.400s", body)
	}
	if status, version, updated := r.entityRow(page.ID); status != statusBefore || version != versionBefore || updated != updatedBefore {
		t.Fatalf("the page entity changed: %s/%d/%s -> %s/%d/%s", statusBefore, versionBefore, updatedBefore, status, version, updated)
	}
	if c := r.canvas(page, refDoc(block.ID)); !strings.Contains(c, "BLOCK-V2") {
		t.Fatal("the canvas render does not show the saved block")
	}
	// The block stays a draft and the page stays published.
	if status, _, _ := r.entityRow(block.ID); status != "draft" {
		t.Errorf("block status %s", status)
	}

	// A save that would loop is refused and changes nothing.
	loop := r.do("owner", "POST", "/admin/entities/"+block.ID.String()+"/builder/save", map[string]any{"version": 2, "doc": refDoc(block.ID), "op": "save"})
	var out struct{ Code string }
	_ = json.Unmarshal(loop.Body.Bytes(), &out)
	if loop.Code != 422 || out.Code != "ref_cycle" {
		t.Fatalf("self reference: %d %s", loop.Code, loop.Body.String())
	}
	// Autosave and publish are not offered for a block.
	if rec := r.do("owner", "POST", "/admin/entities/"+block.ID.String()+"/builder/save", map[string]any{"version": 2, "doc": headingDoc("h", "x"), "op": "publish"}); rec.Code != 409 {
		t.Errorf("publish a block: %d", rec.Code)
	}
}

// A2/ISC-6: a block or part can never be published, scheduled or reached.
func TestReuseBlocksAreNeverPublic_Integration(t *testing.T) {
	r := newReuseIT(t)
	published := entity.StatusPublished
	for _, sc := range []*entity.EntitySchema{r.blockSc, r.partSc} {
		attrs := map[string]any{"page_doc": headingDoc("h", "SECRET-"+sc.Slug)}
		if sc == r.partSc {
			attrs["slot"] = "header"
		}
		raw, _ := json.Marshal(attrs)
		slug := "b6-secret-" + sc.Slug + "-" + r.sfx
		e, err := r.srv.entityService.CreateEntity(r.ctx, &r.owner.ID, &entity.CreateEntityInput{SchemaID: sc.ID, Name: "Secret " + sc.Slug, Slug: slug, Attributes: raw, Status: &published})
		if err != nil {
			t.Fatalf("create %s: %v", sc.Slug, err)
		}
		id := e.ID
		t.Cleanup(func() { r.exec(`DELETE FROM hatamoto_entities WHERE id=$1`, id) })
		if status, _, _ := r.entityRow(id); status != "draft" {
			t.Fatalf("%s created as %s", sc.Slug, status)
		}
		if _, err := r.srv.entityService.UpdateEntityStatus(r.ctx, id, &r.owner.ID, entity.StatusPublished); err == nil {
			t.Errorf("%s: publish was accepted", sc.Slug)
		}
		if _, err := r.srv.entityService.ScheduleEntity(r.ctx, id, &r.owner.ID, e.CreatedAt.AddDate(1, 0, 0)); err == nil {
			t.Errorf("%s: schedule was accepted", sc.Slug)
		}
		r.setDoc(e, headingDoc("h", "SECRET-AGAIN"))
		if status, _, _ := r.entityRow(id); status != "draft" {
			t.Errorf("%s: an update left it %s", sc.Slug, status)
		}
		for _, p := range []string{"/" + slug, "/" + sc.Slug + "/" + slug} {
			if rec := r.get(p); rec.Code != 404 {
				t.Errorf("GET %s = %d", p, rec.Code)
			}
		}
		for _, sm := range []string{"/sitemap.xml", "/sitemap-pages.xml"} {
			if body := r.get(sm).Body.String(); strings.Contains(body, slug) {
				t.Errorf("%s lists the %s", sm, sc.Slug)
			}
		}
	}
}

// A2/ISC-7/8: documents that loop, written around the admin checks, still render.
func TestReuseStoredCyclesRenderAndFinish_Integration(t *testing.T) {
	r := newReuseIT(t)
	a := r.mk(r.blockSc, "Loop A", map[string]any{"page_doc": headingDoc("h", "A-TEXT")})
	b := r.mk(r.blockSc, "Loop B", map[string]any{"page_doc": headingDoc("h", "B-TEXT")})
	self := r.mk(r.blockSc, "Loop Self", map[string]any{"page_doc": headingDoc("h", "SELF-TEXT")})
	r.setDoc(a, refDoc(b.ID))
	r.setDoc(b, refDoc(a.ID))
	r.setDoc(self, refDoc(self.ID))
	page := r.mk(r.pageSc, "Loops B6", map[string]any{"page_doc": map[string]any{"v": 2, "nodes": []any{
		map[string]any{"id": "r1", "t": "ref", "p": map[string]any{"block": a.ID.String()}},
		map[string]any{"id": "r2", "t": "ref", "p": map[string]any{"block": self.ID.String()}},
		map[string]any{"id": "after", "t": "heading", "p": map[string]any{"text": "STILL-HERE", "level": 2}},
	}}})
	r.publish(page)
	rec := r.get("/" + page.Slug)
	if rec.Code != 200 {
		t.Fatalf("page: %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Count(body, "<!-- hm-ref: cycle -->") < 2 || !strings.Contains(body, "STILL-HERE") {
		t.Fatalf("expected cycle comments and the rest of the page:\n%s", body)
	}
	// A missing block and a ref to something that is not a block. The page above
	// is cached and its purge runs on the event goroutine, so wait for it.
	path := "/" + page.Slug
	r.eventually("the page is cached", func() bool { r.get(path); return r.cached(path) })
	r.setDoc(page, refDoc(uuid.New(), page.ID))
	r.eventually("the doc update purges the cached page", func() bool { return !r.cached(path) })
	body = r.get(path).Body.String()
	if strings.Count(body, "<!-- hm-ref: missing -->") != 2 {
		t.Fatalf("expected two missing comments:\n%s", body)
	}
}

type usageOut struct {
	Users []struct {
		ID, Name, Type string
	} `json:"users"`
	Capped bool `json:"capped"`
}

func (r *reuseIT) usage(block *entity.Entity) map[string]bool {
	r.t.Helper()
	rec := r.do("editor", "GET", "/admin/builder/blocks/"+block.ID.String()+"/usage", nil)
	if rec.Code != 200 {
		r.t.Fatalf("usage: %d %s", rec.Code, rec.Body.String())
	}
	var u usageOut
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		r.t.Fatal(err)
	}
	set := map[string]bool{}
	for _, x := range u.Users {
		set[x.ID] = true
	}
	return set
}

// A5/ISC-22/23: the usage list is exactly the entities with a ref; detach gives
// an independent copy with the same visible content.
func TestReuseUsageAndDetach_Integration(t *testing.T) {
	r := newReuseIT(t)
	block := r.mk(r.blockSc, "Shared", map[string]any{"page_doc": headingDoc("h", "SHARED-TEXT")})
	p1 := r.mk(r.pageSc, "Use One", map[string]any{"page_doc": refDoc(block.ID)})
	p2 := r.mk(r.pageSc, "Use Two", map[string]any{"page_doc": refDoc(block.ID)})
	r.mk(r.pageSc, "Mentions", map[string]any{"page_doc": headingDoc("m", block.ID.String())}) // the uuid as text only
	r.publish(p1)

	want := func(label string, es ...*entity.Entity) {
		t.Helper()
		got := r.usage(block)
		if len(got) != len(es) {
			t.Fatalf("%s: usage %v", label, got)
		}
		for _, e := range es {
			if !got[e.ID.String()] {
				t.Fatalf("%s: %s missing from %v", label, e.Name, got)
			}
		}
	}
	want("two pages", p1, p2)
	nest := r.mk(r.blockSc, "Nesting", map[string]any{"page_doc": refDoc(block.ID)})
	part := r.mk(r.partSc, "Header with block", map[string]any{"slot": "header", "page_doc": refDoc(block.ID)})
	want("pages, a block and a part", p1, p2, nest, part)
	r.setDoc(nest, headingDoc("h", "no longer"))
	r.setDoc(part, headingDoc("h", "no longer"))
	r.setDoc(p2, headingDoc("h", "plain"))
	want("after removing", p1)

	// Detach p1: fetch the nodes with fresh ids, put them where the ref was, save.
	before := r.get("/" + p1.Slug).Body.String()
	fetch := func() []any {
		rec := r.do("editor", "GET", "/admin/builder/blocks/"+block.ID.String(), nil)
		var out struct{ Nodes []any }
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || len(out.Nodes) != 1 {
			t.Fatalf("block nodes: %d %s", rec.Code, rec.Body.String())
		}
		return out.Nodes
	}
	nodes, again := fetch(), fetch()
	if nodes[0].(map[string]any)["id"] == again[0].(map[string]any)["id"] || nodes[0].(map[string]any)["id"] == "h" {
		t.Fatal("detach ids are not fresh")
	}
	_, version, _ := r.entityRow(p1.ID)
	res := r.do("owner", "POST", "/admin/entities/"+p1.ID.String()+"/builder/save", map[string]any{"version": version, "doc": map[string]any{"v": 2, "nodes": nodes}, "op": "save"})
	if res.Code != 200 {
		t.Fatalf("save detached page: %d %s", res.Code, res.Body.String())
	}
	var attrs string
	if err := r.db.Pool().QueryRow(r.ctx, `SELECT attributes::text FROM hatamoto_entities WHERE id=$1`, p1.ID).Scan(&attrs); err != nil || strings.Contains(attrs, `"ref"`) {
		t.Fatalf("detached page still has a ref: %v %s", err, attrs)
	}
	after := r.get("/" + p1.Slug).Body.String()
	text := func(s string) string {
		return strings.Join(strings.Fields(tagRe.ReplaceAllString(s[strings.Index(s, "<main"):strings.Index(s, "</main>")], " ")), " ")
	}
	if text(before) != text(after) || !strings.Contains(text(after), "SHARED-TEXT") {
		t.Fatalf("visible content changed:\n%q\n%q", text(before), text(after))
	}
	want("after detach")

	// The block is independent now: editing it leaves the detached page alone.
	r.do("owner", "POST", "/admin/entities/"+block.ID.String()+"/builder/save", map[string]any{"version": 1, "doc": headingDoc("h", "CHANGED"), "op": "save"})
	if body := r.get("/" + p1.Slug).Body.String(); strings.Contains(body, "CHANGED") {
		t.Error("a detached page followed the block")
	}
}

// A3/ISC-13/14/15: a tree template inserts with fresh ids each time; an older
// section template inserts as a component.
func TestReuseTemplates_Integration(t *testing.T) {
	r := newReuseIT(t)
	page := r.mk(r.pageSc, "Template source", map[string]any{"page_doc": headingDoc("h", "TPL")})
	doc := map[string]any{"v": 2, "nodes": []any{map[string]any{"id": "box", "t": "container", "c": []any{
		map[string]any{"id": "t1", "t": "text", "p": map[string]any{"text": "One"}},
		map[string]any{"id": "t2", "t": "text", "p": map[string]any{"text": "Two"}}}}}}
	rec := r.do("editor", "POST", "/admin/entities/"+page.ID.String()+"/builder/template", map[string]any{"name": "Two texts B6 " + r.sfx, "nodeId": "box", "doc": doc})
	var saved struct{ ID string }
	if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &saved) != nil || saved.ID == "" {
		t.Fatalf("save template: %d %s", rec.Code, rec.Body.String())
	}
	tid := saved.ID
	t.Cleanup(func() { r.exec(`DELETE FROM hatamoto_section_templates WHERE id=$1`, tid) })

	var kind string
	if err := r.db.Pool().QueryRow(r.ctx, `SELECT section_type FROM hatamoto_section_templates WHERE id=$1`, tid).Scan(&kind); err != nil || kind != "hm-tree" {
		t.Fatalf("stored as %q: %v", kind, err)
	}
	ids := map[string]int{}
	for i := 0; i < 2; i++ {
		rec := r.do("editor", "GET", "/admin/builder/templates/"+tid, nil)
		var out struct {
			Kind  string
			Nodes []struct {
				ID string
				C  []struct{ ID string }
			}
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Kind != "tree" || len(out.Nodes) != 1 || len(out.Nodes[0].C) != 2 {
			t.Fatalf("fetch: %d %s", rec.Code, rec.Body.String())
		}
		ids[out.Nodes[0].ID]++
		for _, c := range out.Nodes[0].C {
			ids[c.ID]++
		}
	}
	if len(ids) != 6 || ids["box"] != 0 || ids["t1"] != 0 {
		t.Fatalf("ids are not unique and fresh across two inserts: %v", ids)
	}

	// An older section template of a type the theme has.
	legacy, err := r.srv.sectionTemplateService.Create(r.ctx, &r.owner.ID, &sectiontemplate.CreateInput{Name: "Old hero B6 " + r.sfx, SectionType: "hero", Content: json.RawMessage(`{"heading":"Hello"}`)})
	if err != nil {
		t.Fatal(err)
	}
	lid := legacy.ID
	t.Cleanup(func() { r.exec(`DELETE FROM hatamoto_section_templates WHERE id=$1`, lid) })
	rec = r.do("editor", "GET", "/admin/builder/templates/"+lid.String(), nil)
	var old struct {
		Kind  string
		Nodes []struct {
			T string
			P map[string]any
		}
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &old) != nil || old.Kind != "section" || len(old.Nodes) != 1 || old.Nodes[0].T != "component" || old.Nodes[0].P["type"] != "hero" {
		t.Fatalf("legacy insert: %d %s", rec.Code, rec.Body.String())
	}
}

// A site type that owns the slug `block` is not the built-in reuse type: its
// entities publish and update like any other, open in the normal editor, and
// a ref never resolves to them.
func TestReuseForeignBlockSchemaIsUntouched_Integration(t *testing.T) {
	r := newReuseIT(t)
	r.exec(`UPDATE hatamoto_entity_schemas SET is_builtin = false WHERE id = $1`, r.blockSc.ID)
	t.Cleanup(func() { r.exec(`UPDATE hatamoto_entity_schemas SET is_builtin = true WHERE id = $1`, r.blockSc.ID) })
	foreign, err := r.srv.entityService.GetSchemaBySlug(r.ctx, entity.BlockSchemaSlug)
	if err != nil || foreign.IsBuiltin {
		t.Fatalf("foreign schema: %+v %v", foreign, err)
	}

	published := entity.StatusPublished
	e := r.mk(foreign, "Own Block", map[string]any{"page_doc": headingDoc("h", "OWN-BLOCK")})
	if _, err := r.srv.entityService.UpdateEntityStatus(r.ctx, e.ID, &r.owner.ID, published); err != nil {
		t.Fatalf("publish: %v", err)
	}
	r.setDoc(e, headingDoc("h", "OWN-BLOCK-2"))
	if status, _, _ := r.entityRow(e.ID); status != "published" {
		t.Fatalf("an update left it %s", status)
	}
	if _, err := r.srv.entityService.UpdateEntityStatus(r.ctx, e.ID, &r.owner.ID, entity.StatusDraft); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if _, err := r.srv.entityService.ScheduleEntity(r.ctx, e.ID, &r.owner.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if status, _, _ := r.entityRow(e.ID); status != "scheduled" {
		t.Fatalf("schedule left it %s", status)
	}

	rec := r.do("owner", "GET", "/admin/entities/"+e.ID.String()+"/edit", nil)
	if rec.Code == http.StatusSeeOther { // the entity's own editor page
		rec = r.do("owner", "GET", rec.Header().Get("Location"), nil)
	}
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "data-hm-bs-title") {
		t.Fatalf("editor: %d, builder open: %v", rec.Code, strings.Contains(rec.Body.String(), "data-hm-bs-title"))
	}

	if _, err := r.srv.entityService.UpdateEntityStatus(r.ctx, e.ID, &r.owner.ID, published); err != nil {
		t.Fatal(err)
	}
	page := r.mk(r.pageSc, "Uses Own Block", map[string]any{"page_doc": refDoc(e.ID)})
	r.publish(page)
	if body := r.get("/" + page.Slug).Body.String(); strings.Contains(body, "OWN-BLOCK") {
		t.Fatalf("a ref resolved to a site-owned block:\n%.400s", body)
	}
}
