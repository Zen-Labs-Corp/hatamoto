//go:build integration

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/spam"
	"hatamoto.dev/core/internal/user"
)

// itVerifier answers every check with res, and fails the test's secret check
// unless the sealed secret came back out of the database intact.
type itVerifier struct {
	res    spam.Result
	secret string
}

func (v *itVerifier) Verify(_ context.Context, _, secret, _, _, _ string) (spam.Result, error) {
	v.secret = secret
	return v.res, nil
}

// Spam protection end to end on the real server wiring and a migrated Postgres:
// the owner configures a captcha provider in the admin, a cached page picks up
// the provider's CSP origins and widget markup, the secret stays out of every
// surface, and the public submit path enforces the captcha.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run FormSpam
func TestFormSpamIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set: skipping (needs a migrated Postgres)")
	}
	const providerSecret = "it-provider-secret-0123456789"
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "spam-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Spam IT")
	t.Setenv("PUBLIC_BASE_URL", "https://spam-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("HATAMOTO_PAGE_CACHE_TTL", "24h")
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
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := db.Pool().QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(cfg, db, log)
	if srv.renderErr != nil {
		t.Fatal(srv.renderErr)
	}
	verifier := &itVerifier{res: spam.Result{OK: true}}
	srv.spam.Verifier = verifier

	// ---- fixtures: a routed page with a captcha-required contact form, an owner.
	sfx := uuid.NewString()[:6]
	schemaID, pageID, formID := uuid.New(), uuid.New(), uuid.New()
	pageSlug, formSlug := "spam-"+sfx, "spam-form-"+sfx
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, "spamit-"+sfx, `{"url_pattern":"/{slug}"}`)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		VALUES ($1,$2,'Spam IT',$3,'published',now(),$4::jsonb)`, pageID, schemaID, pageSlug,
		`{"page_sections":[{"id":"c1","type":"contact_form","sort_order":1,"content":{"formSlug":"`+formSlug+`","heading":"Write us"}}]}`)
	exec(`INSERT INTO hatamoto_forms (id,name,slug,fields,settings,status) VALUES ($1,'Spam IT form',$2,$3::jsonb,$4::jsonb,'active')`,
		formID, formSlug,
		`[{"name":"email","label":"Email","type":"email","required":true,"position":1}]`,
		`{"enable_captcha":true,"store_submissions":true}`)

	hash, err := auth.HashPassword("a-long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := srv.userRepo.Create(ctx, &user.CreateUserInput{Email: "owner-" + sfx + "@example.test", Role: user.RoleOwner, Name: "Owner"}, hash)
	if err != nil {
		t.Fatal(err)
	}
	completeProfile(t, srv, owner)
	token, err := srv.authService.IssueSession(ctx, owner.ID, "it", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	var prevSite struct {
		name string
		home *uuid.UUID
	}
	hadSite := db.Pool().QueryRow(ctx, `SELECT company_name, homepage_entity_id FROM hatamoto_site_config`).Scan(&prevSite.name, &prevSite.home) == nil
	exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('Spam IT') ON CONFLICT (id) DO NOTHING`)
	t.Cleanup(func() {
		c := context.Background()
		if !hadSite {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_spam_settings`)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_admin_audit WHERE actor_id=$1`, owner.ID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_form_submissions WHERE form_id=$1`, formID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_forms WHERE id=$1`, formID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_users WHERE id=$1`, owner.ID)
	})
	_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_spam_settings`)
	srv.spamStore.Invalidate()

	csrf := adminToken(t, srv.router, token)
	do := func(method, path string, form url.Values, signedIn bool) *httptest.ResponseRecorder {
		t.Helper()
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req := httptest.NewRequest(method, path, body)
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if signedIn {
			adminReq(req, token, csrf)
		}
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		return rec
	}
	pageKey := func() string { return cache.RenderedPageKey(srv.renderEngine.Version(), "/"+pageSlug) }
	cached := func() bool { _, err := srv.infra.Cache.Get(ctx, pageKey()); return err == nil }
	csp := func(rec *httptest.ResponseRecorder) string {
		return rec.Header().Get("Content-Security-Policy-Report-Only") + rec.Header().Get("Content-Security-Policy")
	}
	submit := func(fields map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(fields)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/public/forms/"+formSlug+"/submit", strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		return rec
	}

	// No provider: default CSP, no widget, nothing visitor-specific in the HTML.
	first := do("GET", "/"+pageSlug, nil, false)
	if first.Code != 200 || !cached() {
		t.Fatalf("page: %d cached=%v", first.Code, cached())
	}
	if csp(first) != renderCSPStrict { // the default theme opts in to strict scripts
		t.Errorf("no provider: CSP = %q, want the default", csp(first))
	}
	if strings.Contains(first.Body.String(), "data-hm-captcha") {
		t.Error("widget markup without a provider")
	}

	// ISC-18: switch on, no provider, the submission is accepted.
	if rec := submit(map[string]any{"email": "a@example.test"}); rec.Code != http.StatusCreated {
		t.Fatalf("no keys submit = %d %s", rec.Code, rec.Body)
	}

	// ISC-26: saving in the admin purges the cached page.
	save := do("POST", "/admin/settings/spam", url.Values{
		"provider": {"turnstile"}, "site_key": {"0x4AAA-it"}, "secret_key": {providerSecret},
		"min_seconds": {"0"}, "max_links": {"2"}, "action": {"spam"},
	}, true)
	if save.Code != http.StatusSeeOther {
		t.Fatalf("save spam settings = %d\n%s", save.Code, save.Body)
	}
	if cached() {
		t.Error("saving spam settings left the cached page behind")
	}

	// ISC-22 and ISC-24: the rebuilt page and the cache hit both carry only the
	// chosen provider's origins and the widget markup.
	for _, pass := range []string{"rebuild", "cache hit"} {
		rec := do("GET", "/"+pageSlug, nil, false)
		if rec.Code != 200 {
			t.Fatalf("%s: %d", pass, rec.Code)
		}
		if !cached() {
			t.Errorf("%s: page is not cached", pass)
		}
		got := csp(rec)
		if !strings.Contains(got, "https://challenges.cloudflare.com") || strings.Contains(got, "hcaptcha.com") || strings.Contains(got, "google.com") {
			t.Errorf("%s: CSP = %q", pass, got)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `data-hm-captcha="turnstile"`) || !strings.Contains(body, `data-sitekey="0x4AAA-it"`) {
			t.Errorf("%s: widget markup missing", pass)
		}
		// ISC-A-1: the timing token never reaches cached HTML.
		if regexp.MustCompile(`name="hm_ts"[^>]*value=`).MatchString(body) || regexp.MustCompile(`\bv1\.\d{9,}\.`).MatchString(body) {
			t.Errorf("%s: timing token in the page HTML", pass)
		}
		if strings.Contains(body, providerSecret) {
			t.Errorf("%s: secret in the page HTML", pass)
		}
	}

	// ISC-29: the secret is in no admin page, not in the audit log, and is sealed at rest.
	for _, p := range []string{"/admin/settings/spam", "/admin/settings/forms/" + formID.String() + "/edit"} {
		rec := do("GET", p, nil, true)
		if rec.Code != 200 || strings.Contains(rec.Body.String(), providerSecret) {
			t.Errorf("%s: %d, secret present=%v", p, rec.Code, strings.Contains(rec.Body.String(), providerSecret))
		}
	}
	var audit string
	if err := db.Pool().QueryRow(ctx, `SELECT detail::text FROM hatamoto_admin_audit WHERE actor_id=$1 AND action='spam.settings'`, owner.ID).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audit, providerSecret) || !strings.Contains(audit, `"secret_changed": true`) {
		t.Errorf("audit detail = %s", audit)
	}
	var sealed []byte
	if err := db.Pool().QueryRow(ctx, `SELECT secret_sealed FROM hatamoto_spam_settings`).Scan(&sealed); err != nil || len(sealed) == 0 || strings.Contains(string(sealed), providerSecret) {
		t.Errorf("secret at rest: len=%d err=%v", len(sealed), err)
	}

	// ISC-1/3/5: the public submit path enforces the captcha.
	stored := func() int { return count(`SELECT count(*) FROM hatamoto_form_submissions WHERE form_id=$1`, formID) }
	before := stored()
	if rec := submit(map[string]any{"email": "b@example.test"}); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Please complete the spam check") {
		t.Errorf("no token = %d %s", rec.Code, rec.Body)
	}
	verifier.res = spam.Result{OK: false, Codes: []string{"invalid-input-response"}}
	if rec := submit(map[string]any{"email": "b@example.test", "hm_captcha": "bad"}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("failed token = %d", rec.Code)
	}
	if stored() != before {
		t.Error("a refused captcha was stored")
	}
	verifier.res = spam.Result{OK: true}
	if rec := submit(map[string]any{"email": "c@example.test", "hm_captcha": "good"}); rec.Code != http.StatusCreated {
		t.Errorf("good token = %d %s", rec.Code, rec.Body)
	}
	if verifier.secret != providerSecret {
		t.Error("the verifier did not receive the unsealed secret")
	}
	if stored() != before+1 || count(`SELECT count(*) FROM hatamoto_form_submissions WHERE form_id=$1 AND status='new' AND data::text NOT LIKE '%hm_captcha%'`, formID) < 1 {
		t.Error("a passed captcha was not stored as new without its control field")
	}
}
