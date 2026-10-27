package entity

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

func widgetHTMLDoc(t *testing.T, kind, html string) *builder.Doc {
	t.Helper()
	return &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "root", T: "container", C: []*builder.Node{
		{ID: "w1", T: kind, P: map[string]any{"html": html}},
	}}}}
}

func TestSanitizeDocComponentsWidgetHTML(t *testing.T) {
	vectors := []string{
		`<script>alert(1)</script>`, `<img src=x onerror=alert(1)>`, `<a href="javascript:alert(1)">x</a>`,
		`<iframe src="https://evil.example/x"></iframe>`,
	}
	for _, kind := range []string{"richtext", "html"} {
		for _, v := range vectors {
			d := widgetHTMLDoc(t, kind, "<p>ok</p>"+v)
			if !SanitizeDocComponents(d) {
				t.Errorf("%s %q: reported no change", kind, v)
			}
			got, _ := d.Nodes[0].C[0].P["html"].(string)
			for _, bad := range []string{"<script", "onerror", "javascript:", "<iframe", "evil.example"} {
				if strings.Contains(got, bad) {
					t.Errorf("%s %q: kept %q in %q", kind, v, bad, got)
				}
			}
			if !strings.Contains(got, "<p>ok</p>") {
				t.Errorf("%s: lost safe markup: %q", kind, got)
			}
		}
	}
}

// A value the stored doc already holds is left alone, like component content.
func TestSanitizeDocComponentsWidgetHTMLKeepsStored(t *testing.T) {
	stored := `<p>old</p><script>x</script>`
	d := widgetHTMLDoc(t, "richtext", stored)
	if SanitizeDocComponents(d, widgetHTMLDoc(t, "richtext", stored)) {
		t.Error("an unchanged value was rewritten")
	}
	if got := d.Nodes[0].C[0].P["html"]; got != stored {
		t.Errorf("got %v", got)
	}
	// A different kind with the same id is not the same node.
	if !SanitizeDocComponents(widgetHTMLDoc(t, "html", stored), widgetHTMLDoc(t, "richtext", stored)) {
		t.Error("a node of another kind was trusted")
	}
}

func TestSanitizeDocComponentsInlineHTML(t *testing.T) {
	for _, kind := range []string{"heading", "text"} {
		d := widgetHTMLDoc(t, kind, `<strong>ok</strong><script>alert(1)</script><a href="javascript:x" onclick=y>l</a><img src=x onerror=z>`)
		if !SanitizeDocComponents(d) {
			t.Errorf("%s: reported no change", kind)
		}
		if got, _ := d.Nodes[0].C[0].P["html"].(string); got != "<strong>ok</strong>l" {
			t.Errorf("%s: got %q", kind, got)
		}
		stored := `<em>x</em><script>1</script>`
		if SanitizeDocComponents(widgetHTMLDoc(t, kind, stored), widgetHTMLDoc(t, kind, stored)) {
			t.Errorf("%s: a value equal to the stored one must be left alone", kind)
		}
	}
}
