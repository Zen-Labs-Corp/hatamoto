package agentpolicy

import "hatamoto.dev/core/internal/agentpolicystate"

// Core-only hooks for the external test package.
var (
	ResetForTest  = agentpolicystate.Reset
	FreezeForTest = agentpolicystate.Freeze
	DecideForTest = agentpolicystate.Decide
)
