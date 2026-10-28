package installstats

import (
	"encoding/json"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestForcedOff(t *testing.T) {
	for v, want := range map[string]bool{"off": true, " OFF ": true, "Off": true, "on": false, "": false, "true": false, "0": false} {
		if got := ForcedOff(envOf(map[string]string{EnvTelemetry: v})); got != want {
			t.Errorf("ForcedOff(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestResolveURL(t *testing.T) {
	for _, c := range []struct {
		name, in, want string
		allowLoopback  bool
		warn           bool
	}{
		{"unset", "", DefaultURL, false, false},
		{"https", "https://stats.example.org/ping", "https://stats.example.org/ping", false, false},
		{"http", "http://stats.example.org/ping", DefaultURL, false, true},
		{"loopback http refused by default build", "http://127.0.0.1:8080/ping", DefaultURL, false, true},
		{"loopback http in test build", "http://127.0.0.1:8080/ping", "http://127.0.0.1:8080/ping", true, false},
		{"non-loopback http in test build", "http://example.org/ping", DefaultURL, true, true},
		{"userinfo", "https://u:p@example.org/ping", DefaultURL, false, true},
		{"query", "https://example.org/ping?a=b", DefaultURL, false, true},
		{"empty query", "https://example.org/ping?", DefaultURL, false, true},
		{"fragment", "https://example.org/ping#x", DefaultURL, false, true},
		{"no host", "https:///ping", DefaultURL, false, true},
		{"other scheme", "ftp://example.org/ping", DefaultURL, false, true},
	} {
		got, warn := resolveURL(envOf(map[string]string{EnvURL: c.in}), c.allowLoopback)
		if got != c.want || (warn != "") != c.warn {
			t.Errorf("%s: got %q warn %q, want %q warn=%v", c.name, got, warn, c.want, c.warn)
		}
		if strings.Contains(warn, "u:p") {
			t.Errorf("%s: warning leaks the URL: %q", c.name, warn)
		}
	}
	if got, _ := ResolveURL(envOf(nil)); got != DefaultURL {
		t.Errorf("default = %q", got)
	}
}

func TestPayloadHasExactlyThreeKeys(t *testing.T) {
	for _, b := range [][]byte{mustJSON(t, Payload{}), []byte(Payload{Install: "a", Version: "v1.0.0", Channel: "stable"}.Pretty())} {
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if len(m) != 3 {
			t.Fatalf("payload keys = %v", m)
		}
		for _, k := range []string{"install", "version", "channel"} {
			if _, ok := m[k]; !ok {
				t.Errorf("missing key %q", k)
			}
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
