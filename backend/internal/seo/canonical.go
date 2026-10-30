package seo

import (
	"net/url"
	"strings"
)

// ValidCanonical reports whether c is a root-relative path (no "//", backslash,
// space, CR or LF) or an absolute http(s) URL with a host.
func ValidCanonical(c string) bool {
	if strings.HasPrefix(c, "/") {
		return !strings.HasPrefix(c, "//") && !strings.ContainsAny(c, "\\ \r\n")
	}
	u, err := url.Parse(c)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// CanonicalOK reports whether a posted canonical may be saved: empty, equal to
// the stored value (so an old value does not block an unrelated edit), or valid.
func CanonicalOK(posted, stored string) bool {
	t := strings.TrimSpace(posted)
	return t == "" || t == strings.TrimSpace(stored) || ValidCanonical(t)
}
