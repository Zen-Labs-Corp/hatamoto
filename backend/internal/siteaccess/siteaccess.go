// Package siteaccess holds the pure parts of the site access gate: the modes,
// which paths the gate never touches, and the signed visitor cookie. It
// imports nothing from the rest of the app.
package siteaccess

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Mode is how the public site answers visitors.
type Mode string

const (
	ModeOpen        Mode = "open"
	ModeComingSoon  Mode = "coming-soon"
	ModeMaintenance Mode = "maintenance"
	ModePassword    Mode = "password"
)

// Valid reports whether m is one of the four modes.
func (m Mode) Valid() bool {
	switch m {
	case ModeOpen, ModeComingSoon, ModeMaintenance, ModePassword:
		return true
	}
	return false
}

// Settings is the stored gate configuration. PasswordHash is an argon2 hash,
// never the password. Epoch rises with every password change.
type Settings struct {
	Mode         Mode
	RetryAfter   int
	PasswordHash string
	Heading      string
	Message      string
	Epoch        int
}

// On reports whether visitors are held back.
func (s Settings) On() bool { return s.Mode != ModeOpen && s.Mode.Valid() }

// RetryOptions are the Retry-After values (seconds) an owner can choose.
var RetryOptions = []int{900, 3600, 21600, 86400}

// DefaultRetryAfter is used when the stored value is not one of RetryOptions.
const DefaultRetryAfter = 3600

// ValidRetry reports whether secs is one of RetryOptions.
func ValidRetry(secs int) bool {
	for _, o := range RetryOptions {
		if o == secs {
			return true
		}
	}
	return false
}

// SubmitPath is the reserved path the password form posts to.
const SubmitPath = "/-/site-access"

// CookieName is the visitor cookie of password mode.
const CookieName = "hatamoto_site_access"

// CookieTTL is how long a password stays accepted.
const CookieTTL = 30 * 24 * time.Hour

var bypassPrefixes = []string{
	"/p/", "/.well-known/", "/media/", "/theme/assets/", "/fonts/",
	// Address-independent admin routes that the main mux forwards.
	"/auth/", "/plugin-ui/",
}

var bypassExact = map[string]bool{
	"/health": true, "/ready": true, "/-/edit": true,
	"/favicon.ico": true, "/favicon.svg": true, "/apple-touch-icon.png": true,
	"/apple-touch-icon-precomposed.png": true, "/icon.svg": true, "/icon.png": true,
	"/manifest.webmanifest": true, "/site.webmanifest": true,
	// The pageview beacon.
	"/api/v1/track": true,
	// The MCP endpoint authenticates API keys itself.
	"/mcp": true,
	// Machine ingest of agent access logs, authenticated by its own secret.
	"/api/v1/public/agent-logs": true,
}

const publicAPI = "/api/v1/public"

// Bypass reports whether the gate leaves a request alone in every mode. The
// admin address is not listed: it is served before the public site. Under
// /api/v1 only the authenticated JSON API passes; the anonymous public API
// (/api/v1/public) serves published content and follows the mode.
func Bypass(path string) bool {
	if bypassExact[path] {
		return true
	}
	if path == publicAPI || strings.HasPrefix(path, publicAPI+"/") {
		return false
	}
	if bypassExact[path] || strings.HasPrefix(path, "/api/v1/") {
		return true
	}
	for _, p := range bypassPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func mac(key []byte, payload string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Sign returns the visitor cookie value for the password epoch, valid for
// CookieTTL from now: v1.<expiry>.<epoch>.<hmac>.
func Sign(key []byte, epoch int, now time.Time) string {
	payload := "v1." + strconv.FormatInt(now.Add(CookieTTL).Unix(), 10) + "." + strconv.Itoa(epoch)
	return payload + "." + mac(key, payload)
}

// Verify reports whether val is an untampered, unexpired cookie of epoch.
func Verify(key []byte, val string, epoch int, now time.Time) bool {
	i := strings.LastIndexByte(val, '.')
	if i < 0 {
		return false
	}
	payload, sig := val[:i], val[i+1:]
	if !hmac.Equal([]byte(sig), []byte(mac(key, payload))) {
		return false
	}
	parts := strings.Split(payload, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.Unix() >= exp {
		return false
	}
	e, err := strconv.Atoi(parts[2])
	return err == nil && e == epoch
}

// SafeNext returns next when it is a local path, otherwise "/". Anything that
// could leave the site (scheme, host, "//", backslash, control bytes) is
// refused, and the reserved submit path is never a target.
func SafeNext(next string) string {
	if next == "" || next[0] != '/' || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n\t") {
		return "/"
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || strings.HasPrefix(u.Path, SubmitPath) {
		return "/"
	}
	return next
}
