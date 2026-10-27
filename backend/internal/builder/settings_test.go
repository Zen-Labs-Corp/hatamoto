package builder

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func settingsDoc(ns ...*Node) *Doc { return &Doc{V: 2, Nodes: ns} }

func box(id string, p map[string]any, c ...*Node) *Node {
	return &Node{ID: id, T: "container", P: p, C: c}
}

func TestCheckSettingsAnchors(t *testing.T) {
	cases := []struct {
		name string
		doc  *Doc
		want string // "" = ok
	}{
		{"unique", settingsDoc(box("a", map[string]any{"anchor": "one"}), box("b", map[string]any{"anchor": "two"})), ""},
		{"none", settingsDoc(box("a", nil)), ""},
		{"duplicate", settingsDoc(box("a", map[string]any{"anchor": "dup"}), box("b", nil, box("c", map[string]any{"anchor": "dup"}))), `"dup"`},
		{"duplicate on a hidden node", settingsDoc(box("a", map[string]any{"anchor": "dup"}), box("b", map[string]any{"anchor": "dup", "hidden": true})), `"dup"`},
		{"reserved main-content", settingsDoc(box("a", map[string]any{"anchor": "main-content"})), `"main-content"`},
		{"reserved hm- prefix", settingsDoc(box("a", map[string]any{"anchor": "hm-x"})), `"hm-x"`},
		{"reserved strip label", settingsDoc(box("a", map[string]any{"anchor": "hero-strip-label"})), `"hero-strip-label"`},
		{"equals a node id", settingsDoc(box("pricing", nil, box("b", map[string]any{"anchor": "pricing"}))), `"pricing"`},
		{"equals a later node id", settingsDoc(box("a", map[string]any{"anchor": "zed"}), box("zed", nil)), `"zed"`},
	}
	for _, c := range cases {
		err := c.doc.CheckSettings()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want ErrInvalid naming %s", c.name, err, c.want)
		}
	}
}

func TestCheckSettingsContainerLink(t *testing.T) {
	link := map[string]any{"href": "/x"}
	text := &Node{ID: "t", T: "text", P: map[string]any{"text": "plain"}}
	cases := []struct {
		name string
		n    *Node
		want string
	}{
		{"plain children", box("a", link, text, &Node{ID: "i", T: "image", P: map[string]any{"src": "/a.jpg"}}, box("n", nil, text2("t2"))), ""},
		{"no link, button inside", box("a", nil, &Node{ID: "b", T: "button"}), ""},
		{"button inside", box("a", link, &Node{ID: "b", T: "button"}), "button b"},
		{"nested deep", box("a", link, box("n", nil, &Node{ID: "h", T: "html", P: map[string]any{"html": "<p>x</p>"}})), "html h"},
		{"ref inside", box("a", link, &Node{ID: "r", T: "ref", P: map[string]any{"block": "11111111-2222-3333-4444-555555555555"}}), "ref r"},
		{"pack kind inside", box("a", link, &Node{ID: "p", T: "acme_promo"}), "acme_promo p"},
		{"inline link in text", box("a", link, &Node{ID: "t", T: "text", P: map[string]any{"html": `see <a href="/y">this</a>`}}), "text t"},
		{"inline markup without a link", box("a", link, &Node{ID: "t", T: "text", P: map[string]any{"html": `<strong>b</strong> <abbr>x</abbr>`}}), ""},
		{"linked image inside", box("a", link, &Node{ID: "i", T: "image", P: map[string]any{"href": "/z"}}), "image i"},
		{"lightbox image inside", box("a", link, &Node{ID: "i", T: "image", P: map[string]any{"lightbox": true}}), "image i"},
		{"bound link inside", box("a", link, &Node{ID: "i", T: "image", B: map[string]string{"href": "entity.url"}}), "image i"},
		{"linked container inside", box("a", link, box("b", link)), "container b"},
		{"lightbox gallery inside", box("a", link, &Node{ID: "g", T: "gallery", P: map[string]any{"lightbox": true}}), "gallery g"},
		{"tag section", box("a", map[string]any{"href": "/x", "tag": "section"}), "div tag"},
		{"tag div", box("a", map[string]any{"href": "/x", "tag": "div"}), ""},
	}
	for _, c := range cases {
		err := settingsDoc(c.n).CheckSettings()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want ErrInvalid containing %q", c.name, err, c.want)
		}
	}
}

func text2(id string) *Node { return &Node{ID: id, T: "text", P: map[string]any{"text": "x"}} }

func TestCheckSettingsImageLinkOrLightbox(t *testing.T) {
	both := &Node{ID: "i", T: "image", P: map[string]any{"href": "/x", "lightbox": true}}
	if err := settingsDoc(both).CheckSettings(); err == nil {
		t.Error("href + lightbox accepted")
	}
	inGallery := &Node{ID: "i", T: "image", P: map[string]any{"href": "/x"}}
	g := &Node{ID: "g", T: "gallery", P: map[string]any{"lightbox": true}, C: []*Node{inGallery}}
	if err := settingsDoc(g).CheckSettings(); err == nil {
		t.Error("href inside a lightbox gallery accepted")
	}
	g.P["lightbox"] = false
	if err := settingsDoc(g).CheckSettings(); err != nil {
		t.Errorf("href inside a plain gallery: %v", err)
	}
}

// Parse stays lenient about relations: a stored doc with a duplicate anchor
// still parses (render degrades), only write paths call CheckSettings.
func TestParseDoesNotCheckSettings(t *testing.T) {
	d := settingsDoc(box("a", map[string]any{"anchor": "dup"}), box("b", map[string]any{"anchor": "dup"}))
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// Every core widget whose template prints interactive markup must be in
// interactiveKinds, so a new widget cannot slip into a linked container.
func TestWidgetTemplatesClassified(t *testing.T) {
	markup := regexp.MustCompile(`(?i)<(a|button|summary|input|select|textarea|details|iframe)[\s>/]`)
	files, err := filepath.Glob("../render/widgets/*/widget.html")
	if err != nil || len(files) == 0 {
		t.Fatalf("no widget templates found: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		kind := filepath.Base(filepath.Dir(f))
		if markup.Match(b) && !interactiveKinds[kind] {
			t.Errorf("widget %s prints interactive markup but is not in interactiveKinds", kind)
		}
	}
}

func TestApplyRefusesSettingsViolation(t *testing.T) {
	d := settingsDoc(box("a", map[string]any{"anchor": "one"}), box("b", nil))
	if _, err := d.Apply(Op{Op: "setProp", ID: "b", Key: "anchor", Value: []byte(`"one"`)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate anchor via op: err = %v", err)
	}
	if d.Nodes[1].P["anchor"] != nil {
		t.Error("the refused op was not rolled back")
	}
	if _, err := d.Apply(Op{Op: "setProp", ID: "b", Key: "anchor", Value: []byte(`"two"`)}); err != nil {
		t.Fatalf("distinct anchor: %v", err)
	}
	// A doc that already breaks a rule can still be edited elsewhere.
	bad := settingsDoc(box("a", map[string]any{"anchor": "x"}), box("b", map[string]any{"anchor": "x"}), box("c", nil))
	if _, err := bad.Apply(Op{Op: "setProp", ID: "c", Key: "hidden", Value: []byte(`true`)}); err != nil {
		t.Errorf("edit on a legacy doc: %v", err)
	}
}
