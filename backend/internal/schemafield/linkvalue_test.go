package schemafield

import (
	"strings"
	"testing"
)

func TestLinkHost(t *testing.T) {
	long := "https://a.example/" + strings.Repeat("x", MaxLinkValue-len("https://a.example/"))
	tests := []struct {
		name, in, host string
		ok             bool
	}{
		{"plain", "https://tickets.example/e/1?x=1#y", "tickets.example", true},
		{"punycode", "https://xn--bcher-kva.example/", "xn--bcher-kva.example", true},
		{"no path", "https://a.example", "a.example", true},
		{"exactly 2048", long, "a.example", true},
		{"2049", long + "x", "", false},
		{"empty", "", "", false},
		{"scheme only", "https://", "", false},
		{"http", "http://a.example/", "", false},
		{"upper scheme", "HTTPS://a.example/", "", false},
		{"upper host", "https://Tickets.example/", "", false},
		{"javascript", "javascript:alert(1)", "", false},
		{"user", "https://user@a.example/", "", false},
		{"user password", "https://u:p@a.example/", "", false},
		{"private ipv4", "https://10.0.0.1/", "", false},
		{"public ipv4", "https://8.8.8.8/", "", false},
		{"decimal ip", "https://2130706433/", "", false},
		{"hex ip", "https://0x7f.1/", "", false},
		{"ipv6 loopback", "https://[::1]/", "", false},
		{"ipv6 public", "https://[2001:db8::1]/", "", false},
		{"port", "https://a.example:8443/", "", false},
		{"default port", "https://a.example:443/", "", false},
		{"single label", "https://localhost/", "", false},
		{"trailing dot", "https://a.example./", "", false},
		{"encoded dot", "https://a%2eexample/", "", false},
		{"backslash", `https://a.example\@b.example/`, "", false},
		{"space", "https://a.example/x y", "", false},
		{"control", "https://a.example/x\ty", "", false},
		{"non-ascii host", "https://b\u00fccher.example/", "", false},
		{"pdf", "https://a.example/t.pdf", "", false},
		{"jpg with query", "https://a.example/p.jpg?x", "", false},
		{"media file in query", "https://a.example/e?f=a.mp4", "", false},
		{"hex ip all labels", "https://0x7f.0x0.0x0.0x1/", "", false},
		{"hex ip two labels", "https://0x7f.0x1/", "", false},
		{"octal ip", "https://0177.0.0.1/", "", false},
		{"octal hex ip", "https://0177.0.0.0x1/", "", false},
		{"upper hex ip", "https://0X7F.0X1/", "", false},
		{"bare 0x last label", "https://a.0x/", "", false},
		{"hex-like but a name", "https://0xdead.example/", "0xdead.example", true},
		{"less than", "https://a.example/<b>", "", false},
		{"greater than", "https://a.example/x>", "", false},
		{"double quote", `https://a.example/"x`, "", false},
		{"single quote", "https://a.example/'x", "", false},
		{"backtick", "https://a.example/`x", "", false},
		{"quote closes attribute", `https://a.example/"><img src=x onerror=alert(1)>`, "", false},
		{"nul", "https://a.example/x\x00", "", false},
		{"del", "https://a.example/x\x7f", "", false},
		{"newline", "https://a.example/x\ny", "", false},
		{"leading dash label", "https://-a.example/", "", false},
	}
	for _, tt := range tests {
		host, ok := LinkHost(tt.in)
		if ok != tt.ok || host != tt.host {
			t.Errorf("%s: LinkHost(%q) = %q, %v; want %q, %v", tt.name, tt.in, host, ok, tt.host, tt.ok)
		}
	}
}

func TestLinkFieldDef(t *testing.T) {
	plain := func() map[string]any { return map[string]any{"type": "string", "format": "uri"} }
	with := func(k string, v any) map[string]any { m := plain(); m[k] = v; return m }
	tests := []struct {
		name string
		key  string
		def  any
		want bool
	}{
		{"plain", "ticket_url", plain(), true},
		{"title and description", "ticket_url", with("title", "Ticket link"), true},
		{"x-hm-field url", "ticket_url", with("x-hm-field", "url"), false},
		{"x-media", "ticket_url", with("x-media", true), false},
		{"x-hm-alias", "ticket_url", with("x-hm-alias", []any{"a"}), false},
		{"email format", "ticket_url", map[string]any{"type": "string", "format": "email"}, false},
		{"no format", "ticket_url", map[string]any{"type": "string"}, false},
		{"number", "ticket_url", map[string]any{"type": "number", "format": "uri"}, false},
		{"not an object", "ticket_url", "uri", false},
		{"nil", "ticket_url", nil, false},
		{"og_image", "og_image", plain(), false},
		{"image_url", "image_url", plain(), false},
		{"logo", "logo", plain(), false},
	}
	for _, tt := range tests {
		if got := LinkFieldDef(tt.key, tt.def); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestSameSiteHost(t *testing.T) {
	own := []string{"Example.test", "www.other.test"}
	for host, want := range map[string]bool{
		"example.test": true, "www.example.test": true, "EXAMPLE.test": true,
		"other.test": true, "www.other.test": true,
		"tickets.example.test": false, "example.com": false, "": false,
	} {
		if got := SameSiteHost(host, own); got != want {
			t.Errorf("SameSiteHost(%q) = %v, want %v", host, got, want)
		}
	}
	if SameSiteHost("example.test", nil) {
		t.Error("no own hosts: want false")
	}
}
