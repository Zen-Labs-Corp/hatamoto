package installstats

import (
	"net/netip"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/idna"
)

var hostLabelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Blocked ranges mirror the stats receiver's public-IP rule: an address the
// receiver would reject (422, the whole report lost) is never sent.
var (
	blockedV4 = []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
		"192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4"}
	blockedV6 = []string{"2001::/23", "2001:db8::/32", "2002::/16"}
	globalV6  = netip.MustParsePrefix("2000::/3")
	blocked   = mustPrefixes(blockedV4, blockedV6)
)

func mustPrefixes(lists ...[]string) []netip.Prefix {
	var out []netip.Prefix
	for _, l := range lists {
		for _, s := range l {
			out = append(out, netip.MustParsePrefix(s))
		}
	}
	return out
}

// publicIP returns the canonical text of a public address, or "".
func publicIP(a netip.Addr) string {
	if a.Zone() != "" || a.Is4In6() {
		return ""
	}
	if a.Is6() && !globalV6.Contains(a) {
		return ""
	}
	for _, p := range blocked {
		if p.Contains(a) {
			return ""
		}
	}
	return a.String() // lower-case, compressed: what the receiver requires
}

// NormalizeHost returns the lower-case site domain of a public base URL, or the
// canonical text of a public IP literal, or "" when it must not be sent (local
// names, private or reserved addresses, anything that is not a plain public
// domain). It never looks anything up.
func NormalizeHost(rawURL string) string {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return ""
	}
	if a, err := netip.ParseAddr(strings.Trim(raw, "[]")); err == nil {
		return publicIP(a) // bare IPv6 would not survive url.Parse
	}
	if !strings.Contains(raw, "//") {
		raw = "//" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	h := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if !isASCII(h) {
		if h, err = idna.Lookup.ToASCII(h); err != nil {
			return ""
		}
		h = strings.ToLower(h)
	}
	if a, err := netip.ParseAddr(h); err == nil {
		return publicIP(a)
	}
	if h == "" || len(h) > 253 || !strings.Contains(h, ".") {
		return ""
	}
	for _, sfx := range []string{".local", ".internal", ".localhost"} {
		if strings.HasSuffix(h, sfx) {
			return ""
		}
	}
	labels := strings.Split(h, ".")
	for _, label := range labels {
		if !hostLabelRE.MatchString(label) {
			return ""
		}
	}
	if isAllDigits(labels[len(labels)-1]) {
		return "" // 127.1, 10.0.1: IP shorthand, not a domain
	}
	return h
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
