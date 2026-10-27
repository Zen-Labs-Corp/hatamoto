package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
)

type ctxKey int

const (
	nonceKey ctxKey = iota
	csrfKey
	userKey
	sessionKey
)

// Nonce returns the CSP nonce of the request ("" outside admin routes).
func Nonce(ctx context.Context) string {
	n, _ := ctx.Value(nonceKey).(string)
	return n
}

func newNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// cspHeader is strict regardless of CSP_ENFORCE: no unsafe-inline, every
// inline <script>/<style> must carry the per-request nonce.
func cspHeader(nonce string, frame bool) string {
	ancestors := "'none'"
	if frame {
		ancestors = "'self'"
	}
	return "default-src 'none'; script-src 'self' 'nonce-" + nonce + "'; style-src 'self' 'nonce-" + nonce + "'; " +
		"img-src 'self' data: blob: https:; font-src 'self'; connect-src 'self'; frame-src 'self'; form-action 'self'; " +
		"base-uri 'none'; object-src 'none'; frame-ancestors " + ancestors + "; manifest-src 'self'"
}

// secureHeaders mints the nonce and sets the admin's own strict headers,
// replacing the lax global CSP and X-Frame-Options (the v1-12 canvas and v1-13
// preview iframe same-origin pages). Admin HTML is never cached.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce := newNonce()
		h := w.Header()
		h.Del("Content-Security-Policy-Report-Only")
		frame := frameable(r.Context())
		h.Set("Content-Security-Policy", cspHeader(nonce, frame))
		if frame {
			h.Set("X-Frame-Options", "SAMEORIGIN")
		} else {
			h.Set("X-Frame-Options", "DENY")
		}
		h.Set("Cache-Control", "no-store")
		h.Add("Vary", "Cookie")
		h.Add("Vary", "HX-Request")
		h.Add("Vary", "HX-Target")
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nonceKey, nonce)))
	})
}
