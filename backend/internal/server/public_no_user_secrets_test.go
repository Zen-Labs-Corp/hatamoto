package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/auth"
)

// TestPublicHTMLCarriesNoPerUserSecrets pins what keeps plugin CSS harmless as
// a reader: a plugin without grants may ship CSS (sections, partials and
// assets/plugin.css), and CSS attribute selectors can test any attribute in a
// page's markup. Public HTML must therefore be the same for everyone: an
// owner browsing the site gets byte for byte what a visitor gets, and no
// cookie value reaches the markup. docs/SECURITY_MODEL.md, "Browser
// JavaScript and markup from plugins".
func TestPublicHTMLCarriesNoPerUserSecrets(t *testing.T) {
	h, s := editServer(t, "/admin")
	s.renderHandlers.cache = nil // every request renders, none is a cache hit
	home := pageResponse("Home") // one entity id for both requests
	s.renderHandlers.loadHome = func(*http.Request) (*PublicPageResponse, pageOutcome, error) {
		return home, pageOutcome{Status: 200}, nil
	}
	for _, path := range []string{"/", "/no-such-page"} {
		get := func(signedIn bool) string {
			req := httptest.NewRequest("GET", path, nil)
			req.Header.Set("Sec-Fetch-Mode", "navigate")
			req.Header.Set("Sec-Fetch-Dest", "document")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			if signedIn {
				req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "good"})
				req.AddCookie(&http.Cookie{Name: "hatamoto_csrf", Value: "CSRF-TOKEN-XYZ"})
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			return rec.Body.String()
		}
		owner, visitor := get(true), get(false)
		if visitor == "" {
			t.Fatalf("%s: empty body", path)
		}
		if owner != visitor {
			t.Errorf("%s: an owner's public page differs from a visitor's", path)
		}
		if strings.Contains(owner, "CSRF-TOKEN-XYZ") || strings.Contains(owner, `"good"`) {
			t.Errorf("%s: a cookie value reached the public markup", path)
		}
	}
}
