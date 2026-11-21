package modhost

import (
	"net/http"
	"testing"
)

func TestRouteRefusal(t *testing.T) {
	for _, p := range []string{
		"/", "/admin/", "/api/", "/api/v2/", "/p/", "/mcp/", "/media/", "/auth/", "/auth/login/", "/auth/register/",
		"/auth/login/x/", "/forms/", "/health/", "/-/", "/locale/", "/uploads/", "/robots.txt/", "/Example/",
		"/example", "example/", "/ex//",
	} {
		if RouteRefusal(p) == "" {
			t.Errorf("%q accepted, want refused", p)
		}
	}
	for _, p := range []string{"/example/", "/sync/v2/", "/auth/sso/", "/auth/sso/v2/"} {
		if why := RouteRefusal(p); why != "" {
			t.Errorf("%q refused: %s", p, why)
		}
	}
}

func TestRegisterRoute(t *testing.T) {
	t.Cleanup(Reset)
	h := http.NotFoundHandler()
	RegisterRoute(Route{Module: "example", Prefix: "/example/", Handler: h})
	RegisterRoute(Route{Module: "example", Prefix: "/sync/", Handler: h, MaxBody: 4 << 20})
	RegisterRoute(Route{Module: "sso", Prefix: "/auth/sso/", Handler: h})
	got := Routes()
	if len(got) != 3 || got[0].MaxBody != DefaultRouteMaxBody || got[1].MaxBody != 4<<20 {
		t.Fatalf("routes: %+v", got)
	}
	mustPanic(t, "overlap deeper", func() { RegisterRoute(Route{Module: "x", Prefix: "/example/sub/", Handler: h}) })
	mustPanic(t, "overlap shallower", func() { RegisterRoute(Route{Module: "x", Prefix: "/auth/sso/x/", Handler: h}) })
	mustPanic(t, "same", func() { RegisterRoute(Route{Module: "x", Prefix: "/example/", Handler: h}) })
	mustPanic(t, "nil handler", func() { RegisterRoute(Route{Module: "x", Prefix: "/x/"}) })
	var typedNil *http.ServeMux
	mustPanic(t, "typed nil", func() { RegisterRoute(Route{Module: "x", Prefix: "/x/", Handler: typedNil}) })
	mustPanic(t, "bad module", func() { RegisterRoute(Route{Module: "X", Prefix: "/x/", Handler: h}) })
	mustPanic(t, "refused", func() { RegisterRoute(Route{Module: "x", Prefix: "/auth/login/", Handler: h}) })
	mustPanic(t, "negative body", func() { RegisterRoute(Route{Module: "x", Prefix: "/x/", Handler: h, MaxBody: -1}) })
	mustPanic(t, "huge body", func() { RegisterRoute(Route{Module: "x", Prefix: "/x/", Handler: h, MaxBody: 33 << 20}) })
	Freeze()
	mustPanic(t, "frozen", func() { RegisterRoute(Route{Module: "x", Prefix: "/x/", Handler: h}) })
	Reset()
	if len(Routes()) != 0 {
		t.Fatal("reset did not clear")
	}
}
