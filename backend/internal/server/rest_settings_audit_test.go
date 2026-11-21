package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
)

type restSettingsAuditRow struct {
	action, target string
	detail         map[string]any
}

func restSettingsAuditRouter(role string, store *fakeSiteStore) (http.Handler, *[]restSettingsAuditRow) {
	var rows []restSettingsAuditRow
	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")
	{
		r := r.Group("", injectRole(role))
		mountSiteSettings(r, &siteSettingsHandlers{
			store:      store,
			logger:     slog.New(slog.DiscardHandler),
			invalidate: func(context.Context) {},
			audit: func(_ *http.Request, action, target string, detail map[string]any) {
				rows = append(rows, restSettingsAuditRow{action, target, detail})
			},
		})
	}
	return mux, &rows
}

func TestRestSettingsAuditSiteAndThemeSaves(t *testing.T) {
	t.Parallel()

	h, rows := restSettingsAuditRouter("owner", &fakeSiteStore{})
	if rec := doSite(h, "PATCH", "/settings/site/", `{"phone":"+1 555 0100","name":"Acme"}`); rec.Code != 200 {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	if len(*rows) != 1 {
		t.Fatalf("rows = %v", *rows)
	}
	got := (*rows)[0]
	changed, _ := got.detail["changed"].([]string)
	if got.action != admin.AuditSettingsUpdated || got.target != "site" || strings.Join(changed, ",") != "name,phone" {
		t.Fatalf("site row = %+v", got)
	}
	for _, v := range got.detail {
		if s, ok := v.(string); ok && (strings.Contains(s, "Acme") || strings.Contains(s, "555")) {
			t.Fatalf("a value reached the row: %+v", got)
		}
	}

	*rows = nil
	if rec := doSite(h, "PUT", "/settings/site/theme", `{"colors":{"accent":"#112233"}}`); rec.Code != 200 {
		t.Fatalf("theme: %d %s", rec.Code, rec.Body)
	}
	if len(*rows) != 1 || (*rows)[0].target != "site-theme" || strings.Join((*rows)[0].detail["changed"].([]string), ",") != "colors" {
		t.Fatalf("theme row = %v", *rows)
	}
}

func TestRestSettingsAuditRefusedSaveWritesNothing(t *testing.T) {
	t.Parallel()

	h, rows := restSettingsAuditRouter("owner", &fakeSiteStore{})
	if rec := doSite(h, "PATCH", "/settings/site/", `{"primary_color":"not-a-colour"}`); rec.Code != 400 {
		t.Fatalf("patch: %d", rec.Code)
	}
	if rec := doSite(h, "PUT", "/settings/site/theme", `[]`); rec.Code != 400 {
		t.Fatalf("theme: %d", rec.Code)
	}
	if len(*rows) != 0 {
		t.Fatalf("rows = %v", *rows)
	}
}

func TestRestSettingsAuditPluginSettings(t *testing.T) {
	t.Parallel()

	s, _, h := pluginTestServer()
	lg := &restLoginLog{}
	s.logger = slog.New(lg)
	put := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PUT", "/plugins/hello/settings", strings.NewReader(body))
		req = req.WithContext(middleware.ContextWithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), Email: "owner@example.test", Role: "owner"}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := put(`{"greeting":"x"}`); rec.Code != 400 { // too short: refused
		t.Fatalf("invalid: %d", rec.Code)
	}
	if len(lg.recs) != 0 {
		t.Fatalf("a refused save wrote %v", lg.recs)
	}
	if rec := put(`{"greeting":"hello there","api_key":"sk-typed-secret"}`); rec.Code != 200 {
		t.Fatalf("valid: %d %s", rec.Code, rec.Body)
	}
	if len(lg.recs) != 1 {
		t.Fatalf("rows = %v", lg.recs)
	}
	rec := lg.recs[0]
	d := rec["detail"].(string)
	if rec["action"] != "plugin.settings" || rec["target"] != "plugin:hello" || rec["actor"] != "owner@example.test" ||
		!strings.Contains(d, `"keys":["api_key","greeting"]`) || strings.Contains(d, "sk-typed-secret") || strings.Contains(d, "hello there") {
		t.Fatalf("row = %v", rec)
	}
}
