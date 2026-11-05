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
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/user"
)

// accessRepo is a user.Repository over a map. Like the real one it refuses an
// end date for an owner when no other owner is open-ended.
type accessRepo struct {
	users map[uuid.UUID]*user.User
}

func (f *accessRepo) openOwners(except uuid.UUID) int {
	n := 0
	for id, u := range f.users {
		if id != except && u.Role == user.RoleOwner && u.AccessEndsAt == nil {
			n++
		}
	}
	return n
}

func (f *accessRepo) Create(_ context.Context, in *user.CreateUserInput, _ string) (*user.User, error) {
	if in.Role == user.RoleOwner && in.AccessEndsAt != nil && f.openOwners(uuid.Nil) == 0 {
		return nil, user.ErrLastOwner
	}
	u := &user.User{ID: uuid.New(), Email: in.Email, Role: in.Role, AccessEndsAt: in.AccessEndsAt}
	f.users[u.ID] = u
	return u, nil
}
func (f *accessRepo) GetByID(_ context.Context, id uuid.UUID) (*user.User, error) {
	if u, ok := f.users[id]; ok {
		c := *u
		return &c, nil
	}
	return nil, user.ErrUserNotFound
}
func (f *accessRepo) Update(_ context.Context, id uuid.UUID, in *user.UpdateUserInput) (*user.User, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, user.ErrUserNotFound
	}
	if in.AccessEndsAt.Set && in.AccessEndsAt.At != nil && u.Role == user.RoleOwner && f.openOwners(id) == 0 {
		return nil, user.ErrLastOwner
	}
	if in.AccessEndsAt.Set {
		u.AccessEndsAt = in.AccessEndsAt.At
	}
	c := *u
	return &c, nil
}
func (f *accessRepo) GetByEmail(context.Context, string) (*user.User, error) { return nil, nil }
func (f *accessRepo) ListByEmail(context.Context, string) ([]*user.User, error) {
	return nil, nil
}
func (f *accessRepo) UpdateLastLogin(context.Context, uuid.UUID) error { return nil }
func (f *accessRepo) RecordFailedLogin(context.Context, uuid.UUID, int, time.Duration) error {
	return nil
}
func (f *accessRepo) ResetFailedLogins(context.Context, uuid.UUID) error { return nil }
func (f *accessRepo) Delete(context.Context, uuid.UUID) error            { return nil }
func (f *accessRepo) List(context.Context, int, int) ([]*user.User, error) {
	return nil, nil
}
func (f *accessRepo) Count(context.Context) (int, error) { return len(f.users), nil }

type usersAccessRig struct {
	s      *Server
	repo   *accessRepo
	owner  *user.User
	editor *user.User
	log    *bytes.Buffer
}

func newUsersAccessRig() *usersAccessRig {
	owner := &user.User{ID: uuid.New(), Email: "owner@example.test", Role: user.RoleOwner}
	editor := &user.User{ID: uuid.New(), Email: "ed@example.test", Role: user.RoleEditor}
	repo := &accessRepo{users: map[uuid.UUID]*user.User{owner.ID: owner, editor.ID: editor}}
	var buf bytes.Buffer
	return &usersAccessRig{s: &Server{userRepo: repo, logger: slog.New(slog.NewTextHandler(&buf, nil))}, repo: repo, owner: owner, editor: editor, log: &buf}
}

func (g *usersAccessRig) call(h http.HandlerFunc, as *user.User, method, target, id, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if id != "" {
		req.SetPathValue("id", id)
	}
	claims := &auth.Claims{UserID: as.ID, Email: as.Email, Role: string(as.Role)}
	req = req.WithContext(middleware.ContextWithClaims(req.Context(), claims))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestUsersAccessCreate(t *testing.T) {
	g := newUsersAccessRig()
	end := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	body := `{"email":"new@example.test","password":"a-long-enough-password","role":"editor","access_ends_at":"` + end.Format(time.RFC3339) + `"}`
	rec := g.call(g.s.handleCreateUser, g.owner, "POST", "/users", "", body)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"access_ends_at":"`+end.Format(time.RFC3339)) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(g.log.String(), "user.access_changed") || !strings.Contains(g.log.String(), "created") {
		t.Errorf("no audit line: %s", g.log.String())
	}
	for name, tc := range map[string]struct {
		as   *user.User
		body string
		want int
	}{
		"editor":   {g.editor, `{"email":"a@example.test","password":"a-long-enough-password","role":"editor","access_ends_at":"` + end.Format(time.RFC3339) + `"}`, http.StatusForbidden},
		"past":     {g.owner, `{"email":"b@example.test","password":"a-long-enough-password","role":"editor","access_ends_at":"2020-01-01T00:00:00Z"}`, http.StatusBadRequest},
		"no zone":  {g.owner, `{"email":"c@example.test","password":"a-long-enough-password","role":"editor","access_ends_at":"2030-01-01"}`, http.StatusBadRequest},
		"no limit": {g.owner, `{"email":"d@example.test","password":"a-long-enough-password","role":"editor"}`, http.StatusCreated},
	} {
		if rec := g.call(g.s.handleCreateUser, tc.as, "POST", "/users", "", tc.body); rec.Code != tc.want {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestUsersAccessPatch(t *testing.T) {
	g := newUsersAccessRig()
	id := g.editor.ID.String()
	patch := func(as *user.User, body string) *httptest.ResponseRecorder {
		return g.call(g.s.handleUpdateUser, as, "PATCH", "/users/"+id, id, body)
	}
	end := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)

	if rec := patch(g.owner, `{"access_ends_at":"`+end.Format(time.RFC3339)+`"}`); rec.Code != 200 || g.repo.users[g.editor.ID].AccessEndsAt == nil {
		t.Fatalf("set: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(g.log.String(), "user.access_changed") {
		t.Error("set not audited")
	}
	// Absent leaves it alone.
	if rec := patch(g.owner, `{"name":"Ed"}`); rec.Code != 200 || g.repo.users[g.editor.ID].AccessEndsAt == nil {
		t.Fatalf("absent: %d %s", rec.Code, rec.Body.String())
	}
	// An editor cannot touch it.
	if rec := patch(g.editor, `{"access_ends_at":null}`); rec.Code != http.StatusForbidden || g.repo.users[g.editor.ID].AccessEndsAt == nil {
		t.Fatalf("editor: %d", rec.Code)
	}
	// A past value is refused and stores nothing.
	if rec := patch(g.owner, `{"access_ends_at":"2020-01-01T00:00:00Z"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("past: %d", rec.Code)
	}
	// null clears.
	g.log.Reset()
	if rec := patch(g.owner, `{"access_ends_at":null}`); rec.Code != 200 || g.repo.users[g.editor.ID].AccessEndsAt != nil {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(g.log.String(), "user.access_changed") {
		t.Error("clear not audited")
	}
	// The sole open-ended owner cannot get a date.
	oid := g.owner.ID.String()
	rec := g.call(g.s.handleUpdateUser, g.owner, "PATCH", "/users/"+oid, oid, `{"access_ends_at":"`+end.Format(time.RFC3339)+`"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "another owner without an end date") {
		t.Fatalf("sole owner: %d %s", rec.Code, rec.Body.String())
	}
}
