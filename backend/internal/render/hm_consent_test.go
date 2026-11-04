package render

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func testConsentConfig() ConsentConfig {
	return ConsentConfig{
		Version: "abc12345", Categories: []string{"analytics", "marketing", "functional"},
		Region: "eu_uk", Title: "Hello", Text: "Body", Strings: map[string]string{"accept": "Accept all"},
	}
}

func setConsentForTest(t *testing.T, c ConsentConfig) {
	t.Helper()
	SetConsent(func(context.Context) (ConsentConfig, bool) { return c, true })
	t.Cleanup(func() { SetConsent(nil) })
}

func renderMinimal(t *testing.T, mut func(*PageView)) string {
	t.Helper()
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	if mut != nil {
		mut(v)
	}
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

var headFirstRE = regexp.MustCompile(`(?is)<head[^>]*>\s*<script src="[^"]*hm-consent\.js\?v=[0-9a-f]{12}"></script>`)

func TestConsentTagFirstInHead(t *testing.T) {
	setConsentForTest(t, testConsentConfig())
	page := renderMinimal(t, nil)
	if !headFirstRE.MatchString(page) {
		t.Fatalf("consent script is not the first element in head:\n%.400s", page)
	}
	if n := strings.Count(page, "hm-consent.js"); n != 1 {
		t.Errorf("consent script appears %d times", n)
	}
	if is := inlineScriptIssues(page); len(is) != 0 {
		t.Errorf("inline script issues: %v", is)
	}
}

func TestConsentTagAbsentForEditorAndWhenOff(t *testing.T) {
	off := renderMinimal(t, nil)
	setConsentForTest(t, testConsentConfig())
	for name, mut := range map[string]func(*PageView){
		"preview": func(v *PageView) { v.Preview = true },
		"canvas":  func(v *PageView) { v.Canvas = true },
	} {
		if strings.Contains(renderMinimal(t, mut), "hm-consent") {
			t.Errorf("%s frame carries the consent script", name)
		}
	}
	SetConsent(nil)
	if got := renderMinimal(t, nil); got != off || strings.Contains(got, "hm-consent") {
		t.Error("output changed while the banner is off")
	}
}

func TestConsentTagHashTracksConfig(t *testing.T) {
	a, b := testConsentConfig(), testConsentConfig()
	b.Text = "Other"
	if consentTag(a) == consentTag(b) || consentTag(a) != consentTag(testConsentConfig()) {
		t.Error("tag hash must change with the config and be stable otherwise")
	}
}

func TestSpliceHeadStart(t *testing.T) {
	t.Parallel()
	tag := consentTag(testConsentConfig())
	for name, page := range map[string]string{
		"plain":     `<html><head><meta charset="utf-8"></head><body></body></html>`,
		"attrs":     `<html><head lang="en" data-x="1"><meta></head></html>`,
		"uppercase": `<HTML><HEAD><META></HEAD></HTML>`,
	} {
		out := string(spliceHeadStart([]byte(page), tag))
		if !headFirstRE.MatchString(out) {
			t.Errorf("%s: %s", name, out)
		}
	}
	// <header> must not be mistaken for <head>.
	if out := string(spliceHeadStart([]byte(`<body><header>x</header></body>`), tag)); out != `<body><header>x</header></body>` {
		t.Errorf("header matched: %s", out)
	}
	// No head: output unchanged (spliceHead also needs </head>).
	if out := string(spliceHeadStart([]byte(`<p>x</p>`), tag)); out != `<p>x</p>` {
		t.Errorf("no-head output changed: %s", out)
	}
	// No opening tag but a closing one: falls back to the end of head.
	if out := string(spliceHeadStart([]byte(`<title>x</title></head>`), tag)); !strings.HasPrefix(out, "<title>x</title><script") {
		t.Errorf("fallback: %s", out)
	}
}

func TestConsentScriptContract(t *testing.T) {
	t.Parallel()
	js := string(consentScript(testConsentConfig()))
	if strings.Contains(js, "__HM_CONSENT__") || !strings.Contains(js, `"version":"abc12345"`) {
		t.Fatal("config not substituted")
	}
	// Defaults come first, before any storage read or UI work.
	def := strings.Index(js, "w.gtag('consent', 'default'")
	for _, later := range []string{"readStored()", "showBanner()", "addEventListener"} {
		if i := strings.Index(js, later); i < 0 || i < def {
			t.Errorf("%q appears before the consent default", later)
		}
	}
	for _, want := range []string{
		"ad_storage: 'denied'", "ad_user_data: 'denied'", "ad_personalization: 'denied'",
		"analytics_storage: 'denied'", "functionality_storage: 'denied'",
		"personalization_storage: 'denied'", "security_storage: 'granted'",
		"wait_for_update = 500", "'hm:consent'", "SameSite=Lax", "Max-Age=",
		"role', 'dialog'", "aria-modal", "aria-labelledby", "Escape", "data-hm-consent-open",
		"/api/v1/public/consent",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	for _, bad := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval("} {
		if strings.Contains(js, bad) {
			t.Errorf("script uses %q", bad)
		}
	}
}

func TestConsentScriptHonoursAPIPrefix(t *testing.T) {
	prefix := "/x/api"
	publicAPIPrefix.Store(&prefix)
	defer publicAPIPrefix.Store(nil)
	js := string(consentScript(testConsentConfig()))
	if strings.Contains(js, "/api/v1/public/") || !strings.Contains(js, "/x/api/consent/region") {
		t.Error("API prefix not applied")
	}
}

func TestConsentAssetServing(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		e.AssetHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AssetPrefix+ConsentScriptName+"?v=1", nil))
		return rec
	}
	if rec := get(); rec.Code != http.StatusNotFound {
		t.Errorf("off: status %d, want 404", rec.Code)
	}
	setConsentForTest(t, testConsentConfig())
	rec := get()
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"version":"abc12345"`) {
		t.Errorf("on: status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("content type %q", ct)
	}
}
