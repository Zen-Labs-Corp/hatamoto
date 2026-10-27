package render

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"hatamoto.dev/core/internal/entity"
)

// Cross-language golden for the canvas text walk. The browser walk
// (web/admin/js/canvas, html-text) must agree with it; docs/QA.md "Browser
// modules" says how to check that, and the same files feed the optional
// out-of-repo Playwright harness (docs/ROADMAP.md, v1-15). Generate with
// `go test ./internal/render -run CanvasTextGolden -update`.

const canvasTextDir = "testdata/canvas-text"

// editValue exercises escaping: markup, ampersand, quotes, unicode.
const editValue = `Edited <b>&</b> "q" 'q' 日本 😀`

type textEdit struct {
	Index      int        `json:"index"`
	Expect     string     `json:"expect"`
	Value      string     `json:"value"`
	NodesAfter []TextNode `json:"nodesAfter"`
}

type textReject struct {
	Index  int    `json:"index"`
	Expect string `json:"expect"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

type textGolden struct {
	Nodes   []TextNode   `json:"nodes"`
	Edits   []textEdit   `json:"edits"`
	Rejects []textReject `json:"rejects"`
}

func buildTextGolden(t *testing.T, body string) textGolden {
	t.Helper()
	nodes, err := EditableTextNodes(body)
	if err != nil {
		t.Fatal(err)
	}
	g := textGolden{Nodes: nodes, Edits: []textEdit{}, Rejects: []textReject{}}
	if len(nodes) == 0 {
		t.Fatal("fixture has no editable text")
	}
	seen := map[int]bool{}
	for _, i := range []int{0, len(nodes) / 2, len(nodes) - 1} {
		if seen[i] {
			continue
		}
		seen[i] = true
		res, err := ReplaceTextNode(body, i, nodes[i].Text, editValue)
		if err != nil {
			t.Fatal(err)
		}
		after, err := EditableTextNodes(res)
		if err != nil {
			t.Fatal(err)
		}
		g.Edits = append(g.Edits, textEdit{Index: i, Expect: nodes[i].Text, Value: editValue, NodesAfter: after})
	}
	g.Rejects = []textReject{
		{Index: 0, Expect: "wrong", Value: "x", Reason: "stale"},
		{Index: 999, Expect: "", Value: "x", Reason: "range"},
		{Index: 0, Expect: nodes[0].Text, Value: "  ", Reason: "blank"},
	}
	return g
}

func writeOrRead(t *testing.T, path string, got []byte) []byte {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	return want
}

func TestCanvasTextGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(canvasTextDir, "*.html"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, ".stamped.html") {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(f), ".html")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			body := string(raw)

			// (a) real bodies are sanitiser fixed points; edge.html is a
			// synthetic stress body the sanitiser is allowed to rewrite.
			synthetic := name == "edge"
			if !synthetic {
				once := entity.SanitizeHTMLSectionBody(body)
				if once != body || entity.SanitizeHTMLSectionBody(once) != once {
					t.Fatalf("%s is not a sanitiser fixed point", name)
				}
			}

			// (b) node list matches the golden.
			gotG := buildTextGolden(t, body)
			enc, err := json.MarshalIndent(gotG, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			enc = append(enc, '\n')
			gpath := filepath.Join(canvasTextDir, name+".golden.json")
			want := writeOrRead(t, gpath, enc)
			var wantG textGolden
			if err := json.Unmarshal(want, &wantG); err != nil {
				t.Fatal(err)
			}
			nodes, _ := EditableTextNodes(body)
			if !reflect.DeepEqual(nodes, wantG.Nodes) {
				t.Fatalf("nodes differ from golden")
			}

			// (c) edits: replace -> walk equals nodesAfter; sanitising keeps
			// the same node list and is idempotent.
			for _, e := range wantG.Edits {
				res, err := ReplaceTextNode(body, e.Index, e.Expect, e.Value)
				if err != nil {
					t.Fatalf("edit %d: %v", e.Index, err)
				}
				after, _ := EditableTextNodes(res)
				if !reflect.DeepEqual(after, e.NodesAfter) {
					t.Fatalf("edit %d: nodesAfter mismatch", e.Index)
				}
				if synthetic {
					continue
				}
				san := entity.SanitizeHTMLSectionBody(res)
				sanNodes, _ := EditableTextNodes(san)
				if !reflect.DeepEqual(sanNodes, e.NodesAfter) {
					t.Fatalf("edit %d: sanitiser changed node list", e.Index)
				}
				if entity.SanitizeHTMLSectionBody(san) != san {
					t.Fatalf("edit %d: sanitiser not idempotent", e.Index)
				}
			}

			// (d) rejects.
			reasons := map[string]error{"stale": ErrStaleText, "range": ErrRange, "blank": ErrBlank}
			for _, r := range wantG.Rejects {
				if _, err := ReplaceTextNode(body, r.Index, r.Expect, r.Value); !errors.Is(err, reasons[r.Reason]) {
					t.Fatalf("reject %s: got %v", r.Reason, err)
				}
			}

			// (e) stamped output addresses every walk node, in order.
			stamped := StampEditableText(body, "body")
			// A fixture opts in to a stamped golden by having a .stamped.html file.
			sp := filepath.Join(canvasTextDir, name+".stamped.html")
			if _, err := os.Stat(sp); err == nil {
				if w := writeOrRead(t, sp, []byte(stamped)); string(w) != stamped {
					t.Fatalf("stamped output differs from %s", sp)
				}
			}
			checkStamped(t, stamped, nodes)
		})
	}
}

func checkStamped(t *testing.T, stamped string, nodes []TextNode) {
	t.Helper()
	root, err := parseDivFragment(stamped)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	var gotText []string
	var visit func(n *html.Node)
	visit = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			for _, a := range c.Attr {
				if a.Key == "data-hm-text" {
					got = append(got, a.Val)
					gotText = append(gotText, textOf(c))
				}
			}
			visit(c)
		}
	}
	visit(root)
	if len(got) != len(nodes) {
		t.Fatalf("stamped %d nodes, walk has %d", len(got), len(nodes))
	}
	for i, n := range nodes {
		if got[i] != "body#"+strconv.Itoa(i) {
			t.Fatalf("stamp %d = %q", i, got[i])
		}
		if gotText[i] != n.Text {
			t.Fatalf("stamp %d text %q != walk %q", i, gotText[i], n.Text)
		}
	}
	// Wrapping changes parent tags but must not change the text sequence.
	after, err := EditableTextNodes(stamped)
	if err != nil || len(after) != len(nodes) {
		t.Fatalf("stamping changed the walk length: %v", err)
	}
	for i := range nodes {
		if after[i].Text != nodes[i].Text {
			t.Fatalf("stamping changed text %d", i)
		}
	}
}

func textOf(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		} else if c.Type == html.ElementNode {
			b.WriteString(textOf(c))
		}
	}
	return b.String()
}
