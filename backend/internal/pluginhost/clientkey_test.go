package pluginhost

import (
	"encoding/base64"
	"net/http/httptest"
	"testing"

	"hatamoto.dev/core/internal/clientkey"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/sdk"
)

type keysBody struct {
	AddrKey string `json:"addr_key"`
	NetKey  string `json:"net_key"`
}

// keyRig is a capa rig with a deriver and a public /keys route.
func keyRig(t *testing.T) *capaRig {
	t.Helper()
	r := newCapaRig(t)
	r.rt.caps.keys = clientkey.New("test-secret")
	return r
}

func withKeys(m *sdk.Manifest) {
	m.Permissions = append(m.Permissions, sdk.PermClientKey)
	m.Routes = append(m.Routes, sdk.Route{Method: "GET", Path: "/keys", Public: true}, sdk.Route{Method: "GET", Path: "/keys-admin"})
}

func serveKeys(t *testing.T, r *capaRig, plugin, ip string) (keysBody, string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/p/"+plugin+"/keys", nil)
	if ip != "" {
		req = req.WithContext(clientkey.WithIP(req.Context(), ip))
	}
	rec := httptest.NewRecorder()
	r.rt.PublicRoutes().Serve(rec, req, plugin, "keys", nil)
	var b keysBody
	if err := jsonx.UnmarshalPinned(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	return b, rec.Body.String()
}

func TestClientKeyPublicRoute(t *testing.T) {
	r := keyRig(t)
	r.load(t, withKeys)
	r.grant(t, sdk.PermClientKey)

	a, _ := serveKeys(t, r, "capa", "203.0.113.7")
	b, _ := serveKeys(t, r, "capa", "203.0.113.99")
	for _, k := range []string{a.AddrKey, a.NetKey} {
		if raw, err := base64.RawURLEncoding.DecodeString(k); err != nil || len(raw) != 32 {
			t.Fatalf("key %q is not 32 bytes of base64url: %v", k, err)
		}
	}
	if a.NetKey != b.NetKey || a.AddrKey == b.AddrKey {
		t.Errorf("same /24: net keys %q %q, addr keys %q %q", a.NetKey, b.NetKey, a.AddrKey, b.AddrKey)
	}
	for _, ip := range []string{"not an ip", ""} {
		if got, _ := serveKeys(t, r, "capa", ip); got.AddrKey != "" || got.NetKey != "" {
			t.Errorf("ip %q: keys present: %+v", ip, got)
		}
	}
}

func TestClientKeyNotGranted(t *testing.T) {
	r := keyRig(t)
	r.load(t, withKeys) // asked, never granted
	if got, _ := serveKeys(t, r, "capa", "203.0.113.7"); got.AddrKey != "" || got.NetKey != "" {
		t.Errorf("ungranted plugin got keys: %+v", got)
	}
	r2 := newCapaRig(t) // granted, but no server secret
	r2.load(t, withKeys)
	r2.grant(t, sdk.PermClientKey)
	if got, _ := serveKeys(t, r2, "capa", "203.0.113.7"); got.AddrKey != "" || got.NetKey != "" {
		t.Errorf("no deriver, yet keys: %+v", got)
	}
}

func TestClientKeyTrustedProxyPath(t *testing.T) {
	r := keyRig(t)
	r.load(t, withKeys)
	r.grant(t, sdk.PermClientKey)
	proxied := httptest.NewRequest("GET", "/x", nil)
	proxied.RemoteAddr = "127.0.0.1:5000"
	proxied.Header.Set("X-Forwarded-For", "203.0.113.7")
	got, _ := serveKeys(t, r, "capa", middleware.GetClientIP(proxied))
	want, _ := serveKeys(t, r, "capa", "203.0.113.7")
	if got.AddrKey == "" || got != want {
		t.Errorf("proxied %+v, direct %+v", got, want)
	}
}

func TestClientKeyAdminRouteEmpty(t *testing.T) {
	r := keyRig(t)
	r.load(t, withKeys)
	r.grant(t, sdk.PermClientKey)
	req := httptest.NewRequest("GET", "/admin/keys", nil)
	req = req.WithContext(clientkey.WithIP(req.Context(), "203.0.113.7"))
	rec := httptest.NewRecorder()
	r.rt.AdminRoutes().Serve(rec, req, "capa", "keys-admin", &sdk.HTTPUser{ID: "1", Role: "owner"})
	var got keysBody
	if err := jsonx.UnmarshalPinned(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("admin route: %d %q: %v", rec.Code, rec.Body, err)
	}
	if got.AddrKey != "" || got.NetKey != "" {
		t.Errorf("admin route got keys: %+v", got)
	}
}

func TestClientKeyPerPluginRoute(t *testing.T) {
	r := keyRig(t)
	r.loadAs(t, "capa", withKeys)
	r.loadAs(t, "capb", withKeys)
	r.grant(t, sdk.PermClientKey)
	if _, err := r.rt.Grant(t.Context(), "capb", []string{sdk.PermClientKey}); err != nil {
		t.Fatal(err)
	}
	a, _ := serveKeys(t, r, "capa", "203.0.113.7")
	b, _ := serveKeys(t, r, "capb", "203.0.113.7")
	if a.AddrKey == "" || b.AddrKey == "" || a.AddrKey == b.AddrKey || a.NetKey == b.NetKey {
		t.Errorf("plugins share keys: %+v %+v", a, b)
	}
}

func TestClientKeyRouteNotSharedCacheable(t *testing.T) {
	g := &RouteRegistry{public: true}
	for name, keyed := range map[string]bool{"keyed": true, "plain": false} {
		rec := httptest.NewRecorder()
		res := sdk.HTTPResponse{Body: "x", Headers: map[string]string{"Cache-Control": "public, s-maxage=600"}}
		g.writeRouteResponse(rec, httptest.NewRequest("GET", "/x", nil), res, sdk.Manifest{Name: "capa"}, keyed)
		got := rec.Header().Get("Cache-Control")
		if keyed && got != "no-store" || !keyed && got != "public, s-maxage=600" {
			t.Errorf("%s: Cache-Control = %q", name, got)
		}
		if rec.Header().Get("Vary") != "" {
			t.Errorf("%s: Vary = %q, want none (no cookies)", name, rec.Header().Get("Vary"))
		}
	}
}

func TestClientKeyRevoke(t *testing.T) {
	r := keyRig(t)
	r.load(t, withKeys)
	r.grant(t, sdk.PermClientKey)
	if got, _ := serveKeys(t, r, "capa", "203.0.113.7"); got.AddrKey == "" {
		t.Fatal("granted plugin got no keys")
	}
	r.grant(t) // revoke everything
	if got, _ := serveKeys(t, r, "capa", "203.0.113.7"); got.AddrKey != "" {
		t.Errorf("revoked plugin still gets keys: %+v", got)
	}
}

// A plugin holding client.key is never shared-cacheable, even for a request
// that carries no address (and so no key).
func TestClientKeyGrantMarksRoutePrivate(t *testing.T) {
	r := keyRig(t)
	r.load(t, func(m *sdk.Manifest) {
		withKeys(m)
		m.Cookies = nil // cookies alone make a route private
		m.Routes = append(m.Routes, sdk.Route{Method: "GET", Path: "/cache", Public: true})
	})
	cc := func() string {
		rec := httptest.NewRecorder()
		r.rt.PublicRoutes().Serve(rec, httptest.NewRequest("GET", "/p/capa/cache", nil), "capa", "cache", nil)
		return rec.Header().Get("Cache-Control")
	}
	if got := cc(); got != "public, s-maxage=600" {
		t.Fatalf("not granted: Cache-Control = %q", got)
	}
	r.grant(t, sdk.PermClientKey)
	if got := cc(); got != "no-store" {
		t.Errorf("granted, no address: Cache-Control = %q, want no-store", got)
	}
}
