package pluginhost

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBlockedAddr(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1", "127.1.2.3", "::1", "10.0.0.1", "10.255.255.255", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"169.254.169.254", "169.254.0.1", "fe80::1", "fd00::1", "fc00::1", "0.0.0.0", "::", "100.64.0.1", "224.0.0.1",
		"255.255.255.255", "198.18.0.1", "192.0.2.1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254",
		"64:ff9b::7f00:1", "2002:7f00:1::1", "2002:a9fe:a9fe::1", "ff02::1", "2001:db8::1",
		"::7f00:1", "::a9fe:a9fe", "::ffff:0:7f00:1", "::a00:1", "::ffff:0:a9fe:a9fe",
	} {
		if !blockedAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s must be blocked", s)
		}
	}
	for _, s := range []string{"93.184.216.34", "8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "172.32.0.1", "11.0.0.1"} {
		if blockedAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s must be allowed", s)
		}
	}
}

func TestHostAllowed(t *testing.T) {
	allowed := []string{"api.example.com", "*.googleapis.com", "hooks.example.org:8443"}
	for _, c := range []struct {
		host, port string
		want       bool
	}{
		{"api.example.com", "443", true},
		{"API.Example.com.", "80", true},
		{"api.example.com", "8080", false}, // only 80 and 443 without a port in the entry
		{"evil.api.example.com", "443", false},
		{"example.com", "443", false},
		{"storage.googleapis.com", "443", true},
		{"googleapis.com", "443", false}, // a wildcard is subdomains only
		{"evilgoogleapis.com", "443", false},
		{"hooks.example.org", "8443", true},
		{"hooks.example.org", "443", false},
		{"127.0.0.1", "80", false},
	} {
		if got := hostAllowed(allowed, c.host, c.port); got != c.want {
			t.Errorf("hostAllowed(%s:%s) = %v, want %v", c.host, c.port, got, c.want)
		}
	}
}

// stubLookup answers from a table and counts the questions.
type stubLookup struct {
	answers map[string][]netip.Addr
	calls   atomic.Int32
}

func (s *stubLookup) lookup(_ context.Context, host string) ([]netip.Addr, error) {
	s.calls.Add(1)
	if a, ok := s.answers[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
}

func fetchTo(t *testing.T, f *Fetcher, allowed []string, url string) (*FetchResponse, error) {
	t.Helper()
	return f.Do(context.Background(), "p", allowed, FetchRequest{URL: url, TimeoutMS: 3000})
}

func wantBlocked(t *testing.T, name string, err error) {
	t.Helper()
	if err == nil || !errors.Is(err, ErrFetchBlocked) {
		t.Errorf("%s: err = %v, want ErrFetchBlocked", name, err)
	}
}

func TestFetchBlocksPrivateTargets(t *testing.T) {
	// Every target is on the allowlist, so only the address guard can refuse.
	lk := &stubLookup{answers: map[string][]netip.Addr{
		"loopback.test": {netip.MustParseAddr("127.0.0.1")},
		"meta.test":     {netip.MustParseAddr("169.254.169.254")},
		"ten.test":      {netip.MustParseAddr("10.1.2.3")},
		"v6.test":       {netip.MustParseAddr("::1")},
		"mapped.test":   {netip.MustParseAddr("::ffff:192.168.0.1")},
		// DNS rebinding: the name is allowed and "public" for the first
		// answer, but a blocked address is among the answers.
		"mixed.test": {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("127.0.0.1")},
	}}
	f := &Fetcher{lookup: lk.lookup}
	allowed := []string{"loopback.test", "meta.test", "ten.test", "v6.test", "mapped.test", "mixed.test",
		"127.0.0.1", "169.254.169.254", "10.0.0.5", "[::1]", "::1"}
	for name, url := range map[string]string{
		"127.0.0.1 literal":   "http://127.0.0.1/",
		"127.0.0.1 port":      "http://127.0.0.1:80/admin",
		"metadata literal":    "http://169.254.169.254/latest/meta-data/",
		"10/8 literal":        "http://10.0.0.5/",
		"::1 literal":         "http://[::1]/",
		"loopback by name":    "http://loopback.test/",
		"metadata by name":    "http://meta.test/",
		"10/8 by name":        "http://ten.test/",
		"::1 by name":         "http://v6.test/",
		"mapped v4 by name":   "http://mapped.test/",
		"rebinding mixed":     "http://mixed.test/",
		"https loopback":      "https://127.0.0.1/",
		"decimal-ish literal": "http://2130706433/",
		"zero address":        "http://0.0.0.0/",
	} {
		_, err := fetchTo(t, f, append(allowed, "2130706433", "0.0.0.0"), url)
		if err == nil {
			t.Errorf("%s: fetched", name)
			continue
		}
		// The stub resolver has no answer for a bare decimal "host": that is a
		// resolution failure, not a connection.
		if !errors.Is(err, ErrFetchBlocked) && name != "decimal-ish literal" {
			t.Errorf("%s: err = %v, want ErrFetchBlocked", name, err)
		}
	}
}

func TestFetchAllowlistAndURLRules(t *testing.T) {
	f := NewFetcher()
	for name, c := range map[string]struct {
		allowed []string
		url     string
	}{
		"not listed":        {[]string{"a.example.com"}, "http://b.example.com/"},
		"empty allowlist":   {nil, "http://a.example.com/"},
		"wrong port":        {[]string{"a.example.com"}, "http://a.example.com:22/"},
		"file scheme":       {[]string{"a.example.com"}, "file:///etc/passwd"},
		"gopher scheme":     {[]string{"a.example.com"}, "gopher://a.example.com/"},
		"credentials":       {[]string{"a.example.com"}, "http://user:pw@a.example.com/"},
		"no host":           {[]string{"a.example.com"}, "http:///x"},
		"subdomain of host": {[]string{"a.example.com"}, "http://x.a.example.com/"},
	} {
		_, err := fetchTo(t, f, c.allowed, c.url)
		wantBlocked(t, name, err)
	}
}

func TestFetchRebindingResolvesOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("hello " + r.Host)) }))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	_ = host

	// The name answers with the test server's address first and a poisoned
	// answer afterwards. The fetcher must dial the address it checked and never
	// ask again (allowPrivate only lets the loopback test server through).
	var asked atomic.Int32
	f := &Fetcher{allowPrivate: true, lookup: func(context.Context, string) ([]netip.Addr, error) {
		if asked.Add(1) == 1 {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("10.9.9.9")}, nil
	}}
	res, err := f.Do(context.Background(), "p", []string{"rebind.test:" + port}, FetchRequest{URL: "http://rebind.test:" + port + "/"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || !strings.HasPrefix(res.Body, "hello rebind.test") {
		t.Fatalf("response = %+v", res)
	}
	if n := asked.Load(); n != 1 {
		t.Fatalf("host resolved %d times, want exactly 1 (no second lookup to rebind)", n)
	}
}

func TestFetchRedirects(t *testing.T) {
	var target atomic.Int32
	secret := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target.Add(1)
		_, _ = w.Write([]byte("secret"))
	}))
	defer secret.Close()
	_, secretPort, _ := net.SplitHostPort(secret.Listener.Addr().String())

	mux := http.NewServeMux()
	mux.HandleFunc("/to-listed-private", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:"+secretPort+"/", http.StatusFound)
	})
	mux.HandleFunc("/to-metadata", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	mux.HandleFunc("/to-file", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", http.StatusFound) })
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("fine")) })
	mux.HandleFunc("/to-ok", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ok", http.StatusFound) })
	front := httptest.NewServer(mux)
	defer front.Close()
	_, frontPort, _ := net.SplitHostPort(front.Listener.Addr().String())

	// The front server is reachable (test-only allowPrivate would also open the
	// secret one, so the address guard is exercised in the next test); here the
	// allowlist decides each hop.
	f := &Fetcher{allowPrivate: true, lookup: defaultLookup}
	allowed := []string{"127.0.0.1:" + frontPort}
	base := "http://127.0.0.1:" + frontPort

	if res, err := f.Do(context.Background(), "p", allowed, FetchRequest{URL: base + "/to-ok"}); err != nil || res.Body != "fine" {
		t.Fatalf("a redirect inside the allowlist must work: %v %+v", err, res)
	}
	for _, path := range []string{"/to-listed-private", "/to-metadata", "/to-file"} {
		_, err := f.Do(context.Background(), "p", allowed, FetchRequest{URL: base + path})
		wantBlocked(t, "redirect "+path, err)
	}
	if target.Load() != 0 {
		t.Fatal("the redirect target was requested")
	}
	_, err := f.Do(context.Background(), "p", allowed, FetchRequest{URL: base + "/loop"})
	wantBlocked(t, "redirect loop", err)
}

// A redirect to a private address is stopped by the address guard even when
// the plugin's allowlist names that address (the guard does not trust it).
func TestFetchRedirectToPrivateAddressStoppedByGuard(t *testing.T) {
	var hit atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit.Add(1) }))
	defer internal.Close()
	_, internalPort, _ := net.SplitHostPort(internal.Listener.Addr().String())

	// "public.test" stands for a public site: the stub resolves it to a
	// public address, but the dial is intercepted by the test's dialer below.
	pub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:"+internalPort+"/admin", http.StatusFound)
	}))
	defer pub.Close()
	_, pubPort, _ := net.SplitHostPort(pub.Listener.Addr().String())

	f := NewFetcher()
	f.lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		return nil, errors.New("unexpected lookup of " + host)
	}
	// Without allowPrivate the first hop (loopback) is refused too: the guard
	// holds at every hop, which is the point.
	_, err := f.Do(context.Background(), "p", []string{"127.0.0.1:" + pubPort, "127.0.0.1:" + internalPort},
		FetchRequest{URL: "http://127.0.0.1:" + pubPort + "/"})
	wantBlocked(t, "private first hop", err)
	if hit.Load() != 0 {
		t.Fatal("the internal server was reached")
	}
}

func TestFetchCaps(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxFetchResponseBytes+100)))
	}))
	defer big.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(2 * time.Second) }))
	defer slow.Close()
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "a=b")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(r.Method + " " + r.Header.Get("User-Agent") + " " + r.Header.Get("X-A")))
	}))
	defer echo.Close()
	f := &Fetcher{allowPrivate: true, lookup: defaultLookup}
	allow := func(s *httptest.Server) []string {
		_, p, _ := net.SplitHostPort(s.Listener.Addr().String())
		return []string{"127.0.0.1:" + p}
	}

	res, err := f.Do(context.Background(), "p", allow(big), FetchRequest{URL: big.URL})
	if err != nil || !res.Truncated || len(res.Body) != maxFetchResponseBytes {
		t.Fatalf("response cap: err=%v truncated=%v len=%d", err, res != nil && res.Truncated, len(res.Body))
	}
	start := time.Now()
	if _, err := f.Do(context.Background(), "p", allow(slow), FetchRequest{URL: slow.URL, TimeoutMS: 100}); err == nil || time.Since(start) > time.Second {
		t.Fatalf("timeout not enforced: %v after %s", err, time.Since(start))
	}
	res, err = f.Do(context.Background(), "p", allow(echo), FetchRequest{Method: "POST", URL: echo.URL, Headers: map[string]string{"X-A": "1", "Host": "evil"}, Body: "hi"})
	if err != nil || res.Body != "POST hatamoto-plugin/p 1" {
		t.Fatalf("echo: %v %+v", err, res)
	}
	if _, leaked := res.Headers["set-cookie"]; leaked {
		t.Fatal("Set-Cookie must not be passed on")
	}
	if _, err := f.Do(context.Background(), "p", allow(echo), FetchRequest{Method: "TRACE", URL: echo.URL}); err == nil {
		t.Fatal("TRACE must be refused")
	}
	if _, err := f.Do(context.Background(), "p", allow(echo), FetchRequest{Method: "POST", URL: echo.URL, Body: strings.Repeat("x", maxFetchRequestBytes+1)}); err == nil {
		t.Fatal("oversized request body must be refused")
	}
}

// The environment's proxy settings are ignored: a plugin's request goes to the
// checked address, never through HTTPS_PROXY/HTTP_PROXY.
func TestFetchIgnoresProxyEnvironment(t *testing.T) {
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { proxied.Add(1) }))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("ALL_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("http_proxy", proxy.URL)
	f := &Fetcher{allowPrivate: true, lookup: defaultLookup}
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("direct")) }))
	defer echo.Close()
	_, p, _ := net.SplitHostPort(echo.Listener.Addr().String())
	res, err := f.Do(context.Background(), "p", []string{"127.0.0.1:" + p}, FetchRequest{Method: "GET", URL: echo.URL})
	if err != nil || res.Body != "direct" {
		t.Fatalf("fetch: %+v %v", res, err)
	}
	if proxied.Load() != 0 {
		t.Fatal("the request went through the environment's proxy")
	}
}

func TestHostAllowedWildcardEntry(t *testing.T) {
	if !hostAllowed([]string{"*"}, "hooks.example.com", "443") || !hostAllowed([]string{"*"}, "hooks.example.com", "80") {
		t.Error(`"*" must allow any host on 80 and 443`)
	}
	for _, port := range []string{"25", "6379", "8443"} {
		if hostAllowed([]string{"*"}, "hooks.example.com", port) {
			t.Errorf(`"*" must not allow port %s`, port)
		}
	}
	if !hostAllowed([]string{"*:8443"}, "hooks.example.com", "8443") || hostAllowed([]string{"*:8443"}, "hooks.example.com", "443") {
		t.Error(`"*:8443" allows any host on exactly that port`)
	}
	if hostAllowed(nil, "hooks.example.com", "443") {
		t.Error("an empty allowlist allows nothing")
	}
	// The address guard still applies to a "*" plugin.
	if !blockedAddr(netip.MustParseAddr("169.254.169.254")) {
		t.Error("metadata address must stay blocked")
	}
}

func TestWithoutWildcardKeepsTheOtherEntries(t *testing.T) {
	got := withoutWildcard([]string{"*", "api.example.com", "*:8443", "*.example.org", "x.example.net:8080"})
	want := []string{"api.example.com", "*.example.org", "x.example.net:8080"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
