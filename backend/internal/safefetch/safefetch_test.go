package safefetch

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestBlockedAddr(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "192.168.0.1", "100.64.0.1", "::1", "fe80::1", "fc00::1", "::ffff:10.0.0.1", "2002:0a00:0001::1", "0.0.0.0", "224.0.0.1"} {
		if !BlockedAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s must be blocked", s)
		}
	}
	for _, s := range []string{"93.184.216.34", "8.8.8.8", "2606:4700:4700::1111"} {
		if BlockedAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s must be allowed", s)
		}
	}
}

func TestDialerRefusesMixedAnswers(t *testing.T) {
	d := &Dialer{Lookup: func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.1")}, nil
	}}
	if _, err := d.DialContext(context.Background(), "tcp", "rebind.test:80"); !errors.Is(err, ErrBlocked) {
		t.Errorf("one private answer must refuse the host: %v", err)
	}
	if _, err := d.DialContext(context.Background(), "tcp", "127.0.0.1:1"); !errors.Is(err, ErrBlocked) {
		t.Errorf("IP literal: %v", err)
	}
}
