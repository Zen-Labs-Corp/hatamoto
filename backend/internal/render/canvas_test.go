package render

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectCanvasBridge(t *testing.T) {
	t.Parallel()
	once := string(InjectCanvasBridge([]byte("<html><body><p>x</p></body></html>")))
	tests := []struct {
		name string
		in   string
		want func(out string) bool
	}{
		{"before last body without head", "<body>a</body><!--</body>-->", func(o string) bool {
			return strings.Index(o, `id="hm-canvas-bridge"`) > strings.Index(o, "<!--") && strings.HasSuffix(o, "</body>-->")
		}},
		{"first in head before a page script", `<html><head><script src="/p/x.js"></script></head><body></body></html>`, func(o string) bool {
			b, p := strings.Index(o, `<script id="hm-canvas-bridge">`), strings.Index(o, `<script src="/p/x.js">`)
			return strings.HasPrefix(o, "<html><head><script id=") && b >= 0 && b < p
		}},
		{"header is not head", `<body><header>h</header></body>`, func(o string) bool {
			return strings.Index(o, `id="hm-canvas-bridge"`) > strings.Index(o, "<header>") && strings.HasPrefix(o, "<body><header>h</header><script ")
		}},
		{"head with attributes", `<html><head lang=x><title>t</title></head><body></body></html>`, func(o string) bool {
			return strings.HasPrefix(o, "<html><head lang=x><script id=")
		}},
		{"html without head", `<html lang="en"><body></body></html>`, func(o string) bool {
			return strings.HasPrefix(o, `<html lang="en"><script id=`)
		}},
		{"fragment is unchanged", "<p>frag</p>", func(o string) bool { return o == "<p>frag</p>" }},
		{"no body is no-op", "<p>frag</p>", func(o string) bool { return o == "<p>frag</p>" }},
		{"idempotent", once, func(o string) bool { return o == once && strings.Count(o, `id="hm-canvas-bridge"`) == 1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := string(InjectCanvasBridge([]byte(tt.in)))
			if !tt.want(out) {
				t.Fatalf("unexpected output: %s", out)
			}
		})
	}
	if !strings.HasPrefix(once, "<html><script ") || !strings.HasSuffix(once, "</script>\n<body><p>x</p></body></html>") {
		t.Fatalf("bridge not placed after <html>: %s", once)
	}
}

func TestCanvasBridgeContent(t *testing.T) {
	t.Parallel()
	for _, want := range []string{"hm:ready", "hm:layout", "hm:select", "hm:edit", "hm:edit-text", "data-hm-text", "hm:rich-open", "hm:chrome-click", "hm:scroll", "hm:save", "hm:init", "hm:scroll-to", "plaintext-only", "window.parent"} {
		if !strings.Contains(canvasBridgeJS, want) {
			t.Errorf("bridge missing %q", want)
		}
	}
	for _, bad := range []string{"allow-same-origin", "eval(", "new Function", "fetch(", "XMLHttpRequest", "</script"} {
		if strings.Contains(canvasBridgeJS, bad) {
			t.Errorf("bridge must not contain %q", bad)
		}
	}
}

// An empty container has no height, so a fresh "3 columns" section would be invisible on the canvas
// and could not be right-clicked or dropped into: the editing style gives empty nodes a floor.
func TestCanvasBridgeEmptyNodesHaveHeight(t *testing.T) {
	t.Parallel()
	if !strings.Contains(canvasBridgeJS, "[data-hm-node][data-hm-holds]:empty{min-height:") {
		t.Error("the editing style has no min-height for empty nodes")
	}
}

// TestCanvasBridgeSyntax parses the bridge with node when it is available.
func TestCanvasBridgeSyntax(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	f := filepath.Join(t.TempDir(), "bridge.js")
	if err := os.WriteFile(f, []byte(canvasBridgeJS), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "--check", f).CombinedOutput(); err != nil { // #nosec G204 -- fixed binary, temp file
		t.Fatalf("bridge syntax: %v\n%s", err, out)
	}
}

// Page content that merely mentions the bridge id must not suppress injection.
func TestInjectCanvasBridge_IDInContentStillInjects(t *testing.T) {
	in := []byte(`<html><body><div id="hm-canvas-bridge">x</div></body></html>`)
	if out := InjectCanvasBridge(in); !bytes.Contains(out, []byte(canvasBridgeTag)) {
		t.Fatal("bridge not injected")
	}
}

// TestCanvasBridgeDetailsNotForcedOpen guards visual V5: the bridge must not
// open every <details> (canvas would be taller than the public page); it only
// opens the ones around an edited/clicked field, and only while editable.
func TestCanvasBridgeDetailsNotForcedOpen(t *testing.T) {
	t.Parallel()
	if strings.Contains(canvasBridgeJS, "querySelectorAll('details')") {
		t.Error("bridge must not iterate all <details>")
	}
	i := strings.Index(canvasBridgeJS, "function openDetailsOf")
	if i < 0 {
		t.Fatal("openDetailsOf missing")
	}
	if !strings.Contains(canvasBridgeJS[i:i+120], "!editable") {
		t.Error("openDetailsOf must be a no-op when editable is false")
	}
	if strings.Count(canvasBridgeJS, "openDetailsOf(") < 3 {
		t.Error("openDetailsOf should be used on click and focusin")
	}
}

// TestCanvasBridgeCarriesOpenDetails guards the review-cycle5 P2: details the
// editor opened are reported to the parent and replayed from hm:init.
func TestCanvasBridgeCarriesOpenDetails(t *testing.T) {
	t.Parallel()
	for _, want := range []string{"hm:details-open", "m.openDetails", "function restoreDetails"} {
		if !strings.Contains(canvasBridgeJS, want) {
			t.Errorf("bridge missing %q", want)
		}
	}
	i := strings.Index(canvasBridgeJS, "function restoreDetails")
	if i < 0 || !strings.Contains(canvasBridgeJS[i:i+120], "!editable") {
		t.Error("restoreDetails must be a no-op when editable is false")
	}
}

// Right-click inside text being edited keeps the browser menu; a modifier click on a v1 field sends
// no plain v1 select that would reset the multi-selection.
func TestCanvasBridgeContextMenuAndModifierSelect(t *testing.T) {
	t.Parallel()
	i := strings.Index(canvasBridgeJS, "addEventListener('contextmenu'")
	if i < 0 || !strings.Contains(canvasBridgeJS[i:i+400], "editingProp ||") || !strings.Contains(canvasBridgeJS[i:i+400], "closest('[contenteditable]')") {
		t.Error("the contextmenu handler does not skip text being edited")
	}
	if !strings.Contains(canvasBridgeJS, "else if (field && !(e.ctrlKey || e.metaKey || e.shiftKey))") {
		t.Error("a modifier click on a field still posts the v1 select")
	}
}

// Ctrl+H is forwarded from the frame (and kept from the browser's history), as the parent document's keymap handles it.
func TestCanvasBridgeForwardsCtrlH(t *testing.T) {
	t.Parallel()
	if !strings.Contains(canvasBridgeJS, "/^[cCxXvVgGhH]$/") || !strings.Contains(canvasBridgeJS, "e.preventDefault(); // the browser's history") {
		t.Error("the bridge does not forward Ctrl+H")
	}
}
