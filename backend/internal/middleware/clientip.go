package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// cdnHeader is the header a supported CDN uses to hand the origin the
// address of the visitor it is proxying.
const cdnHeader = "CF-Connecting-IP"

// defaultTrustedProxyCIDRs is loopback only: a proxy on another host or a
// container bridge must be listed in HATAMOTO_TRUSTED_PROXIES, otherwise any
// client could spoof X-Forwarded-For behind a published Docker port.
var defaultTrustedProxyCIDRs = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"), // localhost
	netip.MustParsePrefix("::1/128"),     // IPv6 localhost
}

// trustSet is an immutable snapshot of who may be believed about the client
// address. proxies are our own reverse proxies (loopback by default plus
// HATAMOTO_TRUSTED_PROXIES); cdn holds the edge ranges of the configured CDN
// preset. The two are deliberately separate: only proxies are trusted for
// X-Forwarded-Proto/Host (IsTrustedProxy).
type trustSet struct {
	proxies           []netip.Prefix
	cdn               []netip.Prefix
	proxiesConfigured bool
}

var trust atomic.Pointer[trustSet]

func init() { trust.Store(&trustSet{proxies: defaultTrustedProxyCIDRs}) }

// warnUntrustedXFF makes sure the "forwarded header from an untrusted peer"
// warning is logged once per process.
var warnUntrustedXFF sync.Once

// updateTrust applies edit to a copy of the current set and swaps it in,
// retrying if another writer got there first.
func updateTrust(edit func(ts *trustSet)) {
	for {
		old := trust.Load()
		next := *old
		edit(&next)
		if trust.CompareAndSwap(old, &next) {
			return
		}
	}
}

// SetTrustedProxies replaces the trusted-proxy CIDR list (HATAMOTO_TRUSTED_PROXIES).
// An empty list restores the loopback-only default. Entries that are not CIDRs
// are skipped (config validation rejects them at startup).
func SetTrustedProxies(cidrs []string) {
	var parsed []netip.Prefix
	for _, c := range cidrs {
		if p, err := netip.ParsePrefix(strings.TrimSpace(c)); err == nil {
			parsed = append(parsed, p.Masked())
		}
	}
	updateTrust(func(ts *trustSet) {
		ts.proxiesConfigured = len(cidrs) > 0
		ts.proxies = defaultTrustedProxyCIDRs
		if ts.proxiesConfigured {
			ts.proxies = parsed
		}
	})
}

// SetTrustedCDN swaps the CDN edge ranges. The slice is copied; an empty or nil
// slice switches CDN handling off. Safe to call while requests are served.
func SetTrustedCDN(p []netip.Prefix) {
	cp := slices.Clone(p)
	updateTrust(func(ts *trustSet) { ts.cdn = cp })
}

// IsTrustedCDNIP reports whether a is inside the configured CDN edge ranges.
func IsTrustedCDNIP(a netip.Addr) bool { return inPrefixes(trust.Load().cdn, a) }

// ClientIPSource says which header the client address is read from.
type ClientIPSource struct {
	// Header is the request header carrying the visitor address.
	Header string
	// CDN is true when the CDN preset is active.
	CDN bool
	// ProxiesConfigured is true when HATAMOTO_TRUSTED_PROXIES supplied a list.
	ProxiesConfigured bool
}

// ClientIPInfo describes how GetClientIP currently derives the address.
func ClientIPInfo() ClientIPSource {
	ts := trust.Load()
	src := ClientIPSource{Header: "X-Forwarded-For", ProxiesConfigured: ts.proxiesConfigured}
	if len(ts.cdn) > 0 {
		src.Header, src.CDN = cdnHeader, true
	}
	return src
}

// IsTrustedProxy reports whether remoteAddr (host:port or bare IP) is a trusted
// proxy, i.e. whether its X-Forwarded-* headers may be believed. CDN edge
// ranges do not count.
func IsTrustedProxy(remoteAddr string) bool {
	a, ok := peerAddr(remoteAddr)
	return ok && inPrefixes(trust.Load().proxies, a)
}

func inPrefixes(set []netip.Prefix, a netip.Addr) bool {
	for _, p := range set {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// peerAddr parses a host:port or bare IP into an unmapped address.
func peerAddr(remoteAddr string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

// GetClientIP extracts the client IP from the request as a string. Forwarded
// headers are honoured only when the immediate peer is a trusted proxy (or a
// CDN edge when the CDN preset is on). Exported so other packages share one
// trust policy.
func GetClientIP(r *http.Request) string {
	return getIP(r)
}

// getIP derives the client address.
//
// With a trusted peer the X-Forwarded-For chain is walked from the right,
// skipping our own proxies; the first remaining hop is the earliest one we can
// trust. If that hop is a CDN edge, the visitor is the valid CF-Connecting-IP
// (the CDN set vouches for the hop that carried it), else the next hop to the
// left that is not an edge. Without the CDN preset the CDN set is empty and
// only the plain walk applies. An untrusted peer is taken at face value.
func getIP(r *http.Request) string {
	ts := trust.Load()
	peer, peerOK := peerAddr(r.RemoteAddr)
	peerProxy := peerOK && inPrefixes(ts.proxies, peer)
	peerCDN := peerOK && inPrefixes(ts.cdn, peer)

	if peerProxy || peerCDN {
		parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(parts) - 1; i >= 0; i-- {
			hop := strings.TrimSpace(parts[i])
			a, err := netip.ParseAddr(hop)
			if err != nil {
				continue
			}
			a = a.Unmap()
			if inPrefixes(ts.proxies, a) {
				continue
			}
			return resolveHop(ts, r, hop, a, parts[:i])
		}
		if peerCDN {
			return resolveHop(ts, r, peer.String(), peer, nil)
		}
		if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
			if _, err := netip.ParseAddr(xrip); err == nil {
				return xrip
			}
		}
	}

	if !ts.proxiesConfigured && !peerProxy && !peerCDN && r.Header.Get("X-Forwarded-For") != "" {
		warnUntrustedXFF.Do(func() {
			slog.Warn("ignoring X-Forwarded-For from a non-loopback peer; if hatamoto runs behind a proxy, set HATAMOTO_TRUSTED_PROXIES to its address (in Docker, the bridge gateway such as 172.18.0.1, or the proxy container)",
				"peer", r.RemoteAddr)
		})
	}

	if !peerOK {
		return r.RemoteAddr
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// resolveHop returns hop unless it is a CDN edge, in which case it looks for
// the visitor behind it: the CDN header first, then the remaining (left) XFF
// entries skipping other edges. rest holds the entries left of hop.
func resolveHop(ts *trustSet, r *http.Request, hop string, a netip.Addr, rest []string) string {
	if !inPrefixes(ts.cdn, a) {
		return hop
	}
	if h, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get(cdnHeader))); err == nil && h.Zone() == "" {
		h = h.Unmap()
		if !inPrefixes(ts.cdn, h) && !inPrefixes(ts.proxies, h) {
			return h.String()
		}
	}
	for i := len(rest) - 1; i >= 0; i-- {
		cand := strings.TrimSpace(rest[i])
		c, err := netip.ParseAddr(cand)
		if err != nil {
			continue
		}
		c = c.Unmap()
		if inPrefixes(ts.proxies, c) || inPrefixes(ts.cdn, c) {
			continue
		}
		return cand
	}
	return hop
}
