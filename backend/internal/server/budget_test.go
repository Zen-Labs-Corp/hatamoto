package server

import (
	"testing"

	"hatamoto.dev/core/internal/budgetfile"
)

// Allocation budget of the render-nodes endpoint (12-section page, one move op, one fragment).
func TestBudgetAllocsRenderNodes(t *testing.T) {
	budgetfile.CheckAllocs(t, "server", "alloc_RenderNodesEndpoint", BenchmarkRenderNodesEndpoint)
}
