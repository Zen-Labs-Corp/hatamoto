package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/modhost"
	moddb "hatamoto.dev/core/module/db"
)

// moduleServer mounts the given routes on a route shell and returns its handler.
func moduleServer(t *testing.T, routes ...modhost.Route) (http.Handler, *Server) {
	t.Helper()
	t.Cleanup(modhost.Reset)
	for _, r := range routes {
		modhost.RegisterRoute(r)
	}
	s, err := routeShell()
	if err != nil {
		t.Fatal(err)
	}
	s.setupRoutes()
	return s.buildHandler(), s
}

func moduleDo(h http.Handler, method, path, addr string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	req.RemoteAddr = addr
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func okHandler(calls *int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			*calls++
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("module"))
	})
}

func TestModuleRouteServesAnonymously(t *testing.T) {
	h, s := moduleServer(t, modhost.Route{Module: "example", Prefix: "/example/", Handler: okHandler(nil)})
	for _, m := range []string{"GET", "POST"} {
		rec := moduleDo(h, m, "/example/x", "203.0.113.7:1000", nil)
		if rec.Code != 200 || rec.Body.String() != "module" {
			t.Fatalf("%s /example/x: %d %q", m, rec.Code, rec.Body.String())
		}
	}
	if len(s.modulePrefixes) != 1 {
		t.Fatalf("prefixes: %v", s.modulePrefixes)
	}
	if rec := moduleDo(h, "GET", "/health", "203.0.113.7:1000", nil); rec.Code != 200 {
		t.Fatalf("/health: %d", rec.Code)
	}
}

func TestModuleRouteIsRateLimited(t *testing.T) {
	var calls int
	h, _ := moduleServer(t, modhost.Route{Module: "example", Prefix: "/example/", Handler: okHandler(&calls)})
	limited := false
	for range 12 {
		if moduleDo(h, "GET", "/example/x", "203.0.113.8:1000", nil).Code == http.StatusTooManyRequests {
			limited = true
		}
	}
	if !limited || calls > 10 {
		t.Fatalf("limited=%v calls=%d", limited, calls)
	}
}

func TestModuleRouteBodyCap(t *testing.T) {
	var readErr error
	read := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.Copy(io.Discard, r.Body)
	})
	h, _ := moduleServer(t,
		modhost.Route{Module: "example", Prefix: "/example/", Handler: read},
		modhost.Route{Module: "big", Prefix: "/big/", Handler: read, MaxBody: 4 << 20})
	body := strings.Repeat("a", 2<<20)
	moduleDo(h, "POST", "/example/x", "203.0.113.9:1000", strings.NewReader(body))
	var mbe *http.MaxBytesError
	if !errors.As(readErr, &mbe) {
		t.Fatalf("default cap: read error %v", readErr)
	}
	readErr = nil
	moduleDo(h, "POST", "/big/x", "203.0.113.10:1000", strings.NewReader(body))
	if readErr != nil {
		t.Fatalf("4 MiB cap: read error %v", readErr)
	}
}

func TestModuleRouteNothingRegistered(t *testing.T) {
	_, s := moduleServer(t)
	if s.modulePrefixes != nil {
		t.Fatalf("prefixes: %v", s.modulePrefixes)
	}
	if code, _ := probeThroughIntercept(s, "/auth/sso/x"); code != 404 {
		t.Fatalf("/auth/sso/x: %d", code)
	}
}

// probeThroughIntercept runs a GET through the scanner/admin-address
// middleware alone and reports the status and whether the request got past it.
func probeThroughIntercept(s *Server, path string) (code int, passed bool) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { passed = true })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = "203.0.113.20:1000"
	d := s.newInterceptDeps()
	d.refuse = func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }
	interceptMiddleware(d)(next).ServeHTTP(rec, req)
	if passed {
		return http.StatusOK, true
	}
	return rec.Code, false
}

func TestModuleRouteMigrationErrorUnmounts(t *testing.T) {
	t.Cleanup(func() { modhost.SetMigrationErr("example", nil) })
	modhost.SetMigrationErr("example", errors.New("x"))
	_, s := moduleServer(t, modhost.Route{Module: "example", Prefix: "/example/", Handler: okHandler(nil)})
	if len(s.modulePrefixes) != 0 {
		t.Fatalf("prefixes: %v", s.modulePrefixes)
	}
}

func TestModuleRouteUnderAuth(t *testing.T) {
	h, s := moduleServer(t, modhost.Route{Module: "sso", Prefix: "/auth/sso/", Handler: okHandler(nil)})
	for _, m := range []string{"GET", "POST"} {
		if rec := moduleDo(h, m, "/auth/sso/x", "203.0.113.13:1000", nil); rec.Code != 200 || rec.Body.String() != "module" {
			t.Fatalf("%s /auth/sso/x: %d %q", m, rec.Code, rec.Body)
		}
	}
	// Core's own /auth/ routes stay with core: the mux picks the more specific
	// module pattern only for its prefix.
	for _, p := range []string{"/auth/login", "/auth/register"} {
		req := httptest.NewRequest("GET", p, nil)
		if _, pattern := s.mux.Handler(req); pattern != "GET /auth/{rest...}" {
			t.Fatalf("%s routes to %q", p, pattern)
		}
	}
	// The auth-legacy scanner probe still refuses every other /auth/ path.
	if code, passed := probeThroughIntercept(s, "/auth/x"); passed || code != 404 {
		t.Fatalf("/auth/x: %d passed=%v", code, passed)
	}
	if _, passed := probeThroughIntercept(s, "/auth/sso/x"); !passed {
		t.Fatal("/auth/sso/x was refused as a probe")
	}
}

func TestModuleRouteSeesPool(t *testing.T) {
	p, err := pgxpool.New(context.Background(), "postgres://x@127.0.0.1:1/x") // lazy: does not dial
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	var got *pgxpool.Pool
	var gotErr error
	h, _ := moduleServer(t, modhost.Route{Module: "example", Prefix: "/example/", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, gotErr = moddb.Pool()
		w.WriteHeader(http.StatusOK)
	})})
	modhost.SetPool(p) // moduleServer already registered t.Cleanup(modhost.Reset)
	if rec := moduleDo(h, "GET", "/example/x", "203.0.113.7:1000", nil); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got != p || gotErr != nil {
		t.Fatalf("handler saw %v, %v; want the published pool", got, gotErr)
	}
}
