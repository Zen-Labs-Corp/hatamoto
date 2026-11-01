package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

func TestSiteGraphFilterAddsKey(t *testing.T) {
	h := hooks.New(quietLog())
	var seen sdk.SiteGraph
	hooks.OnFilter(h.For("plsg"), sdk.RenderSiteGraph, 10, func(_ context.Context, g sdk.SiteGraph) (sdk.SiteGraph, error) {
		seen = g
		g.Nodes[0]["slogan"] = "added"
		return g, nil
	})
	in := render.SiteGraphInput{
		Path: "/a", Language: "en-XX", Locale: "en-XX", Segment: "xx", SchemaSlug: "page", EntityID: "e1",
		Nodes: []map[string]any{{"@id": "o", "@type": "Organization"}},
	}
	out := SiteGraphFilter(h)(context.Background(), in)
	if out.Nodes[0]["slogan"] != "added" {
		t.Errorf("nodes = %v", out.Nodes)
	}
	if seen.Path != "/a" || seen.Language != "en-XX" || seen.Locale != "en-XX" || seen.Segment != "xx" || seen.SchemaSlug != "page" || seen.EntityID != "e1" {
		t.Errorf("filter input = %+v", seen)
	}
	if out.Path != "/a" || out.Locale != "en-XX" {
		t.Errorf("the page fields must be kept: %+v", out)
	}
}

func TestSiteGraphFilterNilAndFailing(t *testing.T) {
	if SiteGraphFilter(nil) != nil {
		t.Error("a nil engine must give a nil filter")
	}
	h := hooks.New(quietLog())
	hooks.OnFilter(h.For("plsg"), sdk.RenderSiteGraph, 10, func(context.Context, sdk.SiteGraph) (sdk.SiteGraph, error) {
		return sdk.SiteGraph{}, errors.New("boom")
	})
	in := render.SiteGraphInput{Path: "/", Nodes: []map[string]any{{"@id": "o"}}}
	out := SiteGraphFilter(h)(context.Background(), in)
	if len(out.Nodes) != 1 || out.Nodes[0]["@id"] != "o" {
		t.Errorf("a failing filter must leave the graph as it was: %+v", out)
	}
}

func TestSiteGraphShapeAndOversizedOutput(t *testing.T) {
	if shapeOf(sdk.SiteGraph{}) != headShape {
		t.Fatal("render.site_graph must use the head limits")
	}
	in, _ := json.Marshal(sdk.SiteGraph{Path: "/", Nodes: []map[string]any{{"@id": "o"}}})
	big := make([]string, 65)
	for i := range big {
		big[i] = fmt.Sprintf("%q", fmt.Sprint("https://example.test/", i))
	}
	oversized := `{"nodes":[{"@id":"o","sameAs":[` + strings.Join(big, ",") + `]}]}`
	if _, err := decodeOutput(sdk.SiteGraph{}, in, []byte(oversized)); err == nil {
		t.Error("a 65-element array must be rejected")
	}
	ok := `{"nodes":[{"@id":"o","slogan":"s"}]}`
	got, err := decodeOutput(sdk.SiteGraph{}, in, []byte(ok))
	if err != nil || got.(sdk.SiteGraph).Nodes[0]["slogan"] != "s" {
		t.Errorf("a small answer = %+v, %v", got, err)
	}
	// A rejected answer fails the filter, which marks the render degraded.
	failing := func(ctx context.Context, v any) (any, error) { return decodeOutput(v, in, []byte(oversized)) }
	before := render.DegradedCount()
	if _, err := noteRenderFailure(sdk.RenderSiteGraph.Name, failing)(context.Background(), sdk.SiteGraph{}); err == nil {
		t.Fatal("want the oversized answer to fail")
	}
	if render.DegradedCount() == before {
		t.Error("an oversized answer must mark the render degraded")
	}
}
