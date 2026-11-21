// Package statichost holds the static hosting snapshot the island code reads:
// whether the site is published as static files, the live origin L that keeps
// answering islands, and the static origin S (the Site URL origin).
//
// It is a stdlib-only leaf so both the server and the admin can import it.
package statichost

import (
	"context"
	"net/url"
	"strings"
	"sync/atomic"
)

// StaticHosting is one immutable snapshot. Origins are normalised (see
// NormalizeOrigin).
type StaticHosting struct {
	Static bool   // the site is published as static files
	Live   string // live origin L, "" = unset
	Site   string // static origin S, "" = unset
}

// Active reports whether static-mode island behaviour applies. Both origins
// must be known and distinct: with L == S there is nothing to split, and a
// redirect from L to S would loop.
func (h StaticHosting) Active() bool {
	return h.Static && h.Live != "" && h.Site != "" && h.Live != h.Site
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
	u, err := url.Parse(h.Live)
	if err != nil {
		return ""
	}
	return u.Host
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
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", false
	}
	if strings.Contains(host, ":") { // IPv6 literal
		host = "[" + host + "]"
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
