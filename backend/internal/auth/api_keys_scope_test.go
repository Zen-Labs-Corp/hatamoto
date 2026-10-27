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
		if !(&APIKey{Scopes: []APIKeyScope{sc}}).HasScope(sc) || !(&APIKey{Scopes: []APIKeyScope{ScopeAll}}).HasScope(sc) {
			t.Errorf("%s not honoured", sc)
		}
	}
}
