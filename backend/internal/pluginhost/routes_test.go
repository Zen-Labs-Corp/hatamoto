package pluginhost

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"hatamoto.dev/sdk"
)

// A route receives a request header beyond the fixed list only when the
// manifest declares it; credentials never arrive.
func TestRouteRequestHeaders(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, func(m *sdk.Manifest) { m.RequestHeaders = []string{"Stripe-Signature"} })
	call := func(g *RouteRegistry, rest string, user *sdk.HTTPUser) map[string]string {
		req := httptest.NewRequest("GET", "/x/"+rest, nil)
		req.Header.Set("Stripe-Signature", "t=1,v1=abc")
		req.Header.Set("X-Other", "no")
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("Cookie", "session=secret")
		rec := httptest.NewRecorder()
		g.Serve(rec, req, "capa", rest, user)
		var echoed sdk.HTTPRequest
		if err := json.Unmarshal(rec.Body.Bytes(), &echoed); err != nil {
			t.Fatalf("%s: %d %s", rest, rec.Code, rec.Body)
		}
		return echoed.Headers
	}
	for name, h := range map[string]map[string]string{
		"admin": call(r.rt.AdminRoutes(), "echo", &sdk.HTTPUser{ID: "u1", Role: "owner"}),
	} {
		if h["stripe-signature"] != "t=1,v1=abc" {
			t.Errorf("%s: declared header missing: %v", name, h)
		}
		for _, no := range []string{"x-other", "authorization", "cookie"} {
			if _, ok := h[no]; ok {
				t.Errorf("%s: %q must not be passed: %v", name, no, h)
			}
		}
	}

	// Undeclared: the header does not arrive.
	r2 := newCapaRig(t)
	r2.load(t, nil)
	if _, ok := call(r2.rt.AdminRoutes(), "echo", &sdk.HTTPUser{ID: "u1", Role: "owner"})["stripe-signature"]; ok {
		t.Error("an undeclared header was passed")
	}

	// Forbidden names fail validation, whatever the case.
	bad := capaManifest(func(m *sdk.Manifest) { m.RequestHeaders = []string{"Authorization"} })
	if err := bad.Validate(); err == nil {
		t.Error("authorization must be refused at Validate")
	}
}

// A public response of a plugin that reads cookies is per visitor: a shared
// cache must not store it, and it varies on Cookie.
func TestPublicRouteWithCookiesIsNotSharedCacheable(t *testing.T) {
	for name, tc := range map[string]struct {
		cookies []string
		cc      string
		want    string
		vary    bool
	}{
		"cookies, public":   {[]string{"cart"}, "public, max-age=60", "no-store", true},
		"cookies, s-maxage": {[]string{"cart"}, "s-maxage=60", "no-store", true},
		"no cookies":        {nil, "public, max-age=60", "public, max-age=60", false},
	} {
		g := &RouteRegistry{public: true}
		rec := httptest.NewRecorder()
		res := sdk.HTTPResponse{Body: "x"}
		if tc.cc != "" {
			res.Headers = map[string]string{"Cache-Control": tc.cc}
		}
		g.writeRouteResponse(rec, httptest.NewRequest("GET", "/x", nil), res, sdk.Manifest{Name: "capa", Cookies: tc.cookies})
		if got := rec.Header().Get("Cache-Control"); got != tc.want || (rec.Header().Get("Vary") == "Cookie") != tc.vary {
			t.Errorf("%s: Cache-Control=%q Vary=%q", name, rec.Header().Get("Cache-Control"), rec.Header().Get("Vary"))
		}
	}
}
