//go:build integration

package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/jsonx"
)

// localeNeutralEnv is a running server with one locale-neutral and one plain
// entity type, plus an API key that may write entities.
type localeNeutralEnv struct {
	ts                     *httptest.Server
	key                    string
	flaggedID, plainID     uuid.UUID
	flaggedSlug, plainSlug string
	siteLanguage           string
	srv                    *Server
	ctx                    context.Context
	db                     *database.DB
}

// newLocaleNeutralEnv boots the server over a site with the given locales
// (JSON array for hatamoto_site_config.locales; "[]" is a single-language site).
func newLocaleNeutralEnv(t *testing.T, locales string) *localeNeutralEnv {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; needs a migrated Postgres")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "locale-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Locale IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "Locale IT")
	t.Setenv("PUBLIC_BASE_URL", "https://locale-it.example.test")
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
	var prev struct {
		name, lang, xdef string
		locales          []byte
		prefixAll        bool
	}
	hadSite := db.Pool().QueryRow(ctx, `SELECT company_name, language, locales, prefix_all, x_default_locale FROM hatamoto_site_config`).
		Scan(&prev.name, &prev.lang, &prev.locales, &prev.prefixAll, &prev.xdef) == nil
	exec(`INSERT INTO hatamoto_site_config (company_name, language, locales, prefix_all) VALUES ('Locale IT', 'en', $1::jsonb, false)
		ON CONFLICT (id) DO UPDATE SET language = 'en', locales = EXCLUDED.locales, prefix_all = false, homepage_entity_id = NULL, x_default_locale = ''`, locales)
	t.Cleanup(func() {
		c := context.Background()
		if hadSite {
			_, _ = db.Pool().Exec(c, `UPDATE hatamoto_site_config SET locales=$1::jsonb, prefix_all=$2, x_default_locale=$3`, string(prev.locales), prev.prefixAll, prev.xdef)
		} else {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
	})
	sfx := uuid.NewString()[:6]
	e := &localeNeutralEnv{
		ctx: ctx, db: db,
		flaggedID: uuid.New(), plainID: uuid.New(),
		flaggedSlug: "lnf-" + sfx, plainSlug: "lnp-" + sfx,
	}
	userID := uuid.New()
	exec(`INSERT INTO hatamoto_users (id,email,password_hash,role) VALUES ($1,$2,'x','owner')`, userID, "locale-"+sfx+"@example.test")
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}','{"locale_neutral":true}')`, e.flaggedID, e.flaggedSlug)
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}','{}')`, e.plainID, e.plainSlug)
	if err := db.Pool().QueryRow(ctx, `SELECT public.hatamoto_site_language()`).Scan(&e.siteLanguage); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id = ANY($1)`, []uuid.UUID{e.flaggedID, e.plainID})
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id = ANY($1)`, []uuid.UUID{e.flaggedID, e.plainID})
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_mcp_audit WHERE api_key_name LIKE $1`, "%"+sfx)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_api_keys WHERE created_by=$1`, userID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_users WHERE id=$1`, userID)
	})
	e.srv = New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.ts = httptest.NewServer(e.srv.router)
	t.Cleanup(e.ts.Close)
	key, err := e.srv.apiKeyService.Create(ctx, &userID, &auth.CreateAPIKeyInput{Name: "locale" + sfx, Scopes: []auth.APIKeyScope{auth.ScopeWriteEntities, auth.ScopeReadEntities}})
	if err != nil {
		t.Fatal(err)
	}
	e.key = key.PlainKey
	return e
}

const (
	multiLocales  = `[{"tag":"en","segment":""},{"tag":"fr","segment":"fr"}]`
	singleLocales = `[]`
)

func TestRESTCreate_LocaleNeutral(t *testing.T) {
	e := newLocaleNeutralEnv(t, multiLocales)
	create := func(schema uuid.UUID, locale string) (int, string) {
		t.Helper()
		body := map[string]any{"schema_id": schema, "name": "N " + uuid.NewString()[:6], "attributes": map[string]any{}}
		if locale != "" {
			body["locale"] = locale
		}
		raw, _ := jsonx.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/api/v1/entities", bytes.NewReader(raw))
		req.Header.Set("X-API-Key", e.key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			ID uuid.UUID `json:"id"`
		}
		b, _ := io.ReadAll(resp.Body)
		_ = jsonx.Unmarshal(b, &out)
		if resp.StatusCode != http.StatusCreated {
			return resp.StatusCode, ""
		}
		var stored string
		if err := e.db.Pool().QueryRow(e.ctx, `SELECT locale FROM hatamoto_entities WHERE id = $1`, out.ID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, stored
	}
	for _, c := range []struct {
		name   string
		schema uuid.UUID
		locale string
		want   string
	}{
		{"flagged omitted", e.flaggedID, "", "und"},
		{"flagged explicit", e.flaggedID, "en-AU", "en-AU"},
		{"unflagged omitted", e.plainID, "", e.siteLanguage},
	} {
		if code, got := create(c.schema, c.locale); code != http.StatusCreated || got != c.want {
			t.Errorf("%s: status %d, locale %q; want 201, %q", c.name, code, got, c.want)
		}
	}
	for name, schema := range map[string]uuid.UUID{"flagged": e.flaggedID, "unflagged": e.plainID} {
		if code, _ := create(schema, "und"); code < 400 || code >= 500 {
			t.Errorf("%s: explicit und = %d, want 4xx", name, code)
		}
	}
}

func TestMCPCreate_LocaleNeutral(t *testing.T) {
	e := newLocaleNeutralEnv(t, multiLocales)
	c := mcp.NewClient(&mcp.Implementation{Name: "it", Version: "1"}, nil)
	sess, err := c.Connect(e.ctx, &mcp.StreamableClientTransport{
		Endpoint: e.ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{e.key}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	create := func(schema string) string {
		t.Helper()
		res, err := sess.CallTool(e.ctx, &mcp.CallToolParams{Name: "entities_create", Arguments: map[string]any{
			"schema": schema, "name": "N " + uuid.NewString()[:6], "attributes": map[string]any{}}})
		if err != nil {
			t.Fatal(err)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError {
			t.Fatalf("entities_create: %s", text)
		}
		var out struct {
			Locale string `json:"locale"`
		}
		if err := jsonx.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("%v\n%s", err, text)
		}
		return out.Locale
	}
	if got := create(e.flaggedSlug); got != "und" {
		t.Errorf("flagged omitted locale = %q, want und", got)
	}
	if got := create(e.plainSlug); got != e.siteLanguage {
		t.Errorf("unflagged omitted locale = %q, want %q", got, e.siteLanguage)
	}
}

// On a single-language site the public side serves only the site language, so
// a flagged type must store the site language, never an invisible und.
func TestRESTCreate_LocaleNeutralSingleLanguageSite(t *testing.T) {
	e := newLocaleNeutralEnv(t, singleLocales)
	raw, _ := jsonx.Marshal(map[string]any{"schema_id": e.flaggedID, "name": "N " + uuid.NewString()[:6], "attributes": map[string]any{}})
	req, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/api/v1/entities", bytes.NewReader(raw))
	req.Header.Set("X-API-Key", e.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		ID uuid.UUID `json:"id"`
	}
	b, _ := io.ReadAll(resp.Body)
	_ = jsonx.Unmarshal(b, &out)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	var stored string
	if err := e.db.Pool().QueryRow(e.ctx, `SELECT locale FROM hatamoto_entities WHERE id = $1`, out.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != e.siteLanguage {
		t.Errorf("stored locale = %q, want the site language %q", stored, e.siteLanguage)
	}
}
