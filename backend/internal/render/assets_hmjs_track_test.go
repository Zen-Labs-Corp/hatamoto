package render

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The default theme's hm.js no longer posts pageviews (the core hm-track.js is
// the single sender).
func TestHmJSDoesNotTrackPageViews(t *testing.T) {
	t.Parallel()
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	e.AssetHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AssetPrefix+"hm.js", nil))
	js := rec.Body.String()
	for _, bad := range []string{"/api/v1/track", "page_view", "trackPageView"} {
		if strings.Contains(js, bad) {
			t.Errorf("hm.js still contains %q", bad)
		}
	}
}

// A sandboxed page has an opaque origin: sendBeacon's credentialed JSON post
// would need a CORS preflight the public API never grants, so the beacon goes
// through a credential-less string-body fetch there.
func TestHmTrackUsesCredentiallessFetchWhenSandboxed(t *testing.T) {
	t.Parallel()
	js := string(trackScript(true))
	for _, want := range []string{
		"self.origin === 'null' || !navigator.sendBeacon",
		"fetch(URL_PATH, { method: 'POST', body: body, keepalive: true, credentials: 'omit' }).catch(",
		"navigator.sendBeacon(URL_PATH",
		"hmPreview",
		"navigator.webdriver",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("hm-track.js lacks %q", want)
		}
	}
}
