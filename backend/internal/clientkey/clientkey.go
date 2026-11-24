// Package clientkey derives the pseudonymous per-plugin visitor keys (client.key)
// and carries the raw client address through a context. It never stores or logs it.
package clientkey

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/netip"
	"strings"

	"hatamoto.dev/core/internal/secretbox"
)

// Prefix returns the canonical address and its network: /24 for IPv4 (4-in-6
// addresses are unmapped first), /64 for IPv6, zone dropped. ok is false for
// an unparsable ip.
func Prefix(ip string) (addr, network string, ok bool) {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return "", "", false
	}
	a = a.Unmap().WithZone("")
	bits := 64
	if a.Is4() {
		bits = 24
	}
	n, err := a.Prefix(bits)
	if err != nil {
		return "", "", false
	}
	return a.String(), n.String(), true
}

// Deriver holds the two per-purpose keys derived from the server secret.
type Deriver struct{ addr, net []byte }

// New returns nil for an empty secret: no secret, no keys.
func New(serverSecret string) *Deriver {
	if serverSecret == "" {
		return nil
	}
	return &Deriver{
		addr: secretbox.DeriveKey(serverSecret, "plugin-client/addr"),
		net:  secretbox.DeriveKey(serverSecret, "plugin-client/net"),
	}
}

// For returns the plugin's keys for ip; "", "" for a nil Deriver, an empty
// plugin or an unparsable ip.
func (d *Deriver) For(plugin, ip string) (addrKey, netKey string) {
	if d == nil || plugin == "" {
		return "", ""
	}
	a, n, ok := Prefix(ip)
	if !ok {
		return "", ""
	}
	return mac(d.addr, plugin+":"+a), mac(d.net, plugin+":"+n)
}

func mac(key []byte, s string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

type ipKey struct{}

// WithIP returns ctx carrying the raw client address. It must never be copied
// into an SDK payload, a log line or an event.
func WithIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ipKey{}, ip)
}

// IPFrom returns the address set by WithIP, or "".
func IPFrom(ctx context.Context) string {
	s, _ := ctx.Value(ipKey{}).(string)
	return s
}
