package mail

import "hatamoto.dev/core/internal/modhost"

// Core-only hooks for the external test package.
var (
	ResetForTest     = modhost.Reset
	SetMailerForTest = modhost.SetMailer
)
