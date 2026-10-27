package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
)

func TestDemoAPIGuardRefusesWith403JSON(t *testing.T) {
	reached := false
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true })
	on := (&Server{cfg: &config.Config{Demo: config.DemoConfig{Enabled: true}}}).demoAPIGuard(ok)
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/v1/users/"}, {"PATCH", "/api/v1/users/7"}, {"DELETE", "/api/v1/users/7"},
		{"PATCH", "/api/v1/me"}, {"POST", "/api/v1/api-keys/"}, {"POST", "/api/v1/api-keys/7/regenerate"},
	} {
		reached = false
		rec := httptest.NewRecorder()
		on.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != 403 || reached || !strings.Contains(rec.Body.String(), "public demo") {
			t.Errorf("%s %s: %d reached=%v body=%s", tc.method, tc.path, rec.Code, reached, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s %s: content type %q", tc.method, tc.path, ct)
		}
	}
	// reads and other writes pass
	for _, tc := range []struct{ method, path string }{{"GET", "/api/v1/users/"}, {"POST", "/api/v1/entities/"}} {
		reached = false
		on.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.path, nil))
		if !reached {
			t.Errorf("%s %s was refused", tc.method, tc.path)
		}
	}
}

func TestDemoBannerInjection(t *testing.T) {
	page := func(ct, body string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ct)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(body))
		})
	}
	get := func(h http.Handler, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	html := demoBanner(page("text/html; charset=utf-8", "<html><body><p>hi</p></BODY></html>"))
	rec := get(html, "/about")
	body := rec.Body.String()
	if !strings.Contains(body, config.DemoBanner) || strings.Index(body, config.DemoBanner) > strings.Index(body, "</BODY>") {
		t.Errorf("banner missing or after </body>: %s", body)
	}
	if rec.Header().Get("Content-Length") == "" || rec.Code != 200 {
		t.Errorf("length %q code %d", rec.Header().Get("Content-Length"), rec.Code)
	}
	for _, path := range []string{"/admin/login", "/api/v1/x", "/media/a.png", "/p/plugin/x"} {
		if strings.Contains(get(html, path).Body.String(), config.DemoBanner) {
			t.Errorf("%s must not get the public banner", path)
		}
	}
	if strings.Contains(get(demoBanner(page("application/json", `{"a":1}`)), "/feed").Body.String(), config.DemoBanner) {
		t.Error("a non-HTML response was changed")
	}
}

func TestDemoSwitchesWebhookDeliveryAndUpdatesOff(t *testing.T) {
	t.Setenv("WORKER_DISABLE_JOBS", "")
	s := webhookTestServer(true)
	s.cfg = &config.Config{Demo: config.DemoConfig{Enabled: true}}
	s.scheduleWebhooks()
	if s.webhookDeliveryOn {
		t.Error("webhooks are delivered in demo mode")
	}
	svc, why := s.adminUpdatesDeps()
	if svc != nil || !strings.Contains(why, "public demo") {
		t.Errorf("updates: svc=%v why=%q", svc, why)
	}
	// and with the demo off the delivery job is scheduled as before
	off := webhookTestServer(true)
	off.cfg = &config.Config{}
	off.scheduleWebhooks()
	if !off.webhookDeliveryOn {
		t.Error("demo off must keep webhook delivery on")
	}
}

func TestDemoPluginsHaveNoHTTPFetch(t *testing.T) {
	if (&Server{cfg: &config.Config{Demo: config.DemoConfig{Enabled: true}}}).pluginFetcher() != nil {
		t.Error("plugins can fetch in demo mode")
	}
	if (&Server{cfg: &config.Config{}}).pluginFetcher() == nil {
		t.Error("demo off must keep http.fetch")
	}
}

func TestDemoAuthScreensAreRateLimitedTighter(t *testing.T) {
	hits := func(s *Server) int {
		rl := s.authRateLimiter()
		defer rl.Stop()
		h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		n := 0
		for i := 0; i < 30; i++ {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/admin/login", nil)
			req.RemoteAddr = "203.0.113.9:1234"
			h.ServeHTTP(rec, req)
			if rec.Code == 200 {
				n++
			}
		}
		return n
	}
	demo, normal := hits(&Server{cfg: &config.Config{Demo: config.DemoConfig{Enabled: true}}}), hits(&Server{cfg: &config.Config{}})
	if demo >= normal || demo > 6 {
		t.Errorf("demo allows %d of 30 at once, normal %d", demo, normal)
	}
}

// fakeSessions accepts the tokens "alice", "bob" and "carol".
func fakeSessions(_ context.Context, token string) (*auth.Claims, error) {
	ids := map[string]string{ // alice2 is alice signing in again
		"alice": "00000000-0000-0000-0000-00000000000a", "alice2": "00000000-0000-0000-0000-00000000000a",
		"bob": "00000000-0000-0000-0000-00000000000b", "carol": "00000000-0000-0000-0000-00000000000c",
	}
	if id, ok := ids[token]; ok {
		return &auth.Claims{UserID: uuid.MustParse(id)}, nil
	}
	return nil, auth.ErrInvalidToken
}

func demoWriteServer(d config.DemoConfig) http.Handler {
	s := &Server{cfg: &config.Config{Demo: d}}
	return s.demoWriteLimit(fakeSessions)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
	}))
}

func TestDemoWriteLimitRatesWritesPerAddressButNotReads(t *testing.T) {
	h := demoWriteServer(config.DemoConfig{WritesPerSecond: 1, WriteBurst: 3, WriteBudgetMB: 10})
	do := func(method, ip string) int {
		req := httptest.NewRequest(method, "/admin/x", nil)
		req.RemoteAddr = ip + ":1"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	ok := 0
	for i := 0; i < 10; i++ {
		if do("POST", "203.0.113.1") == 200 {
			ok++
		}
	}
	if ok != 3 {
		t.Errorf("a burst of 10 writes let %d through, want 3", ok)
	}
	if do("POST", "203.0.113.2") != 200 {
		t.Error("another address shares the limit")
	}
	for i := 0; i < 50; i++ {
		if do("GET", "203.0.113.1") != 200 {
			t.Fatal("reads are limited")
		}
	}
}

// post sends a write request; token "" is anonymous.
func post(h http.Handler, ip, path, token string, body io.Reader, claimed int64) int {
	req := httptest.NewRequest("POST", path, body)
	req.RemoteAddr = ip + ":1"
	if claimed != 0 {
		req.ContentLength = claimed
	}
	if token != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestDemoWriteBudgetChargesBytesReadPerSessionAndAddress(t *testing.T) {
	h := demoWriteServer(config.DemoConfig{WritesPerSecond: 1000, WriteBurst: 1000, WriteBudgetMB: 1})
	big := func() io.Reader { return strings.NewReader(strings.Repeat("x", 600<<10)) }
	if got := post(h, "203.0.113.1", "/api/v1/entities", "alice", big(), 0); got != 200 {
		t.Fatalf("first body: %d", got)
	}
	// a second body that declares more than is left is refused before it is read
	if got := post(h, "203.0.113.1", "/api/v1/entities", "alice", big(), 0); got != http.StatusTooManyRequests {
		t.Errorf("a body over the budget: %d, want 429", got)
	}
	if got := post(h, "203.0.113.1", "/api/v1/entities", "alice", strings.NewReader("tiny"), 0); got != 200 {
		t.Errorf("a small body within the budget: %d, want 200", got)
	}
	// the same address is spent for another session too, another address is not
	if got := post(h, "203.0.113.1", "/api/v1/entities", "bob", strings.NewReader(strings.Repeat("x", 300<<10)), 0); got != 200 {
		t.Errorf("bob from the same address, first body: %d", got)
	}
	if got := post(h, "203.0.113.1", "/api/v1/entities", "carol", big(), 0); got != http.StatusTooManyRequests {
		t.Errorf("carol from an address with 900 KB spent: %d, want 429", got)
	}
	if got := post(h, "203.0.113.2", "/api/v1/entities", "carol", big(), 0); got != 200 {
		t.Errorf("carol from a fresh address: %d, want 200", got)
	}
}

func TestDemoWriteBudgetCountsBytesNotDeclaredLength(t *testing.T) {
	h := demoWriteServer(config.DemoConfig{WritesPerSecond: 1000, WriteBurst: 1000, WriteBudgetMB: 1})
	// a signed-in client that claims 3 MB but sends 10 bytes pays 10 bytes ... once it is
	// within its budget (a claim over the budget is refused for that key only)
	if got := post(h, "203.0.113.1", "/x", "alice", strings.NewReader("0123456789"), 500<<10); got != 200 {
		t.Fatalf("claim within budget: %d", got)
	}
	for i := 0; i < 20; i++ {
		if got := post(h, "203.0.113.1", "/x", "alice", strings.NewReader("0123456789"), 500<<10); got != 200 {
			t.Fatalf("request %d: charged the claimed length (%d)", i, got)
		}
	}
}

func TestDemoWriteBudgetAnonymousCannotLockOthersOut(t *testing.T) {
	h := demoWriteServer(config.DemoConfig{WritesPerSecond: 1000, WriteBurst: 1000, WriteBudgetMB: 1})
	for i := 0; i < 50; i++ {
		// anonymous, claiming 3 MB each, from several addresses and with a forged cookie
		post(h, "198.51.100.7", "/anything", "", strings.NewReader("x"), 3<<20)
		post(h, "198.51.100.7", "/anything", "forged", strings.NewReader("x"), 3<<20)
	}
	if got := post(h, "198.51.100.7", "/admin/login", "", strings.NewReader("x"), 3<<20); got != 200 {
		t.Errorf("login after the flood: %d", got)
	}
	if got := post(h, "203.0.113.9", "/admin/login", "", strings.NewReader("x"), 0); got != 200 {
		t.Errorf("login for another visitor: %d", got)
	}
	if got := post(h, "203.0.113.9", "/api/v1/entities", "alice", strings.NewReader("hello"), 0); got != 200 {
		t.Errorf("a signed-in write after the flood: %d", got)
	}
	// even the flooder's own address can still sign in and out once its own budget is gone
	if got := post(h, "198.51.100.7", "/admin/logout", "alice", strings.NewReader("x"), 5<<20); got != 200 {
		t.Errorf("logout with an over-budget claim: %d", got)
	}
}

func TestDemoWriteBudgetIPv6CountsPerSlash64(t *testing.T) {
	h := demoWriteServer(config.DemoConfig{WritesPerSecond: 1000, WriteBurst: 1000, WriteBudgetMB: 1})
	body := func() io.Reader { return strings.NewReader(strings.Repeat("x", 700<<10)) }
	post(h, "2001:db8:1:1::1", "/x", "alice", body(), 0)
	if got := post(h, "2001:db8:1:1:ffff::9", "/x", "bob", body(), 0); got != http.StatusTooManyRequests {
		t.Errorf("another address in the same /64: %d, want 429", got)
	}
	if got := post(h, "2001:db8:1:2::1", "/x", "bob", body(), 0); got != 200 {
		t.Errorf("another /64: %d, want 200", got)
	}
}

func TestDemoRestorePathPattern(t *testing.T) {
	for path, want := range map[string]bool{
		"/admin/entities/7/versions/3/restore":          true,
		"/admin/entities/7/versions/3/nodes/n1/restore": true,
		"/api/v1/entities/7/restore/3":                  true,
		"/admin/entities/7/builder/template":            true,
		"/admin/entities/7/builder/block":               true,
		"/admin/entities/7/builder/blocks":              false,
		"/admin/entities/7":                             false,
		"/admin/entities/7/versions/3":                  false,
		"/api/v1/entities/7/restore":                    false,
		"/api/v1/entities/7/restore/3/extra":            false,
	} {
		if got := demoRestorePath.MatchString(path); got != want {
			t.Errorf("%s: %v, want %v", path, got, want)
		}
	}
}

func TestDemoVersionRestoresAreRatedPerSession(t *testing.T) {
	h := demoWriteServer(config.DemoConfig{WritesPerSecond: 1000, WriteBurst: 1000, WriteBudgetMB: 10})
	restore := func(ip, token string) int {
		return post(h, ip, "/admin/entities/7/versions/3/restore", token, strings.NewReader("csrf=x"), 0)
	}
	ok := 0
	for i := 0; i < 20; i++ {
		if restore("203.0.113.1", "alice") == 200 {
			ok++
		}
	}
	if ok != 3 {
		t.Errorf("a burst of 20 restores let %d through, want 3", ok)
	}
	if restore("203.0.113.2", "bob") != 200 {
		t.Error("another session shares the restore limit")
	}
	if post(h, "203.0.113.1", "/admin/entities/7", "alice", strings.NewReader("x"), 0) != 200 {
		t.Error("ordinary writes are held back by the restore limit")
	}
}

func TestDemoLimitersCountIPv6BySlash64(t *testing.T) {
	// 30 sign-in attempts from 30 addresses of one /64 share one bucket in demo mode
	s := &Server{cfg: &config.Config{Demo: config.DemoConfig{Enabled: true}}}
	rl := s.authRateLimiter()
	defer rl.Stop()
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	n := 0
	for i := 0; i < 30; i++ {
		req := httptest.NewRequest("POST", "/admin/login", nil)
		req.RemoteAddr = fmt.Sprintf("[2001:db8:7:7::%x]:1", i+1)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == 200 {
			n++
		}
	}
	if n > 6 {
		t.Errorf("30 addresses of one /64 got %d sign-in attempts through, want one shared bucket (<= 6)", n)
	}
	// the same for writes
	w := demoWriteServer(config.DemoConfig{WritesPerSecond: 1, WriteBurst: 3, WriteBudgetMB: 10})
	ok := 0
	for i := 0; i < 30; i++ {
		if post(w, fmt.Sprintf("2001:db8:8:8::%x", i+1), "/admin/x", "", strings.NewReader("x"), 0) == 200 {
			ok++
		}
	}
	if ok != 3 {
		t.Errorf("30 writes from one /64 let %d through, want 3", ok)
	}
	if post(w, "2001:db8:8:9::1", "/admin/x", "", strings.NewReader("x"), 0) != 200 {
		t.Error("another /64 shares the limit")
	}
}

// A junk Authorization header must not hide a valid cookie on /admin, which reads only the cookie.
func TestDemoJunkBearerDoesNotHideAdminCookie(t *testing.T) {
	h := demoWriteServer(config.DemoConfig{WritesPerSecond: 1000, WriteBurst: 1000, WriteBudgetMB: 1})
	send := func(path string) int {
		req := httptest.NewRequest("POST", path, strings.NewReader(strings.Repeat("x", 600<<10)))
		req.RemoteAddr = "203.0.113.1:1"
		req.Header.Set("Authorization", "Bearer x")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "alice"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := send("/admin/entities/7"); got != 200 {
		t.Fatalf("first write: %d", got)
	}
	if got := send("/admin/entities/7"); got != http.StatusTooManyRequests {
		t.Errorf("second 600 KB write through a 1 MB budget with a junk bearer: %d, want 429", got)
	}
	ok := 0
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest("POST", "/admin/entities/7/versions/3/restore", strings.NewReader("csrf=x"))
		req.RemoteAddr = "203.0.113.2:1"
		req.Header.Set("Authorization", "Bearer x")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "bob"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == 200 {
			ok++
		}
	}
	if ok != 3 {
		t.Errorf("a burst of 20 restores with a junk bearer let %d through, want 3", ok)
	}
}

func TestDemoRestoreLimitIsPerUserNotPerSession(t *testing.T) {
	h := demoWriteServer(config.DemoConfig{WritesPerSecond: 1000, WriteBurst: 1000, WriteBudgetMB: 10})
	n := func(token, path string) int {
		ok := 0
		for i := 0; i < 10; i++ {
			if post(h, "203.0.113.1", path, token, strings.NewReader("x"), 0) == 200 {
				ok++
			}
		}
		return ok
	}
	if got := n("alice", "/admin/entities/7/versions/3/restore"); got != 3 {
		t.Fatalf("first session: %d restores, want 3", got)
	}
	if got := n("alice2", "/admin/entities/7/versions/3/restore"); got != 0 {
		t.Errorf("signing in again gave a fresh burst of %d", got)
	}
	// save-as-template and make-block draw on the same allowance
	if got := n("alice2", "/admin/entities/7/builder/template"); got != 0 {
		t.Errorf("save as template had %d free writes after the restores", got)
	}
	if got := n("bob", "/admin/entities/7/builder/block"); got != 3 {
		t.Errorf("make block for another user: %d, want 3", got)
	}
}

// kindSessions behaves like the production validators: the admin token is
// accepted only as an admin session, the api token by the bearer validator.
func kindSessions(_ context.Context, token string) (*auth.Claims, error) {
	if token == "api-token" {
		return &auth.Claims{UserID: uuid.MustParse("00000000-0000-0000-0000-0000000000a1")}, nil
	}
	return nil, auth.ErrInvalidToken
}

func TestDemoWriteLimitChargesAPILoginSessions(t *testing.T) {
	s := &Server{cfg: &config.Config{Demo: config.DemoConfig{WritesPerSecond: 1000, WriteBurst: 1000, WriteBudgetMB: 1}}}
	for name, set := range map[string]func(*http.Request){
		"bearer": func(r *http.Request) { r.Header.Set("Authorization", "Bearer api-token") },
		"cookie": func(r *http.Request) { r.AddCookie(&http.Cookie{Name: auth.APISessionCookieName, Value: "api-token"}) },
	} {
		t.Run(name, func(t *testing.T) {
			h := s.demoWriteLimit(kindSessions)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
			}))
			send := func(ip string) int {
				req := httptest.NewRequest("POST", "/api/v1/entities", strings.NewReader(strings.Repeat("x", 600<<10)))
				req.RemoteAddr = ip + ":1"
				set(req)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				return rec.Code
			}
			if got := send("203.0.113.1"); got != 200 {
				t.Fatalf("first write: %d", got)
			}
			// a new address: only the session budget can refuse this one
			if got := send("203.0.113.9"); got != http.StatusTooManyRequests {
				t.Errorf("second 600 KB write by the same api session: %d, want 429 (uncharged)", got)
			}
		})
	}
}
