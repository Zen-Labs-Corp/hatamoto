package pluginhost

import (
	"context"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func applyQuery(r *capaRig, q map[string]string) sdk.PageContext {
	out, _ := r.engine.Apply(context.Background(), sdk.RenderPage.Name, sdk.PageContext{Path: "/echo-query", Query: q}).(sdk.PageContext)
	return out
}

func blockIDs(pc sdk.PageContext) []string {
	var ids []string
	for _, b := range pc.Blocks {
		ids = append(ids, b.ID)
	}
	slices.Sort(ids)
	return ids
}

func TestPageQueryDeclaredOnly(t *testing.T) {
	r := newCapaRig(t)
	r.loadAs(t, "capa", func(m *sdk.Manifest) {
		m.PagePaths, m.PageQuery = []string{"/echo-query"}, []string{"p"}
	})
	out := applyQuery(r, map[string]string{"p": "2", "x": "1"})
	if len(out.Blocks) != 1 || out.Blocks[0].ID != "q:p" || out.Blocks[0].Title != "2" {
		t.Fatalf("blocks = %+v, want only q:p=2", out.Blocks)
	}
	if len(out.Query) != 2 {
		t.Errorf("the chain value's query changed: %v", out.Query)
	}
}

func TestPageQueryLongValueDropped(t *testing.T) {
	r := newCapaRig(t)
	r.loadAs(t, "capa", func(m *sdk.Manifest) {
		m.PagePaths, m.PageQuery = []string{"/echo-query"}, []string{"p"}
	})
	if out := applyQuery(r, map[string]string{"p": strings.Repeat("a", sdk.MaxPageQueryValueBytes+1)}); len(out.Blocks) != 0 {
		t.Fatalf("a long value reached the plugin: %+v", out.Blocks)
	}
	if out := applyQuery(r, map[string]string{"p": strings.Repeat("a", sdk.MaxPageQueryValueBytes)}); len(out.Blocks) != 1 {
		t.Fatalf("a value at the limit was dropped: %+v", out.Blocks)
	}
}

func TestPageQueryUndeclaredEmpty(t *testing.T) {
	r := newCapaRig(t)
	r.loadAs(t, "capa", func(m *sdk.Manifest) { m.PagePaths = []string{"/echo-query"} })
	if out := applyQuery(r, map[string]string{"p": "2"}); len(out.Blocks) != 0 {
		t.Fatalf("a plugin without page_query got %+v", out.Blocks)
	}
}

func TestPageQueryPerPlugin(t *testing.T) {
	r := newCapaRig(t)
	r.loadAs(t, "capa", func(m *sdk.Manifest) {
		m.PagePaths, m.PageQuery = []string{"/echo-query"}, []string{"p"}
	})
	r.loadAs(t, "capa2", func(m *sdk.Manifest) {
		m.PagePaths, m.PageQuery = []string{"/echo-query"}, []string{"day"}
	})
	if got := NewPageFilter(r.rt, r.engine).QueryKeys(); !slices.Equal(got, []string{"day", "p"}) {
		t.Fatalf("QueryKeys = %v", got)
	}
	// Each echo replaces the blocks, so what is left is what the last plugin
	// to run saw: only its own key, never the other plugin's.
	out := applyQuery(r, map[string]string{"p": "2", "day": "mon"})
	if ids := blockIDs(out); len(ids) != 1 {
		t.Fatalf("blocks = %v, want one plugin's own key", ids)
	}
}
