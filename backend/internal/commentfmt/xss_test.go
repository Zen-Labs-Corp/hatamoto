package commentfmt

import (
	_ "embed"
	"net/url"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

//go:embed testdata/xss_corpus.txt
var xssCorpus string

func TestXSSCorpus(t *testing.T) {
	t.Parallel()
	allowed := map[atom.Atom]bool{
		atom.P: true, atom.Br: true, atom.Em: true, atom.Strong: true, atom.Code: true,
		atom.Pre: true, atom.Blockquote: true, atom.A: true,
	}
	payloads := 0
	for _, line := range strings.Split(xssCorpus, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		payloads++
		res, err := Render(line)
		if err != nil {
			if err != ErrEmpty {
				t.Errorf("Render(%q): %v", line, err)
			}
			continue
		}
		div := &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div}
		nodes, err := xhtml.ParseFragment(strings.NewReader(res.HTML), div)
		if err != nil {
			t.Errorf("%q: output does not parse: %v", line, err)
			continue
		}
		for _, n := range nodes {
			checkNode(t, line, res.HTML, n, allowed)
		}
		if again, _ := Render(line); again.HTML != res.HTML {
			t.Errorf("%q: Render is not deterministic", line)
		}
		// Sanitising the output again must change nothing.
		if sanitise(res.HTML) != res.HTML {
			t.Errorf("%q: output is not canonical: %s", line, res.HTML)
		}
	}
	if payloads < 60 {
		t.Fatalf("corpus has %d payloads, want at least 60", payloads)
	}
}

// checkNode fails on any element outside the allowlist, any attribute other
// than a link's href and rel, and any non-http(s) href. It inspects parsed
// nodes, since escaped text may legitimately show "&lt;script".
func checkNode(t *testing.T, in, out string, n *xhtml.Node, allowed map[atom.Atom]bool) {
	t.Helper()
	if n.Type == xhtml.ElementNode {
		if !allowed[n.DataAtom] {
			t.Errorf("%q: element <%s> in %s", in, n.Data, out)
		}
		if n.DataAtom == atom.A {
			if len(n.Attr) != 2 {
				t.Errorf("%q: <a> attrs %v in %s", in, n.Attr, out)
			}
			for _, a := range n.Attr {
				switch a.Key {
				case "rel":
					if a.Val != Rel {
						t.Errorf("%q: rel %q in %s", in, a.Val, out)
					}
				case "href":
					u, err := url.Parse(a.Val)
					if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
						t.Errorf("%q: href %q in %s", in, a.Val, out)
					}
				default:
					t.Errorf("%q: attribute %q on <a> in %s", in, a.Key, out)
				}
			}
		} else if len(n.Attr) > 0 {
			t.Errorf("%q: attributes %v on <%s> in %s", in, n.Attr, n.Data, out)
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		checkNode(t, in, out, c, allowed)
	}
}
