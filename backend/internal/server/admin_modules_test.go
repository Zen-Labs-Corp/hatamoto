package server

import (
	"context"
	"testing"
	"testing/fstest"

	"hatamoto.dev/core/internal/adminpagestate"
	"hatamoto.dev/core/internal/config"
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

// buildAdminDeps is the only server-to-admin edge: dropping the module fields
// would make every module page and banner vanish with all other tests green.
func TestBuildAdminDepsCarriesModules(t *testing.T) {
	adminpagestate.Reset()
	t.Cleanup(adminpagestate.Reset)
	adminpagestate.Register(adminpagestate.Page{
		ID: "wiring", Label: "Wiring", Templates: fstest.MapFS{},
		Routes: []adminpagestate.Route{{Method: "GET", Handle: func(adminpagestate.Context) error { return nil }}},
	})
	adminpagestate.RegisterBanner("wiring", func(context.Context) ([]adminpagestate.Banner, error) { return nil, nil })

	d, err := (&Server{cfg: &config.Config{}}).buildAdminDeps()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.ModulePages) != 1 || d.ModulePages[0].ID != "wiring" {
		t.Errorf("ModulePages = %+v, want the registered page", d.ModulePages)
	}
	if len(d.ModuleBanners) != 1 || d.ModuleBanners[0].Name != "wiring" {
		t.Errorf("ModuleBanners = %+v, want the registered banner", d.ModuleBanners)
	}
}
