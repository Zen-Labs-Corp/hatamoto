package pluginhost

import (
	"fmt"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
)

// The largest answer the builder.data contract allows (16 requests of 48
// items with 16 attrs each) must pass the shape scan.
func TestBuilderDataShapeFitsAFullBatch(t *testing.T) {
	attrs := map[string]any{}
	for i := 0; i < 16; i++ {
		attrs[fmt.Sprintf("a%d", i)] = "value"
	}
	item := map[string]any{"title": "t", "url": "/u", "image": "/i", "date": "2026-01-02", "attrs": attrs}
	items := make([]any, 48)
	for i := range items {
		items[i] = item
	}
	var results, requests []any
	for i := 0; i < 16; i++ {
		id := fmt.Sprintf("r%d", i)
		requests = append(requests, map[string]any{"id": id, "source": "ext.p.s", "limit": 48})
		results = append(results, map[string]any{"id": id, "ok": true, "items": items})
	}
	raw, _ := jsonx.MarshalPinned(map[string]any{"requests": requests, "results": results})
	if err := builderDataShape.check(raw); err != nil {
		t.Fatalf("a full batch is refused: %v", err)
	}
}

// A request without params marshals exactly as before 0.20 added them.
func TestBuilderDataQueryLegacyShape(t *testing.T) {
	for _, params := range []map[string]any{nil, {}} {
		b, err := jsonx.MarshalPinned(sdk.BuilderDataQuery{ID: "i:ext.p.s:3", Source: "ext.p.s", Limit: 3, Params: params})
		if err != nil {
			t.Fatal(err)
		}
		if want := `{"id":"i:ext.p.s:3","source":"ext.p.s","limit":3}`; string(b) != want {
			t.Errorf("params=%#v: %s, want %s", params, b, want)
		}
	}
}

// The largest request batch (16 requests of 8 full-size params) still fits
// the shape scan.
func TestBuilderDataShapeFitsParamsBatch(t *testing.T) {
	params := map[string]any{}
	for i := 0; i < sdk.MaxBuilderParams; i++ {
		params[fmt.Sprintf("p%d", i)] = strings.Repeat("v", sdk.MaxBuilderParamValue)
	}
	in := sdk.BuilderDataRequest{Path: "/", Language: "en"}
	for i := 0; i < sdk.MaxBuilderDataRequests; i++ {
		in.Requests = append(in.Requests, sdk.BuilderDataQuery{ID: fmt.Sprintf("i:ext.p.s:48:p%016d", i), Source: "ext.p.s", Limit: 48, Params: params})
	}
	raw, err := jsonx.MarshalPinned(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := builderDataShape.check(raw); err != nil {
		t.Fatalf("a full params batch is refused: %v", err)
	}
}
