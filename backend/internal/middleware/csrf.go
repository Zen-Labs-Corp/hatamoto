package middleware

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
)

// CSRF protects the COOKIE-authenticated JSON API (ENABLE_COOKIE_AUTH, default
// off; see server/handlers.go) with a synchronizer token bound to the API
// session cookie: the token is an HMAC of the cookie value under a server key.
// Page script cannot read the HttpOnly cookie, so it can neither compute nor
// fetch the token; login returns it in the response body. Unsafe cookie
// requests must also be same-origin per Sec-Fetch-Site and Origin. Requests
// that were not authenticated by the cookie (bearer, API key) are exempt:
// bearer auth is not CSRF-vulnerable.
const CSRFHeaderName = "X-CSRF-Token"

// CSRFCookieName is the double-submit cookie name. Like auth.SessionCookieName
// it keeps the historical default and is switched once at boot.
var CSRFCookieName = "hatamoto_csrf"

// UseNeutralCSRFCookieName sets "__Host-c" when secure, "c" otherwise (see
// auth.UseNeutralCookieNames for why plain-HTTP dev cannot use the prefix).
func UseNeutralCSRFCookieName(secure bool) {
	if secure {
		CSRFCookieName = "__Host-c"
		return
	}
	CSRFCookieName = "c"
}

// APICSRFToken returns the CSRF token bound to the API session cookie value.
func APICSRFToken(key []byte, cookieToken string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("api-csrf:" + cookieToken))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// CSRF returns middleware enforcing the synchronizer check on unsafe methods
// when the request was authenticated via the API session cookie.
func CSRF(key []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			trace(r, "csrf")
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
				next.ServeHTTP(w, r)
				return
			}
			// API-key requests carry no ambient credential (the key is an explicit
			// header), so they are not CSRF-vulnerable even if a cookie rides along.
			if GetAuthType(r.Context()) == AuthTypeAPIKey {
				next.ServeHTTP(w, r)
				return
			}
			// Only cookie-authenticated requests are CSRF-protected.
			tok, ok := GetAPICookieToken(r.Context())
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			header := r.Header.Get(CSRFHeaderName)
			if len(key) == 0 || header == "" || !sameOrigin(r) ||
				subtle.ConstantTimeCompare([]byte(header), []byte(APICSRFToken(key, tok))) != 1 {
				http.Error(w, `{"error":"invalid csrf token"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// sameOrigin admits an unsafe request only when the browser says it is
// same-origin. Origin "null" is what a browser sends on a same-origin form
// navigation under Referrer-Policy no-referrer, so it passes here: the
// same-origin Sec-Fetch-Site and the token still decide. It is a private copy
// of the admin check, which this package must not import.
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") != "same-origin" {
		return false // missing, "none", same-site and cross-site all fail closed
	}
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	}
	return true
}

// NewCSRFToken returns a fresh random CSRF token.
func NewCSRFToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// SetCSRFCookie mints a token and sets it as a NON-HttpOnly cookie (the SPA must
// read it to echo in the header). SameSite=Strict, Secure configurable. Returns
// the token so callers may also surface it in a response body if desired.
func SetCSRFCookie(w http.ResponseWriter, secure bool) string {
	token := NewCSRFToken()
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: false, // must be readable by the SPA
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(30 * 24 * 60 * 60),
	})
	return token
}

// ClearCSRFCookie expires the CSRF cookie (logout). Legacy-named cookies are
// expired by the server's legacy cookie middleware.
func ClearCSRFCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}
