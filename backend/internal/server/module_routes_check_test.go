package server

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/modhost"
)

func TestCheckModuleRoutes(t *testing.T) {
	cases := []struct {
		name, admin, api, prefix string
		wantErr                  bool
	}{
		{"under admin address", "/secretaddr", "", "/secretaddr/", true},
		{"admin address, other case", "/SecretAddr", "", "/secretaddr/x/", true},
		{"under api prefix", "/admin", "/content", "/content/v1/", true},
		{"over api prefix", "/admin", "/content/v1", "/content/", true},
		{"probe wp-admin", "/admin", "", "/wp-admin/", true},
		{"probe phpmyadmin", "/admin", "", "/phpmyadmin/", true},
		{"auth sso", "/admin", "", "/auth/sso/", false},
		{"clean", "/secretaddr", "/content", "/example/", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Cleanup(modhost.Reset)
			modhost.RegisterRoute(modhost.Route{Module: "example", Prefix: c.prefix, Handler: http.NotFoundHandler()})
			if err := CheckModuleRoutes(c.admin, c.api); (err != nil) != c.wantErr {
				t.Fatalf("err = %v, want error %v", err, c.wantErr)
			}
		})
	}
	if err := CheckModuleRoutes("/secretaddr", "/content"); err != nil {
		t.Fatalf("nothing registered: %v", err)
	}
}

// TestCoreAuthPathsMatchRoutes fails when core serves a new path under /auth/
// that modhost.CoreAuthPaths does not name, so a module could shadow it.
func TestCoreAuthPathsMatchRoutes(t *testing.T) {
	table, err := RouteTable()
	if err != nil {
		t.Fatal(err)
	}
	for _, rt := range table {
		_, p, _ := strings.Cut(rt.Pattern, " ")
		if !strings.HasPrefix(p, "/auth/") || p == "/auth/{rest...}" {
			continue
		}
		seg := strings.Split(strings.TrimPrefix(p, "/auth/"), "/")[0]
		if !slices.Contains(modhost.CoreAuthPaths, seg) || modhost.RouteRefusal("/auth/"+seg+"/") == "" {
			t.Errorf("core route %s is not covered by modhost.CoreAuthPaths", rt.Pattern)
		}
	}
	for _, seg := range modhost.CoreAuthPaths {
		if modhost.RouteRefusal("/auth/"+seg+"/") == "" {
			t.Errorf("/auth/%s/ is not refused", seg)
		}
	}
}
