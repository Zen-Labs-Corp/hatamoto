package pluginhost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/pluginkey"
	"hatamoto.dev/sdk"
)

const testKeySecret = "test-secret"

// commenterKeyRig is a capa rig configured with commenter keys; capa asks for
// comments.read and is granted it when granted is true.
func commenterKeyRig(t *testing.T, granted bool) *capaRig {
	t.Helper()
	r := newCapaRig(t)
	r.rt.Configure(Config{Services: Services{Entities: r.svc, Fetcher: NewFetcher()}, Scheduler: r.sched,
		CommenterKey: pluginkey.New(testKeySecret).Commenter})
	r.load(t, func(m *sdk.Manifest) { m.Permissions = append(m.Permissions, sdk.PermCommentsRead) })
	if granted {
		r.grant(t, sdk.PermCommentsRead)
	}
	return r
}

func TestRenderPageCommenterKey(t *testing.T) {
	id := uuid.New()
	want := pluginkey.New(testKeySecret).Commenter("capa", id)
	seen := func(r *capaRig, ctx context.Context) (string, sdk.PageContext) {
		out, _ := r.engine.Apply(ctx, sdk.RenderPage.Name, sdk.PageContext{Path: "/echo-key", Blocks: pathsBlocks()}).(sdk.PageContext)
		if len(out.Blocks) != 1 {
			t.Fatalf("blocks = %+v", out.Blocks)
		}
		return out.Blocks[0].Title, out
	}
	resolver := func(calls *atomic.Int32) context.Context {
		return pluginkey.WithCommenterResolver(context.Background(), func(context.Context) (uuid.UUID, bool) {
			calls.Add(1)
			return id, true
		})
	}

	var calls atomic.Int32
	r := commenterKeyRig(t, true)
	got, out := seen(r, resolver(&calls))
	if got != want || want == "" {
		t.Errorf("granted plugin saw key %q, want %q", got, want)
	}
	if out.CommenterKey != "" {
		t.Errorf("the chain value carries commenter_key %q: the plugin's echo must be dropped", out.CommenterKey)
	}
	if got, _ := seen(r, context.Background()); got != "" {
		t.Errorf("no resolver: key %q", got)
	}

	calls.Store(0)
	if got, _ := seen(commenterKeyRig(t, false), resolver(&calls)); got != "" || calls.Load() != 0 {
		t.Errorf("ungranted: key %q, resolver calls %d; want empty and none", got, calls.Load())
	}
}

// recordingCaller is a route Caller that answers with the request it was given.
func recordingCaller(seen *sdk.HTTPRequest) Caller {
	return func(_ context.Context, _, _ string, in []byte) ([]byte, error) {
		if err := jsonx.UnmarshalPinned(in, seen); err != nil {
			return nil, err
		}
		return []byte(`{"status":200}`), nil
	}
}

func TestPublicRouteCommenterKey(t *testing.T) {
	m := &sdk.Manifest{Name: "capa", Routes: []sdk.Route{{Method: "GET", Path: "/k", Public: true}, {Method: "GET", Path: "/a"}}}
	keyFor := func(_ context.Context, plugin string) string { return "key-" + plugin }
	serve := func(public bool, rest string) sdk.HTTPRequest {
		var seen sdk.HTTPRequest
		g := NewRouteRegistry(recordingCaller(&seen), public, nil)
		g.keyFor = keyFor
		g.Set("capa", m)
		user := &sdk.HTTPUser{}
		if public {
			user = nil
		}
		g.Serve(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil), "capa", rest, user)
		return seen
	}
	if got := serve(true, "k").CommenterKey; got != "key-capa" {
		t.Errorf("public route: commenter_key = %q", got)
	}
	if got := serve(false, "a").CommenterKey; got != "" {
		t.Errorf("admin route: commenter_key = %q, want none", got)
	}

	// The runtime wires the public registry only, and only for a granted plugin.
	r := commenterKeyRig(t, true)
	if r.rt.PublicRoutes().keyFor == nil || r.rt.AdminRoutes().keyFor != nil {
		t.Error("keyFor must be set on the public registry and not on the admin one")
	}
}

// A public response built from a commenter key is per visitor: never
// shared-cacheable. Without a key the plugin's own caching stands.
func TestPublicRouteCommenterKeyNotShared(t *testing.T) {
	m := &sdk.Manifest{Name: "capa", Routes: []sdk.Route{{Method: "GET", Path: "/k", Public: true}}}
	call := func(context.Context, string, string, []byte) ([]byte, error) {
		return jsonx.MarshalPinned(sdk.HTTPResponse{Body: "x", Headers: map[string]string{"Cache-Control": "public, s-maxage=600"}})
	}
	cc := func(key string) string {
		g := NewRouteRegistry(call, true, nil)
		g.keyFor = func(context.Context, string) string { return key }
		g.Set("capa", m)
		rec := httptest.NewRecorder()
		g.Serve(rec, httptest.NewRequest("GET", "/p/capa/k", nil), "capa", "k", nil)
		return rec.Header().Get("Cache-Control")
	}
	if got := cc("key-capa"); got != "no-store" {
		t.Errorf("with commenter key: Cache-Control = %q, want no-store", got)
	}
	if got := cc(""); got != "public, s-maxage=600" {
		t.Errorf("without commenter key: Cache-Control = %q, want the plugin's own", got)
	}
}
