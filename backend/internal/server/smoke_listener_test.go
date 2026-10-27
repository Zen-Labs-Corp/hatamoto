package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/render"
)

// smokeServer builds a Server whose router is h, enough for the smoke checks.
func smokeTestServer(h http.Handler) *Server {
	return &Server{router: h, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func healthyRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("home")) })
	mux.HandleFunc("GET /sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<urlset><url><loc>https://example.test/about</loc></url></urlset>`))
	})
	mux.HandleFunc("GET /about", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("about")) })
	mux.HandleFunc("GET /admin/login", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("login")) })
	return mux
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func getSmoke(t *testing.T, addr, token string) (*http.Response, smokeResult) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/smoke", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res smokeResult
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			t.Fatal(err)
		}
	}
	return resp, res
}

func TestSmokeListenerAuthAndResult(t *testing.T) {
	s := smokeTestServer(healthyRouter())
	addr := freeAddr(t)
	if err := s.startSmoke(addr, "tok"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.stopSmoke(context.Background()) })

	for _, tok := range []string{"", "wrong", "tok2"} {
		if resp, _ := getSmoke(t, addr, tok); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("token %q: status %d", tok, resp.StatusCode)
		}
	}
	resp, res := getSmoke(t, addr, "tok")
	if resp.StatusCode != 200 || !res.Pass {
		t.Fatalf("status %d result %+v", resp.StatusCode, res)
	}
	var names []string
	for _, c := range res.Checks {
		names = append(names, c.Name)
	}
	want := "GET /,GET /sitemap.xml,published page,GET /admin/login"
	if strings.Join(names, ",") != want {
		t.Fatalf("checks %v, want %s", names, want)
	}
	if res.PluginErrors == nil {
		t.Fatal("plugin_errors must be [] not null")
	}
	// Only GET /smoke exists on this listener.
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != http.StatusNotFound {
		t.Fatalf("other paths: %v %v", r, err)
	}
}

func TestSmokeRefusesNonLoopback(t *testing.T) {
	s := smokeTestServer(healthyRouter())
	for _, addr := range []string{"0.0.0.0:0", "192.0.2.1:9000", "localhost:9000", ":9000", "[::]:9000", "nonsense"} {
		if err := s.startSmoke(addr, "tok"); err == nil {
			t.Errorf("%q accepted", addr)
			s.stopSmoke(context.Background())
		}
	}
	if err := s.startSmoke("127.0.0.1:0", ""); err == nil {
		t.Error("empty token accepted")
	}
}

func TestSmokeFailsWhenRootBroken(t *testing.T) {
	inner := healthyRouter()
	s := smokeTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	res := s.runSmoke()
	if res.Pass || res.Checks[0].Name != "GET /" || res.Checks[0].OK || res.Checks[0].Detail != "500" {
		t.Fatalf("%+v", res)
	}
}

// A box with no home page is reported as it is (status 404, still a pass);
// only the supervisor decides, against the last good run, whether that is a
// regression.
func TestSmokeReportsNotFoundHomeWithoutFailing(t *testing.T) {
	s := smokeTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("<urlset/>"))
	}))
	res := s.runSmoke()
	if !res.Pass || res.Checks[0].Name != "GET /" || res.Checks[0].Status != 404 {
		t.Fatalf("%+v", res)
	}
}

func TestSmokeLoginVariants(t *testing.T) {
	for name, tc := range map[string]struct {
		code     int
		location string
		ok       bool
	}{
		"fresh install": {303, "/admin/setup", true},
		"plain":         {200, "", true},
		"redirect":      {303, "/elsewhere", true}, // judged against the last good run, not here
		"not found":     {404, "", true},
		"error":         {500, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			s := smokeTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/admin/login" {
					if tc.location != "" {
						w.Header().Set("Location", tc.location)
					}
					w.WriteHeader(tc.code)
					return
				}
				_, _ = w.Write([]byte(`<loc>https://x.test/</loc>`))
			}))
			res := s.runSmoke()
			last := res.Checks[len(res.Checks)-1]
			if last.Name != "GET /admin/login" || last.OK != tc.ok || last.Status != tc.code {
				t.Fatalf("%+v", res.Checks)
			}
		})
	}
}

func TestSmokeNoPagesIsSkippedNotFailed(t *testing.T) {
	s := smokeTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<urlset/>")) }))
	res := s.runSmoke()
	if !res.Pass {
		t.Fatalf("%+v", res)
	}
	found := false
	for _, c := range res.Checks {
		found = found || c.Detail == "no pages"
	}
	if !found {
		t.Fatalf("no skip note: %+v", res.Checks)
	}
}

func TestSmokeReportsPluginErrors(t *testing.T) {
	s := smokeTestServer(healthyRouter())
	s.pluginStatusFn = func() []pluginhost.Status {
		return []pluginhost.Status{{Name: "zeta", Error: "bad"}, {Name: "ok", Loaded: true}, {Name: "alpha", Error: "bad"}}
	}
	res := s.runSmoke()
	if strings.Join(res.PluginErrors, ",") != "alpha,zeta" {
		t.Fatalf("%v", res.PluginErrors)
	}
}

func TestSmokeRequestsLookLikeSmoke(t *testing.T) {
	var seen []http.Header
	s := smokeTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Clone())
		_, _ = w.Write([]byte("<urlset/>"))
	}))
	s.runSmoke()
	if len(seen) == 0 {
		t.Fatal("no requests")
	}
	for _, h := range seen {
		if h.Get("User-Agent") != "hatamoto-smoke" || h.Get("X-Hatamoto-Smoke") != "1" || h.Get("Cache-Control") != "no-cache" {
			t.Fatalf("headers %v", h)
		}
	}
	// The agent-hit capture matches on user agent; smoke traffic must not look like an AI agent.
	if agents.Default().Match("hatamoto-smoke") != nil {
		t.Fatal("smoke user agent is classified as an agent")
	}
}

func TestCountingHandler(t *testing.T) {
	before := renderErrors.Load()
	var buf bytes.Buffer
	log := slog.New(NewCountingHandler(slog.NewTextHandler(&buf, nil)))
	ctx := smokeContext(context.Background())
	log.ErrorContext(ctx, "render: page", "path", "/")
	log.ErrorContext(ctx, "render: theme rebuild failed")
	log.WarnContext(ctx, "render: query", "error", "x") // warn: not counted
	log.ErrorContext(ctx, "something else failed")      // other error: not counted
	log.InfoContext(ctx, "render: fine")
	log.Error("render: visitor page") // no smoke marker: a visitor's
	log.ErrorContext(context.Background(), "render: visitor page")
	if got := renderErrors.Load() - before; got != 2 {
		t.Fatalf("counted %d, want 2", got)
	}
	if !strings.Contains(buf.String(), "render: page") || !strings.Contains(buf.String(), "visitor page") {
		t.Fatal("record not passed through")
	}
	log.With("k", "v").WithGroup("g").ErrorContext(ctx, "render: grouped")
	if got := renderErrors.Load() - before; got != 3 {
		t.Fatalf("derived logger lost the counter: %d", got)
	}
}

func TestSmokeAddressInUseDoesNotBlockBoot(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var buf bytes.Buffer
	s := smokeTestServer(healthyRouter())
	s.logger = slog.New(slog.NewTextHandler(&buf, nil))
	done := make(chan struct{})
	go func() { s.startSmokeFromEnv(ln.Addr().String(), "tok"); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("startSmokeFromEnv blocked")
	}
	if !strings.Contains(buf.String(), "smoke listener not started") {
		t.Fatalf("not logged: %s", buf.String())
	}
	s.startSmokeFromEnv("", "") // unset env: silent no-op
}

// Render errors from visitors are never the smoke pass's, not before it, not
// between passes and not while a pass runs. Only errors logged with a smoke
// request's context count.
func TestSmokeCountsOnlyItsOwnRenderErrors(t *testing.T) {
	log := slog.New(NewCountingHandler(slog.NewTextHandler(io.Discard, nil)))
	smokeFault := false
	stop := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if smokeFault && r.Header.Get("X-Hatamoto-Smoke") == "1" {
			log.ErrorContext(r.Context(), "render: page", "path", "/") // the smoke request's own failure
		}
		_, _ = w.Write([]byte("home"))
	})
	mux.HandleFunc("GET /sitemap.xml", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("GET /admin/login", func(w http.ResponseWriter, r *http.Request) {})
	s := smokeTestServer(mux)

	log.Error("render: page") // visitor traffic before the pass
	if got := s.runSmoke().RenderErrors; got != 0 {
		t.Fatalf("visitor errors counted: %d", got)
	}
	// Visitors failing concurrently with the passes.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				log.ErrorContext(context.Background(), "render: section failed, rendered empty")
			}
		}
	}()
	for range 20 {
		if got := s.runSmoke().RenderErrors; got != 0 {
			close(stop)
			wg.Wait()
			t.Fatalf("visitor errors during a pass counted: %d", got)
		}
	}
	close(stop)
	wg.Wait()
	smokeFault = true
	if got := s.runSmoke().RenderErrors; got != 1 {
		t.Fatalf("smoke request's own render error: counted %d, want 1", got)
	}
}

// Boot faults are reported by the smoke result, whatever the pass does.
func TestSmokeReportsBootFaults(t *testing.T) {
	s := smokeTestServer(healthyRouter())
	render.NoteBootFault("theme fell back to the default theme: test")
	if got := s.runSmoke().BootFaults; !slices.Contains(got, "theme fell back to the default theme: test") {
		t.Fatalf("boot faults %v", got)
	}
}

// A fault noted by the pass's own first render (plugin templates are vetted
// lazily) is in that pass's result, not only in the next one.
func TestSmokeReportsFaultNotedDuringPass(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		render.NoteBootFault("plugin templates rejected: lazy-during-pass")
		_, _ = w.Write([]byte("home"))
	})
	got := smokeTestServer(mux).runSmoke().BootFaults
	if !slices.Contains(got, "plugin templates rejected: lazy-during-pass") {
		t.Fatalf("boot faults %v", got)
	}
}

// The check name is the baseline key the update gate compares, so it must not
// follow the admin address; only the requested path does.
func TestSmokeLoginCheckNameIgnoresAdminAddress(t *testing.T) {
	for _, addr := range []string{"", "/admin", "/k7qm-x2rf-9tdc"} {
		var got string
		s := smokeTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/login") {
				got = r.URL.Path
			}
			_, _ = w.Write([]byte(`<loc>https://x.test/</loc>`))
		}))
		s.cfg = &config.Config{}
		s.cfg.Admin.Resolved = addr
		res := s.runSmoke()
		last := res.Checks[len(res.Checks)-1]
		if last.Name != "GET /admin/login" {
			t.Fatalf("address %q: check name %q", addr, last.Name)
		}
		if want := s.adminPathValue() + "/login"; got != want {
			t.Fatalf("address %q: requested %q, want %q", addr, got, want)
		}
	}
}

// The smoke GET for the admin sign-in page reaches the real admin guard, which
// refuses anything that is not a browser navigation; it must get the form.
func TestSmokeGetReachesAdminLogin(t *testing.T) {
	h, s := bootRoutes(t)
	s.router = h
	rec := s.smokeGet("/admin/login")
	if rec.Code != http.StatusOK && rec.Code != http.StatusSeeOther {
		t.Fatalf("smoke GET /admin/login through the admin guard: %d, want 200 or 303", rec.Code)
	}
}
