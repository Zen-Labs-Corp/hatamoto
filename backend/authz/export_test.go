package authz

import "hatamoto.dev/core/internal/authzstate"

// Core-only hooks for the external test package.
var (
	ResetForTest          = authzstate.Reset
	FreezeForTest         = authzstate.Freeze
	KnownScopesForTest    = knownScopes
	OwnerOnlyScopeForTest = ownerOnlyScope
)

// TableScopeForTest returns the key scope the default table requires for a.
func TableScopeForTest(a Action) string { return table[a].scope }
