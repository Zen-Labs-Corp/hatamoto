package review

import "hatamoto.dev/core/internal/reviewstate"

// Core-only hooks for the external test package.
var (
	ResetForTest  = reviewstate.Reset
	FreezeForTest = reviewstate.Freeze

	SetPendingSourceForTest = reviewstate.SetPendingSource
	MayDecideForTest        = reviewstate.MayDecide
)
