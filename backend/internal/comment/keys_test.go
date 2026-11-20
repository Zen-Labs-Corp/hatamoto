package comment

import (
	"strings"
	"testing"
)

func TestKeysHashEmail(t *testing.T) {
	t.Parallel()
	k := NewKeys("secret-a")
	if k.HashEmail("  A@Example.TEST ") != k.HashEmail("a@example.test") {
		t.Error("normalisation should make these equal")
	}
	if k.HashEmail("a@example.test") == k.HashEmail("b@example.test") {
		t.Error("different addresses hashed equal")
	}
	if k.HashEmail("a@example.test") == NewKeys("secret-b").HashEmail("a@example.test") {
		t.Error("different secrets hashed equal")
	}
	if k.HashEmail("a+x@example.test") == k.HashEmail("a@example.test") {
		t.Error("plus tags must not be folded")
	}
	if strings.Contains(k.HashEmail("a@example.test"), "example") {
		t.Error("hash leaks the input")
	}
	if EmailDomain(" Me@Sub.Example.TEST ") != "sub.example.test" || EmailDomain("nope") != "" {
		t.Error("EmailDomain wrong")
	}
}

func TestKeysHashIP(t *testing.T) {
	t.Parallel()
	k := NewKeys("secret-a")
	ip1, net1 := k.HashIP("192.0.2.10")
	ip2, net2 := k.HashIP("192.0.2.99")
	if net1 != net2 || ip1 == ip2 || ip1 == "" {
		t.Errorf("IPv4: same /24 should share net hash and differ in ip hash")
	}
	if _, other := k.HashIP("192.0.3.10"); other == net1 {
		t.Error("another /24 shares a net hash")
	}
	a, na := k.HashIP("2001:db8::1")
	b, nb := k.HashIP("2001:db8::ffff")
	if na != nb || a == b {
		t.Error("IPv6: same /64 should share net hash and differ in ip hash")
	}
	if _, other := k.HashIP("2001:db8:0:1::1"); other == na {
		t.Error("another /64 shares a net hash")
	}
	m, nm := k.HashIP("::ffff:192.0.2.10")
	if m != ip1 || nm != net1 {
		t.Error("4-in-6 address should hash like the IPv4 one")
	}
	if i, n := k.HashIP("not an ip"); i != "" || n != "" {
		t.Error("unparsable ip should return empty hashes")
	}
	if ip1 == net1 {
		t.Error("ip and net hashes must use different keys")
	}
	if strings.Contains(ip1, "192.0.2") {
		t.Error("hash leaks the input")
	}
}

func TestKeysSealEmail(t *testing.T) {
	t.Parallel()
	k := NewKeys("secret-a")
	h := k.HashEmail("a@example.test")
	blob, err := k.SealEmail(" A@Example.test", h)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := k.OpenEmail(blob, h); !ok || got != "a@example.test" {
		t.Errorf("OpenEmail = %q, %v", got, ok)
	}
	if _, ok := k.OpenEmail(blob, k.HashEmail("b@example.test")); ok {
		t.Error("opened with another hash as AAD")
	}
	if _, ok := NewKeys("secret-b").OpenEmail(blob, h); ok {
		t.Error("opened with another secret")
	}
	if got, ok := k.OpenEmail([]byte("short"), h); ok || got != "" {
		t.Error("damaged blob should not open")
	}
}
