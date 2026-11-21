package app

import (
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/user"
)

func TestUserRolesFlag(t *testing.T) {
	for _, r := range user.Roles() {
		f, err := parseUserFlags("create", []string{"--email", "a@example.com", "--role", string(r)}, true)
		if err != nil {
			t.Fatalf("%s: %v", r, err)
		}
		if got, err := user.ParseRole(f.role); err != nil || got != r {
			t.Errorf("ParseRole(%q) = %q, %v", f.role, got, err)
		}
	}
	f, err := parseUserFlags("create", []string{"--email", "a@example.com"}, true)
	if err != nil || f.role != string(user.RoleOwner) {
		t.Errorf("default role = %q, %v; want owner", f.role, err)
	}
}

func TestUserRolesBadRole(t *testing.T) {
	f, err := parseUserFlags("create", []string{"--email", "a@example.com", "--role", "viewer"}, true)
	if err != nil {
		t.Fatal(err)
	}
	err = runUserCreate(context.Background(), nil, f, strings.NewReader(""), &strings.Builder{})
	if err == nil {
		t.Fatal("want error for viewer")
	}
	for _, r := range user.Roles() {
		if !strings.Contains(err.Error(), string(r)) {
			t.Errorf("error %q does not name %s", err, r)
		}
	}
}

func TestUserRolesUsage(t *testing.T) {
	if !strings.Contains(usage, "read-only") {
		t.Error("usage does not mention read-only")
	}
	if got := roleList(); got != "owner, editor, author, contributor, reviewer or read-only" {
		t.Errorf("roleList = %q", got)
	}
}
