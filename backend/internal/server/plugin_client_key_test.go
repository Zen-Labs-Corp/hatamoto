package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/clientkey"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/statichost"
	"hatamoto.dev/sdk"
)

func TestPluginClientKeysNeedSecret(t *testing.T) {
	if pluginClientKeys(nil) != nil {
		t.Error("nil config must give no deriver")
	}
	if pluginClientKeys(&config.Config{}) != nil {
		t.Error("empty secret must give no deriver")
	}
	if pluginClientKeys(&config.Config{Secret: "s"}) == nil {
		t.Error("a secret must give a deriver")
	}
}

// keyServer is capa with a deriver and the client.key grant, serving /keys.
func keyServer(t *testing.T) (http.Handler, *Server, *pluginhost.Runtime) {
	t.Helper()
	h, s := bootRoutes(t)
	rt := capaRuntimeWith(t, s, func(m *sdk.Manifest) {
		m.Permissions = append(m.Permissions, sdk.PermClientKey)
		m.Routes = append(m.Routes, sdk.Route{Method: "GET", Path: "/keys", Public: true})
	})
	if _, err := rt.Grant(context.Background(), "capa", []sdk.Permission{sdk.PermClientKey}); err != nil {
		t.Fatal(err)
	}
	return h, s, rt
}

// Synthetic renders (static export, page preload, smoke pass) have no visitor:
// a client.key plugin gets no keys from them, a live request does.
func TestSyntheticRenderGetsNoClientKey(t *testing.T) {
	f := newRenderFixture(t)
	_, s, rt := keyServer(t)
	f.h.pageFilter = pluginhost.NewPageFilter(rt, s.hooks)
	f.next = membersPage
	key, _ := clientkey.New("test-secret").For("capa", "192.0.2.1")

	if body := f.get("/echo-keys").Body.String(); !strings.Contains(body, key) {
		t.Fatalf("a live request did not reach the plugin with keys:\n%s", body)
	}
	for name, mark := range map[string]func(context.Context) context.Context{
		"export":  statichost.WithExport,
		"preload": withRefresh,
		"smoke":   smokeContext,
	} {
		w := f.get("/echo-keys", func(r *http.Request) { *r = *r.WithContext(mark(r.Context())) })
		if strings.Contains(w.Body.String(), key) {
			t.Errorf("%s render gave the plugin a visitor key", name)
		}
	}
}

// A request through the trusted proxy reaches a client.key plugin with keys
// derived from the forwarded visitor, on a public plugin route and on a
// filtered page render.
func TestClientKeysReachPluginThroughProxy(t *testing.T) {
	h, s, rt := keyServer(t)
	want, _ := clientkey.New("test-secret").For("capa", "203.0.113.7")
	proxied := func(r *http.Request) {
		r.RemoteAddr = "127.0.0.1:5000"
		r.Header.Set("X-Forwarded-For", "203.0.113.7")
	}

	req := httptest.NewRequest("GET", "/p/capa/keys", nil)
	proxied(req)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) {
		t.Errorf("public route: %d %s, want key %s", rec.Code, rec.Body, want)
	}

	f := newRenderFixture(t)
	f.h.pageFilter = pluginhost.NewPageFilter(rt, s.hooks)
	f.next = membersPage
	if body := f.get("/echo-keys", proxied).Body.String(); !strings.Contains(body, want) {
		t.Errorf("filtered page render has no key %s:\n%s", want, body)
	}
}
