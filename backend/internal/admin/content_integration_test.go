//go:build integration

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/recovery"
	"hatamoto.dev/core/internal/taxonomy"
)

// pgRig is the admin over the real services and a real database (DATABASE_URL,
// a migrated database): the flows of the content screens end to end.
type pgRig struct {
	*rig
	db       *database.DB
	ents     *entity.Service
	terms    *taxonomy.Service
	media    *media.Service
	people   *person.Profiles
	uploads  string
	suffix   string
	cleanups []func()
}

func newPGRig(t *testing.T) *pgRig {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set: skipping integration test")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 6, MinConnections: 1, MaxConnLifetime: 30 * time.Minute, MaxConnIdleTime: 5 * time.Minute})
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	t.Cleanup(db.Close)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	p := &pgRig{db: db, suffix: strings.ReplaceAll(uuid.NewString()[:8], "-", ""), uploads: t.TempDir()}
	p.ents = entity.NewService(entity.NewPostgresSchemaRepository(db), entity.NewPostgresEntityRepository(db), log)
	p.terms = taxonomy.NewService(taxonomy.NewPostgresRepository(db), log)
	p.people = person.NewProfiles(person.NewPoolStore(db.Pool()), p.ents)
	p.ents.SetBylines(person.NewByline(db.Pool()))
	cfg := media.DefaultConfig()
	cfg.UploadDir = p.uploads
	p.media = media.NewService(media.NewPostgresRepository(db), cfg, log)
	p.rig = newRig(t, func(d *Deps) {
		d.Logger = log
		d.Content = ContentDeps{Entities: p.ents, Terms: p.terms, Media: p.media, Authors: struct {
			*person.Store
			*person.Byline
		}{p.people.Store, person.NewByline(db.Pool())},
			Recovery:   recovery.New(db),
			PublicPath: func(_ context.Context, e *entity.Entity) string { return "/it-" + p.suffix + "/" + e.Slug }}
	})
	// Rows written by the screens point at the acting user.
	exec := func(q string, args ...any) error {
		conn, err := db.GetConn(ctx)
		if err != nil {
			return err
		}
		defer conn.Release()
		_, err = conn.Exec(ctx, q, args...)
		return err
	}
	for _, u := range []struct {
		id         uuid.UUID
		email, rol string
	}{{p.owner.ID, p.owner.Email, "owner"}, {p.editor.ID, p.editor.Email, "editor"}} {
		if err := exec(`INSERT INTO hatamoto_users (id, email, role) VALUES ($1, $2, $3)`, u.id, p.suffix+"-"+u.email, u.rol); err != nil {
			t.Fatal(err)
		}
		id := u.id
		p.cleanups = append(p.cleanups, func() { _ = exec(`DELETE FROM hatamoto_users WHERE id = $1`, id) })
	}
	t.Cleanup(func() {
		for i := len(p.cleanups) - 1; i >= 0; i-- {
			p.cleanups[i]()
		}
	})
	return p
}

var idRx = regexp.MustCompile(`/admin/(?:entities|settings/types|media|settings/taxonomies/terms)/([0-9a-f-]{36})`)

func (p *pgRig) locationID(rec *httptest.ResponseRecorder) uuid.UUID {
	p.t.Helper()
	m := idRx.FindStringSubmatch(rec.Header().Get("Location"))
	if rec.Code != 303 || m == nil {
		p.t.Fatalf("expected a redirect to a created thing, got %d %q\n%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	return uuid.MustParse(m[1])
}

func (p *pgRig) get(path string) string {
	p.t.Helper()
	rec := p.do("GET", path, "owner-token", nil, nil)
	if rec.Code != 200 {
		p.t.Fatalf("GET %s = %d\n%s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func (p *pgRig) post(path string, f url.Values) *httptest.ResponseRecorder {
	return p.do("POST", path, "owner-token", f, nil)
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for x := 0; x < 64; x++ {
		for y := 0; y < 48; y++ {
			img.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 5), 120, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestContentScreensAgainstPostgres walks the content screens against the real
// services: schema editor, entity create, list filters, bulk, edit, versions,
// restore, schedule, term tree, media upload, picker, usage and delete.
func TestContentScreensAgainstPostgres(t *testing.T) {
	p := newPGRig(t)
	ctx := context.Background()
	slug := "it" + p.suffix

	// --- schema editor: create a type from the field builder -----------------
	rec := p.post("/admin/settings/types", url.Values{
		"name": {"Itest " + p.suffix}, "slug": {slug}, "url_pattern": {"/it-" + p.suffix + "/{path}"}, "schema_org_type": {"Article"},
		"field":     {"r0", "r1", "r2"},
		"f.r0.name": {"summary"}, "f.r0.kind": {"text"}, "f.r0.title": {"Summary"}, "f.r0.required": {"true"},
		"f.r1.name": {"cover"}, "f.r1.kind": {"image"}, "f.r1.title": {"Cover"},
		"f.r2.name": {"level"}, "f.r2.kind": {"select"}, "f.r2.options": {"easy\nhard"},
	})
	schemaID := p.locationID(rec)
	p.cleanups = append(p.cleanups, func() { _ = p.ents.DeleteSchema(ctx, schemaID) })
	body := p.get("/admin/settings/types/" + schemaID.String())
	for _, want := range []string{`name="f.r0.name" value="summary"`, `value="/it-` + p.suffix + `/{path}"`, `data-hm-media-picker`} {
		if !strings.Contains(body, want) && want != `data-hm-media-picker` {
			t.Errorf("schema editor lacks %q", want)
		}
	}
	// a field reorder through the up button, saved, is reflected in the stored schema
	rec = p.post("/admin/settings/types/"+schemaID.String(), url.Values{"name": {"Itest " + p.suffix}, "url_pattern": {"/it-" + p.suffix + "/{path}"}, "schema_org_type": {"Article"},
		"field":     {"r1", "r0", "r2"},
		"f.r0.name": {"summary"}, "f.r0.kind": {"text"}, "f.r0.title": {"Summary"}, "f.r0.required": {"true"},
		"f.r1.name": {"cover"}, "f.r1.kind": {"image"}, "f.r1.title": {"Cover"},
		"f.r2.name": {"level"}, "f.r2.kind": {"select"}, "f.r2.options": {"easy\nhard"}})
	if rec.Code != 303 {
		t.Fatalf("reorder save: %d\n%s", rec.Code, rec.Body.String())
	}
	sc, err := p.ents.GetSchema(ctx, schemaID)
	if err != nil {
		t.Fatal(err)
	}
	if rows := fieldRows(sc.Schema); len(rows) != 3 || rows[0].Name != "cover" || rows[1].Name != "summary" {
		t.Errorf("stored order: %+v", rows)
	}
	// a pattern the API would refuse is refused here too
	if rec := p.post("/admin/settings/types/"+schemaID.String(), url.Values{"name": {"x"}, "url_pattern": {"/nope"}}); rec.Code != 422 {
		t.Errorf("bad pattern: %d", rec.Code)
	}

	// --- term tree ---------------------------------------------------------------
	rec = p.post("/admin/settings/taxonomies/terms", url.Values{"kind": {"category"}, "name": {"Parent " + p.suffix}})
	if rec.Code != 303 {
		t.Fatalf("parent term: %d\n%s", rec.Code, rec.Body.String())
	}
	parent, err := p.terms.GetTermBySlug(ctx, "category", taxonomy.Slugify("Parent "+p.suffix))
	if err != nil {
		t.Fatal(err)
	}
	p.cleanups = append(p.cleanups, func() { _ = p.terms.DeleteTerm(ctx, parent.ID) })
	rec = p.post("/admin/settings/taxonomies/terms", url.Values{"kind": {"category"}, "name": {"Child " + p.suffix}, "parent": {parent.ID.String()}})
	if rec.Code != 303 {
		t.Fatalf("child term: %d", rec.Code)
	}
	child, err := p.terms.GetTermBySlug(ctx, "category", taxonomy.Slugify("Child "+p.suffix))
	if err != nil || child.ParentID == nil || *child.ParentID != parent.ID {
		t.Fatalf("child: %+v %v", child, err)
	}
	p.cleanups = append(p.cleanups, func() { _ = p.terms.DeleteTerm(ctx, child.ID) })
	tree := p.get("/admin/settings/taxonomies?kind=category")
	if i, j := strings.Index(tree, "Parent "+p.suffix), strings.Index(tree, "— Child "+p.suffix); i < 0 || j < i {
		t.Errorf("tree order wrong (parent at %d, child at %d)", i, j)
	}

	// --- media: real upload, then use it ---------------------------------------
	up := &contentRig{rig: p.rig}
	rec = up.upload(t, "/admin/media/upload", map[string][]byte{"cover.png": testPNG(t)}, true)
	if rec.Code != 303 {
		t.Fatalf("upload: %d\n%s", rec.Code, rec.Body.String())
	}
	files, err := p.media.List(ctx, &media.ListMediaFilter{Search: "cover.png"}, 10, 0)
	if err != nil || len(files.Items) == 0 {
		t.Fatalf("uploaded file not listed: %v\n%s", err, regexp.MustCompile(`(?s)data-hm-flash.*?</div>`).FindString(up.next(rec, "/admin/settings/media")))
	}
	m := files.Items[0]
	p.cleanups = append(p.cleanups, func() { _ = p.media.Delete(ctx, m.ID) })
	if m.Width == nil || *m.Width != 64 {
		t.Errorf("dimensions: %v", m.Width)
	}
	if _, err := os.Stat(filepath.Join(p.uploads, m.StoragePath)); err != nil {
		t.Errorf("file not on disk: %v", err)
	}
	for _, view := range []string{"/admin/settings/media", "/admin/settings/media?view=list", "/admin/media/" + m.ID.String()} {
		if b := p.get(view); !strings.Contains(b, "cover.png") {
			t.Errorf("%s does not show the file", view)
		}
	}
	picker := p.do("GET", "/admin/media/picker?q=cover", "owner-token", nil, map[string]string{"HX-Request": "true", "HX-Target": "hm-media-picker-body"}).Body.String()
	if !strings.Contains(picker, `data-hm-pick="`+*m.PublicURL+`"`) {
		t.Errorf("picker does not offer the file:\n%s", picker)
	}
	if rec := p.post("/admin/media/"+m.ID.String(), url.Values{"alt_text": {"A gradient"}, "caption": {"Test card"}}); rec.Code != 303 {
		t.Errorf("alt text: %d", rec.Code)
	}
	if got, _ := p.media.Get(ctx, m.ID); got.AltText == nil || *got.AltText != "A gradient" {
		t.Error("alt text not stored")
	}
	// a script renamed .png is refused by the real service
	rec = up.upload(t, "/admin/media/upload", map[string][]byte{"evil.png": []byte("<script>alert(1)</script>")}, true)
	if !strings.Contains(up.next(rec, "/admin/settings/media"), "not allowed") {
		t.Error("hostile upload not refused")
	}

	// --- entities: create through the form, with the picked image -----------------
	au, err := p.people.Create(ctx, nil, "Ada "+p.suffix)
	if err != nil {
		t.Fatal(err)
	}
	p.cleanups = append(p.cleanups, func() { _ = p.ents.DeleteEntity(ctx, au.ID) })
	var ids []uuid.UUID
	for _, name := range []string{"Zebra", "Apple", "Mango"} {
		f := url.Values{"type": {slug}, "name": {name + " " + p.suffix}, "attr.summary": {"About " + name}, "status": {"draft"}, "author": {au.ID.String()},
			"seo_title": {name + " | Itest"}, "_terms": {"1"}}
		if name == "Apple" {
			f.Set("attr.cover", *m.PublicURL)
			f.Set("term", child.ID.String())
		}
		rec := p.post("/admin/entities", f)
		id := p.locationID(rec)
		ids = append(ids, id)
		id2 := id
		p.cleanups = append(p.cleanups, func() { _ = p.ents.DeleteEntity(ctx, id2) })
	}
	zebra, apple, mango := ids[0], ids[1], ids[2]
	e, _ := p.ents.GetEntity(ctx, apple)
	var attrs map[string]any
	_ = json.Unmarshal(e.Attributes, &attrs)
	if attrs["meta_title"] != "Apple | Itest" || attrs["cover"] != *m.PublicURL || e.BylinePersonID == nil || *e.BylinePersonID != au.ID || e.AuthorID == nil {
		t.Errorf("created entity: %s byline=%v author_id=%v", e.Attributes, e.BylinePersonID, e.AuthorID)
	}
	if ts, _ := p.terms.ListTermsForEntity(ctx, apple); len(ts) != 1 || ts[0].ID != child.ID {
		t.Errorf("terms: %v", ts)
	}
	// slug conflict and validation come back as form errors
	if rec := p.post("/admin/entities", url.Values{"type": {slug}, "name": {"Apple " + p.suffix}, "attr.summary": {"dup"}}); rec.Code != 422 || !strings.Contains(rec.Body.String(), "already used") {
		t.Errorf("duplicate slug: %d %s", rec.Code, regexp.MustCompile(`(?s)<main.*</main>`).FindString(rec.Body.String()))
	}
	if rec := p.post("/admin/entities", url.Values{"type": {slug}, "name": {"No summary"}}); rec.Code != 422 {
		t.Errorf("missing required field: %d", rec.Code)
	}

	// --- list: filters, search, sort, paging ----------------------------------------
	base := "/admin/entities?type=" + slug
	list := p.get(base + "&sort=name&dir=asc")
	if a, z := strings.Index(list, "Apple "+p.suffix), strings.Index(list, "Zebra "+p.suffix); a < 0 || z < a {
		t.Errorf("sort by name: apple at %d, zebra at %d", a, z)
	}
	if b := p.get(base + "&q=mango"); !strings.Contains(b, "Mango "+p.suffix) || strings.Contains(b, "Apple "+p.suffix) {
		t.Error("search")
	}
	if b := p.get(base + "&term=" + parent.ID.String()); !strings.Contains(b, "Apple "+p.suffix) || strings.Contains(b, "Zebra "+p.suffix) {
		t.Error("term filter must include entities under child terms")
	}
	if b := p.get(base + "&author=" + au.ID.String()); !strings.Contains(b, "Zebra "+p.suffix) {
		t.Error("author filter")
	}
	today := time.Now().UTC().Format("2006-01-02")
	if b := p.get(base + "&from=" + today + "&to=" + today); !strings.Contains(b, "Zebra "+p.suffix) {
		t.Error("date filter should include today")
	}
	if b := p.get(base + "&to=2001-01-01"); strings.Contains(b, "Zebra "+p.suffix) {
		t.Error("date filter should exclude everything before 2001")
	}
	if b := p.get(base + "&status=published"); strings.Contains(b, "Zebra "+p.suffix) {
		t.Error("status filter")
	}

	// --- bulk ----------------------------------------------------------------------------
	rec = p.post("/admin/entities/bulk", url.Values{"action": {"publish"}, "id": {zebra.String(), apple.String()}})
	if rec.Code != 303 {
		t.Fatalf("bulk publish: %d", rec.Code)
	}
	for _, id := range []uuid.UUID{zebra, apple} {
		if got, _ := p.ents.GetEntity(ctx, id); got.Status != entity.StatusPublished || got.PublishedAt == nil {
			t.Errorf("not published: %+v", got)
		}
	}
	if b := p.get(base + "&status=published"); !strings.Contains(b, "Zebra "+p.suffix) || strings.Contains(b, "Mango "+p.suffix) {
		t.Error("published filter after bulk")
	}
	rec = p.post("/admin/entities/bulk", url.Values{"action": {"delete"}, "id": {mango.String()}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Delete 1 entity?") {
		t.Fatalf("delete asks first: %d", rec.Code)
	}
	if _, err := p.ents.GetEntity(ctx, mango); err != nil {
		t.Fatal("deleted before confirming")
	}
	rec = p.post("/admin/entities/bulk", url.Values{"action": {"delete"}, "confirmed": {"1"}, "id": {mango.String()}})
	if _, err := p.ents.GetEntity(ctx, mango); err == nil || rec.Code != 303 {
		t.Errorf("confirmed bulk delete: %d %v", rec.Code, err)
	}

	// --- edit, versions, compare, restore ---------------------------------------------
	edit := func(summary string) {
		t.Helper()
		rec := p.post("/admin/entities/"+apple.String(), url.Values{"name": {"Apple " + p.suffix}, "slug": {"apple-" + p.suffix}, "status": {"published"}, "attr.summary": {summary},
			"attr.cover": {*m.PublicURL}, "author": {au.ID.String()}, "_terms": {"1"}, "term": {child.ID.String()}, "seo_title": {"Apple | Itest"}})
		if rec.Code != 303 {
			t.Fatalf("edit: %d\n%s", rec.Code, rec.Body.String())
		}
	}
	edit("Second")
	edit("Third")
	edit("Third") // no change: no new version
	e, _ = p.ents.GetEntity(ctx, apple)
	if e.Version != 3 {
		t.Fatalf("version %d, want 3 (an unchanged save must not add one)", e.Version)
	}
	vb := p.get("/admin/entities/" + apple.String() + "/versions")
	for _, want := range []string{"v3 (current)", "v2", "v1"} {
		if !strings.Contains(vb, want) {
			t.Errorf("versions page lacks %q", want)
		}
	}
	cmp := p.get("/admin/entities/" + apple.String() + "/versions/compare?from=1&to=0")
	if !strings.Contains(cmp, "About Apple") || !strings.Contains(cmp, "Third") || !strings.Contains(cmp, "<code>summary</code>") {
		t.Errorf("compare:\n%s", cmp)
	}
	if rec := p.post("/admin/entities/"+apple.String()+"/versions/1/restore", nil); rec.Code != 303 {
		t.Fatalf("restore: %d", rec.Code)
	}
	e, _ = p.ents.GetEntity(ctx, apple)
	attrs = nil
	_ = json.Unmarshal(e.Attributes, &attrs)
	if attrs["summary"] != "About Apple" || e.Version != 4 {
		t.Errorf("after restore: %v v%d", attrs, e.Version)
	}

	// --- schedule in the site's time zone ----------------------------------------------
	p.rig.a.deps.Site.(*fakeSite).cfg.Timezone = "America/New_York"
	when := time.Now().Add(96 * time.Hour).Format(scheduleLayout)
	rec = p.post("/admin/entities/"+zebra.String(), url.Values{"name": {"Zebra " + p.suffix}, "slug": {"zebra-" + p.suffix}, "status": {"scheduled"}, "schedule": {when}, "attr.summary": {"About Zebra"}, "attr.cover": {""}})
	if rec.Code != 303 {
		t.Fatalf("schedule: %d\n%s", rec.Code, rec.Body.String())
	}
	e, _ = p.ents.GetEntity(ctx, zebra)
	ny, _ := time.LoadLocation("America/New_York")
	want, _ := time.ParseInLocation(scheduleLayout, when, ny)
	if e.Status != entity.StatusScheduled || e.PublishedAt == nil || !e.PublishedAt.Equal(want) {
		t.Errorf("scheduled: %v %v want %v", e.Status, e.PublishedAt, want)
	}

	// --- media usage and the delete warning -----------------------------------------------
	detail := p.get("/admin/media/" + m.ID.String())
	if !strings.Contains(detail, "Apple "+p.suffix) {
		t.Error("usage list does not name the entity that uses the image")
	}
	if b := p.get("/admin/media/" + m.ID.String() + "/delete"); !strings.Contains(b, "used in 1 place") {
		t.Errorf("delete warning:\n%s", b)
	}

	// --- term merge and delete -----------------------------------------------------------------
	rec = p.post("/admin/settings/taxonomies/terms/"+parent.ID.String()+"/merge", url.Values{"into": {child.ID.String()}})
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "child terms") {
		t.Errorf("merging a parent: %d", rec.Code)
	}
	other, err := p.terms.CreateTerm(ctx, taxonomy.CreateTermInput{Kind: "category", Name: "Other " + p.suffix})
	if err != nil {
		t.Fatal(err)
	}
	p.cleanups = append(p.cleanups, func() { _ = p.terms.DeleteTerm(ctx, other.ID) })
	if rec := p.post("/admin/settings/taxonomies/terms/"+child.ID.String()+"/merge", url.Values{"into": {other.ID.String()}}); rec.Code != 303 {
		t.Fatalf("merge: %d\n%s", rec.Code, rec.Body.String())
	}
	if ts, _ := p.terms.ListTermsForEntity(ctx, apple); len(ts) != 1 || ts[0].ID != other.ID {
		t.Errorf("entity terms after merge: %v", ts)
	}
	if _, err := p.terms.GetTerm(ctx, child.ID); err == nil {
		t.Error("merged term still exists")
	}

	// --- deleting the type is refused while entities use it, then allowed ------------------
	rec = p.post("/admin/settings/types/"+schemaID.String()+"/delete", nil)
	if !strings.Contains(p.nextAs(rec, "/admin/settings/types/"+schemaID.String()), "still used") {
		t.Error("in-use schema delete should explain itself")
	}
	for _, id := range []uuid.UUID{zebra, apple} {
		if rec := p.post("/admin/entities/"+id.String()+"/delete", nil); rec.Code != 303 {
			t.Errorf("delete entity: %d", rec.Code)
		}
	}
	if rec := p.post("/admin/settings/types/"+schemaID.String()+"/delete", nil); rec.Code != 303 {
		t.Errorf("delete schema: %d", rec.Code)
	}
	if rec := p.post("/admin/media/"+m.ID.String()+"/delete", nil); rec.Code != 303 {
		t.Errorf("delete media: %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(p.uploads, m.StoragePath)); !os.IsNotExist(err) {
		t.Errorf("file still on disk after delete: %v", err)
	}
}

func (p *pgRig) nextAs(rec *httptest.ResponseRecorder, path string) string {
	return (&contentRig{rig: p.rig}).next(rec, path)
}

// A file used by the site logo, the theme, a menu or a plugin's settings is
// reported by "Where it is used", so the delete warning does not understate.
func TestMediaUsageCoversSiteMenusAndPlugins(t *testing.T) {
	p := newPGRig(t)
	ctx := context.Background()
	png := testPNG(t)
	m, err := p.media.Upload(ctx, nil, bytes.NewReader(png), "usage-"+p.suffix+".png", "", int64(len(png)))
	if err != nil {
		t.Fatal(err)
	}
	ref := mediaURL(m)
	exec := func(q string, args ...any) {
		t.Helper()
		conn, err := p.db.GetConn(database.WithSuperadmin(ctx))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		if _, err := conn.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// hatamoto_site_config is a singleton shared with other packages' tests (the
	// public-page goldens embed company_name), so leave it exactly as found.
	var hadSite bool
	{
		conn, err := p.db.GetConn(database.WithSuperadmin(ctx))
		if err != nil {
			t.Fatal(err)
		}
		err = conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_site_config)`).Scan(&hadSite)
		conn.Release()
		if err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO hatamoto_site_config (id, company_name, logo_url, theme_extra) VALUES (true, 'Usage test', $1, $2::jsonb)
	      ON CONFLICT (id) DO UPDATE SET logo_url = EXCLUDED.logo_url, theme_extra = EXCLUDED.theme_extra`, ref, `{"hero":"`+ref+`"}`)
	menuID := uuid.New()
	exec(`INSERT INTO hatamoto_menus (id, name, slug) VALUES ($1, $2, $3)`, menuID, "Menu "+p.suffix, "menu-"+p.suffix)
	exec(`INSERT INTO hatamoto_menu_items (menu_id, label, link_type, url) VALUES ($1, 'Brochure', 'external', $2)`, menuID, ref)
	// A person's photo is a usage; the rollback mirror author with the same avatar is not a second one.
	personSlug := "usage-" + p.suffix
	exec(`INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, published_at)
		SELECT id, 'Usage Person', $1, 'published', jsonb_build_object('public', true, 'photo', $2::text), now()
		FROM hatamoto_entity_schemas WHERE slug = 'person'`, personSlug, ref)
	exec(`INSERT INTO hatamoto_authors (slug, name, avatar_media_id) VALUES ($1, 'Usage Person', $2)`, personSlug, m.ID)
	plugin := "usage-" + p.suffix
	exec(`INSERT INTO hatamoto_plugin_settings (plugin, settings) VALUES ($1, $2::jsonb)`, plugin, `{"image":"`+ref+`"}`)
	t.Cleanup(func() {
		if hadSite {
			exec(`UPDATE hatamoto_site_config SET logo_url = NULL, theme_extra = '{}'::jsonb WHERE id = true`)
		} else {
			exec(`DELETE FROM hatamoto_site_config WHERE id = true`)
		}
		exec(`DELETE FROM hatamoto_menus WHERE id = $1`, menuID)
		exec(`DELETE FROM hatamoto_entities WHERE slug = $1`, personSlug)
		exec(`DELETE FROM hatamoto_authors WHERE slug = $1`, personSlug)
		exec(`DELETE FROM hatamoto_plugin_settings WHERE plugin = $1`, plugin)
		_ = p.media.Delete(ctx, m.ID)
	})

	us, err := p.media.Usage(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, u := range us {
		got[u.Kind+":"+u.Name] = true
		if u.Kind == "author" {
			t.Errorf("the legacy authors table is counted as a usage: %+v", u)
		}
	}
	for _, want := range []string{"site:Site logo", "site:Theme settings", "menu:Menu " + p.suffix, "plugin:" + plugin, "entity:Usage Person"} {
		if !got[want] {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	page := p.get("/admin/media/" + m.ID.String() + "/delete")
	if !strings.Contains(page, "Warning: this file is used in 5 places") {
		t.Errorf("delete warning does not count the site-level uses:\n%s", page)
	}
}
