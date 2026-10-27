package admin

import (
	"os"
	"strings"
	"testing"
)

// Screen readers announce text inserted into a live region that already exists; a toast that
// brings its own role arrives pre-filled and is often silent. The layout must carry both
// persistent regions (polite status and assertive alert, siblings, not nested), and the toast
// script must target them without setting a role on the node.
func TestToastsLiveRegions(t *testing.T) {
	t.Parallel()

	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	layout := read("../../web/admin/layouts/base.html")
	for _, want := range []string{
		`<div id="hm-toasts" role="status" aria-live="polite"></div><div id="hm-toasts-alert" role="alert"></div>`,
	} {
		if !strings.Contains(layout, want) {
			t.Errorf("base.html lacks persistent toast regions: %s", want)
		}
	}
	if strings.Contains(read("../../web/admin/js/components.js"), `node.setAttribute("role"`) {
		t.Error("components.js must not set a role on toast nodes (the hosts are the live regions)")
	}
	js := read("../../web/admin/js/components.js")
	if !strings.Contains(js, `"hm-toasts-alert"`) || !strings.Contains(js, `"hm-toasts"`) {
		t.Error("components.js must use both toast hosts")
	}
}
