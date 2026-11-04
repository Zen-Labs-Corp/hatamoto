package pluginhost

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"hatamoto.dev/sdk"
)

// Limits on the cookies a plugin may set (render.page filters and public
// routes share them).
const (
	maxSetCookies  = 8
	maxCookieValue = 2048
	maxCookieAge   = 365 * 24 * 3600
)

// CookiePrefix starts the name of every cookie a plugin may read or set.
const CookiePrefix = "hmp_"

// PluginCookieName is the browser cookie name of a plugin's cookie: plugins
// are confined to the namespace hmp_<plugin>_<name>, so they can neither read
// nor set the session cookie, the CSRF cookie or another plugin's cookies.
// The plugin itself only ever sees and uses <name>.
func PluginCookieName(plugin, name string) string { return CookiePrefix + plugin + "_" + name }

// checkSetCookies validates the cookies a plugin asks to set: declared names
// only, and the count, value, age and path limits.
func checkSetCookies(m sdk.Manifest, in []sdk.SetCookie) error {
	if len(in) > maxSetCookies {
		return fmt.Errorf("more than %d cookies", maxSetCookies)
	}
	for _, c := range in {
		if !slices.Contains(m.Cookies, c.Name) {
			return fmt.Errorf("cookie %q is not declared in the manifest", c.Name)
		}
		if len(c.Value) > maxCookieValue || c.MaxAge < 0 || c.MaxAge > maxCookieAge {
			return fmt.Errorf("cookie %q: value over %d bytes or max_age outside 0..%d", c.Name, maxCookieValue, maxCookieAge)
		}
		if !validCookieValue(c.Value) {
			return fmt.Errorf("cookie %q: value has characters outside the cookie-safe set", c.Name)
		}
		if c.Path != "" && (c.Path[0] != '/' || len(c.Path) > 256) {
			return fmt.Errorf("cookie %q: bad path", c.Name)
		}
	}
	return nil
}

func validCookieValue(v string) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c <= 0x20 || c >= 0x7f || c == '"' || c == ',' || c == ';' || c == '\\' {
			return false
		}
	}
	return true
}

// WriteSetCookies writes cookies a plugin set. The names were checked against
// the manifest and namespaced (PluginCookieName); the attributes are fixed
// here: HttpOnly, SameSite=Lax, Secure over HTTPS.
func WriteSetCookies(w http.ResponseWriter, r *http.Request, cookies []sdk.SetCookie) {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	for _, c := range cookies {
		path := c.Path
		if path == "" {
			path = "/"
		}
		http.SetCookie(w, &http.Cookie{
			Name: c.Name, Value: c.Value, Path: path, MaxAge: c.MaxAge,
			HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
		})
	}
}
