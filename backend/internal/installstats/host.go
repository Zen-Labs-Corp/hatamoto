package installstats

import (
	"net"
	"net/url"
	"regexp"
	"strings"
)

var hostLabelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// NormalizeHost returns the lower-case site domain of a public base URL, or ""
// when it must not be sent (local names, IP literals, anything that is not a
// plain public domain). It never looks anything up.
func NormalizeHost(rawURL string) string {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "//") {
		raw = "//" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	h := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if h == "" || len(h) > 253 || net.ParseIP(h) != nil || !strings.Contains(h, ".") {
		return ""
	}
	for _, sfx := range []string{".local", ".internal", ".localhost"} {
		if strings.HasSuffix(h, sfx) {
			return ""
		}
	}
	for _, label := range strings.Split(h, ".") {
		if !hostLabelRE.MatchString(label) {
			return ""
		}
	}
	return h
}
