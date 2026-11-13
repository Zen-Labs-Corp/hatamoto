package server

import (
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/entity"
)

const (
	// localeCookieName remembers the visitor's chosen locale.
	localeCookieName = "hm_locale"
	// localeCookieMaxAge is one year, in seconds.
	localeCookieMaxAge = 365 * 24 * 3600

	localeTargetMaxLen = 2048
)

// cleanLocalTarget reports whether to is a plain same-site path that is safe
// to echo into a Location header: it starts with a single "/", carries no
// scheme, host, backslash, control byte, query, fragment, encoded slash,
// backslash or dot, and has no "." or ".." segment.
func cleanLocalTarget(to string) bool {
	if !strings.HasPrefix(to, "/") || len(to) > localeTargetMaxLen || strings.Contains(to, "//") {
		return false
	}
	for i := 0; i < len(to); i++ {
		switch c := to[i]; {
		case c <= 0x20, c == 0x7f, c == '\\', c == '?', c == '#':
			return false
		}
	}
	low := strings.ToLower(to)
	for _, enc := range []string{"%2f", "%5c", "%2e"} {
		if strings.Contains(low, enc) {
			return false
		}
	}
	for _, seg := range strings.Split(to[1:], "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// lookupLocaleValue maps a switcher value to a public locale tag: the locale's
// non-empty URL segment, or the tag of a default locale kept without a
// segment. Hidden and unknown values report ok false.
func lookupLocaleValue(set *entity.LocaleSet, v string) (tag string, ok bool) {
	if v == "" {
		return "", false
	}
	for _, e := range set.Entries() {
		if e.Hidden {
			continue
		}
		if e.Segment != "" && e.Segment == v {
			return e.Tag, true
		}
		if e.Segment == "" && e.Tag == set.Default() && e.Tag == v {
			return e.Tag, true
		}
	}
	return "", false
}

// rawQueryValue returns the still-encoded value of the first key parameter.
func rawQueryValue(rawQuery, key string) string {
	for _, part := range strings.Split(rawQuery, "&") {
		if k, v, _ := strings.Cut(part, "="); k == key {
			return v
		}
	}
	return ""
}

// localeSwitch handles GET /locale?l=<value>&to=<path>: it remembers the
// chosen locale in a cookie and redirects to to when that is a clean path under
// the locale, else to the locale's home. An unknown or hidden locale is a 404.
func (s *Server) localeSwitch(w http.ResponseWriter, r *http.Request, set *entity.LocaleSet) {
	q := r.URL.Query()
	l := q.Get("l")
	tag, ok := lookupLocaleValue(set, l)
	if !ok {
		s.writeStandardNotFound(w, r)
		return
	}
	prefix := set.Prefix(tag)
	home := prefix
	if home == "" {
		home = "/"
	}
	dest := home
	to := q.Get("to")
	if cleanLocalTarget(to) && cleanLocalTarget(rawQueryValue(r.URL.RawQuery, "to")) && underLocale(set, tag, prefix, to) {
		dest = to
	}

	http.SetCookie(w, &http.Cookie{
		Name:     localeCookieName,
		Value:    l,
		Path:     "/",
		MaxAge:   localeCookieMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   (&themePreview{secureCookie: s.cfg.AdminCookieSecure}).cookieSecure(r),
	})
	h := w.Header()
	h.Set("Location", dest)
	h.Set("Cache-Control", "private, no-store")
	h.Add("Vary", "Cookie")
	w.WriteHeader(http.StatusFound)
}

// underLocale reports whether the clean path to belongs to locale tag: inside
// its prefix, or, for an unprefixed locale, not inside another locale's.
func underLocale(set *entity.LocaleSet, tag, prefix, to string) bool {
	if prefix != "" {
		return to == prefix || strings.HasPrefix(to, prefix+"/")
	}
	got, _, ok := set.Resolve(to)
	return !ok || got == tag
}
