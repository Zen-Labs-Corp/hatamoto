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

func testRedirects() []Redirect {
	return []Redirect{
		{"/old-page", "/new-page", 301},
		{"/promo", "https://example.com/offers?src=old", 302},
		{"/blog/:p1/amp", "/articles/:p1", 301},
		{"/docs/*", "/help/:splat", 308},
		{"/gone", "/elsewhere", 410},
	}
}

func sameHostInput() Input {
	return Input{
		Site:     "https://example.com",
		Root:     "/srv/site/public",
		Upstream: "127.0.0.1:8090",
		Proxy:    statichost.ProxyPaths("/admin", "/api-public"),
		Headers:  testHeaders(),

		MaxBodyMB: 15,
		Redirects: testRedirects(),
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

// Hatamoto's media, theme assets and fonts are served from the folder when the
// file is there; a theme preview (query or cookie) always goes to Hatamoto.
func TestSameHostPassesFallbackPrefixesAndThemePreview(t *testing.T) {
	t.Parallel()
	same, _ := Generate(sameHostInput())
	live, _ := Generate(liveOriginInput())
	for _, want := range []string{
		"@hatamoto_preview_query query hm_theme_preview=*",
		"@hatamoto_preview_cookie header_regexp Cookie (^|;\\s*)hm_theme_preview=",
		"@hatamoto_fallback path /.well-known/* /fonts/* /media/* /theme/*",
		"@missing not file",
		"reverse_proxy @missing 127.0.0.1:8090",
	} {
		if !strings.Contains(same.Caddy, want) {
			t.Errorf("caddy same host lacks %q", want)
		}
	}
	for _, want := range []string{
		"location ^~ /media/ {",
		"location ^~ /theme/ {",
		"location ^~ /fonts/ {",
		"location @hatamoto {",
		"if ($arg_hm_theme_preview != \"\") {",
		"if ($cookie_hm_theme_preview != \"\") {",
		"error_page 418 = @hatamoto;",
	} {
		if !strings.Contains(same.Nginx, want) {
			t.Errorf("nginx same host lacks %q", want)
		}
	}
	// The static origin of a split install never sees these paths.
	for _, bad := range []string{"hm_theme_preview", "@hatamoto", "/media/"} {
		if strings.Contains(live.Caddy, bad) || strings.Contains(live.Nginx, bad) {
			t.Errorf("live origin config must not mention %q", bad)
		}
	}
}

// nginx rejects a body over 1 MB by default, so an admin upload would get a 413
// before Hatamoto saw it; MCP streams, so it must not be buffered.
func TestNginxUploadLimitAndStreaming(t *testing.T) {
	t.Parallel()
	same, _ := Generate(sameHostInput())
	live, _ := Generate(liveOriginInput())
	for name, conf := range map[string]string{"same host": same.Nginx, "live origin": live.Nginx} {
		if !strings.Contains(conf, "client_max_body_size 15m;") || !strings.Contains(conf, "proxy_http_version 1.1;") {
			t.Errorf("%s: no body limit or HTTP/1.1 upstream:\n%s", name, conf)
		}
	}
	for _, loc := range []string{"location = /mcp {", "location ^~ /mcp/ {"} {
		_, rest, ok := strings.Cut(same.Nginx, loc)
		block, _, _ := strings.Cut(rest, "}")
		if !ok || !strings.Contains(block, "proxy_buffering off;") {
			t.Errorf("%s must turn proxy buffering off", loc)
		}
	}
	if strings.Count(same.Nginx, "proxy_buffering off;") != 2 {
		t.Error("only the MCP locations stream")
	}
	in := sameHostInput()
	in.MaxBodyMB = 0
	if out, _ := Generate(in); strings.Contains(out.Nginx, "client_max_body_size") {
		t.Error("an unset limit must leave nginx's default")
	}
	in.MaxBodyMB = -1
	if _, err := Generate(in); !errors.Is(err, ErrUnsafe) {
		t.Errorf("negative limit: %v", err)
	}
}

// Caddy's error route runs for every failed request, proxy failures included.
func TestCaddyErrorRouteIsFor404Only(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]Input{"same host": sameHostInput(), "live origin": liveOriginInput()} {
		out, _ := Generate(in)
		_, errs, ok := strings.Cut(out.Caddy, "handle_errors {")
		errs, _, _ = strings.Cut(errs, "\n\t}\n")
		if !ok || !strings.Contains(errs, "@notfound expression {err.status_code} == 404") || !strings.Contains(errs, "handle @notfound {") {
			t.Fatalf("%s: the error route is not limited to 404:\n%s", name, out.Caddy)
		}
		for _, h := range testHeaders() {
			if !strings.Contains(errs, h.Name+" ") {
				t.Errorf("%s: the 404 page lacks %s", name, h.Name)
			}
		}
		if strings.Count(errs, "rewrite * /404.html") != 1 || strings.Contains(strings.Split(errs, "handle @notfound")[0], "rewrite") {
			t.Errorf("%s: the rewrite must sit inside the 404 handle", name)
		}
	}
}

// A theme asset kept at the same path in every theme (a script, an image) must
// come from Hatamoto, not from the export, while a preview is on.
func TestNginxPreviewReachesHatamotoFromFallbackPrefixes(t *testing.T) {
	t.Parallel()
	got, _ := Generate(sameHostInput())
	for _, p := range []string{"/media/", "/theme/", "/fonts/"} {
		want := "location ^~ " + p + " {\n\t\tif ($arg_hm_theme_preview != \"\") {\n\t\t\treturn 418;\n\t\t}\n" +
			"\t\tif ($cookie_hm_theme_preview != \"\") {\n\t\t\treturn 418;\n\t\t}\n\t\ttry_files $uri @hatamoto;"
		if !strings.Contains(got.Nginx, want) {
			t.Errorf("nginx %s block does not hand a preview to Hatamoto", p)
		}
	}
}

// certbot --webroot writes its token into the folder; Hatamoto must not take
// /.well-known/acme-challenge/ over.
func TestACMEChallengeIsServedFromTheFolder(t *testing.T) {
	t.Parallel()
	got, _ := Generate(sameHostInput())
	if !strings.Contains(got.Nginx, "location ^~ /.well-known/ {") || !strings.Contains(got.Nginx, "try_files $uri @hatamoto;") {
		t.Error("nginx must serve /.well-known/ from the folder first")
	}
	if strings.Contains(got.Nginx, "location ^~ /.well-known/ {\n\t\tproxy_pass") {
		t.Error("nginx proxies all of /.well-known/")
	}
	if !strings.Contains(got.Caddy, "/.well-known/*") || strings.Contains(strings.Split(got.Caddy, "@hatamoto_fallback")[0], "/.well-known/*") {
		t.Error("caddy must list /.well-known/ among the file-first paths")
	}
}
