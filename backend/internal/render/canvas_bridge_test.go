package render

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The bridge is one script serving protocol v1 and (once negotiated) v2. There
// is no JS engine in the module, so these are source-level guards; the
// behaviour is exercised in a real browser by the QA harness.

func bridgeSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("canvas_bridge.js")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBridgeHasNoDynamicCodeOrNavigation(t *testing.T) {
	src := bridgeSource(t)
	for _, bad := range []string{"eval(", "new Function", "Function(", "document.write", "</script", "importScripts", "fetch(", "XMLHttpRequest", "WebSocket"} {
		if strings.Contains(src, bad) {
			t.Errorf("bridge must not contain %q", bad)
		}
	}
	for _, assign := range regexp.MustCompile(`(?:window\.|document\.|top\.|parent\.)?location(?:\.href)?\s*=[^=]`).FindAllString(src, -1) {
		t.Errorf("bridge assigns a location: %q", assign)
	}
	if strings.Count(src, ".innerHTML") != 1 || !strings.Contains(src, "t.innerHTML = html") {
		t.Error("innerHTML may only be set on a <template> (fromHTML)")
	}
}

// Every v1 or v2 message the bridge posts is gated: v2 ones only after init.
func TestBridgeV2PostsOnlyFixtureTypes(t *testing.T) {
	raw, err := os.ReadFile("../admin/testdata/canvas_protocol_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []struct {
			Dir   string `json:"dir"`
			Valid bool   `json:"valid"`
			Msg   any    `json:"msg"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	inbound := map[string]bool{}
	for _, c := range f.Cases {
		if m, ok := c.Msg.(map[string]any); ok && c.Dir != "out" && c.Valid && m["hm"] == float64(2) {
			inbound[m["type"].(string)] = true
		}
	}
	src := bridgeSource(t)
	posted := regexp.MustCompile(`post2\(\{\s*type: '([A-Za-z]+)'`).FindAllStringSubmatch(src, -1)
	if len(posted) < 6 {
		t.Fatalf("found %d v2 posts, the scan is broken", len(posted))
	}
	for _, m := range posted {
		if !inbound[m[1]] {
			t.Errorf("bridge posts v2 %q, which the protocol fixture does not know", m[1])
		}
	}
	// The v1 posts stay v1 and the v2 family stays behind the init handshake.
	if !strings.Contains(src, "msg.hm = 1") || !strings.Contains(src, "msg.hm = 2") {
		t.Error("bridge must stamp hm on every post")
	}
	if !strings.Contains(src, "if (!v2) return;") || !strings.Contains(src, "if (v2) { sendLayout2(); return; }") {
		t.Error("v2 behaviour is not gated on the init handshake")
	}
	for _, typ := range []string{"init", "patch", "insert", "move", "remove", "setVar", "css", "edit", "select", "scrollTo", "force", "format"} {
		if !strings.Contains(src, "case '"+typ+"'") {
			t.Errorf("bridge does not handle v2 %q", typ)
		}
	}
}

// v1 hosts never send hm:2, so the v1 handlers must still be there verbatim.
func TestBridgeKeepsV1Messages(t *testing.T) {
	src := bridgeSource(t)
	for _, want := range []string{"'hm:ready'", "'hm:layout'", "'hm:select'", "'hm:edit'", "'hm:edit-text'", "'hm:rich-open'",
		"'hm:details-open'", "'hm:chrome-click'", "'hm:save'", "'hm:scroll'", "m.hm !== 1"} {
		if !strings.Contains(src, want) {
			t.Errorf("bridge lost v1 piece %s", want)
		}
	}
}

// An empty rule map entry removes the node's <style>: a fresh render has none.
func TestBridgeEmptyCSSRemovesStyleElement(t *testing.T) {
	src := bridgeSource(t)
	i := strings.Index(src, "function setCSS(")
	j := strings.Index(src, "function dropCSSUnder(")
	if i < 0 || j < i {
		t.Fatal("setCSS not found")
	}
	body := src[i:j]
	if !strings.Contains(body, "=== ''") || !strings.Contains(body, "tag.remove()") {
		t.Error("setCSS must remove the style element for an empty rule")
	}
}

// The inline normaliser is a pure block QA/unit/bridge-inline.test.mjs slices out and runs.
func TestBridgeInlineBlockIsPure(t *testing.T) {
	src := bridgeSource(t)
	i, j := strings.Index(src, "/* hm:inline:start */"), strings.Index(src, "/* hm:inline:end */")
	if i < 0 || j < i {
		t.Fatal("inline marker block not found")
	}
	block := src[i:j]
	for _, bad := range []string{"doc.", "window.", "document.", "innerHTML", "execCommand", "editingProp", "post2(", "post("} {
		if strings.Contains(block, bad) {
			t.Errorf("inline block must not reference %q", bad)
		}
	}
	for _, want := range []string{"function clean(", "function ser(", "function build("} {
		if !strings.Contains(block, want) {
			t.Errorf("inline block lost %q", want)
		}
	}
	// Only the code and link marks are written by ranges; nothing formats through execCommand.
	if strings.Contains(src, "execCommand('bold'") || strings.Contains(src, "execCommand('italic'") || strings.Contains(src, "execCommand('createLink'") {
		t.Error("formatting must not use execCommand")
	}
	if !strings.Contains(src, "rk ? 'true' : 'plaintext-only'") {
		t.Error("heading and text edit as contenteditable=true, everything else as plaintext-only")
	}
}

// Chromium fires the editor's focusout while the frame still has focus when the host takes it (the formatting bar's
// link field). A rich edit must therefore not end in that handler; it asks again a tick later. The browser behaviour
// is covered by scripts/admin-qa/builder-inline.mjs.
func TestBridgeRichEditSurvivesHostFocusInFocusout(t *testing.T) {
	src := bridgeSource(t)
	i := strings.LastIndex(src, "doc.addEventListener('focusout'")
	if i < 0 {
		t.Fatal("no focusout handler")
	}
	end := strings.Index(src[i:], "\n  });")
	h := src[i : i+end]
	if !regexp.MustCompile(`setTimeout\(function \(\) \{ if \(editingProp === el && doc\.hasFocus\(\)\) stopProp\(\); \}, 0\)`).MatchString(h) {
		t.Errorf("a rich focusout must re-check doc.hasFocus() in a timeout before stopProp:\n%s", h)
	}
}

// The bridge asks for the one port with a single data-free window message
// before any page script runs, swallows the handover in the capture phase so no
// later listener sees the port, and posts nothing else to the parent window.
func TestBridgePortHandshakeIsFirstWins(t *testing.T) {
	src := bridgeSource(t)
	for _, want := range []string{
		"var parentWin = window.parent;", "var pm = MessagePort.prototype.postMessage;",
		"e.stopImmediatePropagation();", "if (latched) return;", "latched = true;",
		"{ capture: true }", "e.ports.length !== 1", "e.source !== parentWin", "d.type !== 'hm:port'",
		"{ hm: 1, type: 'hm:ready', contract: 'port' }",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("bridge port handshake missing %q", want)
		}
	}
	if n := strings.Count(src, "parentWin.postMessage("); n != 1 {
		t.Errorf("bridge must post to the parent window exactly once (the data-free port request), got %d", n)
	}
	if strings.Contains(src, "window.parent.postMessage") || strings.Contains(src, "parent.postMessage(m") {
		t.Error("bridge must not post data to the parent window")
	}
	if !strings.Contains(src, "doc.head.appendChild(style)") || strings.Index(src, "doc.head.appendChild(style)") < strings.Index(src, "function init()") {
		t.Error("the bridge style must be appended in init(), after the theme's stylesheets")
	}
}

// With a v2 parent the parent draws hover and selection in one layer: the frame outlines are scoped to
// html:not([data-hm-v2]) (set at init), and an empty node keeps a 64 px floor so it can be hit and dropped into.
func TestCanvasBridgeOutlinesAreV1Only(t *testing.T) {
	src := bridgeSource(t)
	for _, want := range []string{
		"setAttribute('data-hm-v2', '')",
		"html[data-hm-editing]:not([data-hm-v2]) [data-hm-node].hm-canvas-selected{",
		"html[data-hm-editing]:not([data-hm-v2]) [data-hm-node].hm-canvas-hover{",
		"[data-hm-node][data-hm-holds]:empty{min-height:64px;",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("bridge missing %q", want)
		}
	}
}

// Outside a text edit the frame starts editing on Enter (the selected element's own text) and forwards
// Ctrl/Cmd+K (without Alt or Shift) to the host after cancelling the browser search; both live in the
// guarded non-edit keydown, and the `edit` message shares the helper.
func TestBridgeEnterEditsAndPaletteKeyIsForwarded(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"k === 'Enter' && !mod && !e.shiftKey && !e.altKey && selectedKey",
		"editIn(find('section', selectedKey) || find('node', selectedKey), true)",
		"(k === 'k' || k === 'K') && mod && !e.altKey && !e.shiftKey",
		"post2({ type: 'key', key: 'k', shift: false })",
		"editIn(find(m.sel, m.key), false)",
		"(own && nodeOf(t) !== el)",
	} {
		if !strings.Contains(canvasBridgeJS, want) {
			t.Errorf("bridge missing %q", want)
		}
	}
}

// The frame forwards the builder's plain and Alt shortcuts (mod:false), but only outside form controls and text,
// the selection keys only with an element selected, and never Alt+Left/Right (browser back/forward); the
// Ctrl/Cmd chords are cancelled so Ctrl+D cannot bookmark the page.
func TestBridgeForwardsShortcutSet(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"k === 'd' || k === 'D'",
		"if (ctrl) e.preventDefault();",
		"e.target.closest('input,textarea,select,button,summary,[contenteditable]')",
		"var send = !alt && (k === '/' || k === '?' || k === 'a');",
		"if (alt) send = (k === 'ArrowUp' || k === 'ArrowDown');",
		"k === 'Escape' || k === 'ArrowUp' || k === 'ArrowDown' || k === 'Backspace'",
		"k === '1' || k === '2' || k === '3'",
		"post2({ type: 'key', key: k, shift: !!shift, alt: !!alt, mod: false })",
	} {
		if !strings.Contains(canvasBridgeJS, want) {
			t.Errorf("bridge missing %q", want)
		}
	}
	if strings.Contains(canvasBridgeJS, "ArrowLeft") || strings.Contains(canvasBridgeJS, "ArrowRight") {
		t.Error("Alt+Left/Right must never be forwarded")
	}
}

// A format rewrites the editor's DOM, which breaks the browser's own undo: each format keeps a snapshot and
// Ctrl/Cmd+Z (or the Edit menu's historyUndo) restores it while the text still is what the format produced.
// The stack is per edit session, and the pure inline block stays free of it.
func TestBridgeFormatUndoSnapshots(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"var fmtStack = [];",
		"fmtStack.push({ html: ser(before), after: ser(tree), offs: o })",
		"lk === 'z' && !e.shiftKey && undoFormat(el, rk)",
		"t === 'historyUndo' && undoFormat(editingProp, richKind(editingProp))",
		"ser(clean(el, rk)) !== top.after",
	} {
		if !strings.Contains(canvasBridgeJS, want) {
			t.Errorf("bridge missing %q", want)
		}
	}
	if strings.Count(canvasBridgeJS, "fmtStack = [];") < 3 {
		t.Error("the snapshot stack must reset when an edit starts and when it ends")
	}
	start, end := strings.Index(canvasBridgeJS, "hm:inline:start"), strings.Index(canvasBridgeJS, "hm:inline:end")
	if start < 0 || end < start || strings.Contains(canvasBridgeJS[start:end], "fmtStack") {
		t.Error("the inline block must not touch the snapshot stack")
	}
}

// Every top-level node (v1 section or native container) reports its rendered padding as four whole px in the layout node; nothing else does.
func TestBridgeLayoutCarriesSectionPadding(t *testing.T) {
	src := bridgeSource(t)
	for _, want := range []string{
		"if (item.p === '')",
		"window.getComputedStyle(boxOf(el))",
		"while (boxless(el) && el.firstElementChild)",
		"item.pd = [pad(ps.paddingTop), pad(ps.paddingRight), pad(ps.paddingBottom), pad(ps.paddingLeft)]",
		"Math.round(parseFloat(v))",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("layout padding lost %q", want)
		}
	}
	if strings.Count(src, "item.pd") != 1 {
		t.Error("pd must be set in exactly one place (top-level nodes)")
	}
}
