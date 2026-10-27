package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func ok(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

var csrfTestKey = []byte("0123456789abcdef0123456789abcdef")

// reqCSRF builds a request authenticated by the API cookie token "tok" (or by
// bearer when tok is empty), carrying the given CSRF header and fetch metadata.
func reqCSRF(method, tok, header, site, origin string) *http.Request {
	r := httptest.NewRequest(method, "/api/v1/entities", nil)
	if tok != "" {
		r = r.WithContext(context.WithValue(r.Context(), apiCookieKey{}, tok))
	}
	if header != "" {
		r.Header.Set(CSRFHeaderName, header)
	}
	if site != "" {
		r.Header.Set("Sec-Fetch-Site", site)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

func runKey(key []byte, r *http.Request) int {
	rec := httptest.NewRecorder()
	CSRF(key)(http.HandlerFunc(ok)).ServeHTTP(rec, r)
	return rec.Code
}

func run(r *http.Request) int { return runKey(csrfTestKey, r) }

func TestCSRF(t *testing.T) {
	good := APICSRFToken(csrfTestKey, "tok")
	other := APICSRFToken(csrfTestKey, "other-session")
	cases := []struct {
		name string
		req  *http.Request
		want int
	}{
		{"GET is exempt", reqCSRF("GET", "tok", "", "", ""), http.StatusOK},
		{"no cookie auth -> exempt (bearer)", reqCSRF("POST", "", "", "", ""), http.StatusOK},
		{"bearer plus stray cookie -> exempt", reqCSRF("POST", "", "x", "", ""), http.StatusOK},
		{"bound token, same-origin -> ok", reqCSRF("POST", "tok", good, "same-origin", ""), http.StatusOK},
		{"forged double-submit pair -> 403", reqCSRF("POST", "tok", "x", "same-origin", ""), http.StatusForbidden},
		{"token of another session -> 403", reqCSRF("POST", "tok", other, "same-origin", ""), http.StatusForbidden},
		{"empty header -> 403", reqCSRF("POST", "tok", "", "same-origin", ""), http.StatusForbidden},
		{"cross-site -> 403", reqCSRF("POST", "tok", good, "cross-site", ""), http.StatusForbidden},
		{"same-site -> 403", reqCSRF("POST", "tok", good, "same-site", ""), http.StatusForbidden},
		{"missing Sec-Fetch-Site -> 403", reqCSRF("POST", "tok", good, "", ""), http.StatusForbidden},
		{"foreign Origin -> 403", reqCSRF("POST", "tok", good, "same-origin", "https://evil.example"), http.StatusForbidden},
		{"Origin null -> ok", reqCSRF("POST", "tok", good, "same-origin", "null"), http.StatusOK},
		{"matching Origin -> ok", reqCSRF("POST", "tok", good, "same-origin", "http://example.com"), http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := run(c.req); got != c.want {
				t.Errorf("got %d want %d", got, c.want)
			}
		})
	}
}

func TestCSRFEmptyKeyNeverMatches(t *testing.T) {
	r := reqCSRF("POST", "tok", APICSRFToken(nil, "tok"), "same-origin", "")
	if got := runKey(nil, r); got != http.StatusForbidden {
		t.Errorf("empty key must fail closed: got %d", got)
	}
}

func TestSetCSRFCookie_ReadableByJS(t *testing.T) {
	rec := httptest.NewRecorder()
	tok := SetCSRFCookie(rec, true)
	c := rec.Result().Cookies()[0]
	if c.HttpOnly {
		t.Error("CSRF cookie must NOT be HttpOnly (SPA reads it)")
	}
	if c.Value != tok || tok == "" {
		t.Errorf("token/cookie mismatch")
	}
}

func TestCSRFSkippedForAPIKeyAuthType(t *testing.T) {
	r := reqCSRF("PUT", "tok", "", "", "") // cookie marker present, no CSRF header
	r = r.WithContext(context.WithValue(r.Context(), authTypeContextKey, AuthTypeAPIKey))
	if got := run(r); got != http.StatusOK {
		t.Errorf("api-key request must skip CSRF: got %d", got)
	}
}

func TestCSRFCookieHelpersNeverNameLegacy(t *testing.T) {
	old := CSRFCookieName
	t.Cleanup(func() { CSRFCookieName = old })
	UseNeutralCSRFCookieName(true)
	set, clear := httptest.NewRecorder(), httptest.NewRecorder()
	SetCSRFCookie(set, true)
	ClearCSRFCookie(clear, true)
	for _, rec := range []*httptest.ResponseRecorder{set, clear} {
		cs := rec.Result().Cookies()
		if len(cs) != 1 || cs[0].Name != "__Host-c" {
			t.Fatalf("cookies %v, want only __Host-c", cs)
		}
	}
}

// A __Host- cookie needs Secure, Path=/ and no Domain, or browsers drop it.
func TestNeutralCSRFCookieAttributes(t *testing.T) {
	old := CSRFCookieName
	t.Cleanup(func() { CSRFCookieName = old })
	for _, tc := range []struct {
		secure bool
		name   string
	}{{true, "__Host-c"}, {false, "c"}} {
		UseNeutralCSRFCookieName(tc.secure)
		set, clear := httptest.NewRecorder(), httptest.NewRecorder()
		SetCSRFCookie(set, tc.secure)
		ClearCSRFCookie(clear, tc.secure)
		for what, rec := range map[string]*httptest.ResponseRecorder{"set": set, "clear": clear} {
			var c *http.Cookie
			for _, got := range rec.Result().Cookies() {
				if got.Name == tc.name {
					c = got
				}
			}
			if c == nil {
				t.Fatalf("secure=%v %s: no %q cookie", tc.secure, what, tc.name)
			}
			if c.Secure != tc.secure || c.Path != "/" || c.Domain != "" {
				t.Errorf("secure=%v %s: Secure=%v Path=%q Domain=%q", tc.secure, what, c.Secure, c.Path, c.Domain)
			}
		}
	}
}
