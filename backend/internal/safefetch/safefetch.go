// Package safefetch is the outbound-network guard shared by plugin http.fetch
// and the WordPress importer: it resolves a host itself, refuses private,
// loopback, link-local, metadata and other non-public addresses, and connects
// to the checked IP literal so DNS cannot answer differently between the check
// and the connect (rebinding).
package safefetch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

// ErrBlocked is wrapped by every policy refusal (address, scheme, redirect).
var ErrBlocked = errors.New("fetch blocked")

// Lookup resolves a host name to addresses.
type Lookup func(ctx context.Context, host string) ([]netip.Addr, error)

// DefaultLookup uses the system resolver.
func DefaultLookup(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// Errorf returns an error wrapping ErrBlocked.
func Errorf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBlocked, fmt.Sprintf(format, a...))
}

var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/3", // multicast and reserved
		"::/96", "::ffff:0:0:0/96", // IPv4-compatible and SIIT forms embed an IPv4 address
		"::/128", "::1/128", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/32", "2001:db8::/32",
		"fc00::/7", "fe80::/10", "ff00::/8",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// BlockedAddr reports whether a connection to ip must be refused.
func BlockedAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	if ip.Is6() { // 6to4 (2002::/16) and NAT64 embed an IPv4 address
		b := ip.As16()
		if b[0] == 0x20 && b[1] == 0x02 {
			return BlockedAddr(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}))
		}
		if ip.Is4In6() {
			return true
		}
	}
	return false
}

// Dialer connects only to public addresses.
type Dialer struct {
	Lookup Lookup // nil: DefaultLookup
	// AllowPrivate skips the address ranges. For tests and for importing from
	// a site on a private network, never for plugin traffic.
	AllowPrivate bool
	Net          net.Dialer
}

// DialContext resolves, checks and dials the IP literal.
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else {
		lookup := d.Lookup
		if lookup == nil {
			lookup = DefaultLookup
		}
		if ips, err = lookup(ctx, host); err != nil {
			return nil, err
		}
	}
	if len(ips) == 0 {
		return nil, Errorf("%s did not resolve", host)
	}
	if !d.AllowPrivate {
		for _, ip := range ips { // one bad answer refuses the host: no picking among mixed ones
			if BlockedAddr(ip) {
				return nil, Errorf("%s resolves to a blocked address (%s)", host, ip.Unmap())
			}
		}
	}
	var last error
	for _, ip := range ips {
		c, err := d.Net.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, last
}

// ClientOptions configures NewClient.
type ClientOptions struct {
	AllowPrivate bool
	Lookup       Lookup
	Timeout      time.Duration // per request; 0 means 30s
	MaxRedirects int           // 0 means 5
}

// NewClient returns an http.Client whose every connection (and every redirect
// hop, which must stay on http/https) goes through the Dialer guard.
func NewClient(o ClientOptions) *http.Client {
	if o.Timeout == 0 {
		o.Timeout = 30 * time.Second
	}
	if o.MaxRedirects == 0 {
		o.MaxRedirects = 5
	}
	d := &Dialer{Lookup: o.Lookup, AllowPrivate: o.AllowPrivate}
	return &http.Client{
		Timeout: o.Timeout,
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            d.DialContext,
			ForceAttemptHTTP2:      true,
			TLSHandshakeTimeout:    10 * time.Second,
			ResponseHeaderTimeout:  o.Timeout,
			MaxResponseHeaderBytes: 64 << 10,
			MaxIdleConnsPerHost:    8,
			IdleConnTimeout:        30 * time.Second,
		},
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) > o.MaxRedirects {
				return Errorf("more than %d redirects", o.MaxRedirects)
			}
			return CheckScheme(r.URL)
		},
	}
}

// CheckScheme refuses anything but http and https, and credentials in the URL.
func CheckScheme(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return Errorf("scheme %q", u.Scheme)
	}
	if u.User != nil {
		return Errorf("credentials in the URL")
	}
	if u.Hostname() == "" {
		return Errorf("no host")
	}
	return nil
}
