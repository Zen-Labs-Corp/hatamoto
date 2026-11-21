package authz_test

import (
	"context"
	"testing"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/user"
)

// A role id the user package lists must be known to the default authorizer.
func TestUserRolesKnownToDefault(t *testing.T) {
	for _, r := range user.Roles() {
		if r == user.RoleOwner || r == user.RoleEditor {
			continue
		}
		s := authz.Subject{UserID: "u", Role: string(r)}
		ok, err := authz.Default().Can(context.Background(), s, authz.ActionDashboardRead, authz.Object{})
		if err != nil || !ok {
			t.Errorf("role %q: dashboard.read = (%v, %v), want allowed", r, ok, err)
		}
	}
}
