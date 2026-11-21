package server

import (
	"testing"

	"hatamoto.dev/core/internal/adminpagestate"
)

// A community build registers nothing, so buildAdminDeps copies empty slices
// and the admin route table (admin_routes.golden) has no module routes.
func TestModuleRegistryEmptyInCommunityBuild(t *testing.T) {
	if n := len(adminpagestate.Pages()); n != 0 {
		t.Fatalf("pages registered in a community build: %d", n)
	}
	if n := len(adminpagestate.Banners()); n != 0 {
		t.Fatalf("banners registered in a community build: %d", n)
	}
}
