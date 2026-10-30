package middleware

import (
	"net/netip"
	"sync"
	"testing"
)

var testCDN = []netip.Prefix{
	netip.MustParsePrefix("172.64.0.0/13"),
	netip.MustParsePrefix("104.16.0.0/13"),
	netip.MustParsePrefix("2606:4700::/32"),
}

func TestGetIP_CDN(t *testing.T) {
	cases := []struct {
		name       string
		cdn        bool
		remoteAddr string
		xff        string
		header     string
		proxies    []string
		want       string
	}{
		{"preset on: trusted peer, edge hop, header wins", true, "127.0.0.1:1", "172.64.1.1", "203.0.113.9", nil, "203.0.113.9"},
		{"preset off: header never read", false, "127.0.0.1:1", "172.64.1.1", "203.0.113.9", nil, "172.64.1.1"},
		{"forged header from untrusted peer ignored", true, "198.51.100.5:1", "1.2.3.4", "203.0.113.9", nil, "198.51.100.5"},
		{"trusted peer, non-CDN hop: header ignored", true, "127.0.0.1:1", "198.51.100.77", "203.0.113.9", nil, "198.51.100.77"},
		{"proxy overwrites XFF with the edge", true, "127.0.0.1:1", "104.16.0.5", "203.0.113.9", nil, "203.0.113.9"},
		{"proxy appends: edge then visitor in XFF", true, "127.0.0.1:1", "203.0.113.9, 104.16.0.5", "", nil, "203.0.113.9"},
		{"proxy appends and header agrees", true, "127.0.0.1:1", "203.0.113.9, 104.16.0.5", "203.0.113.9", nil, "203.0.113.9"},
		{"direct CDN peer, no XFF", true, "172.64.9.9:443", "", "203.0.113.9", nil, "203.0.113.9"},
		{"direct CDN peer without header keeps the peer", true, "172.64.9.9:443", "", "", nil, "172.64.9.9"},
		{"edge hop without header and no other hop", true, "127.0.0.1:1", "172.64.1.1", "", nil, "172.64.1.1"},
		{"v6 visitor", true, "127.0.0.1:1", "2606:4700::1", "2001:db8::7", nil, "2001:db8::7"},
		{"v4-mapped header", true, "127.0.0.1:1", "172.64.1.1", "::ffff:203.0.113.9", nil, "203.0.113.9"},
		{"v4-mapped peer", true, "[::ffff:127.0.0.1]:1", "172.64.1.1", "203.0.113.9", nil, "203.0.113.9"},
		{"header equal to an edge is ignored", true, "127.0.0.1:1", "172.64.1.1", "104.16.0.9", nil, "172.64.1.1"},
		{"header equal to a proxy is ignored", true, "127.0.0.1:1", "172.64.1.1", "127.0.0.1", nil, "172.64.1.1"},
		{"garbage header ignored", true, "127.0.0.1:1", "172.64.1.1", "not-an-ip", nil, "172.64.1.1"},
		{"configured proxy chain plus edge", true, "10.0.0.2:1", "172.64.1.1, 10.0.0.3", "203.0.113.9", []string{"10.0.0.0/8"}, "203.0.113.9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			SetTrustedProxies(c.proxies)
			if c.cdn {
				SetTrustedCDN(testCDN)
			}
			t.Cleanup(func() { SetTrustedProxies(nil); SetTrustedCDN(nil) })
			r := reqWith(c.remoteAddr, c.xff)
			if c.header != "" {
				r.Header.Set("CF-Connecting-IP", c.header)
			}
			if got := getIP(r); got != c.want {
				t.Errorf("getIP() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestGetIP_CDNDoesNotWidenProxyTrust(t *testing.T) {
	SetTrustedCDN(testCDN)
	t.Cleanup(func() { SetTrustedCDN(nil) })
	if IsTrustedProxy("172.64.1.1:443") {
		t.Error("a CDN edge must not be a trusted proxy for forwarded proto/host")
	}
	if !IsTrustedProxy("127.0.0.1:1") {
		t.Error("loopback stays trusted")
	}
	if !IsTrustedCDNIP(netip.MustParseAddr("104.16.0.1")) || IsTrustedCDNIP(netip.MustParseAddr("8.8.8.8")) {
		t.Error("IsTrustedCDNIP mismatch")
	}
}

func TestClientIPInfo(t *testing.T) {
	t.Cleanup(func() { SetTrustedProxies(nil); SetTrustedCDN(nil) })
	if got := ClientIPInfo(); got.Header != "X-Forwarded-For" || got.CDN || got.ProxiesConfigured {
		t.Errorf("default = %+v", got)
	}
	SetTrustedProxies([]string{"10.0.0.0/8"})
	SetTrustedCDN(testCDN)
	if got := ClientIPInfo(); got.Header != "CF-Connecting-IP" || !got.CDN || !got.ProxiesConfigured {
		t.Errorf("cdn = %+v", got)
	}
	SetTrustedCDN(nil)
	if ClientIPInfo().CDN {
		t.Error("empty set must switch the CDN off")
	}
}

func TestSetTrustedCDN_CopiesInput(t *testing.T) {
	in := append([]netip.Prefix(nil), testCDN...)
	SetTrustedCDN(in)
	t.Cleanup(func() { SetTrustedCDN(nil) })
	in[0] = netip.MustParsePrefix("8.8.8.0/24")
	if IsTrustedCDNIP(netip.MustParseAddr("8.8.8.8")) {
		t.Error("caller mutation leaked into the trust set")
	}
}

// Run with -race: lookups must be safe while the ranges are swapped.
func TestGetIP_ConcurrentSwap(t *testing.T) {
	t.Cleanup(func() { SetTrustedProxies(nil); SetTrustedCDN(nil) })
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := reqWith("127.0.0.1:1", "172.64.1.1")
			r.Header.Set("CF-Connecting-IP", "203.0.113.9")
			for {
				select {
				case <-stop:
					return
				default:
				}
				if got := getIP(r); got != "203.0.113.9" && got != "172.64.1.1" {
					t.Errorf("unexpected %q", got)
					return
				}
				_ = ClientIPInfo()
			}
		}()
	}
	for i := range 2000 {
		if i%2 == 0 {
			SetTrustedCDN(testCDN)
		} else {
			SetTrustedCDN(nil)
		}
		SetTrustedProxies(nil)
	}
	close(stop)
	wg.Wait()
}
