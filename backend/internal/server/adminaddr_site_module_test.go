package server

import (
	"net/http"
	"testing"

	"hatamoto.dev/core/internal/modhost"
)

func TestModulePrefixIsAFixedRoutePrefix(t *testing.T) {
	t.Cleanup(modhost.Reset)
	if computeFixedRoutePrefixes()["example"] {
		t.Fatal("example is reserved with nothing registered")
	}
	modhost.RegisterRoute(modhost.Route{Module: "example", Prefix: "/example/", Handler: http.NotFoundHandler()})
	if !computeFixedRoutePrefixes()["example"] {
		t.Fatal("a module prefix is not refused as an admin address")
	}
	modhost.Reset()
	if computeFixedRoutePrefixes()["example"] {
		t.Fatal("example is still reserved after Reset")
	}
}
