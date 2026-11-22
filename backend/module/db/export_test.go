package db

import "hatamoto.dev/core/internal/modhost"

// Core-only hooks for the external test package.
var (
	SetPoolForTest = modhost.SetPool
	ResetForTest   = modhost.Reset
)
