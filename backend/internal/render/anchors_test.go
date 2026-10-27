package render

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

func container(id string, p map[string]any, c ...*builder.Node) *builder.Node {
	return &builder.Node{ID: id, T: "container", P: p, C: c}
}

func TestAnchorRendersAsID(t *testing.T) {
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{
		container("box", map[string]any{"anchor": "pricing", "tag": "section", "aria": `Plans "all"`, "tip": "Compare"}),
		{ID: "go", T: "button", P: map[string]any{"text": "Buy", "href": "#pricing", "aria": "Buy now", "tip": "Go"}},
	}}
	out := renderDoc(t, nil, d, false)
	if !strings.Contains(out, `<section class="hm-n hm-c hm-n-box" id="pricing" aria-label="Plans &#34;all&#34;" title="Compare">`) {
		t.Errorf("container attributes missing:\n%s", out)
	}
	if !strings.Contains(out, `class="hm-n hm-btn hm-w-btn hm-n-go" aria-label="Buy now" title="Go" href="#pricing"`) {
		t.Errorf("button attributes missing:\n%s", out)
	}
}

func TestAnchorUnsafeOrReservedIsDropped(t *testing.T) {
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{
		container("a", map[string]any{"anchor": `x" onclick="y`}),
		container("b", map[string]any{"anchor": "main-content"}),
		container("c", map[string]any{"anchor": "hm-x"}),
		container("d", map[string]any{"anchor": "Upper"}),
	}}
	out := renderDoc(t, nil, d, false)
	for _, bad := range []string{"onclick", `hm-n-b" id=`, `hm-n-c" id=`, `hm-n-d" id=`} {
		if strings.Contains(out, bad) {
			t.Errorf("page carries %q", bad)
		}
	}
}

func TestNoElementSettingsOnSpacer(t *testing.T) {
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "s", T: "spacer", P: map[string]any{"anchor": "x", "aria": "y"}}}}
	out := renderDoc(t, nil, d, false)
	if strings.Contains(out, `id="x"`) || strings.Contains(out, `hm-n-s" aria-label`) {
		t.Errorf("spacer rendered element settings:\n%s", out)
	}
}

// The same anchor in a block used twice and on the page renders its id once:
// the first rendered wins, later ones are dropped (a ref's nodes render without canvas markers, so the drop is silent there).
func TestAnchorDuplicateAcrossRefsRendersOnce(t *testing.T) {
	blockID := "11111111-2222-3333-4444-555555555555"
	src := fakeRefs{blockID: &builder.Doc{V: 2, Nodes: []*builder.Node{container("in", map[string]any{"anchor": "shared"})}}}
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{
		container("page", map[string]any{"anchor": "shared"}),
		refNode("r1", blockID),
		refNode("r2", blockID),
	}}
	for _, canvas := range []bool{false, true} {
		e, err := New(Options{Refs: src})
		if err != nil {
			t.Fatal(err)
		}
		v := minimalView()
		v.Doc, v.Canvas = d, canvas
		var sb strings.Builder
		if err := e.RenderPage(&sb, v); err != nil {
			t.Fatal(err)
		}
		out := sb.String()
		if n := strings.Count(out, ` id="shared"`); n != 1 {
			t.Errorf("canvas=%v: id=\"shared\" written %d times", canvas, n)
		}
		if !strings.Contains(out, `hm-n-page" id="shared"`) {
			t.Errorf("canvas=%v: the page's own node did not win", canvas)
		}
		// A second render of the same view starts afresh.
		sb.Reset()
		if err := e.RenderPage(&sb, v); err != nil || !strings.Contains(sb.String(), ` id="shared"`) {
			t.Errorf("canvas=%v: re-render lost the anchor (%v)", canvas, err)
		}
	}
}

// Every literal id the default theme prints is an id an anchor must not claim.
func TestThemeIDsAreReservedAnchors(t *testing.T) {
	idRe := regexp.MustCompile(`\bid="([^"]*)"`)
	root := "themes/default"
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range idRe.FindAllStringSubmatch(string(b), -1) {
			id := m[1]
			if strings.Contains(id, "{{") {
				continue // dynamic: node ids ({{$id}}, -strip-label), release versions (dotted, never an anchor)
			}
			n++
			if builder.ValidAnchor(id) {
				t.Errorf("%s prints id=%q, which an author could claim as an anchor; add it to builder.ReservedAnchors", p, id)
			}
		}
		return nil
	})
	if err != nil || n == 0 {
		t.Fatalf("scanned %d ids: %v", n, err)
	}
}

// aria-label is prohibited on a plain div and a paragraph, and the icon template writes its own: none is rendered twice or at all.
func TestAriaOnlyWhereItNamesSomething(t *testing.T) {
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{
		container("plain", map[string]any{"aria": "ignored"}),
		{ID: "para", T: "text", P: map[string]any{"text": "Hi", "aria": "ignored"}},
		{ID: "ic", T: "icon", P: map[string]any{"name": "check", "label": "Done", "aria": "ignored"}},
	}}
	out := renderDoc(t, nil, d, false)
	if strings.Contains(out, "ignored") {
		t.Errorf("a stored aria on a kind that takes none was rendered:\n%s", out)
	}
	if n := strings.Count(out, `aria-label="Done"`); n != 1 {
		t.Errorf("icon label written %d times", n)
	}
}
