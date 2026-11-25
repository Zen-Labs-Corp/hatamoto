package schemafield

import (
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

// MaxLinkValue is the longest link a declared link field may hold, in bytes.
const MaxLinkValue = 2048

// linkHostRe is a lower-case ASCII (or punycode) host name with at least one
// dot: no IP literal, no single label such as "localhost", no trailing dot.
var linkHostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// linkDefKeys are the only members a plain link field definition may have.
var linkDefKeys = map[string]bool{"type": true, "format": true, "title": true, "description": true}

// LinkHost reports whether raw is one absolute https link an automatic create
// may carry in a declared link field, and returns its host. It reads raw byte
// for byte, without decoding anything: lower-case "https://" and host, at most
// MaxLinkValue bytes of printable ASCII without a backslash, no user or
// password, no port, a dotted host name that is not an IP address in any
// notation, and no image, audio, video or PDF file name after the host.
func LinkHost(raw string) (string, bool) {
	const prefix = "https://"
	if len(raw) <= len(prefix) || len(raw) > MaxLinkValue || !strings.HasPrefix(raw, prefix) {
		return "", false
	}
	for i := 0; i < len(raw); i++ {
		if c := raw[i]; c < 0x21 || c > 0x7e || c == '\\' {
			return "", false
		}
	}
	rest := raw[len(prefix):]
	authority, tail := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority, tail = rest[:i], rest[i:]
	}
	if !linkHostRe.MatchString(authority) {
		return "", false
	}
	last := authority[strings.LastIndexByte(authority, '.')+1:]
	if strings.Trim(last, "0123456789") == "" {
		return "", false
	}
	if _, err := netip.ParseAddr(authority); err == nil {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Port() != "" || u.Host != authority {
		return "", false
	}
	if mediaExtRe.MatchString(strings.ToLower(tail)) {
		return "", false
	}
	return authority, true
}

// LinkFieldDef reports whether a schema property is a plain link field: only
// type, format, title and description, type string, format uri, and a key no
// reader treats as an image.
func LinkFieldDef(key string, def any) bool {
	d, ok := def.(map[string]any)
	if !ok {
		return false
	}
	for k := range d {
		if !linkDefKeys[k] {
			return false
		}
	}
	if d["type"] != "string" || d["format"] != "uri" {
		return false
	}
	return !IsImageAttributeName(key) && !IsImageField(key, "uri", false)
}

// SameSiteHost reports whether host is one of own, ignoring case and a
// leading "www." on either side.
func SameSiteHost(host string, own []string) bool {
	h := bareHost(host)
	for _, o := range own {
		if bareHost(o) == h {
			return true
		}
	}
	return false
}

func bareHost(h string) string {
	return strings.TrimPrefix(strings.ToLower(h), "www.")
}
