package adminpages

import "hatamoto.dev/core/internal/adminpagestate"

// Core-only hooks for the external test package.
var (
	ResetForTest  = adminpagestate.Reset
	FreezeForTest = adminpagestate.Freeze
)
