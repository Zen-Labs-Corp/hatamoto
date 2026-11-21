package route_test

import (
	"net/http"
	"testing"

	"hatamoto.dev/core/module/route"
)

func panics(fn func()) (p bool) {
	defer func() { p = recover() != nil }()
	fn()
	return
}

var ok = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

func TestRegister(t *testing.T) {
	t.Cleanup(route.ResetForTest)
	route.Register(route.Mount{Module: "example", Prefix: "/example/", Handler: ok})
	got := route.RoutesForTest()
	if len(got) != 1 || got[0].Module != "example" || got[0].Prefix != "/example/" || got[0].MaxBody != route.DefaultMaxBody {
		t.Fatalf("routes: %+v", got)
	}
}

func TestRegisterRefusals(t *testing.T) {
	t.Cleanup(route.ResetForTest)
	for _, prefix := range []string{"/admin/", "/", "/auth/", "/auth/login/", "/Example/"} {
		if !panics(func() { route.Register(route.Mount{Module: "example", Prefix: prefix, Handler: ok}) }) {
			t.Errorf("prefix %q did not panic", prefix)
		}
	}
	if !panics(func() { route.Register(route.Mount{Module: "example", Prefix: "/x/"}) }) {
		t.Error("nil handler did not panic")
	}
}

func TestRegisterAuthSSO(t *testing.T) {
	t.Cleanup(route.ResetForTest)
	route.Register(route.Mount{Module: "sso", Prefix: "/auth/sso/", Handler: ok})
	if len(route.RoutesForTest()) != 1 {
		t.Fatal("/auth/sso/ was not registered")
	}
}

func TestRegisterAfterFreeze(t *testing.T) {
	t.Cleanup(route.ResetForTest)
	route.FreezeForTest()
	if !panics(func() { route.Register(route.Mount{Module: "example", Prefix: "/example/", Handler: ok}) }) {
		t.Error("register after freeze did not panic")
	}
}
