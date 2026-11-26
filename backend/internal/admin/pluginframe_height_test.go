package admin

import (
	"strings"
	"testing"
)

// The SDK reports the page height to the admin, which sizes the frame to it.
// It posts directly (no pending promise) and only after the greeting gave it the nonce.
func TestPluginSDKReportsHeight(t *testing.T) {
	t.Parallel()

	js := string(readFileT(t, "../../web/admin/js/pluginsdk.js"))
	for _, want := range []string{
		`new ResizeObserver(`,
		`requestAnimationFrame(`,
		`op: "height"`,
		`nonce, op: "height"`,
		`height === last`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("pluginsdk.js lost %q", want)
		}
	}
	// The last mention is the call in the hello branch; the first is the declaration.
	if i, j := strings.Index(js, `nonce = m.nonce`), strings.LastIndex(js, `reportHeight()`); i < 0 || j < i {
		t.Error("the height report must start after the greeting set the nonce")
	}
}

// The host applies the page's height to its own frame only: after the source,
// nonce and first-load checks, clamped, and without a reply or a bridge request.
func TestPluginFrameHeightIsLocalAndClamped(t *testing.T) {
	t.Parallel()

	js := string(readFileT(t, "../../web/admin/js/pluginframe.js"))
	for _, want := range []string{
		`const MIN_HEIGHT = 320`,
		`const MAX_HEIGHT = 20000`,
		`holder.dataset.fit === "1"`,
		`Number.isFinite(h)`,
		`frame.style.height`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("pluginframe.js lost %q", want)
		}
	}
	last := -1
	for _, frag := range []string{`e.source !== frame.contentWindow`, `m.nonce !== nonce`, `m.op === "height"`, `await forward(m)`} {
		i := strings.Index(js, frag)
		if i < 0 || i < last {
			t.Fatalf("%q is missing or out of order (source, nonce, height, forward)", frag)
		}
		last = i
	}
	start := strings.Index(js, "const setHeight")
	end := strings.Index(js[start:], "};")
	if start < 0 || end < 0 {
		t.Fatal("setHeight not found")
	}
	if strings.Contains(js[start:start+end], "querySelector") {
		t.Error("setHeight must resize only its own frame")
	}
}
