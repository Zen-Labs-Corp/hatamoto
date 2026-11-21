package server

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/user"
)

// restUserAuditRepo is an in-memory user.Repository for the user handlers.
type restUserAuditRepo struct {
	user.Repository
	users map[uuid.UUID]*user.User
}

func (r *restUserAuditRepo) GetByID(_ context.Context, id uuid.UUID) (*user.User, error) {
	u, ok := r.users[id]
	if !ok {
		return nil, user.ErrUserNotFound
	}
	c := *u
	return &c, nil
}

func (r *restUserAuditRepo) Create(_ context.Context, in *user.CreateUserInput, hash string) (*user.User, error) {
	u := &user.User{ID: uuid.New(), Email: in.Email, Role: in.Role, PasswordHash: hash}
	r.users[u.ID] = u
	return u, nil
}

func (r *restUserAuditRepo) Update(_ context.Context, id uuid.UUID, in *user.UpdateUserInput) (*user.User, error) {
	u, ok := r.users[id]
	if !ok {
		return nil, user.ErrUserNotFound
	}
	if in.Role != nil {
		u.Role = *in.Role
	}
	c := *u
	return &c, nil
}

func (r *restUserAuditRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := r.users[id]; !ok {
		return user.ErrUserNotFound
	}
	delete(r.users, id)
	return nil
}

func restUserAuditRig(t *testing.T) (*Server, *restLoginLog, *restUserAuditRepo, *user.User) {
	t.Helper()
	owner := &user.User{ID: uuid.New(), Email: "owner@example.test", Role: user.RoleOwner}
	repo := &restUserAuditRepo{users: map[uuid.UUID]*user.User{owner.ID: owner}}
	lg := &restLoginLog{}
	return &Server{userRepo: repo, logger: slog.New(lg)}, lg, repo, owner
}

func restUserAuditDo(s *Server, h http.HandlerFunc, caller *user.User, method, path, id, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if id != "" {
		r.SetPathValue("id", id)
	}
	r = r.WithContext(middleware.ContextWithClaims(r.Context(), &auth.Claims{UserID: caller.ID, Email: caller.Email, Role: string(caller.Role)}))
	rr := httptest.NewRecorder()
	h(rr, r)
	return rr
}

func restUserAuditOne(t *testing.T, lg *restLoginLog, action string) map[string]any {
	t.Helper()
	if len(lg.recs) != 1 || lg.recs[0]["action"] != action {
		t.Fatalf("rows = %v, want one %s", lg.recs, action)
	}
	rec := lg.recs[0]
	if rec["actor"] != "owner@example.test" || !strings.Contains(rec["detail"].(string), `"via":"api"`) {
		t.Fatalf("row = %v", rec)
	}
	return rec
}

func TestRestUserAuditCreateRoleChangeDelete(t *testing.T) {
	t.Parallel()

	s, lg, repo, owner := restUserAuditRig(t)
	rr := restUserAuditDo(s, s.handleCreateUser, owner, "POST", "/users", "",
		`{"email":"ed@example.test","password":"a-long-typed-password","role":"editor"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body)
	}
	rec := restUserAuditOne(t, lg, admin.AuditUserCreated)
	if rec["target"] != "ed@example.test" || !strings.Contains(rec["detail"].(string), `"role":"editor"`) ||
		strings.Contains(rec["detail"].(string)+rec["actor"].(string), "a-long-typed-password") {
		t.Fatalf("create row = %v", rec)
	}
	var id uuid.UUID
	for k, u := range repo.users {
		if u.Email == "ed@example.test" {
			id = k
		}
	}

	lg.recs = nil
	if rr := restUserAuditDo(s, s.handleUpdateUser, owner, "PUT", "/users/x", id.String(), `{"Role":"owner"}`); rr.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rr.Code, rr.Body)
	}
	rec = restUserAuditOne(t, lg, admin.AuditUserRoleChanged)
	if d := rec["detail"].(string); !strings.Contains(d, `"from":"editor"`) || !strings.Contains(d, `"to":"owner"`) {
		t.Fatalf("role row = %v", rec)
	}

	lg.recs = nil
	if rr := restUserAuditDo(s, s.handleDeleteUser, owner, "DELETE", "/users/x", id.String(), ""); rr.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body)
	}
	rec = restUserAuditOne(t, lg, admin.AuditUserDeleted)
	if rec["target"] != "ed@example.test" || !strings.Contains(rec["detail"].(string), `"role":"owner"`) {
		t.Fatalf("delete row = %v", rec)
	}
}

func TestRestUserAuditRefusedWritesWriteNothing(t *testing.T) {
	t.Parallel()

	s, lg, _, owner := restUserAuditRig(t)
	if rr := restUserAuditDo(s, s.handleCreateUser, owner, "POST", "/users", "", `{"email":"x@example.test","password":"pw","role":"nope"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("create: %d", rr.Code)
	}
	if rr := restUserAuditDo(s, s.handleDeleteUser, owner, "DELETE", "/users/x", owner.ID.String(), ""); rr.Code != http.StatusBadRequest {
		t.Fatalf("delete self: %d", rr.Code)
	}
	if rr := restUserAuditDo(s, s.handleDeleteUser, owner, "DELETE", "/users/x", uuid.NewString(), ""); rr.Code != http.StatusNotFound {
		t.Fatalf("delete unknown: %d", rr.Code)
	}
	if len(lg.recs) != 0 {
		t.Fatalf("rows = %v", lg.recs)
	}
}

func TestRestUserAuditAPIKeyDetailNeverHoldsTheKey(t *testing.T) {
	t.Parallel()

	exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	k := &auth.APIKeyWithSecret{
		APIKey:   auth.APIKey{ID: uuid.New(), Name: "ci", KeyPrefix: "hm_abcd", Scopes: []auth.APIKeyScope{auth.ScopeReadEntities}, ExpiresAt: &exp},
		PlainKey: "hm_abcd_plain_secret_value",
	}
	d := restAPIKeyCreatedDetail(k)
	if d["name"] != "ci" || d["prefix"] != "hm_abcd" || d["expires"] != "2030-01-02T03:04:05Z" {
		t.Fatalf("detail = %v", d)
	}
	if strings.Contains(strings.Join(d["scopes"].([]string), ","), "secret") || len(d) != 4 {
		t.Fatalf("detail = %v", d)
	}
	if restAPIKeyTarget(k.ID) != "api_key:"+k.ID.String() {
		t.Fatal("target")
	}
	name := "new"
	up := restAPIKeyUpdatedDetail(&k.APIKey, &auth.UpdateAPIKeyInput{Name: &name, Scopes: []auth.APIKeyScope{auth.ScopeReadEntities}})
	if got := strings.Join(up["changed"].([]string), ","); got != "name,scopes" || up["name"] != "ci" {
		t.Fatalf("update detail = %v", up)
	}
}
