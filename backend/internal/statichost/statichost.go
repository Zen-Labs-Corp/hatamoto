// Package statichost holds the static hosting snapshot the island code reads:
// whether the site is published as static files, the live origin L that keeps
// answering islands, and the static origin S (the Site URL origin).
//
// It is a small leaf (stdlib plus x/net/idna) so both the server and the admin can import it.
package statichost

import (
	"context"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"

	"golang.org/x/net/idna"
)

// StaticHosting is one immutable snapshot. Origins are normalised (see
// NormalizeOrigin).
type StaticHosting struct {
	Static bool   // the site is published as static files
	Live   string // live origin L, "" = unset
	Site   string // static origin S, "" = unset
}

// Active reports whether static-mode island behaviour applies. Both origins
// must be known and on different hosts: with L == S there is nothing to split,
// and when only the scheme differs the live 301 (matched by host) would send a
// request for S back to S, a loop.
func (h StaticHosting) Active() bool {
	return h.Static && h.Live != "" && h.Site != "" && originHost(h.Live) != originHost(h.Site)
}

// InactiveReason names why a static-mode snapshot is not active, or "" when it
// is active or the site is served.
func (h StaticHosting) InactiveReason() string {
	switch {
	case !h.Static || h.Active():
		return ""
	case h.Live == "":
		return "the live origin is empty or not a plain origin (scheme://host[:port])"
	case h.Site == "":
		return "the Site URL is unset or not a plain origin (no path allowed)"
	default:
		return "the live origin and the Site URL share one host (a scheme-only difference counts)"
	}
}

// originHost is the host[:port] of a normalised origin.
func originHost(o string) string {
	_, host, _ := strings.Cut(o, "://")
	return host
}

// AllowsOrigin reports whether an Origin header value is exactly S while the
// snapshot is active. The opaque origin "null" is never allowed here: any
// sandboxed page can send it, so it must not be treated as the static site.
func (h StaticHosting) AllowsOrigin(origin string) bool {
	return h.Active() && origin != "" && origin == h.Site
}

// LiveHost is the host[:port] of Live, or "" when inactive. The live 301 uses
// it so only requests addressed to L are redirected.
func (h StaticHosting) LiveHost() string {
	if !h.Active() {
		return ""
	}
	return originHost(h.Live)
}

// NormalizeOrigin returns "scheme://host[:port]". Only http and https are
// accepted; the host is lowercased and a default port (:443 for https, :80 for
// http) is dropped, so two spellings of one origin compare equal. Userinfo,
// query, fragment and any path other than "" or "/" are rejected, because an
// origin that carries them could never equal a browser's Origin header.
func NormalizeOrigin(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	if u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", false
	}
	if u.Path != "" && u.Path != "/" {
		return "", false
	}
	host, ok := normalizeHost(u.Hostname())
	if !ok {
		return "", false
	}
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host, true
}

// normalizeHost accepts a DNS name (letters, digits, hyphens, dots; a Unicode
// name is converted to punycode) or an IP literal, and returns it lowercased,
// with an IPv6 address in brackets. Anything else is rejected, because the
// value is later written into HTML attributes and headers.
func normalizeHost(h string) (string, bool) {
	h = strings.ToLower(h)
	if strings.Contains(h, ":") {
		a, err := netip.ParseAddr(h)
		if err != nil || a.Zone() != "" {
			return "", false
		}
		return "[" + a.String() + "]", true
	}
	ascii, err := idna.Lookup.ToASCII(h)
	if err != nil || ascii == "" {
		return "", false
	}
	for _, label := range strings.Split(strings.TrimSuffix(ascii, "."), ".") {
		if label == "" {
			return "", false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return "", false
			}
		}
	}
	return ascii, true
}

// Holder is the per-Server atomic slot for the snapshot. The zero value (and a
// nil *Holder) means served mode. It is deliberately not a package-level
// variable: tests run many servers in parallel.
type Holder struct{ p atomic.Pointer[StaticHosting] }

// Load returns the current snapshot; it is nil-safe and never reads the DB.
func (h *Holder) Load() StaticHosting {
	if h == nil {
		return StaticHosting{}
	}
	if v := h.p.Load(); v != nil {
		return *v
	}
	return StaticHosting{}
}

// Store replaces the snapshot after normalising Live and Site. An invalid
// origin is stored as "", which makes the snapshot inactive (served behaviour)
// instead of trusting a malformed value for CORS.
func (h *Holder) Store(v StaticHosting) {
	v.Live, _ = NormalizeOrigin(v.Live)
	v.Site, _ = NormalizeOrigin(v.Site)
	h.p.Store(&v)
}

type exportKey struct{}

// WithExport marks a request rendered by the static export: no live 301, and
// S-relative links are kept.
func WithExport(ctx context.Context) context.Context {
	return context.WithValue(ctx, exportKey{}, true)
}

// IsExport reports whether ctx was marked by WithExport.
func IsExport(ctx context.Context) bool {
	v, _ := ctx.Value(exportKey{}).(bool)
	return v
}
