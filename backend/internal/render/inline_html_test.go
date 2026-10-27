package render

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

func inlineNodes(h, t string, bound bool) *builder.Doc {
	mk := func(id, kind string) *builder.Node {
		n := &builder.Node{ID: id, T: kind, P: map[string]any{"text": t, "html": h}}
		if bound {
			n.B = map[string]string{"text": "entity.name"}
		}
		return n
	}
	return &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "row", T: "container", C: []*builder.Node{mk("h", "heading"), mk("t", "text")}}}}
}

func TestInlineHTMLRender(t *testing.T) {
	const evil = `Hi <strong>there</strong><script>alert(1)</script><a href="javascript:x" onclick=y>l</a><a href="/x" target="_blank">m</a>`
	const clean = `Hi <strong>there</strong>l<a href="/x" target="_blank" rel="noopener noreferrer">m</a>`
	pub := renderDoc(t, nil, inlineNodes(evil, "plain <i>", false), false)
	if strings.Count(pub, clean) != 2 {
		t.Errorf("html not rendered sanitised in heading and text:\n%s", pub)
	}
	for _, bad := range []string{"alert(1)", "onclick", "javascript:", "plain"} {
		if strings.Contains(pub, bad) {
			t.Errorf("public output has %q", bad)
		}
	}
	can := renderDoc(t, nil, inlineNodes(evil, "x", false), true)
	if strings.Count(can, `data-hm-prop="html"`) != 2 || strings.Contains(can, `data-hm-prop="text"`) {
		t.Errorf("canvas should mark html on heading and text:\n%s", can)
	}
}

func TestInlineHTMLBoundTextWins(t *testing.T) {
	d := inlineNodes("<strong>html</strong>", "", true)
	out := renderDoc(t, nil, d, false)
	if strings.Contains(out, "<strong>html</strong>") {
		t.Errorf("html rendered over a bound text:\n%s", out)
	}
	if can := renderDoc(t, nil, d, true); strings.Contains(can, `data-hm-prop=`) {
		t.Errorf("bound text is not inline editable:\n%s", can)
	}
}

func TestInlineHTMLUnsetKeepsPlain(t *testing.T) {
	d := inlineNodes("", "a <b> & c", false)
	pub := renderDoc(t, nil, d, false)
	if strings.Count(pub, "a &lt;b&gt; &amp; c") != 2 {
		t.Errorf("plain text not escaped:\n%s", pub)
	}
	if can := renderDoc(t, nil, d, true); strings.Count(can, `data-hm-prop="text"`) != 2 {
		t.Errorf("canvas should keep data-hm-prop=text:\n%s", can)
	}
}

// A sanitiser result of "" renders the node empty rather than falling back to text.
func TestInlineHTMLEmptyAfterSanitise(t *testing.T) {
	out := renderDoc(t, nil, inlineNodes("<script>x</script>", "shown?", false), false)
	if strings.Contains(out, "shown?") {
		t.Errorf("fell back to text:\n%s", out)
	}
}
