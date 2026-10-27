package packages

import (
	"testing"

	"hatamoto.dev/core/internal/builder"
)

// The widget pack example packs, parses and passes the production validators
// with room to spare under every size limit.
func TestWidgetPackExampleValidates(t *testing.T) {
	raw, err := PackDir("../../../examples/plugins/widgetpack-demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(raw, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != KindPlugin || p.Name != "widgetpack-demo" {
		t.Fatalf("package = %s %s", p.Kind, p.Name)
	}
	if err := validateWidgets(p); err != nil {
		t.Fatal(err)
	}
	ws, err := p.Widgets()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"widgetpack-demo_promo": false, "widgetpack-demo_pricing": false}
	for _, w := range ws.Items {
		if _, ok := want[w.Kind]; !ok {
			t.Errorf("unexpected widget %s", w.Kind)
		}
		want[w.Kind] = true
		if !builder.IsPackKind(w.Kind) {
			t.Errorf("%s is not a pack kind", w.Kind)
		}
		// at most half of each limit: the example is a model, not a stress test
		for name, f := range map[string]struct {
			body  []byte
			limit int
		}{"schema": {w.Schema, builder.MaxPackSchemaBytes}, "html": {w.HTML, 16 << 10}, "css": {w.CSS, 16 << 10}, "island": {w.Island, 32 << 10}} {
			if len(f.body) > f.limit/2 {
				t.Errorf("%s %s is %d bytes, over half of the %d limit", w.Kind, name, len(f.body), f.limit)
			}
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("widget %s missing", k)
		}
	}
}
