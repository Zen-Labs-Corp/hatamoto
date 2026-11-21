package secret

import "hatamoto.dev/core/internal/modhost"

// Core-only hooks for the external test package.
var (
	SetSourceForTest = modhost.SetSecretSource
	ResetForTest     = modhost.Reset
)
