//go:build integration

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/user"
)

// Settings > Contact button end to end on the real server wiring and a migrated
// Postgres: the owner saves the floating button, an audit row is written, the
// other theme settings keys survive, and a public page shows the link.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run ContactButton
func TestContactButtonIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set: skipping (needs a migrated Postgres)")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "contact-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Contact IT")
	t.Setenv("PUBLIC_BASE_URL", "https://contact-it.example.test")
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
	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if srv.renderErr != nil {
		t.Fatal(srv.renderErr)
	}

	sfx := uuid.NewString()[:6]
	schemaID, pageID := uuid.New(), uuid.New()
	pageSlug := "contact-" + sfx
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, "contactit-"+sfx, `{"url_pattern":"/{slug}"}`)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		VALUES ($1,$2,'Contact IT',$3,'published',now(),'{}'::jsonb)`, pageID, schemaID, pageSlug)

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

	var prev struct {
		phone *string
		extra []byte
	}
	hadSite := db.Pool().QueryRow(ctx, `SELECT phone, theme_extra FROM hatamoto_site_config`).Scan(&prev.phone, &prev.extra) == nil
	exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('Contact IT') ON CONFLICT (id) DO NOTHING`)
	exec(`UPDATE hatamoto_site_config SET phone = '+41 79 123 45 67', theme_extra = '{"socialLinks":{"github":"https://g.example"}}'::jsonb`)
	srv.siteConfig.Invalidate()
	t.Cleanup(func() {
		c := context.Background()
		if hadSite {
			_, _ = db.Pool().Exec(c, `UPDATE hatamoto_site_config SET phone = $1, theme_extra = $2`, prev.phone, prev.extra)
		} else {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_admin_audit WHERE actor_id=$1`, owner.ID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_users WHERE id=$1`, owner.ID)
		srv.siteConfig.Invalidate()
	})

	csrf := adminToken(t, srv.router, token)
	post := func(form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", "/admin/settings/contact-button", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		adminReq(req, token, csrf)
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		return rec
	}
	page := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, httptest.NewRequest("GET", "/"+pageSlug, nil))
		if rec.Code != 200 {
			t.Fatalf("page = %d", rec.Code)
		}
		return rec.Body.String()
	}

	if strings.Contains(page(), "hm-contact-float") {
		t.Fatal("floating link before it was enabled")
	}
	if rec := post(url.Values{"enabled": {"true"}, "channel": {"fax"}, "position": {"bottom-right"}}); rec.Code != 422 {
		t.Fatalf("invalid save = %d", rec.Code)
	}
	rec := post(url.Values{"enabled": {"true"}, "channel": {"whatsapp"}, "position": {"bottom-left"}, "label": {"Chat with us"}, "message": {"Hi"}})
	if rec.Code != 303 {
		t.Fatalf("save = %d\n%s", rec.Code, rec.Body)
	}

	var raw []byte
	if err := db.Pool().QueryRow(ctx, `SELECT theme_extra FROM hatamoto_site_config`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	got := siteconfig.ParseContactButton(raw)
	want := siteconfig.ContactButton{Enabled: true, Channel: "whatsapp", Position: "bottom-left", Label: "Chat with us", Message: "Hi"}
	if got != want {
		t.Errorf("stored = %+v", got)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || len(top["socialLinks"]) == 0 {
		t.Errorf("other theme keys lost: %s", raw)
	}

	var detail string
	if err := db.Pool().QueryRow(ctx, `SELECT detail::text FROM hatamoto_admin_audit WHERE actor_id=$1 AND action='contact_button.settings'`, owner.ID).Scan(&detail); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if !strings.Contains(detail, `"channel": "whatsapp"`) || strings.Contains(detail, "4179") {
		t.Errorf("audit detail = %s", detail)
	}

	body := page()
	if n := strings.Count(body, `class="hm-contact-float`); n != 1 || !strings.Contains(body, `href="https://wa.me/41791234567?text=Hi"`) || !strings.Contains(body, ">Chat with us</a>") {
		t.Errorf("public page: %d floating links, body lacks the link", n)
	}
}
