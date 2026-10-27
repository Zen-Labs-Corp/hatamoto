package render

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// FuzzScanMarkup runs the markup check shared by widget packs and plugin
// sections over arbitrary text. It must not panic, and what it accepts must
// hold no script element and no event handler attribute.
func FuzzScanMarkup(f *testing.F) {
	for _, s := range []string{
		`<p class="a">hello</p>`,
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`<a href="javascript:alert(1)">x</a>`,
		`<style>a{}</style><p style="color:red">x</p>`,
		`<form action="/x" method="post"><input name="a"></form>`,
		`<iframe src="https://example.test/"></iframe>`,
		`<svg><script>x</script></svg>`,
		`<ScRiPt >x</sCrIpT>`,
		`<<a href=`,
		``,
	} {
		f.Add(s, false, false)
		f.Add(s, true, true)
	}
	f.Fuzz(func(t *testing.T, markup string, allowForm, allowStyle bool) {
		if scanMarkup("fuzz.html", "markup", markup, markupRules{allowForm: allowForm, allowStyle: allowStyle}) != nil {
			return
		}
		// Accepted: a real HTML parser, not the scanner's tokenizer, must find
		// no script element and no event handler in it.
		nodes, err := html.ParseFragment(strings.NewReader(markup), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
		if err != nil {
			return
		}
		var walk func(n *html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode {
				if n.Data == "script" {
					t.Fatalf("accepted a script element: %q", markup)
				}
				for _, a := range n.Attr {
					if strings.HasPrefix(strings.ToLower(a.Key), "on") {
						t.Fatalf("accepted the handler %q: %q", a.Key, markup)
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		for _, n := range nodes {
			walk(n)
		}
	})
}
