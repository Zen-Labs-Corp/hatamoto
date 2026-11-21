package webconf

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/statichost"
)

var update = flag.Bool("update", false, "rewrite golden files")

func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/webconf -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from golden (run with -update to accept):\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

func testHeaders() []Header {
	return []Header{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "SAMEORIGIN"},
		{"Referrer-Policy", "strict-origin-when-cross-origin"},
		{"Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; form-action 'self'"},
	}
}

func sameHostInput() Input {
	return Input{
		Site:     "https://example.com",
		Root:     "/srv/site/public",
		Upstream: "127.0.0.1:8090",
		Proxy:    statichost.ProxyPaths("/admin", "/api-public"),
		Headers:  testHeaders(),
	}
}

func liveOriginInput() Input {
	in := sameHostInput()
	in.Live = "https://live.example.com"
	return in
}

func TestCaddyGolden(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]Input{"caddy-samehost.golden": sameHostInput(), "caddy-liveorigin.golden": liveOriginInput()} {
		out, err := Generate(in)
		if err != nil {
			t.Fatal(err)
		}
		compareGolden(t, name, out.Caddy)
	}
}

func TestNginxGolden(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]Input{"nginx-samehost.golden": sameHostInput(), "nginx-liveorigin.golden": liveOriginInput()} {
		out, err := Generate(in)
		if err != nil {
			t.Fatal(err)
		}
		compareGolden(t, name, out.Nginx)
	}
}

func TestNginxLiveOriginSplitsServers(t *testing.T) {
	t.Parallel()
	same, _ := Generate(sameHostInput())
	live, _ := Generate(liveOriginInput())
	if !strings.Contains(same.Nginx, "location ^~ /forms/ {") || strings.Count(same.Nginx, "server {") != 1 {
		t.Errorf("same host must be one server with proxy locations:\n%s", same.Nginx)
	}
	site, liveServer, ok := strings.Cut(live.Nginx, "\n\nserver {")
	if !ok || strings.Contains(site, "proxy_pass") || !strings.Contains(liveServer, "server_name live.example.com;") ||
		!strings.Contains(liveServer, "proxy_pass http://127.0.0.1:8090;") {
		t.Errorf("live origin must split the proxy from the files:\n%s", live.Nginx)
	}
	// Headers sit only in the static locations: add_header in a proxied location would drop inherited ones.
	if strings.Count(same.Nginx, "add_header X-Frame-Options") != 2 {
		t.Errorf("headers must be set in / and /404.html only:\n%s", same.Nginx)
	}
}

func TestGenerateRejectsUnsafeValues(t *testing.T) {
	t.Parallel()
	for name, mut := range map[string]func(*Input){
		"root semicolon":  func(in *Input) { in.Root = "/srv/a;b" },
		"root newline":    func(in *Input) { in.Root = "/srv/a\nb" },
		"root brace":      func(in *Input) { in.Root = "/srv/a}" },
		"root space":      func(in *Input) { in.Root = "/srv/my site" },
		"root dollar":     func(in *Input) { in.Root = "/srv/$x" },
		"root relative":   func(in *Input) { in.Root = "srv/site" },
		"upstream space":  func(in *Input) { in.Upstream = "127.0.0.1 :8090" },
		"upstream noport": func(in *Input) { in.Upstream = "127.0.0.1" },
		"upstream brace":  func(in *Input) { in.Upstream = "a{b}:80" },
		"proxy path":      func(in *Input) { in.Proxy = append(in.Proxy, statichost.ProxyPath{Path: "/a b"}) },
		"proxy brace":     func(in *Input) { in.Proxy = append(in.Proxy, statichost.ProxyPath{Path: "/a{"}) },
		"site path":       func(in *Input) { in.Site = "https://example.com/x" },
		"site scheme":     func(in *Input) { in.Site = "ftp://example.com" },
		"site host":       func(in *Input) { in.Site = "https://exa mple.com" },
		"live host":       func(in *Input) { in.Live = "https://live.example.com;x" },
		"header quote":    func(in *Input) { in.Headers = []Header{{"X-A", `a"b`}} },
		"header var":      func(in *Input) { in.Headers = []Header{{"X-A", "${x}"}} },
		"header newline":  func(in *Input) { in.Headers = []Header{{"X-A", "a\nb"}} },
		"header name":     func(in *Input) { in.Headers = []Header{{"X A", "a"}} },
	} {
		in := sameHostInput()
		mut(&in)
		out, err := Generate(in)
		if !errors.Is(err, ErrUnsafe) || out.Caddy != "" || out.Nginx != "" {
			t.Errorf("%s: err %v, output %q", name, err, out)
		}
	}
}

func TestCaddyLiveOriginSplitsBlocks(t *testing.T) {
	t.Parallel()
	same, _ := Generate(sameHostInput())
	live, _ := Generate(liveOriginInput())
	if !strings.Contains(same.Caddy, "reverse_proxy 127.0.0.1:8090") || strings.Count(same.Caddy, "\n}\n") != 1 {
		t.Errorf("same host must be one block with a proxy:\n%s", same.Caddy)
	}
	site, liveBlock, ok := strings.Cut(live.Caddy, "\n\nlive.example.com {")
	if !ok || strings.Contains(site, "reverse_proxy") || !strings.Contains(liveBlock, "reverse_proxy 127.0.0.1:8090") {
		t.Errorf("live origin must split the proxy from the files:\n%s", live.Caddy)
	}
}
