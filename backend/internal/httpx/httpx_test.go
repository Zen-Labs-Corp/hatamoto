package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func body(s string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(s)) }
}

func tag(s string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(s))
			next.ServeHTTP(w, r)
		})
	}
}

func do(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestGroupPrefixMiddlewareAndRoot(t *testing.T) {
	mux := http.NewServeMux()
	root := NewGroup(mux, "")
	api := root.Group("/api", tag("a>"))
	users := api.Group("/users", tag("u>"))
	users.HandleFunc("GET /", body("list"))
	users.HandleFunc("GET /{id}", body("get"))
	root.HandleFunc("GET /", body("home"))

	for _, c := range []struct{ method, path, want string }{
		{"GET", "/api/users", "a>u>list"},
		{"GET", "/api/users/", "a>u>list"},
		{"GET", "/api/users/7", "a>u>get"},
		{"GET", "/", "home"},
	} {
		if got := do(mux, c.method, c.path).Body.String(); got != c.want {
			t.Errorf("%s %s = %q, want %q", c.method, c.path, got, c.want)
		}
	}
	// "/" is exact: it must not swallow unknown paths.
	if rec := do(mux, "GET", "/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("/nope = %d, want 404", rec.Code)
	}
	if rec := do(mux, "GET", "/api/users/7/x"); rec.Code != http.StatusNotFound {
		t.Errorf("/api/users/7/x = %d, want 404", rec.Code)
	}
}

func TestRoutesRecorded(t *testing.T) {
	mux := http.NewServeMux()
	g := NewGroup(mux, "/v1")
	g.Group("/x", tag("m")).HandleFunc("POST /{id}", body("z"))
	rs := g.Routes()
	if len(rs) != 1 || rs[0].Pattern != "POST /v1/x/{id}" || !strings.HasSuffix(rs[0].Handler, ".body.func1") {
		t.Fatalf("routes = %+v", rs)
	}
}

func TestAmbiguousPatternsPanicAtBoot(t *testing.T) {
	mux := http.NewServeMux()
	g := NewGroup(mux, "/api")
	g.HandleFunc("GET /{a}/x", body("1"))
	defer func() {
		msg, _ := recover().(string)
		if !strings.Contains(msg, "GET /api/x/{b}") || !strings.Contains(msg, "conflicts") {
			t.Fatalf("want a loud conflict panic naming the pattern, got %q", msg)
		}
	}()
	g.HandleFunc("GET /x/{b}", body("2"))
	t.Fatal("conflicting patterns were accepted")
}

func TestWithErrorBodies(t *testing.T) {
	mux := http.NewServeMux()
	NewGroup(mux, "").HandleFunc("GET /only", body("ok"))
	h := WithErrorBodies(mux, func(w http.ResponseWriter, status int) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte("E" + http.StatusText(status)))
	})
	if rec := do(h, "GET", "/missing"); rec.Code != 404 || rec.Body.String() != "ENot Found" {
		t.Errorf("404: %d %q", rec.Code, rec.Body)
	}
	rec := do(h, "DELETE", "/only")
	if rec.Code != 405 || rec.Body.String() != "EMethod Not Allowed" || rec.Header().Get("Allow") == "" {
		t.Errorf("405: %d %q allow=%q", rec.Code, rec.Body, rec.Header().Get("Allow"))
	}
	if got := do(h, "GET", "/only").Body.String(); got != "ok" {
		t.Errorf("match: %q", got)
	}
}

func TestRecorderUnwrapAndFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	r := NewRecorder(rec)
	_, _ = r.Write([]byte("abc"))
	if r.Status() != 200 || r.BytesWritten() != 3 {
		t.Errorf("status=%d bytes=%d", r.Status(), r.BytesWritten())
	}
	if err := http.NewResponseController(r).Flush(); err != nil {
		t.Errorf("Flush through Unwrap: %v", err)
	}
	if !rec.Flushed {
		t.Error("underlying writer not flushed")
	}
}

func TestMethodlessPatternRejected(t *testing.T) {
	g := NewGroup(http.NewServeMux(), "/api")
	for _, pattern := range []string{"/hook", "/{id}", "/"} {
		func() {
			defer func() {
				msg, _ := recover().(string)
				if !strings.Contains(msg, "no method") || !strings.Contains(msg, pattern) {
					t.Errorf("%q: want a no-method panic naming the pattern, got %q", pattern, msg)
				}
			}()
			g.HandleFunc(pattern, body("x"))
		}()
	}
}

func TestWithErrorBodiesNonGetNonCleanPath(t *testing.T) {
	mux := http.NewServeMux()
	NewGroup(mux, "").HandleFunc("DELETE /{rest...}", body("ok"))
	h := WithErrorBodies(mux, func(w http.ResponseWriter, status int) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte("E"))
	})
	req := httptest.NewRequest("DELETE", "/x", nil)
	req.URL.Path = "//x"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 404 || rec.Header().Get("Location") != "" {
		t.Errorf("DELETE //x = %d %q, want 404 without a redirect", rec.Code, rec.Header().Get("Location"))
	}
}

func TestRecorderIgnoresInformationalStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	r := NewRecorder(rec)
	r.WriteHeader(http.StatusEarlyHints)
	if r.Status() != 0 {
		t.Errorf("103 recorded as final status %d", r.Status())
	}
	_, _ = r.Write([]byte("x"))
	if r.Status() != 200 {
		t.Errorf("final status = %d, want 200", r.Status())
	}
}
