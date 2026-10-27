//go:build integration

package server

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"hatamoto.dev/core/internal/auth"
)

var csrfMeta = regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`)

// adminToken GETs the admin entities page as a navigation with the session
// cookie and returns the <meta name="csrf-token"> value, the way a browser
// learns it. The token is bound to the session, so one fetch serves a whole test.
func adminToken(t *testing.T, h http.Handler, sessionToken string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/entities", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionToken})
	navigation(req)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	m := csrfMeta.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatalf("no csrf meta (status %d)", rec.Code)
	}
	return m[1]
}

// navigation marks req as a same-origin top-level document navigation.
func navigation(req *http.Request) {
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
}

// adminReq signs req in and makes it look like the browser sent it: a GET is a
// navigation; any other method is a same-origin request carrying the token.
func adminReq(req *http.Request, sessionToken, csrf string) {
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionToken})
	if req.Method == http.MethodGet {
		navigation(req)
		return
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-CSRF-Token", csrf)
}
