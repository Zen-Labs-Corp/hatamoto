package installstats

import (
	"strings"
	"testing"
)

func TestNormalizeHost(t *testing.T) {
	long := strings.Repeat("a", 60) + "." + strings.Repeat("b", 60) + "." + strings.Repeat("c", 60) + "." + strings.Repeat("d", 60) + ".example.com"
	cases := []struct{ in, want string }{
		{"https://example.com", "example.com"},
		{"https://Example.COM", "example.com"},
		{"https://example.com:8443", "example.com"},
		{"https://www.example.com/blog/x?y=1", "www.example.com"},
		{"https://example.com.", "example.com"},
		{"example.com", "example.com"},
		{"example.com/path", "example.com"},
		{"  https://example.com  ", "example.com"},
		{"https://xn--bcher-kva.example", "xn--bcher-kva.example"},
		{"http://localhost:8080", ""},
		{"localhost", ""},
		{"http://127.0.0.1", ""},
		{"http://127.1", ""},
		{"http://10.0.1", ""},
		{"http://192.168.1.256", ""},
		{"https://example.123", ""},
		{"http://[::1]:8080", ""},
		{"https://a.local", ""},
		{"https://b.internal", ""},
		{"https://c.localhost", ""},
		{"https://" + long, ""},
		{"", ""},
		{"   ", ""},
		{"http://%zz", ""},
		{"https://exa mple.com", ""},
		{"https://-bad.example.com", ""},
		{"https://under_score.example.com", ""},
		{"https://a..com", ""},
		{"intranet", ""},
		{"https://" + strings.Repeat("a", 63) + ".com", strings.Repeat("a", 63) + ".com"},
		{"https://" + strings.Repeat("a", 64) + ".com", ""},
		{"https://" + strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 58) + ".com", ""},
		{"https://trailing-.example.com", ""},
		{"https://example-.com", ""},
		{"https://[2001:db8::1]/x", ""},
		{"http://192.168.1.10:8080", ""},
		{"https://bücher.example", "xn--bcher-kva.example"},
		{"https://münchen", ""},
		{"http://8.8.8.8/", "8.8.8.8"},
		{"https://1.1.1.1:8443/x", "1.1.1.1"},
		{"http://[2606:4700:4700::1111]:8080/", "2606:4700:4700::1111"},
		{"http://[2606:4700:4700::ABCD]/", "2606:4700:4700::abcd"},
		{"2606:4700:4700::1111", "2606:4700:4700::1111"},
		{"http://[2606:4700:4700:0:0:0:0:1111]/", "2606:4700:4700::1111"},
		{"2a00:1450:4001:80b::200e", "2a00:1450:4001:80b::200e"},
		{"http://0.1.2.3", ""},
		{"http://10.0.0.5", ""},
		{"http://100.64.0.1", ""},
		{"http://127.0.0.1", ""},
		{"http://169.254.1.1", ""},
		{"http://172.16.0.1", ""},
		{"http://192.0.0.1", ""},
		{"http://192.0.2.7", ""},
		{"http://192.168.1.10", ""},
		{"http://198.18.0.1", ""},
		{"http://198.51.100.7", ""},
		{"http://203.0.113.7", ""},
		{"http://224.0.0.1", ""},
		{"http://255.255.255.255", ""},
		{"http://[::1]/", ""},
		{"http://[fe80::1]/", ""},
		{"http://[fe80::1%25eth0]/", ""},
		{"http://[fc00::1]/", ""},
		{"http://[::ffff:8.8.8.8]/", ""},
		{"http://[2001:db8::1]/", ""},
		{"http://[2001:2::1]/", ""},
		{"http://[2002::1]/", ""},
		{"http://[4000::1]/", ""},
		{"http://[::]/", ""},
	}
	for _, c := range cases {
		if got := NormalizeHost(c.in); got != c.want {
			t.Errorf("NormalizeHost(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
