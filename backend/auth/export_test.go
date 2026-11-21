package auth

import "hatamoto.dev/core/internal/authstate"

// Core-only hooks for the external test package.
var (
	ResetForTest  = authstate.Reset
	FreezeForTest = authstate.Freeze
)
