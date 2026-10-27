package render

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAssetsJSServedAndReferenced(t *testing.T) {
	t.Parallel()
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	e.AssetHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AssetPrefix+"hm.js", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "data-hm-form") {
		t.Fatalf("hm.js: status %d", rec.Code)
	}

	// Go-rendered pages are the pageview producer: without this beacon
	// hatamoto_analytics_daily never gets a pageview row.
	if body := rec.Body.String(); !strings.Contains(body, "/api/v1/track") || !strings.Contains(body, "page_view") {
		t.Fatal("hm.js does not send the page_view beacon")
	}

	if !strings.Contains(rec.Body.String(), "hmPreview") {
		t.Fatal("hm.js does not skip the beacon in preview renders")
	}

	// Spam checks: timing token, provider widgets, and the 422 handling that
	// keeps a failed check from falling back to a native re-post.
	js := rec.Body.String()
	for _, want := range []string{
		"/token", "hm_ts", "hm_captcha", "data-hm-captcha", "challenges.cloudflare.com/turnstile/v0/api.js",
		"js.hcaptcha.com/1/api.js", "google.com/recaptcha/api.js", "action: 'hm_form'", "res.status === 422",
		"'cf-turnstile-response': 1", "'g-recaptcha-response': 1", "'h-captcha-response': 1",
		"CAPTCHA_TIMEOUT", "Promise.race", "catch (e) { resolve(false); }", "could not be completed. Please try again.",
		// A 422 on a form without a widget reloads once, guarded per form slug.
		"delete scriptLoads[src]", "function mount()", "if (widget !== undefined) return Promise.resolve()", "Promise.race([mount(), timeout])", "staleCachedPage", "body.code !== 'captcha_required'", "hm-form-draft-", "restoreDraft(form", "hm-form-reload-", "sessionStorage.getItem(RELOAD_KEY + slug)", "location.reload()",
		// Draft restore keeps the original timing token and is safe per field.
		"setHidden(form, 'hm_ts', draft.hm_ts)", "!(form.elements.hm_ts && form.elements.hm_ts.value)", "form.elements.namedItem(name)",
		"field.type === 'hidden'", "field.type !== 'file'", "field.type === 'radio'",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("hm.js lacks %q", want)
		}
	}

	var page strings.Builder
	if err := e.RenderPage(&page, &PageView{Path: "/", BaseURL: "https://x.io"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page.String(), `<script src="`+AssetPrefix+`hm.js" defer>`) {
		t.Fatalf("page does not reference hm.js:\n%s", page.String())
	}

	// Editor frames run no theme script: the preview and canvas renders omit it.
	for name, v := range map[string]*PageView{
		"preview": {Path: "/", BaseURL: "https://x.io", Preview: true},
		"canvas":  {Path: "/", BaseURL: "https://x.io", Canvas: true},
	} {
		var editor strings.Builder
		if err := e.RenderPage(&editor, v); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(editor.String(), "hm.js") {
			t.Errorf("%s render references hm.js", name)
		}
	}
}

func TestAssetsJSPluginDirectorySearch(t *testing.T) {
	t.Parallel()
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	e.AssetHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AssetPrefix+"hm.js", nil))
	js := rec.Body.String()
	for _, want := range []string{"function enhancePluginDirectory()", "[data-hm-plugin-search]", "[data-hm-plugin-count]", "enhancePluginDirectory();", "input.hidden = false", "data-noun", "' ' + noun + (shown === 1"} {
		if !strings.Contains(js, want) {
			t.Errorf("hm.js lacks %q", want)
		}
	}
	// Card text is only ever written through textContent: no markup injection.
	if strings.Contains(js, "innerHTML") {
		t.Error("hm.js must not assign innerHTML")
	}
}

// Not parallel: the prefix is process-wide.
func TestHmJSPublicAPIPrefix(t *testing.T) {
	serve := func() string {
		e, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		e.AssetHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AssetPrefix+"hm.js", nil))
		return rec.Body.String()
	}
	t.Cleanup(func() { SetPublicAPIPrefix("") })

	SetPublicAPIPrefix("/x/v1")
	js := serve()
	for _, want := range []string{"'/x/v1/forms/'", "'/x/v1/track'"} {
		if !strings.Contains(js, want) {
			t.Errorf("hm.js lacks %s", want)
		}
	}
	if strings.Contains(js, "/api/v1/") {
		t.Error("hm.js still calls /api/v1")
	}

	SetPublicAPIPrefix("")
	if js := serve(); !strings.Contains(js, "/api/v1/public/forms/") || !strings.Contains(js, "/api/v1/track") {
		t.Error("default hm.js changed")
	}
}
