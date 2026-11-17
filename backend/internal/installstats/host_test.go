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
	}
	for _, c := range cases {
		if got := NormalizeHost(c.in); got != c.want {
			t.Errorf("NormalizeHost(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
