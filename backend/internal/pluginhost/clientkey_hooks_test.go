package pluginhost

import (
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/clientkey"
	"hatamoto.dev/sdk"
)

func echoKeys(t *testing.T, pf *PageFilter, ctx context.Context) (title string, chain sdk.PageContext) {
	t.Helper()
	out := pf.Apply(ctx, sdk.PageContext{Path: "/echo-keys"})
	for _, b := range out.Blocks {
		if b.ID == "keys" {
			title = b.Title
		}
	}
	return title, out
}

func TestClientKeyRenderPage(t *testing.T) {
	r := keyRig(t)
	r.load(t, withKeys)
	pf := NewPageFilter(r.rt, r.engine)
	ctx := clientkey.WithIP(context.Background(), "203.0.113.7")

	if got, _ := echoKeys(t, pf, ctx); got != "|" {
		t.Errorf("not granted: %q", got)
	}
	r.grant(t, sdk.PermClientKey)
	got, _ := echoKeys(t, pf, ctx)
	a, n, ok := strings.Cut(got, "|")
	if !ok || a == "" || n == "" {
		t.Fatalf("granted: %q", got)
	}
	if got, _ := echoKeys(t, pf, context.Background()); got != "|" {
		t.Errorf("no address in ctx: %q", got)
	}
	if got, _ := echoKeys(t, pf, clientkey.WithIP(context.Background(), "not an ip")); got != "|" {
		t.Errorf("unparsable address: %q", got)
	}
}

// A plugin that was not granted sees no key, even right after one that was,
// and the chain value never carries a key.
func TestClientKeyRenderPageNoLeak(t *testing.T) {
	r := keyRig(t)
	r.loadAs(t, "capa", withKeys)
	r.loadAs(t, "capb", withKeys)
	r.grant(t, sdk.PermClientKey) // capa only
	pf := NewPageFilter(r.rt, r.engine)
	ctx := clientkey.WithIP(context.Background(), "203.0.113.7")

	got, chain := echoKeys(t, pf, ctx)
	if got != "|" { // capb ran last and replaced the block with what it saw
		t.Errorf("a non-granted plugin saw keys: %q", got)
	}
	if chain.AddrKey != "" || chain.NetKey != "" {
		t.Errorf("chain value carries keys: %+v", chain)
	}
}

func TestClientKeyCommentCheck(t *testing.T) {
	ctx := clientkey.WithIP(context.Background(), "203.0.113.7")
	reasons := func(r *commentRig) (string, sdk.CommentCheck) {
		t.Helper()
		got, err := r.check(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(got.Reasons, " "), got
	}

	r := newCommentRig(t)
	r.rt.caps.keys = clientkey.New("test-secret")
	r.load(t, "keys", true) // comments.read only
	if got, _ := reasons(r); strings.Contains(got, "addr_key") || strings.Contains(got, "area_key") {
		t.Errorf("without client.key: %s", got)
	}
	r.grant(t, sdk.PermCommentsRead, sdk.PermClientKey)
	got, chain := reasons(r)
	if !strings.Contains(got, "addr_key") || !strings.Contains(got, "area_key") {
		t.Errorf("with client.key: %s", got)
	}
	if chain.AddrKey != "" || chain.AreaKey != "" {
		t.Errorf("chain value carries keys: %+v", chain)
	}
	// No client address (a background path): no keys, no error.
	if out, err := r.check(context.Background()); err != nil || strings.Contains(strings.Join(out.Reasons, " "), "addr_key") {
		t.Errorf("no address: %v %v", out.Reasons, err)
	}
}
