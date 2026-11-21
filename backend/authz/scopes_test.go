package authz_test

import (
	"testing"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/auth"
)

func TestScopeSetMatchesAuth(t *testing.T) {
	want := map[string]bool{}
	for _, s := range auth.ValidScopes() {
		if s != auth.ScopeAll {
			want[string(s)] = true
		}
	}
	got := map[string]bool{}
	for _, s := range authz.KnownScopesForTest {
		got[s] = true
	}
	for s := range want {
		if !got[s] {
			t.Errorf("scope %q is valid in auth but unknown to authz", s)
		}
	}
	for s := range got {
		if !want[s] {
			t.Errorf("scope %q is known to authz but not valid in auth", s)
		}
	}
}

func TestOwnerOnlyScopesMatchAuth(t *testing.T) {
	for _, s := range auth.ValidScopes() {
		if got, want := authz.OwnerOnlyScopeForTest(string(s)), auth.OwnerOnlyScope(s); got != want {
			t.Errorf("owner-only(%q) = %v, auth says %v", s, got, want)
		}
	}
}

func TestTableScopesAreValid(t *testing.T) {
	valid := map[string]bool{}
	for _, s := range auth.ValidScopes() {
		valid[string(s)] = true
	}
	for _, a := range authz.Actions() {
		if s := authz.TableScopeForTest(a); s != "" && !valid[s] {
			t.Errorf("%s requires unknown scope %q", a, s)
		}
	}
}
