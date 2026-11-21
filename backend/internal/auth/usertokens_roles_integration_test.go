//go:build integration

package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/user"
)

func TestInviteAcceptCreatesUserWithBuiltinRoles(t *testing.T) {
	r := newTokRig(t)
	ctx := context.Background()
	owner := r.newUser(t, user.RoleOwner)
	for _, role := range []user.Role{user.RoleAuthor, user.RoleContributor, user.RoleReviewer, user.RoleReadOnly} {
		t.Run(string(role), func(t *testing.T) {
			email := "inv-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + "@example.test"
			token, _, err := r.tok.IssueInvite(ctx, email, role, owner.ID, nil)
			if err != nil {
				t.Fatalf("IssueInvite: %v", err)
			}
			peek, err := r.tok.PeekInvite(ctx, token)
			if err != nil {
				t.Fatalf("PeekInvite: %v", err)
			}
			if peek.Role != role {
				t.Fatalf("peeked role = %q, want %q", peek.Role, role)
			}
			u, err := r.tok.ConsumeInvite(ctx, token, "Invited", "invited-password-123")
			if err != nil {
				t.Fatalf("ConsumeInvite: %v", err)
			}
			t.Cleanup(func() { _ = r.users.Delete(context.Background(), u.ID) })
			if u.Role != role {
				t.Fatalf("created role = %q, want %q", u.Role, role)
			}
			got, err := r.users.GetByID(ctx, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Role != role {
				t.Fatalf("stored role = %q, want %q", got.Role, role)
			}
		})
	}
}
