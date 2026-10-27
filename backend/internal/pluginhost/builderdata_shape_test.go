package pluginhost

import (
	"encoding/json"
	"fmt"
	"testing"
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
	raw, _ := json.Marshal(map[string]any{"requests": requests, "results": results})
	if err := builderDataShape.check(raw); err != nil {
		t.Fatalf("a full batch is refused: %v", err)
	}
}
