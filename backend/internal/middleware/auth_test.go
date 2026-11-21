package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authztest"
)

func reqWith(remoteAddr, xff string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remoteAddr
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func TestGetIP_RightmostUntrusted(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		proxies    []string // HATAMOTO_TRUSTED_PROXIES; nil = loopback-only default
		want       string
	}{
		{
			name:       "single public client via trusted proxy",
			remoteAddr: "127.0.0.1:5000",
			xff:        "203.0.113.7",
			want:       "203.0.113.7",
		},
		{
			name:       "skips trailing private proxy hop",
			remoteAddr: "127.0.0.1:5000",
			xff:        "203.0.113.7, 10.0.0.2",
			proxies:    []string{"127.0.0.0/8", "10.0.0.0/8"},
			want:       "203.0.113.7",
		},
		{
			name:       "ignores spoofed leftmost entry (rightmost-untrusted wins)",
			remoteAddr: "127.0.0.1:5000",
			// Attacker prepends 1.2.3.4; the proxy appended the real 203.0.113.7.
			xff:  "1.2.3.4, 203.0.113.7",
			want: "203.0.113.7",
		},
		{
			name:       "multiple private proxies then real client",
			remoteAddr: "10.0.0.9:5000",
			xff:        "198.51.100.23, 10.0.0.2, 192.168.1.5",
			proxies:    []string{"10.0.0.0/8", "192.168.0.0/16"},
			want:       "198.51.100.23",
		},
		{
			name:       "untrusted RemoteAddr: XFF ignored entirely",
			remoteAddr: "203.0.113.99:5000",
			xff:        "1.2.3.4",
			want:       "203.0.113.99",
		},
		{
			name:       "docker bridge peer is not trusted by default",
			remoteAddr: "172.17.0.1:5000",
			xff:        "1.2.3.4",
			want:       "172.17.0.1",
		},
		{
			name:       "docker bridge peer trusted when configured",
			remoteAddr: "172.17.0.1:5000",
			xff:        "1.2.3.4",
			proxies:    []string{"172.16.0.0/12"},
			want:       "1.2.3.4",
		},
		{
			name:       "no XFF falls back to RemoteAddr",
			remoteAddr: "203.0.113.50:4444",
			xff:        "",
			want:       "203.0.113.50",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			SetTrustedProxies(c.proxies)
			t.Cleanup(func() { SetTrustedProxies(nil) })
			got := getIP(reqWith(c.remoteAddr, c.xff))
			if got != c.want {
				t.Errorf("getIP() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestGetIP_AllPrivateChainFallsBack(t *testing.T) {
	// Every XFF hop is a trusted proxy, so none qualifies as the client; fall
	// back to X-Real-IP if present, else RemoteAddr.
	SetTrustedProxies([]string{"127.0.0.0/8", "10.0.0.0/8", "192.168.0.0/16"})
	t.Cleanup(func() { SetTrustedProxies(nil) })
	r := reqWith("127.0.0.1:5000", "10.0.0.1, 192.168.0.1")
	r.Header.Set("X-Real-IP", "198.51.100.7")
	if got := getIP(r); got != "198.51.100.7" {
		t.Errorf("getIP() = %q, want X-Real-IP fallback 198.51.100.7", got)
	}
}

func TestAuthMiddleware_AuthenticatesViaSessionCookie(t *testing.T) {
	// sessionFallback that accepts "good-session" and sets claims.
	uid := uuid.New()
	fallback := func(_ context.Context, token string) (*auth.Claims, error) {
		if token == "good-session" {
			return &auth.Claims{UserID: uid, Role: "owner"}, nil
		}
		return nil, auth.ErrInvalidToken
	}
	h := AuthMiddleware(fallback, fallback, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, ok := GetClaims(r.Context()); !ok || c.UserID != uid {
			t.Error("expected claims from the cookie session")
		}
		w.WriteHeader(http.StatusOK)
	}))

	// No Authorization header, valid session cookie -> authenticated.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: auth.APISessionCookieName, Value: "good-session"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("cookie auth should succeed, got %d", rec.Code)
	}

	// No header, bad cookie -> 401.
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	r2.AddCookie(&http.Cookie{Name: auth.APISessionCookieName, Value: "bad"})
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, r2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("bad cookie should 401, got %d", rec2.Code)
	}

	// No header, no cookie -> 401 (legacy behavior preserved).
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if rec3.Code != http.StatusUnauthorized {
		t.Fatalf("no creds should 401, got %d", rec3.Code)
	}
}

func TestAuthMiddleware_CookieIgnoredWhenCookieAuthOff(t *testing.T) {
	fallback := func(_ context.Context, token string) (*auth.Claims, error) {
		if token == "good-session" {
			return &auth.Claims{UserID: uuid.New(), Role: "owner"}, nil
		}
		return nil, auth.ErrInvalidToken
	}
	// cookieAuth=false: a valid cookie must NOT authenticate (pure legacy).
	h := AuthMiddleware(fallback, fallback, false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: auth.APISessionCookieName, Value: "good-session"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("cookie must be ignored when cookieAuth=false, got %d", rec.Code)
	}
}

// kindFakes returns a bearer validator accepting admin and api tokens and a
// cookie validator accepting api tokens only, like the real auth.Service.
func kindFakes(uid uuid.UUID) (bearer, cookie SessionValidator) {
	claims := &auth.Claims{UserID: uid, Role: "owner"}
	bearer = func(_ context.Context, t string) (*auth.Claims, error) {
		if t == "admin-tok" || t == "api-tok" {
			return claims, nil
		}
		return nil, auth.ErrInvalidToken
	}
	cookie = func(_ context.Context, t string) (*auth.Claims, error) {
		if t == "api-tok" {
			return claims, nil
		}
		return nil, auth.ErrInvalidToken
	}
	return bearer, cookie
}

func serveAuth(cookieAuth bool, r *http.Request) (int, string) {
	bearer, cookie := kindFakes(uuid.New())
	var got string
	h := AuthMiddleware(bearer, cookie, cookieAuth)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = GetAPICookieToken(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Code, got
}

func TestAuthMiddleware_AdminCookieNeverAuthenticatesAPI(t *testing.T) {
	var called bool
	bearer, _ := kindFakes(uuid.New())
	cookie := func(_ context.Context, _ string) (*auth.Claims, error) {
		called = true
		return nil, auth.ErrInvalidToken
	}
	h := AuthMiddleware(bearer, cookie, true)(http.HandlerFunc(ok))
	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "admin-tok"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("admin cookie must 401, got %d", rec.Code)
	}
	if called {
		t.Fatal("cookie validator must never see the admin cookie value")
	}
}

func TestAuthMiddleware_APICookieBearerAndFlag(t *testing.T) {
	cookieReq := func(tok string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		r.AddCookie(&http.Cookie{Name: auth.APISessionCookieName, Value: tok})
		return r
	}
	bearerReq := func(tok string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		return r
	}
	if code, tok := serveAuth(true, cookieReq("api-tok")); code != 200 || tok != "api-tok" {
		t.Errorf("api cookie: code %d token %q", code, tok)
	}
	if code, _ := serveAuth(true, cookieReq("admin-tok")); code != 401 {
		t.Errorf("admin-kind token in api cookie: got %d", code)
	}
	for _, tok := range []string{"admin-tok", "api-tok"} {
		if code, ck := serveAuth(true, bearerReq(tok)); code != 200 || ck != "" {
			t.Errorf("bearer %s: code %d cookie marker %q", tok, code, ck)
		}
	}
	// Valid bearer plus a cookie: bearer wins and the request is not cookie-authenticated.
	r := bearerReq("admin-tok")
	r.AddCookie(&http.Cookie{Name: auth.APISessionCookieName, Value: "api-tok"})
	if code, ck := serveAuth(true, r); code != 200 || ck != "" {
		t.Errorf("bearer plus cookie: code %d marker %q", code, ck)
	}
	// Invalid bearer plus a valid cookie falls back to the cookie.
	r = bearerReq("bad")
	r.AddCookie(&http.Cookie{Name: auth.APISessionCookieName, Value: "api-tok"})
	if code, ck := serveAuth(true, r); code != 200 || ck != "api-tok" {
		t.Errorf("bad bearer plus cookie: code %d marker %q", code, ck)
	}
	// Cookie auth off: both cookies are ignored.
	for _, name := range []string{auth.APISessionCookieName, auth.SessionCookieName} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		r.AddCookie(&http.Cookie{Name: name, Value: "api-tok"})
		if code, _ := serveAuth(false, r); code != 401 {
			t.Errorf("cookie auth off, cookie %s: got %d", name, code)
		}
	}
}

func TestRequireOwnerOrKeyScope(t *testing.T) {
	h := RequireOwnerOrKeyScope(authz.ActionSettingsSEORead, authz.ActionSettingsSEOWrite)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	keyCtxAs := func(creator string, scope auth.APIKeyScope) context.Context {
		creatorID := uuid.New()
		ctx := context.WithValue(context.Background(), apiKeyContextKey, &auth.APIKey{ID: uuid.New(), CreatedBy: &creatorID, Scopes: []auth.APIKeyScope{scope}, CreatorRole: creator})
		ctx = context.WithValue(ctx, authTypeContextKey, AuthTypeAPIKey)
		// keys run with editor claims; the decision follows the creator's role
		return ContextWithClaims(ctx, &auth.Claims{UserID: uuid.New(), Role: "editor"})
	}
	keyCtx := func(scope auth.APIKeyScope) context.Context { return keyCtxAs("owner", scope) }
	session := func(role string) context.Context {
		return ContextWithClaims(context.Background(), &auth.Claims{UserID: uuid.New(), Role: role})
	}
	cases := []struct {
		name   string
		ctx    context.Context
		method string
		want   int
	}{
		{"owner GET", session("owner"), "GET", 200},
		{"owner PATCH", session("owner"), "PATCH", 200},
		{"editor GET", session("editor"), "GET", 403},
		{"editor PATCH", session("editor"), "PATCH", 403},
		{"read key GET", keyCtx(auth.ScopeReadSEO), "GET", 200},
		{"read key PATCH", keyCtx(auth.ScopeReadSEO), "PATCH", 403},
		{"write key GET", keyCtx(auth.ScopeWriteSEO), "GET", 200},
		{"write key PATCH", keyCtx(auth.ScopeWriteSEO), "PATCH", 200},
		{"other key GET", keyCtx(auth.ScopeWriteEntities), "GET", 403},
		{"other key PATCH", keyCtx(auth.ScopeWriteEntities), "PATCH", 403},
		{"editor-creator write key GET", keyCtxAs("editor", auth.ScopeWriteSEO), "GET", 403},
		{"editor-creator write key PATCH", keyCtxAs("editor", auth.ScopeWriteSEO), "PATCH", 403},
		{"editor-creator all key PATCH", keyCtxAs("editor", auth.ScopeAll), "PATCH", 403},
		{"no auth", context.Background(), "GET", 401},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "/", nil).WithContext(c.ctx)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: got %d want %d", c.name, rec.Code, c.want)
		}
	}
}

func TestRequireOwnerOrKeyScopeDenyAll(t *testing.T) {
	authztest.Use(t, authztest.DenyAll)
	h := RequireOwnerOrKeyScope(authz.ActionSettingsSEORead, authz.ActionSettingsSEOWrite)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	creator := uuid.New()
	keyCtx := context.WithValue(context.Background(), apiKeyContextKey, &auth.APIKey{ID: uuid.New(), CreatedBy: &creator, CreatorRole: "owner", Scopes: []auth.APIKeyScope{auth.ScopeAll}})
	keyCtx = context.WithValue(keyCtx, authTypeContextKey, AuthTypeAPIKey)
	for name, ctx := range map[string]context.Context{
		"owner session": ContextWithClaims(context.Background(), &auth.Claims{UserID: uuid.New(), Role: "owner"}),
		"owner * key":   keyCtx,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: got %d want 403", name, rec.Code)
		}
	}
}

func TestRequireCan(t *testing.T) {
	var tags []string
	TraceHook = func(_ *http.Request, tag string) { tags = append(tags, tag) }
	t.Cleanup(func() { TraceHook = nil })

	mw := func(act authz.Action) http.Handler {
		return RequireCan(act)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
	}
	session := func(role string) context.Context {
		return ContextWithClaims(context.Background(), &auth.Claims{UserID: uuid.New(), Role: role})
	}
	// editorKey is an editor-created "*" key: it runs with synthesised editor claims.
	editorKey := func(withKey bool) context.Context {
		creator := uuid.New()
		ctx := context.WithValue(context.Background(), authTypeContextKey, AuthTypeAPIKey)
		if withKey {
			ctx = context.WithValue(ctx, apiKeyContextKey, &auth.APIKey{ID: uuid.New(), CreatedBy: &creator, CreatorRole: "editor", Scopes: []auth.APIKeyScope{auth.ScopeAll}})
		}
		return ContextWithClaims(ctx, &auth.Claims{UserID: creator, Role: "editor"})
	}
	cases := []struct {
		name string
		ctx  context.Context
		act  authz.Action
		want int
		body string
	}{
		{"no claims", context.Background(), authz.ActionUserManage, 401, `{"error":"unauthorized"}`},
		{"editor user.manage", session("editor"), authz.ActionUserManage, 403, `{"error":"forbidden"}`},
		{"owner user.manage", session("owner"), authz.ActionUserManage, 204, ""},
		{"editor author.manage", session("editor"), authz.ActionAuthorManage, 204, ""},
		{"viewer author.manage", session("viewer"), authz.ActionAuthorManage, 403, `{"error":"forbidden"}`},
		{"editor-created * key user.manage", editorKey(true), authz.ActionUserManage, 403, `{"error":"forbidden"}`},
		{"editor-created * key author.manage", editorKey(true), authz.ActionAuthorManage, 403, `{"error":"forbidden"}`},
		{"api key type without key", editorKey(false), authz.ActionAuthorManage, 403, `{"error":"forbidden"}`},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		mw(c.act).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(c.ctx))
		if rec.Code != c.want {
			t.Errorf("%s: got %d want %d", c.name, rec.Code, c.want)
		}
		if c.body != "" && strings.TrimSpace(rec.Body.String()) != c.body {
			t.Errorf("%s: body %q want %q", c.name, rec.Body.String(), c.body)
		}
	}

	tags = nil
	mw(authz.ActionAuthorManage).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(session("owner")))
	mw(authz.ActionUserManage).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(session("owner")))
	if want := []string{"role[owner editor]", "role[owner]"}; !slices.Equal(tags, want) {
		t.Errorf("trace tags = %v, want %v", tags, want)
	}
}

func TestRequireCanDenyAll(t *testing.T) {
	authztest.Use(t, authztest.DenyAll)
	h := RequireCan(authz.ActionCacheFlush)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	ctx := ContextWithClaims(context.Background(), &auth.Claims{UserID: uuid.New(), Role: "owner"})
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx))
	if rec.Code != http.StatusForbidden {
		t.Errorf("owner under deny-all: got %d want 403", rec.Code)
	}
}
