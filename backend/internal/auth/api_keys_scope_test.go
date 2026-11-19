package auth

import "testing"

// The site-structure write scopes are valid, separate from write:entities,
// and write:entities does not imply them.
func TestSiteWriteScopes(t *testing.T) {
	for _, sc := range []APIKeyScope{ScopeWriteMedia, ScopeWriteMenus, ScopeWriteRedirects, ScopeWriteTerms} {
		if !IsValidScope(string(sc)) {
			t.Errorf("%s is not a valid scope", sc)
		}
		if (&APIKey{Scopes: []APIKeyScope{ScopeWriteEntities}}).HasScope(sc) {
			t.Errorf("write:entities grants %s", sc)
		}
		if !(&APIKey{Scopes: []APIKeyScope{sc}, CreatorRole: "owner"}).HasScope(sc) || !(&APIKey{Scopes: []APIKeyScope{ScopeAll}, CreatorRole: "owner"}).HasScope(sc) {
			t.Errorf("%s not honoured", sc)
		}
	}
}

func TestSEOScopes(t *testing.T) {
	for _, sc := range []APIKeyScope{ScopeReadSEO, ScopeWriteSEO} {
		if !IsValidScope(string(sc)) {
			t.Errorf("%s is not a valid scope", sc)
		}
		if (&APIKey{Scopes: []APIKeyScope{ScopeWriteEntities}}).HasScope(sc) {
			t.Errorf("write:entities grants %s", sc)
		}
		if !(&APIKey{Scopes: []APIKeyScope{ScopeAll}, CreatorRole: "owner"}).HasScope(sc) {
			t.Errorf("* does not grant %s", sc)
		}
	}
	w := &APIKey{Scopes: []APIKeyScope{ScopeWriteSEO}, CreatorRole: "owner"}
	r := &APIKey{Scopes: []APIKeyScope{ScopeReadSEO}, CreatorRole: "owner"}
	if !w.HasScope(ScopeReadSEO) {
		t.Error("write:seo must imply read:seo")
	}
	if r.HasScope(ScopeWriteSEO) {
		t.Error("read:seo must not imply write:seo")
	}
}

func TestMediaScopes(t *testing.T) {
	if !IsValidScope("read:media") {
		t.Fatal("read:media is not a valid scope")
	}
	r := &APIKey{Scopes: []APIKeyScope{ScopeReadMedia}}
	w := &APIKey{Scopes: []APIKeyScope{ScopeWriteMedia}}
	all := &APIKey{Scopes: []APIKeyScope{ScopeAll}}
	if !r.HasScope(ScopeReadMedia) || r.HasScope(ScopeWriteMedia) {
		t.Error("read:media must grant read only")
	}
	if !w.HasScope(ScopeReadMedia) || !w.HasScope(ScopeWriteMedia) {
		t.Error("write:media must imply read:media")
	}
	if !all.HasScope(ScopeReadMedia) || !all.HasScope(ScopeWriteMedia) {
		t.Error("* must grant both")
	}
}

func TestFormsScope(t *testing.T) {
	if !IsValidScope("read:forms") {
		t.Fatal("read:forms is not a valid scope")
	}
	listed := false
	for _, sc := range ValidScopes() {
		listed = listed || sc == ScopeReadForms
	}
	if !listed {
		t.Error("read:forms missing from ValidScopes")
	}
	if (&APIKey{Scopes: []APIKeyScope{ScopeReadMedia}}).HasScope(ScopeReadForms) {
		t.Error("read:media grants read:forms")
	}
	if !(&APIKey{Scopes: []APIKeyScope{ScopeAll}}).HasScope(ScopeReadForms) {
		t.Error("* does not grant read:forms")
	}
}

// Owner-only scopes follow the creator's current role, also through "*".
func TestAPIKeyOwnerOnlyScopes(t *testing.T) {
	ownerOnly := []APIKeyScope{ScopeReadSEO, ScopeWriteSEO, ScopeWriteTerms}
	granted := func(held, want APIKeyScope) bool {
		if held == ScopeAll || held == want {
			return true
		}
		return held == ScopeWriteSEO && want == ScopeReadSEO
	}
	for _, role := range []string{"owner", "editor", ""} {
		for _, held := range append([]APIKeyScope{ScopeAll}, ownerOnly...) {
			k := &APIKey{Scopes: []APIKeyScope{held}, CreatorRole: role}
			for _, want := range ownerOnly {
				if got, exp := k.HasScope(want), role == "owner" && granted(held, want); got != exp {
					t.Errorf("role %q holding %s: HasScope(%s) = %v, want %v", role, held, want, got, exp)
				}
			}
		}
	}
	editor := &APIKey{Scopes: []APIKeyScope{ScopeAll}, CreatorRole: "editor"}
	for _, sc := range []APIKeyScope{ScopeReadForms, ScopeWriteEntities} {
		if !editor.HasScope(sc) {
			t.Errorf("* held by an editor creator must still grant %s", sc)
		}
	}
}
