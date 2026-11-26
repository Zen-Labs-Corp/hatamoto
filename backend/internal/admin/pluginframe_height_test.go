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
