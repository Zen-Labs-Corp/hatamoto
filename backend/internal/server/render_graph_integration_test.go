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
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

var graphLDRE = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)

// The site graph over the full server, for three locales: en (office), fr (no
// office) and nl (office), plus a shared page. The assertions look at key sets
// and values, not bytes.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run RenderGraph
func TestRenderGraph_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set - skipping site graph integration test")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "graph-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Example Co")
	t.Setenv("PUBLIC_BASE_URL", "https://graph-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	sfx := "x" + uuid.NewString()[:5]
	schemaID := uuid.New()
	slug, sharedSlug := "about-"+sfx, "shared-"+sfx
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, "rg-"+sfx, `{"url_pattern":"/{slug}"}`)
	ins := func(slug, locale string) {
		t.Helper()
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,published_at,attributes)
			VALUES ($1,$2,$3,$3,$4,'published',now(),$5::jsonb)`, uuid.New(), schemaID, slug, locale,
			`{"page_sections":[{"id":"s1","type":"hero","sort_order":1,"content":{"headline":"Graph test"}}]}`)
	}
	for _, l := range []string{"en", "fr", "nl"} {
		ins(slug, l)
	}
	ins(sharedSlug, "und")

	var prev struct {
		name, lang, xdef    string
		locales, biz, cards []byte
		prefixAll           bool
	}
	hadSite := db.Pool().QueryRow(ctx, `SELECT company_name, language, locales, prefix_all, x_default_locale, business_profile, locale_profiles FROM hatamoto_site_config`).
		Scan(&prev.name, &prev.lang, &prev.locales, &prev.prefixAll, &prev.xdef, &prev.biz, &prev.cards) == nil
	t.Cleanup(func() {
		c := context.Background()
		if hadSite {
			_, _ = db.Pool().Exec(c, `UPDATE hatamoto_site_config SET locales=$1::jsonb, prefix_all=$2, x_default_locale=$3, business_profile=$4::jsonb, locale_profiles=$5::jsonb`,
				string(prev.locales), prev.prefixAll, prev.xdef, string(prev.biz), string(prev.cards))
		} else {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
	})

	const cards = `{
	  "en": {"address":{"street":"1 En Street","city":"Sample City","country":"XA"},"phone":"+00 100","country_name":"Enland",
	         "contact_points":[{"type":"sales","phone":"+00 101","name":"Sales"}],
	         "opening_hours":[{"days":["Monday"],"opens":"09:00","closes":"17:00"}]},
	  "fr": {"no_office":true,"country_name":"Frland"},
	  "nl": {"address":{"street":"1 Nl Street","city":"Sample Town","country":"XB"},"phone":"+00 300","country_name":"Nlland",
	         "special_hours":[{"from":"2030-01-01"}],"rating":{"value":"4.5","count":"12"}}
	}`
	const siteAddress = `{"street":"1 Site Street","city":"Site City","country":"XS"}`
	setProfile := func(orgAddress string) {
		t.Helper()
		biz := map[string]any{"name": "Example Co Ltd", "org_address": orgAddress}
		bizJSON, _ := json.Marshal(biz)
		exec(`INSERT INTO hatamoto_site_config (company_name, language, locales, prefix_all, business_profile, locale_profiles, postal_address)
			VALUES ('Example Co', 'en', $1::jsonb, false, $2::jsonb, $3::jsonb, $4::jsonb)
			ON CONFLICT (id) DO UPDATE SET language='en', locales=EXCLUDED.locales, prefix_all=false, homepage_entity_id=NULL,
			  business_profile=EXCLUDED.business_profile, locale_profiles=EXCLUDED.locale_profiles,
			  phone='', postal_address=EXCLUDED.postal_address`,
			`[{"tag":"en","segment":""},{"tag":"fr","segment":"fr"},{"tag":"nl","segment":"nl"}]`, string(bizJSON), cards, siteAddress)
	}
	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	graph := func(path string) []map[string]any {
		t.Helper()
		srv.siteConfig.Invalidate()
		srv.invalidateSiteSettings(ctx)
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		for _, m := range graphLDRE.FindAllSubmatch(rec.Body.Bytes(), -1) {
			var doc struct {
				Graph []map[string]any `json:"@graph"`
			}
			if json.Unmarshal(m[1], &doc) == nil && doc.Graph != nil {
				return doc.Graph
			}
		}
		t.Fatalf("GET %s has no site graph", path)
		return nil
	}
	node := func(g []map[string]any, typ string) map[string]any {
		for _, n := range g {
			if n["@type"] == typ {
				return n
			}
		}
		return nil
	}
	street := func(n map[string]any) string {
		a, _ := n["address"].(map[string]any)
		s, _ := a["streetAddress"].(string)
		return s
	}

	setProfile("")
	en := graph("/" + slug)
	org, lb := node(en, "Organization"), node(en, "LocalBusiness")
	if org == nil || lb == nil {
		t.Fatalf("en page: organization %v, local business %v", org, lb)
	}
	if street(org) != "1 Site Street" {
		t.Errorf("default org address = %q, want the site address", street(org))
	}
	if served, _ := org["areaServed"].([]any); len(served) != 3 {
		t.Errorf("areaServed = %v, want one Country per locale", org["areaServed"])
	}
	if cps, _ := org["contactPoint"].([]any); len(cps) < 2 {
		t.Errorf("contactPoint = %v, want a list", org["contactPoint"])
	}
	if lb["url"] != "https://graph-it.example.test" && lb["url"] != "https://graph-it.example.test/" {
		t.Errorf("en local business url = %v", lb["url"])
	}
	if !strings.HasSuffix(lb["@id"].(string), "#localbusiness-en") {
		t.Errorf("en local business id = %v", lb["@id"])
	}

	nl := graph("/nl/" + slug)
	nlLB := node(nl, "LocalBusiness")
	if nlLB == nil {
		t.Fatal("nl page has no local business")
	}
	if url, _ := nlLB["url"].(string); !strings.HasSuffix(url, "/nl") {
		t.Errorf("nl local business url = %v", nlLB["url"])
	}
	if _, ok := nlLB["specialOpeningHoursSpecification"]; ok {
		t.Errorf("a special-hours row without times printed: %v", nlLB["specialOpeningHoursSpecification"])
	}
	if r, _ := nlLB["aggregateRating"].(map[string]any); r["ratingCount"] != "12" {
		t.Errorf("nl rating = %v, want the default string count", nlLB["aggregateRating"])
	}

	if fr := graph("/fr/" + slug); node(fr, "LocalBusiness") != nil {
		t.Errorf("fr has no office but printed a local business")
	}

	for _, tc := range []struct{ setting, frStreet, enStreet string }{
		{"", "1 Site Street", "1 Site Street"},
		{"locale", "", "1 En Street"},
		{"locale_or_site", "1 Site Street", "1 En Street"},
	} {
		setProfile(tc.setting)
		if got := street(node(graph("/fr/"+slug), "Organization")); got != tc.frStreet {
			t.Errorf("org_address %q: fr organization street = %q, want %q", tc.setting, got, tc.frStreet)
		}
		if got := street(node(graph("/"+slug), "Organization")); got != tc.enStreet {
			t.Errorf("org_address %q: en organization street = %q, want %q", tc.setting, got, tc.enStreet)
		}
		if got := street(node(graph("/"+sharedSlug), "Organization")); got != "1 Site Street" {
			t.Errorf("org_address %q: shared organization street = %q, want the site address", tc.setting, got)
		}
	}

	shared := graph("/" + sharedSlug)
	if node(shared, "LocalBusiness") != nil {
		t.Errorf("a shared page printed a local business")
	}
	if sorg := node(shared, "Organization"); sorg["areaServed"] != nil {
		t.Errorf("a shared page printed areaServed: %v", sorg["areaServed"])
	}
}
