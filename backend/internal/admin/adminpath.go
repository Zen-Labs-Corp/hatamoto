package admin

import (
	"fmt"
	"regexp"
	"strings"
)

// adminPathRx is "/" plus one lowercase [a-z0-9-] segment of 1 to 64
// characters, not starting or ending with "-". It is a syntax check only: the
// reserved names and the length rules of the admin-address spec are enforced
// where the config is read (PR 2).
var adminPathRx = regexp.MustCompile(`^/[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

// normalizeAdminPath validates the configured prefix; empty means the default.
// The prefix lands in redirects and route patterns, so anything but one plain
// lowercase segment is refused (no "//host", backslash, space, braces or "/").
func normalizeAdminPath(p string) (string, error) {
	if p == "" {
		return DefaultAdminPath, nil
	}
	if !adminPathRx.MatchString(p) {
		return "", fmt.Errorf("admin: AdminPath %q must be / plus one segment of a-z, 0-9 and - (1 to 64 characters, not starting or ending with -)", p)
	}
	return p, nil
}

// AdminPath is the URL prefix this instance is mounted under.
func (a *Admin) AdminPath() string { return a.adminPath }

// url joins parts under the prefix: a.url("settings", "forms") is
// "/admin/settings/forms" and a.url() is the prefix itself. Each part is
// trimmed of slashes and joined with one; a part that starts with "?" or "#"
// is appended as is.
func (a *Admin) url(parts ...string) string {
	return urlUnder(a.adminPath, parts...)
}

// urlUnder is url for code that has the prefix but no *Admin.
func urlUnder(prefix string, parts ...string) string {
	var b strings.Builder
	b.WriteString(prefix)
	for _, p := range parts {
		if strings.HasPrefix(p, "?") || strings.HasPrefix(p, "#") {
			b.WriteString(p)
			continue
		}
		b.WriteByte('/')
		b.WriteString(strings.Trim(p, "/"))
	}
	return b.String()
}
