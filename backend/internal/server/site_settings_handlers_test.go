package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/httpx"

	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/testutil"
)

// fakeSiteStore applies siteconfig.UpdateInput the way the SQL COALESCE does.
type fakeSiteStore struct{ cfg siteconfig.Config }

func (f *fakeSiteStore) Get(context.Context) (*siteconfig.Config, error) { c := f.cfg; return &c, nil }

func (f *fakeSiteStore) Update(_ context.Context, in siteconfig.UpdateInput) (*siteconfig.Config, error) {
	if in.CompanyName != nil {
		f.cfg.CompanyName = *in.CompanyName
	}
	if in.LogoURL != nil {
		f.cfg.LogoURL = *in.LogoURL
	}
	if in.PrimaryColor != nil {
		f.cfg.PrimaryColor = *in.PrimaryColor
	}
	if in.Timezone != nil {
		f.cfg.Timezone = *in.Timezone
	}
	if in.HomepageEntityID != nil {
		f.cfg.HomepageEntityID = *in.HomepageEntityID
	}
	if len(in.ThemeExtra) > 0 {
		f.cfg.ThemeExtra = in.ThemeExtra
	}
	if in.Language != nil {
		f.cfg.Language = *in.Language
	}
	if in.Phone != nil {
		f.cfg.Phone = *in.Phone
	}
	if in.BusinessHours != nil {
		f.cfg.BusinessHours = *in.BusinessHours
	}
	if len(in.PostalAddress) > 0 {
		f.cfg.PostalAddress = in.PostalAddress
	}
	if in.LegacyWPRedirects != nil {
		f.cfg.LegacyWPRedirects = *in.LegacyWPRedirects
	}
	return f.Get(context.Background())
}

func siteSettingsRouter(role string, store *fakeSiteStore, invalidated *int) http.Handler {
	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")
	{
		r := r.Group("", injectRole(role))
		mountSiteSettings(r, &siteSettingsHandlers{
			store:      store,
			logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
			invalidate: func(context.Context) { *invalidated++ },
		})
	}
	return mux
}

func doSite(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestSiteSettings_GetPatch(t *testing.T) {
	store := &fakeSiteStore{cfg: siteconfig.Config{CompanyName: "Acme", Timezone: "UTC"}}
	var inv int
	h := siteSettingsRouter("owner", store, &inv)

	rec := doSite(h, http.MethodGet, "/settings/site/", "")
	testutil.Equal(t, http.StatusOK, rec.Code)
	if !strings.Contains(rec.Body.String(), `"name":"Acme"`) {
		t.Fatalf("GET body: %s", rec.Body)
	}

	id := uuid.New()
	rec = doSite(h, http.MethodPatch, "/settings/site/", `{"name":"Beta","homepage_entity_id":"`+id.String()+`"}`)
	testutil.Equal(t, http.StatusOK, rec.Code)
	if !strings.Contains(rec.Body.String(), `"name":"Beta"`) || !strings.Contains(rec.Body.String(), id.String()) {
		t.Fatalf("PATCH body: %s", rec.Body)
	}
	testutil.Equal(t, "UTC", store.cfg.Timezone, "omitted field untouched")
	testutil.Equal(t, 1, inv, "rendered cache invalidated")

	testutil.Equal(t, http.StatusBadRequest, doSite(h, http.MethodPatch, "/settings/site/", `{`).Code)
}

func TestSiteSettings_Theme(t *testing.T) {
	store := &fakeSiteStore{}
	var inv int
	h := siteSettingsRouter("owner", store, &inv)

	rec := doSite(h, http.MethodPut, "/settings/site/theme", `{"colors":{"primary":"#123456"}}`)
	testutil.Equal(t, http.StatusOK, rec.Code)
	testutil.Equal(t, 1, inv)

	rec = doSite(h, http.MethodGet, "/settings/site/theme", "")
	testutil.Equal(t, http.StatusOK, rec.Code)
	if !strings.Contains(rec.Body.String(), "#123456") {
		t.Fatalf("theme not persisted: %s", rec.Body)
	}
}

func TestSiteSettings_ThemePutMergesStoredKeys(t *testing.T) {
	store := &fakeSiteStore{cfg: siteconfig.Config{ThemeExtra: []byte(
		`{"colors":{"primary":"#111111"},"socialLinks":{"twitter":"https://x.com/a"},"headerCta":{"text":"Go","url":"/go"},"theme_extra":{"k":1}}`)}}
	var inv int
	h := siteSettingsRouter("owner", store, &inv)

	// Reset-style body: no headerCta / socialLinks sent.
	rec := doSite(h, http.MethodPut, "/settings/site/theme", `{"colors":{"primary":"#222222"},"typography":{"fontFamily":"Inter, sans-serif"}}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := string(store.cfg.ThemeExtra)
	for _, want := range []string{"socialLinks", "https://x.com/a", "headerCta", `"/go"`, "theme_extra", "Inter"} {
		if !strings.Contains(got, want) {
			t.Errorf("stored theme lost %q: %s", want, got)
		}
	}
	testutil.Equal(t, "#222222", store.cfg.PrimaryColor)
	if strings.Contains(got, "#111111") {
		t.Errorf("sent key not replaced: %s", got)
	}
	// Explicitly sent keys do override.
	doSite(h, http.MethodPut, "/settings/site/theme", `{"headerCta":{"text":"New","url":"https://e.com"}}`)
	if !strings.Contains(string(store.cfg.ThemeExtra), "New") {
		t.Errorf("headerCta not overridden: %s", store.cfg.ThemeExtra)
	}
}

func TestSiteSettings_ThemeViewMatchesRenderer(t *testing.T) {
	store := &fakeSiteStore{cfg: siteconfig.Config{PrimaryColor: "#cc0000", ThemeExtra: []byte(`{}`)}}
	var inv int
	h := siteSettingsRouter("owner", store, &inv)

	rec := doSite(h, http.MethodGet, "/settings/site/theme", "")
	if !strings.Contains(rec.Body.String(), "#cc0000") {
		t.Fatalf("GET theme ignores the branding colour: %s", rec.Body)
	}
	rec = doSite(h, http.MethodGet, "/settings/site/", "")
	if !strings.Contains(rec.Body.String(), `"theme_config":{"colors":{"primary":"#cc0000"`) {
		t.Fatalf("GET site theme_config ignores the branding colour: %s", rec.Body)
	}
}

func TestSiteSettings_RejectsUnsafeValues(t *testing.T) {
	evil := `red;}</style><script>alert(1)</script>`
	store := &fakeSiteStore{}
	var inv int
	h := siteSettingsRouter("owner", store, &inv)

	puts := []string{
		`{"colors":{"primary":"` + evil + `"}}`,
		`{"colors":{"background":"url(javascript:x)"}}`,
		`{"typography":{"fontFamily":"a;}</style>"}}`,
		`{"borderRadius":{"md":"4px;}"}}`,
		`{"headerCta":{"text":"x","url":"javascript:alert(1)"}}`,
		`{"headerCta":{"text":"x","url":"data:text/html,x"}}`,
		`{"headerCta":{"text":"x","url":"JaVaScRiPt:alert(1)"}}`,
		`{"typography":{"fontFamily":"\"Inter"}}`,
		`{"socialLinks":{"x":"javascript:alert(1)"}}`,
		`{"colors":"nope"}`,
	}
	for _, body := range puts {
		testutil.Equal(t, http.StatusBadRequest, doSite(h, http.MethodPut, "/settings/site/theme", body).Code, body)
	}
	patches := []string{
		`{"primary_color":"` + evil + `"}`,
		`{"logo_url":"javascript:alert(1)"}`,
		`{"logo_url":"data:image/png;base64,AAAA"}`,
		`{"logo_url":"vbscript:x"}`,
		"{\"logo_url\":\"/a\\u0001b\"}",
	}
	for _, body := range patches {
		testutil.Equal(t, http.StatusBadRequest, doSite(h, http.MethodPatch, "/settings/site/", body).Code, body)
	}
	testutil.Equal(t, 0, len(store.cfg.ThemeExtra), "nothing stored")
	testutil.Equal(t, "", store.cfg.PrimaryColor)

	for _, body := range []string{
		`{"colors":{"primary":"#0ea5e9","secondary":"rgb(1, 2, 3)","accent":"hsl(10 20% 30%)","text":"tomato"},"typography":{"fontFamily":"'Open Sans', sans-serif"},"borderRadius":{"sm":"0","md":"0.5rem"},"headerCta":{"text":"x","url":"/contact"}}`,
	} {
		testutil.Equal(t, http.StatusOK, doSite(h, http.MethodPut, "/settings/site/theme", body).Code, body)
	}
	testutil.Equal(t, http.StatusOK, doSite(h, http.MethodPatch, "/settings/site/", `{"primary_color":"#c00","logo_url":"https://e.com/l.png"}`).Code)
}

func TestSiteSettings_AcceptsAnchorMailtoRelativeURLs(t *testing.T) {
	store := &fakeSiteStore{}
	var inv int
	h := siteSettingsRouter("owner", store, &inv)
	for _, u := range []string{"#contact", "mailto:sales@example.com", "tel:+441234567", "?ref=x", "contact", "../c", "/c", "https://e.com/x"} {
		testutil.Equal(t, http.StatusOK, doSite(h, http.MethodPut, "/settings/site/theme",
			`{"headerCta":{"text":"x","url":"`+u+`"}}`).Code, u)
	}
	for _, u := range []string{"logo.png", "//cdn.example.com/l.png", "/media/l.png"} {
		testutil.Equal(t, http.StatusOK, doSite(h, http.MethodPatch, "/settings/site/", `{"logo_url":"`+u+`"}`).Code, u)
	}
}

func TestSiteSettings_ThemePrimarySingleSourceOfTruth(t *testing.T) {
	store := &fakeSiteStore{cfg: siteconfig.Config{PrimaryColor: "#cc0000", ThemeExtra: []byte(`{"colors":{"primary":"#111111","text":"red"}}`)}}
	var inv int
	h := siteSettingsRouter("owner", store, &inv)

	// Theme PUT writes the primary to the column and keeps it out of the blob.
	rec := doSite(h, http.MethodPut, "/settings/site/theme", `{"colors":{"primary":"#222222","text":"blue"}}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	testutil.Equal(t, "#222222", store.cfg.PrimaryColor)
	if strings.Contains(string(store.cfg.ThemeExtra), "primary") {
		t.Errorf("primary left in blob: %s", store.cfg.ThemeExtra)
	}
	if !strings.Contains(rec.Body.String(), "#222222") {
		t.Errorf("GET/PUT view lost primary: %s", rec.Body)
	}
	// A later Branding change is not shadowed.
	testutil.Equal(t, http.StatusOK, doSite(h, http.MethodPatch, "/settings/site/", `{"primary_color":"#006600"}`).Code)
	rec = doSite(h, http.MethodGet, "/settings/site/theme", "")
	if !strings.Contains(rec.Body.String(), "#006600") {
		t.Errorf("branding colour shadowed: %s", rec.Body)
	}
	// Legacy primary left in the blob is migrated on the next save.
	store.cfg.ThemeExtra = []byte(`{"colors":{"primary":"#abcdef"}}`)
	testutil.Equal(t, http.StatusOK, doSite(h, http.MethodPut, "/settings/site/theme", `{"typography":{"fontFamily":"Inter"}}`).Code)
	testutil.Equal(t, "#abcdef", store.cfg.PrimaryColor)
	if strings.Contains(string(store.cfg.ThemeExtra), "abcdef") {
		t.Errorf("legacy primary left in blob: %s", store.cfg.ThemeExtra)
	}
}

func TestSiteSettings_OwnerOnly(t *testing.T) {
	routes := []struct{ method, path string }{
		{http.MethodGet, "/settings/site/"},
		{http.MethodPatch, "/settings/site/"},
		{http.MethodGet, "/settings/site/theme"},
		{http.MethodPut, "/settings/site/theme"},
	}
	for _, role := range []string{"editor", "viewer", "client"} {
		for _, rt := range routes {
			var inv int
			rec := doSite(siteSettingsRouter(role, &fakeSiteStore{}, &inv), rt.method, rt.path, `{}`)
			testutil.Equal(t, http.StatusForbidden, rec.Code, role+" "+rt.method+" "+rt.path)
			testutil.Equal(t, 0, inv)
		}
	}
	var inv int
	rec := doSite(siteSettingsRouter("", &fakeSiteStore{}, &inv), http.MethodGet, "/settings/site/", "")
	testutil.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestSiteSettings_LanguageAndContact(t *testing.T) {
	store := &fakeSiteStore{cfg: siteconfig.Config{CompanyName: "Acme"}}
	var inv int
	h := siteSettingsRouter("owner", store, &inv)

	// Default language is en; the legacy WordPress redirects are off.
	var got siteSettingsResponse
	rec := doSite(h, http.MethodGet, "/settings/site/", "")
	testutil.Equal(t, http.StatusOK, rec.Code)
	testutil.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	testutil.Equal(t, "en", got.Language)
	testutil.Equal(t, false, got.LegacyWPRedirects)

	rec = doSite(h, http.MethodPatch, "/settings/site/", `{"language":"en-au","phone":"+61 2 5550 0100","business_hours":"Mon-Fri",
		"address":{"street":"1 Example St","city":"Sydney","country":"AU"},"legacy_wp_redirects":true}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	testutil.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	testutil.Equal(t, "en-AU", got.Language) // canonical case
	testutil.Equal(t, "+61 2 5550 0100", got.Phone)
	testutil.Equal(t, "Sydney", got.Address.City)
	testutil.Equal(t, true, got.LegacyWPRedirects)
	testutil.Equal(t, 1, inv)

	for _, body := range []string{`{"language":""}`, `{"language":"not a tag"}`, `{"language":"en-"}`, `{"language":"und"}`,
		`{"phone":"` + strings.Repeat("1", 51) + `"}`, `{"address":{"city":"` + strings.Repeat("x", 201) + `"}}`} {
		testutil.Equal(t, http.StatusBadRequest, doSite(h, http.MethodPatch, "/settings/site/", body).Code, body)
	}
	testutil.Equal(t, "en-AU", store.cfg.Language, "rejected patches change nothing")
}
