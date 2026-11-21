package user

import (
	"errors"
	"slices"
	"testing"
)

func TestRolesValid(t *testing.T) {
	for _, r := range []Role{"owner", "editor", "author", "contributor", "reviewer", "read-only"} {
		if !ValidRole(r) {
			t.Errorf("ValidRole(%q) = false", r)
		}
	}
	for _, r := range []Role{"", "viewer", "Owner", "read_only"} {
		if ValidRole(r) {
			t.Errorf("ValidRole(%q) = true", r)
		}
		if _, err := ParseRole(string(r)); !errors.Is(err, ErrInvalidRole) {
			t.Errorf("ParseRole(%q) err = %v", r, err)
		}
	}
	if r, err := ParseRole("read-only"); err != nil || r != RoleReadOnly {
		t.Errorf("ParseRole(read-only) = %q, %v", r, err)
	}
}

func TestRolesOrderAndLabels(t *testing.T) {
	want := []Role{"owner", "editor", "author", "contributor", "reviewer", "read-only"}
	if got := Roles(); !slices.Equal(got, want) {
		t.Fatalf("Roles() = %v, want %v", got, want)
	}
	labels := []string{"Owner", "Editor", "Author", "Contributor", "Reviewer", "Read-only"}
	for i, r := range want {
		if got := r.Label(); got != labels[i] {
			t.Errorf("%q.Label() = %q, want %q", r, got, labels[i])
		}
	}
	if got := Role("viewer").Label(); got != "viewer" {
		t.Errorf("unknown label = %q, want the id", got)
	}
}

func TestRolesReturnsFreshSlice(t *testing.T) {
	a := Roles()
	a[0] = "changed"
	if Roles()[0] != RoleOwner {
		t.Error("mutating the result changed the next call")
	}
}
