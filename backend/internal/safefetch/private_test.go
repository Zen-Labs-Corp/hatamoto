package safefetch

import (
	"net/netip"
	"testing"
)

func TestPrivateNetworkAddr(t *testing.T) {
	t.Parallel()
	allowed := []string{"127.0.0.1", "::1", "10.1.2.3", "172.16.0.1", "192.168.1.10", "100.100.1.1",
		"169.254.1.1", "fd00::1", "fe80::1%eth0", "::ffff:10.0.0.1"}
	refused := []string{"0.0.0.0", "::", "224.0.0.1", "ff02::1", "255.255.255.255", "8.8.8.8",
		"203.0.113.5", "2001:db8::1", "::ffff:8.8.8.8", "172.32.0.1", "100.128.0.1"}
	for _, s := range allowed {
		if !PrivateNetworkAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
	for _, s := range refused {
		if PrivateNetworkAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s should be refused", s)
		}
	}
	if PrivateNetworkAddr(netip.Addr{}) {
		t.Error("zero address should be refused")
	}
}

func TestBlockedPrefixCount(t *testing.T) {
	t.Parallel()
	if n := len(blockedPrefixes); n != 26 {
		t.Fatalf("blockedPrefixes has %d entries, want 26", n)
	}
}
