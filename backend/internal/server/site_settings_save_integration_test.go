//go:build integration

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/session"
)

// settingsSaveFixture is a one-locale site (language "en") with three entities
// in "en". reset puts the site back to that state between saves.
//
// The tests call localeCheckServer themselves and hand the server over: the
// parallel test sharder (scripts' go wrapper) keeps a test with the database
// beside the other database tests only when the test body names a database
// helper, and these tests rewrite the one shared site_config row.
type settingsSaveFixture struct {
	srv   *Server
	ctx   context.Context
	slugs []string
}

func newSettingsSaveFixture(t *testing.T, srv *Server, ctx context.Context) *settingsSaveFixture {
	t.Helper()
	pool := srv.db.Pool()
	f := &settingsSaveFixture{srv: srv, ctx: ctx, slugs: []string{"ss-one", "ss-two", "ss-three"}}

	var prev struct {
		language, tz, xdef string
		locales            []byte
		prefixAll          bool
	}
	hadSite := pool.QueryRow(ctx, `SELECT language, timezone, x_default_locale, locales, prefix_all FROM hatamoto_site_config`).
		Scan(&prev.language, &prev.tz, &prev.xdef, &prev.locales, &prev.prefixAll) == nil
	if !hadSite {
		if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_site_config (company_name, language) VALUES ('Save IT', 'en')`); err != nil {
			t.Fatal(err)
		}
	}
	var schemaID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM hatamoto_entity_schemas WHERE slug = 'page'`).Scan(&schemaID); err != nil {
		t.Fatal(err)
	}
	f.reset(t)
	for _, s := range f.slugs {
		if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, locale, status, attributes)
			VALUES ($1, $2, $2, 'en', 'draft', '{}'::jsonb)`, schemaID, s); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM hatamoto_entities WHERE slug = ANY($1)`, f.slugs)
		if hadSite {
			_, _ = pool.Exec(c, `UPDATE hatamoto_site_config SET language=$1, timezone=$2, x_default_locale=$3, locales=$4::jsonb, prefix_all=$5`,
				prev.language, prev.tz, prev.xdef, string(prev.locales), prev.prefixAll)
		} else {
			_, _ = pool.Exec(c, `DELETE FROM hatamoto_site_config`)
		}
	})
	return f
}

// reset restores the one-locale site: language "en", no locale list.
func (f *settingsSaveFixture) reset(t *testing.T) {
	t.Helper()
	if _, err := f.srv.db.Pool().Exec(f.ctx, `UPDATE hatamoto_site_config
		SET language = 'en', locales = '[]'::jsonb, x_default_locale = '', prefix_all = false`); err != nil {
		t.Fatal(err)
	}
	f.srv.siteConfig.Invalidate()
}

// assertSaved checks the save kept every entity's locale and stored the new
// language, both locales and the x-default.
func (f *settingsSaveFixture) assertSaved(t *testing.T) {
	t.Helper()
	rows, err := f.srv.db.Pool().Query(f.ctx, `SELECT slug, locale FROM hatamoto_entities WHERE slug = ANY($1)`, f.slugs)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var slug, loc string
		if err := rows.Scan(&slug, &loc); err != nil {
			t.Fatal(err)
		}
		if loc != "en" {
			t.Errorf("entity %s locale = %q, want en (retagged by the save)", slug, loc)
		}
		n++
	}
	if n != len(f.slugs) {
		t.Errorf("found %d fixture entities, want %d", n, len(f.slugs))
	}
	var lang, xdef string
	var locales []byte
	if err := f.srv.db.Pool().QueryRow(f.ctx, `SELECT language, x_default_locale, locales FROM hatamoto_site_config`).Scan(&lang, &xdef, &locales); err != nil {
		t.Fatal(err)
	}
	var list []struct{ Tag, Segment string }
	if err := json.Unmarshal(locales, &list); err != nil {
		t.Fatal(err)
	}
	if lang != "en-XX" || xdef != "en-XY" || len(list) != 2 || list[0].Tag != "en-XX" || list[1].Tag != "en-XY" || list[1].Segment != "yy" {
		t.Errorf("stored language=%q x-default=%q locales=%s, want en-XX / en-XY / [en-XX, en-XY yy]", lang, xdef, locales)
	}
}

// fixedSess is editSess with one session id, so the CSRF token read from a page
// is the one the next request must present.
type fixedSess struct {
	editSess
	id uuid.UUID
}

func (f fixedSess) Validate(_ context.Context, tok string) (*session.Session, error) {
	if tok != "good" {
		return nil, auth.ErrInvalidToken
	}
	u, now := uuid.New(), time.Now()
	return &session.Session{ID: f.id, Kind: session.KindAdmin, UserID: &u, LastUsedAt: &now}, nil
}

// The admin saves language, locale list and x-default through the General and Locales pages. The
// site is single-locale until then; no entity is retagged and the x-default
// (a tag only the posted list contains) is stored.
func TestSettingsSave_AdminForm_Integration(t *testing.T) {
	srv, ctx := localeCheckServer(t)
	f := newSettingsSaveFixture(t, srv, ctx)
	d := f.srv.adminDeps
	d.Sessions, d.Users, d.AdminPath = fixedSess{id: uuid.New()}, editUsers{}, "/admin"
	d.People = nil // no profile-completion detour
	a, err := admin.New(d)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.Mount(httpx.NewGroup(mux, ""))

	do := func(method, path string, body url.Values) *httptest.ResponseRecorder {
		var rd *strings.Reader
		if body != nil {
			rd = strings.NewReader(body.Encode())
		} else {
			rd = strings.NewReader("")
		}
		req := httptest.NewRequest(method, path, rd)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "good"})
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		if method == http.MethodGet {
			req.Header.Set("Sec-Fetch-Mode", "navigate")
			req.Header.Set("Sec-Fetch-Dest", "document")
		} else {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	page := do(http.MethodGet, "/admin/settings/general", nil)
	if page.Code != http.StatusOK {
		t.Fatalf("GET settings: %d %s", page.Code, page.Body)
	}
	m := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if m == nil {
		t.Fatal("no csrf token in the settings page")
	}
	rec := do(http.MethodPost, "/admin/settings/general/identity", url.Values{
		"_csrf": {m[1]}, "name": {"Save IT"}, "url": {"https://save-it.example.test"}, "timezone": {"UTC"},
		"language": {"en-XX"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST identity: %d %s", rec.Code, rec.Body)
	}
	if rec = do(http.MethodPost, "/admin/settings/locales/add", url.Values{"_csrf": {m[1]}, "tag": {"en-XY"}, "segment": {"yy"}, "public": {"true"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST add locale: %d %s", rec.Code, rec.Body)
	}
	if rec = do(http.MethodPost, "/admin/settings/locales/addresses", url.Values{"_csrf": {m[1]}, "xdefault": {"en-XY"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST addresses: %d %s", rec.Code, rec.Body)
	}
	f.assertSaved(t)
}

// The REST API takes the same change in one PATCH.
func TestSettingsSave_RESTPatch_Integration(t *testing.T) {
	srv, ctx := localeCheckServer(t)
	f := newSettingsSaveFixture(t, srv, ctx)
	mux := http.NewServeMux()
	g := httpx.NewGroup(mux, "").Group("", injectRole("owner"))
	mountSiteSettings(g, &siteSettingsHandlers{
		store: f.srv.siteConfig, logger: f.srv.logger, invalidate: f.srv.invalidateSiteSettings,
		checkLocales: f.srv.checkLocaleSegments,
	})
	body := `{"language":"en-XX","locales":[{"tag":"en-XX","segment":""},{"tag":"en-XY","segment":"yy"}],"x_default_locale":"en-XY"}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/settings/site/", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", rec.Code, rec.Body)
	}
	f.assertSaved(t)
}
