package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authztest"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/testutil"
	"hatamoto.dev/core/internal/user"
)

// emailUsers answers GetByEmail with one fixed account.
type emailUsers struct {
	user.Repository
	u *user.User
}

func (r emailUsers) GetByEmail(context.Context, string) (*user.User, error) { return r.u, nil }

func TestAuthzInline_DeleteUserAsksAuthorizer(t *testing.T) {
	s := &Server{logger: quietLogger()}
	owner := &auth.Claims{UserID: uuid.New(), Role: "owner"}
	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/users/x", nil)
		r.SetPathValue("id", uuid.NewString())
		r = r.WithContext(middleware.ContextWithClaims(r.Context(), owner))
		w := httptest.NewRecorder()
		s.handleDeleteUser(w, r)
		return w
	}
	authztest.Use(t, authztest.DenyAll)
	w := call()
	testutil.Equal(t, http.StatusForbidden, w.Code, "denied owner")
	testutil.Contains(t, w.Body.String(), "only owners can delete users")
}

func TestAuthzInline_CommentStaffAsksAuthorizer(t *testing.T) {
	s := &Server{logger: quietLogger()}
	claims := &auth.Claims{UserID: uuid.New(), Role: "owner"}
	testutil.True(t, s.commentStaff(context.Background(), claims), "default allows an owner")
	authztest.Use(t, authztest.DenyAll)
	testutil.True(t, !s.commentStaff(context.Background(), claims), "deny means not staff")
}

func TestAuthzInline_FormAdminLinkAsksAuthorizer(t *testing.T) {
	s := &Server{logger: quietLogger(), userRepo: emailUsers{u: &user.User{ID: uuid.New(), Role: user.RoleOwner}}}
	testutil.True(t, s.isAdminAccountEmail(context.Background(), "a@example.test"), "default allows an owner")
	authztest.Use(t, authztest.DenyAll)
	testutil.True(t, !s.isAdminAccountEmail(context.Background(), "a@example.test"), "deny means no admin link")
}

// Changing a role to owner needs the owner grant, not only user management.
func TestAuthzInline_UpdateUserToOwnerAsksOwnerGrant(t *testing.T) {
	s := &Server{logger: quietLogger()}
	owner := &auth.Claims{UserID: uuid.New(), Role: "owner"}
	authztest.Use(t, authztest.Func(func(_ context.Context, _ authz.Subject, act authz.Action, _ authz.Object) (bool, error) {
		return act == authz.ActionUserManage, nil
	}))
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/users/x", strings.NewReader(`{"Role":"owner"}`))
	r.SetPathValue("id", uuid.NewString())
	r = r.WithContext(middleware.ContextWithClaims(r.Context(), owner))
	w := httptest.NewRecorder()
	s.handleUpdateUser(w, r)
	testutil.Equal(t, http.StatusForbidden, w.Code, "user.manage without user.owner.grant")
	testutil.Contains(t, w.Body.String(), "only owners can grant the owner role")
}
