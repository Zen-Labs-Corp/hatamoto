package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/database"
)

type fakeValidator struct {
	keys  map[string]*auth.APIKey
	usage atomic.Int32
}

func (f *fakeValidator) ValidateKey(_ context.Context, raw string) (*auth.APIKey, error) {
	if k, ok := f.keys[raw]; ok {
		return k, nil
	}
	return nil, auth.ErrAPIKeyRevoked
}

func (f *fakeValidator) RecordUsage(context.Context, uuid.UUID, string) error {
	f.usage.Add(1)
	return nil
}

type fakeAllower struct{ allow bool }

func (f fakeAllower) Allow(uuid.UUID, int) bool { return f.allow }

type keyHarness struct {
	handler      http.Handler
	reachedKey   atomic.Int32
	reachedFall  atomic.Int32
	gotClaims    *auth.Claims
	gotAuthType  AuthType
	gotKeyInCtx  bool
	superadminDB bool
}

func newKeyHarness(t *testing.T, allow bool, keys map[string]*auth.APIKey) *keyHarness {
	t.Helper()
	h := &keyHarness{}
	fallback := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.reachedFall.Add(1)
			next.ServeHTTP(w, r)
		})
	}
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.reachedKey.Add(1)
		h.gotClaims, _ = GetClaims(r.Context())
		h.gotAuthType = GetAuthType(r.Context())
		_, h.gotKeyInCtx = GetAPIKey(r.Context())
		h.superadminDB = database.IsSuperadmin(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h.handler = AuthOrAPIKey(&fakeValidator{keys: keys}, fakeAllower{allow}, fallback)(final)
	return h
}

func testKey(scopes ...auth.APIKeyScope) *auth.APIKey {
	creator := uuid.New()
	return &auth.APIKey{ID: uuid.New(), Name: "tool", Scopes: scopes, CreatedBy: &creator, CreatorRole: "editor", IsActive: true, RateLimit: 1000}
}

func do(h http.Handler, method, target, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(""))
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuthOrAPIKeyAllowlist(t *testing.T) {
	read, write, all, none := auth.ScopeReadEntities, auth.ScopeWriteEntities, auth.ScopeAll, auth.ScopeReadReports
	keys := map[string]*auth.APIKey{
		"read": testKey(read), "write": testKey(write), "all": testKey(all), "none": testKey(none),
	}
	h := newKeyHarness(t, true, keys)

	const id = "6f1b1d52-8b3e-4a52-9c2f-0a1b2c3d4e5f"
	cases := []struct {
		method, path                string
		read, write, all, noneScope bool
	}{
		{"GET", "/api/v1/entities", true, true, true, false},
		{"GET", "/api/v1/entities/" + id, true, true, true, false},
		{"GET", "/api/v1/entities/" + id + "/versions", true, true, true, false},
		{"GET", "/api/v1/entities/" + id + "/versions/3", true, true, true, false},
		{"GET", "/api/v1/render/theme", true, true, true, false},
		{"POST", "/api/v1/render/preview", true, true, true, false},
		{"PUT", "/api/v1/entities/" + id, false, true, true, false},
		{"PATCH", "/api/v1/entities/" + id, false, true, true, false},
		{"POST", "/api/v1/entities/" + id + "/restore/2", false, true, true, false},
		{"POST", "/api/v1/entities", false, true, true, false},
		{"POST", "/api/v1/entities/", false, true, true, false},
		{"PATCH", "/api/v1/entities/" + id + "/status", false, true, true, false},
		{"POST", "/api/v1/linking/suggest/" + id, true, true, true, false},
		{"POST", "/api/v1/linking/insert", false, true, true, false},
	}
	for _, c := range cases {
		for name, want := range map[string]bool{"read": c.read, "write": c.write, "all": c.all, "none": c.noneScope} {
			rec := do(h.handler, c.method, c.path, name)
			wantCode := http.StatusForbidden
			if want {
				wantCode = http.StatusOK
			}
			if rec.Code != wantCode {
				t.Errorf("%s %s with %s key: got %d want %d", c.method, c.path, name, rec.Code, wantCode)
			}
		}
	}
	if h.reachedFall.Load() != 0 {
		t.Fatal("key requests must never reach session auth")
	}
}

func TestDeniedRoutes(t *testing.T) {
	h := newKeyHarness(t, true, map[string]*auth.APIKey{"all": testKey(auth.ScopeAll)})
	denied := []struct{ method, path string }{
		{"GET", "/api/v1/users"},
		{"GET", "/api/v1/settings"},
		{"PUT", "/api/v1/settings"},
		{"GET", "/api/v1/api-keys"},
		{"POST", "/api/v1/api-keys"},
		{"GET", "/api/v1/plugins"},
		{"POST", "/api/v1/media"},
		{"GET", "/api/v1/media"},
		// the write:media/menus/redirects/terms scopes are MCP-only
		{"PUT", "/api/v1/menus/x"},
		{"POST", "/api/v1/menus"},
		{"POST", "/api/v1/redirects"},
		{"DELETE", "/api/v1/redirects/x"},
		{"POST", "/api/v1/terms"},
		{"DELETE", "/api/v1/entities/x"},
		{"GET", "/api/v1/entities/x/../users"},
		{"GET", "/api/v1/entities/x/%2e%2e/users"},
		{"GET", "/api/v1/entities/x%2Fusers"},
		{"GET", "/api/v1/entities//x"},
		{"GET", "/api/v1/entities/x/other"},
		{"POST", "/api/v1/entities/x/restore"},
		{"POST", "/api/v1/entities/x/status"},
		{"PUT", "/api/v1/entities/x/status"},
		{"POST", "/api/v1/entities/validate/extra"},
		{"HEAD", "/api/v1/entities"},
		{"GET", "/api/v1/renderx/theme"},
		{"GET", "/prefix/api/v1/entities"},
		// linking: only suggest + insert are key-reachable
		{"GET", "/api/v1/linking/suggest/x"},
		{"POST", "/api/v1/linking/suggest"},
		{"POST", "/api/v1/linking/suggest/x/extra"},
		{"GET", "/api/v1/linking/insert"},
		{"POST", "/api/v1/linking/insert/x"},
		{"GET", "/api/v1/linking/rules"},
		{"POST", "/api/v1/linking/rules"},
		{"DELETE", "/api/v1/linking/rules/all"},
		{"GET", "/api/v1/linking/equity"},
	}
	for _, c := range denied {
		if rec := do(h.handler, c.method, c.path, "all"); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: got %d want 403", c.method, c.path, rec.Code)
		}
	}
	if h.reachedKey.Load() != 0 {
		t.Fatal("denied routes reached the handler")
	}
}

func TestRevokedKey401(t *testing.T) {
	h := newKeyHarness(t, true, map[string]*auth.APIKey{})
	if rec := do(h.handler, "GET", "/api/v1/entities", "gone"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d want 401", rec.Code)
	}
}

func TestNilCreatedBy401(t *testing.T) {
	k := testKey(auth.ScopeAll)
	k.CreatedBy = nil
	h := newKeyHarness(t, true, map[string]*auth.APIKey{"orphan": k})
	if rec := do(h.handler, "GET", "/api/v1/entities", "orphan"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d want 401", rec.Code)
	}
}

func TestNoHeaderFallsThrough(t *testing.T) {
	h := newKeyHarness(t, true, nil)
	do(h.handler, "GET", "/api/v1/users", "")
	if h.reachedFall.Load() != 1 {
		t.Fatal("request without X-API-Key must go to session auth")
	}
}

func TestClaimsNotSuperadminRoleEditor(t *testing.T) {
	k := testKey(auth.ScopeAll)
	h := newKeyHarness(t, true, map[string]*auth.APIKey{"k": k})
	if rec := do(h.handler, "GET", "/api/v1/entities", "k"); rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	c := h.gotClaims
	if c == nil || c.Role != "editor" || c.IsSuperadmin || c.UserID != *k.CreatedBy {
		t.Fatalf("unexpected claims: %+v", c)
	}
	if h.superadminDB {
		t.Fatal("db context must not be superadmin")
	}
	if h.gotAuthType != AuthTypeAPIKey || !h.gotKeyInCtx {
		t.Fatal("api key and auth type must be in context")
	}
}

func TestRateLimited429(t *testing.T) {
	h := newKeyHarness(t, false, map[string]*auth.APIKey{"k": testKey(auth.ScopeAll)})
	if rec := do(h.handler, "GET", "/api/v1/entities", "k"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d want 429", rec.Code)
	}
}

func TestHeaderPlusCookieStillKeyPath(t *testing.T) {
	h := newKeyHarness(t, true, map[string]*auth.APIKey{"k": testKey(auth.ScopeAll)})
	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	req.Header.Set("X-API-Key", "k")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || h.reachedFall.Load() != 0 {
		t.Fatalf("cookie+key on a non-allowlisted route: code=%d fallback=%d", rec.Code, h.reachedFall.Load())
	}
}

func TestWriteKeyCanCreateAndPublish(t *testing.T) {
	h := newKeyHarness(t, true, map[string]*auth.APIKey{"w": testKey(auth.ScopeWriteEntities)})
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/v1/entities"},
		{"PATCH", "/api/v1/entities/abc/status"},
	} {
		if rec := do(h.handler, c.method, c.path, "w"); rec.Code != http.StatusOK {
			t.Errorf("%s %s: got %d want 200", c.method, c.path, rec.Code)
		}
	}
}

func TestReadKeyCannotCreateOrPublish(t *testing.T) {
	h := newKeyHarness(t, true, map[string]*auth.APIKey{"r": testKey(auth.ScopeReadEntities)})
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/v1/entities"},
		{"PATCH", "/api/v1/entities/abc/status"},
	} {
		if rec := do(h.handler, c.method, c.path, "r"); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: got %d want 403", c.method, c.path, rec.Code)
		}
	}
}

func TestStillNoDelete(t *testing.T) {
	h := newKeyHarness(t, true, map[string]*auth.APIKey{"all": testKey(auth.ScopeAll)})
	for _, p := range []string{"/api/v1/entities/abc", "/api/v1/entities/abc/status", "/api/v1/entities"} {
		if rec := do(h.handler, "DELETE", p, "all"); rec.Code != http.StatusForbidden {
			t.Errorf("DELETE %s: got %d want 403", p, rec.Code)
		}
	}
}

func TestAuthOrAPIKeyRejectsCreatorWithoutEditRole(t *testing.T) {
	for _, tc := range []struct {
		role string
		want int
	}{
		{"owner", http.StatusOK},
		{"editor", http.StatusOK},
		{"viewer", http.StatusUnauthorized},
		{"", http.StatusUnauthorized}, // user deleted/missing: ValidateKey leaves the role empty
		{"EDITOR", http.StatusUnauthorized},
	} {
		t.Run("role="+tc.role, func(t *testing.T) {
			k := testKey(auth.ScopeReadEntities)
			k.CreatorRole = tc.role
			h := newKeyHarness(t, true, map[string]*auth.APIKey{"k": k})
			if rec := do(h.handler, "GET", "/api/v1/entities", "k"); rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestAPIKeyCreatorCanAct(t *testing.T) {
	for role, want := range map[string]bool{"owner": true, "editor": true, "viewer": false, "": false} {
		if got := (&auth.APIKey{CreatorRole: role}).CreatorCanAct(); got != want {
			t.Errorf("role %q: got %v want %v", role, got, want)
		}
	}
}
