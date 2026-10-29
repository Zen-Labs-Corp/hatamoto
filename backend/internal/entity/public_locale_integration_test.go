//go:build integration

package entity

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// publicLocaleFixture inserts published entities directly: slug "same-*"
// exists in the site language and in another locale, "only-*" only in the
// other locale.
type publicLocaleFixture struct {
	repo                      *PostgresPublicRepository
	schemaSlug                string
	site, other               string
	sameSite, sameOther, only uuid.UUID
	sameSlug, onlySlug        string
}

func newPublicLocaleFixture(t *testing.T) *publicLocaleFixture {
	t.Helper()
	db := newIntegrationDB(t)
	ctx := context.Background()
	f := &publicLocaleFixture{repo: NewPostgresPublicRepository(db), schemaSlug: "pl-" + uuid.NewString()[:8]}
	if err := db.Pool().QueryRow(ctx, `SELECT public.hatamoto_site_language()`).Scan(&f.site); err != nil {
		t.Fatal(err)
	}
	f.other = "fr"
	if f.site == "fr" {
		f.other = "en"
	}
	schemaID := uuid.New()
	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}','{}')`,
		schemaID, f.schemaSlug); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
	})
	f.sameSlug, f.onlySlug = "same-"+uuid.NewString()[:8], "only-"+uuid.NewString()[:8]
	f.sameSite, f.sameOther, f.only = uuid.New(), uuid.New(), uuid.New()
	for _, r := range []struct {
		id           uuid.UUID
		slug, locale string
	}{{f.sameSite, f.sameSlug, f.site}, {f.sameOther, f.sameSlug, f.other}, {f.only, f.onlySlug, f.other}} {
		if _, err := db.Pool().Exec(ctx,
			`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,attributes,published_at)
			 VALUES ($1,$2,$3,$3,$4,'published','{}',now())`,
			r.id, schemaID, r.slug, r.locale); err != nil {
			t.Fatal(err)
		}
		_ = r
	}
	return f
}

func TestPublicReads_OmitOtherLocale(t *testing.T) {
	f := newPublicLocaleFixture(t)
	ctx := context.Background()
	schemas := []string{f.schemaSlug}

	if e, err := f.repo.GetPublishedBySlug(ctx, f.sameSlug); err != nil || e.ID != f.sameSite {
		t.Errorf("by slug: %v, %v; want the site-language entity", e, err)
	}
	if _, err := f.repo.GetPublishedBySlug(ctx, f.onlySlug); !errors.Is(err, ErrEntityNotFound) {
		t.Errorf("by slug other-only = %v, want not found", err)
	}
	if e, err := f.repo.GetPublishedBySlugAndSchemas(ctx, f.sameSlug, schemas); err != nil || e.ID != f.sameSite {
		t.Errorf("by slug+schema: %v, %v", e, err)
	}
	if _, err := f.repo.GetPublishedBySlugAndSchemas(ctx, f.onlySlug, schemas); !errors.Is(err, ErrEntityNotFound) {
		t.Errorf("by slug+schema other-only = %v", err)
	}
	if e, err := f.repo.GetPublishedByPath(ctx, []string{f.sameSlug}, schemas); err != nil || e.ID != f.sameSite {
		t.Errorf("by path: %v, %v", e, err)
	}
	if _, err := f.repo.GetPublishedByPath(ctx, []string{f.onlySlug}, schemas); !errors.Is(err, ErrEntityNotFound) {
		t.Errorf("by path other-only = %v", err)
	}
	if _, err := f.repo.GetPublishedByID(ctx, f.only); !errors.Is(err, ErrEntityNotFound) {
		t.Errorf("by id other = %v", err)
	}
	got, err := f.repo.GetPublishedByIDs(ctx, []uuid.UUID{f.sameSite, f.sameOther, f.only})
	if err != nil || len(got) != 1 || got[0].ID != f.sameSite {
		t.Errorf("by ids: %v, %v", got, err)
	}

	res, err := f.repo.ListPublishedBySchemas(ctx, schemas, 1, 50)
	if err != nil || res.Total != 1 || len(res.Items) != 1 || res.Items[0].ID != f.sameSite {
		t.Errorf("list: %+v, %v", res, err)
	}
	filtered, total, err := f.repo.ListPublishedFiltered(ctx, ListFilter{SchemaSlugs: schemas}, 1, 50)
	if err != nil || total != 1 || len(filtered) != 1 || filtered[0].ID != f.sameSite {
		t.Errorf("filtered: %d, %v", total, err)
	}
	all, err := f.repo.ListAllPublishedWithAttrs(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range all {
		if s.ID == f.sameOther || s.ID == f.only {
			t.Errorf("sitemap entries include other-locale entity %s", s.ID)
		}
	}
}

// getFirstPublishedPage must skip a page that is not in the site language.
func TestFirstPublishedPage_SkipsOtherLocale(t *testing.T) {
	db := newIntegrationDB(t)
	ctx := context.Background()
	repo := NewPostgresPublicRepository(db)
	var pageSchema uuid.UUID
	if err := db.Pool().QueryRow(ctx, `SELECT id FROM hatamoto_entity_schemas WHERE slug = 'page'`).Scan(&pageSchema); err != nil {
		t.Skip("no page schema")
	}
	var site string
	if err := db.Pool().QueryRow(ctx, `SELECT public.hatamoto_site_language()`).Scan(&site); err != nil {
		t.Fatal(err)
	}
	other := "fr"
	if site == "fr" {
		other = "en"
	}
	id := uuid.New()
	// created_at far in the past so it would win the ORDER BY if not filtered.
	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,attributes,published_at,created_at)
		 VALUES ($1,$2,'First other','first-other-'||$3,$4,'published','{}',now(),'1990-01-01')`,
		id, pageSchema, id.String()[:8], other); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_entities WHERE id = $1`, id) })
	e, err := repo.getFirstPublishedPage(ctx)
	if err != nil {
		return // no site-language page at all: also correct
	}
	if e.ID == id || e.Locale != site {
		t.Errorf("first page = %s locale %q, want a %q page", e.ID, e.Locale, site)
	}
}

// After the site language changes (trigger re-tag), the same rows are public
// in the new language. Done in a rolled-back transaction so other tests never
// see the changed site language.
func TestPublicLocale_FollowsSiteLanguageChange(t *testing.T) {
	f := newPublicLocaleFixture(t)
	db := newIntegrationDB(t)
	ctx := context.Background()
	tx, err := db.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The shared test database may have no site config row: add one inside
	// the transaction (rolled back) so the language UPDATE has a row to fire on.
	if _, err := tx.Exec(ctx,
		`INSERT INTO hatamoto_site_config (company_name, language) VALUES ('t', $1) ON CONFLICT DO NOTHING`, f.site); err != nil {
		t.Fatal(err)
	}
	// f.sameSite would collide with f.sameOther in the new language, so the
	// re-tag skips it; f.sameOther and f.only are already in the new language.
	if _, err := tx.Exec(ctx, `UPDATE hatamoto_site_config SET language = $1`, f.other); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		WHERE s.slug = $1 AND `+HiddenPersonFor("$2"), f.schemaSlug, nil).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("public rows after site language change = %d, want 2 (same slug and only-slug in %s)", n, f.other)
	}
}

// The site language is compared through an uncorrelated scalar subquery so
// Postgres evaluates the non-inlinable SECURITY DEFINER function once per
// statement (an InitPlan), not once per candidate row.
func TestHiddenPersonSQLSiteLanguageIsInitPlan(t *testing.T) {
	db := newIntegrationDB(t)
	ctx := context.Background()
	rows, err := db.Pool().Query(ctx, `EXPLAIN (VERBOSE, COSTS OFF) SELECT count(*) FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON s.id = e.schema_id WHERE e.status = 'published' AND `+HiddenPersonFor("NULL"))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan += line + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "InitPlan") {
		t.Fatalf("site language is not evaluated as an InitPlan:\n%s", plan)
	}
}

// With a locale scope the public reads serve that locale plus shared content
// and hide the site language; the first-page fallback follows the scope.
func TestPublicReads_LocaleScope(t *testing.T) {
	f := newPublicLocaleFixture(t)
	db := newIntegrationDB(t)
	ctx := WithVisibleLocales(context.Background(), f.other)
	schemas := []string{f.schemaSlug}

	var schemaID uuid.UUID
	if err := db.Pool().QueryRow(ctx, `SELECT id FROM hatamoto_entity_schemas WHERE slug = $1`, f.schemaSlug).Scan(&schemaID); err != nil {
		t.Fatal(err)
	}
	shared := uuid.New()
	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,attributes,published_at)
		 VALUES ($1,$2,'shared','shared-'||$3,$4,'published','{}',now())`,
		shared, schemaID, shared.String()[:8], LocaleShared); err != nil {
		t.Fatal(err)
	}

	if e, err := f.repo.GetPublishedBySlug(ctx, f.sameSlug); err != nil || e.ID != f.sameOther {
		t.Errorf("by slug in scope: %v, %v; want the scoped entity", e, err)
	}
	if e, err := f.repo.GetPublishedBySlug(ctx, f.onlySlug); err != nil || e.ID != f.only {
		t.Errorf("by slug other-only in scope: %v, %v", e, err)
	}
	if e, err := f.repo.GetPublishedByID(ctx, shared); err != nil || e.ID != shared {
		t.Errorf("shared by id: %v, %v", e, err)
	}
	if _, err := f.repo.GetPublishedByID(ctx, f.sameSite); !errors.Is(err, ErrEntityNotFound) {
		t.Errorf("site-language entity in scope = %v, want not found", err)
	}
	res, err := f.repo.ListPublishedBySchemas(ctx, schemas, 1, 50)
	if err != nil || res.Total != 3 {
		t.Errorf("list in scope: %+v, %v; want 3 (scoped x2 + shared)", res, err)
	}
	filtered, total, err := f.repo.ListPublishedFiltered(ctx, ListFilter{SchemaSlugs: schemas}, 1, 50)
	if err != nil || total != 3 || len(filtered) != 3 {
		t.Errorf("filtered in scope: %d, %v", total, err)
	}
	counts, err := f.repo.ListAttrValues(ctx, schemas, "nope", 10)
	if err != nil || len(counts) != 0 {
		t.Errorf("attr values: %v, %v", counts, err)
	}
}

// The first-page fallback of the home resolution follows the locale scope.
func TestFirstPublishedPage_FollowsLocaleScope(t *testing.T) {
	db := newIntegrationDB(t)
	repo := NewPostgresPublicRepository(db)
	var pageSchema uuid.UUID
	if err := db.Pool().QueryRow(context.Background(), `SELECT id FROM hatamoto_entity_schemas WHERE slug = 'page'`).Scan(&pageSchema); err != nil {
		t.Skip("no page schema")
	}
	id := uuid.New()
	if _, err := db.Pool().Exec(context.Background(),
		`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,attributes,published_at,created_at)
		 VALUES ($1,$2,'First scoped','first-scoped-'||$3,'nl','published','{}',now(),'1980-01-01')`,
		id, pageSchema, id.String()[:8]); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_entities WHERE id = $1`, id) })
	e, err := repo.getFirstPublishedPage(WithVisibleLocales(context.Background(), "nl"))
	if err != nil || e.ID != id {
		t.Errorf("scoped first page = %v, %v; want %s", e, err, id)
	}
}
