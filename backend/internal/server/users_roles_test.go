package server

import (
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/user"
)

// createUserHandler is the handler behind its mount: POST /users sits behind
// RequireCan(user.manage).
func createUserHandler(g *usersAccessRig) http.HandlerFunc {
	return middleware.RequireCan(authz.ActionUserManage)(http.HandlerFunc(g.s.handleCreateUser)).ServeHTTP
}

func TestUsersRolesCreateReadOnly(t *testing.T) {
	g := newUsersAccessRig()
	body := `{"email":"ro@example.test","password":"a-long-enough-password","role":"read-only"}`
	rec := g.call(createUserHandler(g), g.owner, "POST", "/users", "", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var found bool
	for _, u := range g.repo.users {
		if u.Email == "ro@example.test" {
			found = true
			if u.Role != user.RoleReadOnly {
				t.Errorf("repo got role %q, want %q", u.Role, user.RoleReadOnly)
			}
		}
	}
	if !found {
		t.Error("user not created")
	}
}

func TestUsersRolesInvalidRole(t *testing.T) {
	g := newUsersAccessRig()
	body := `{"email":"v@example.test","password":"a-long-enough-password","role":"viewer"}`
	rec := g.call(createUserHandler(g), g.owner, "POST", "/users", "", body)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid role") {
		t.Fatalf("%d %s, want 400 invalid role", rec.Code, rec.Body.String())
	}
}

func TestUsersRolesReadOnlyCannotCreate(t *testing.T) {
	g := newUsersAccessRig()
	ro := &user.User{ID: g.owner.ID, Email: "ro@example.test", Role: user.RoleReadOnly}
	before := len(g.repo.users)
	body := `{"email":"x@example.test","password":"a-long-enough-password","role":"editor"}`
	rec := g.call(createUserHandler(g), ro, "POST", "/users", "", body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("%d %s, want 403", rec.Code, rec.Body.String())
	}
	if len(g.repo.users) != before {
		t.Error("a user was created")
	}
}
