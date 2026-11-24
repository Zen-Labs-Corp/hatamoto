package server

import (
	"context"
	"net/http"
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
	rt.Configure(pluginhost.Config{ClientKeys: clientkey.New("test-secret")})
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
