package render

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A sandboxed page has an opaque origin: sendBeacon's credentialed JSON post
// would need a CORS preflight the public API never grants, so the pageview
// goes through a credential-less string-body fetch there.
func TestHmJSTrackUsesCredentiallessFetchWhenSandboxed(t *testing.T) {
	t.Parallel()
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	e.AssetHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AssetPrefix+"hm.js", nil))
	js := rec.Body.String()
	for _, want := range []string{
		"self.origin === 'null' || !navigator.sendBeacon",
		"fetch('/api/v1/track', { method: 'POST', body: body, keepalive: true, credentials: 'omit' }).catch(",
		"navigator.sendBeacon('/api/v1/track'",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("hm.js lacks %q", want)
		}
	}
}
