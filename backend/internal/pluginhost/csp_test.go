package pluginhost

import (
	"context"
	"slices"
	"testing"

	"hatamoto.dev/sdk"
)

func TestCSPHostsFollowTheLoadedPlugin(t *testing.T) {
	r := newCapaRig(t)
	if !r.rt.CSPHosts(nil).Empty() {
		t.Fatal("nothing loaded, hosts listed")
	}
	r.load(t, func(m *sdk.Manifest) {
		m.CSP = &sdk.CSP{ScriptSrc: []string{"https://js.example.com"}, ConnectSrc: []string{"https://api.example.com"}, ImgSrc: []string{"https://img.example.com"}}
	})
	got := r.rt.CSPHosts(nil)
	if !slices.Equal(got.Script, []string{"https://js.example.com"}) || !slices.Equal(got.Connect, []string{"https://api.example.com"}) || !slices.Equal(got.Img, []string{"https://img.example.com"}) {
		t.Fatalf("hosts = %+v", got)
	}
	// A community plugin contributes image hosts only.
	got = r.rt.CSPHosts(func(string) bool { return true })
	if len(got.Script)+len(got.Connect)+len(got.Frame) != 0 || len(got.Img) != 1 {
		t.Fatalf("community hosts = %+v", got)
	}
	// Turning the plugin off removes its hosts.
	r.rt.Unload(context.Background(), "capa")
	if !r.rt.CSPHosts(nil).Empty() {
		t.Fatalf("hosts after unload = %+v", r.rt.CSPHosts(nil))
	}
}
