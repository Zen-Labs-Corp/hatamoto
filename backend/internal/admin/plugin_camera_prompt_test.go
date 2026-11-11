package admin

import (
	"strings"
	"testing"
)

// The camera is never a hidden grant: a fresh install shows it as an off
// switch, a newly asked one carries the New badge, and an earlier grant stays
// ticked on an update.
func TestPromptCameraSwitch(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	for _, tc := range []struct {
		src     string
		checked bool
		isNew   bool
	}{
		{"plugin-camera:scan", false, false},
		{"plugin-camera-update:scan", false, true},
		{"plugin-camera-kept:scan", true, false},
	} {
		body := postMultipart(g, "/admin/plugins/upload", "owner-token", "p.hmp", tc.src).Body.String()
		if strings.Contains(body, `type="hidden" name="permission" value="ui.allow:camera"`) {
			t.Errorf("%s: the camera must never be a hidden grant", tc.src)
		}
		if !strings.Contains(body, `type="hidden" name="permission" value="kv"`) {
			t.Errorf("%s: kv stays a hidden grant", tc.src)
		}
		if !strings.Contains(body, `value="ui.allow:camera"`) || !strings.Contains(body, "scan tickets") {
			t.Fatalf("%s: camera switch missing:\n%s", tc.src, body)
		}
		if got := strings.Contains(body, `value="ui.allow:camera" checked`); got != tc.checked {
			t.Errorf("%s: checked = %v, want %v", tc.src, got, tc.checked)
		}
		if got := strings.Contains(body, "New in this version"); got != tc.isNew {
			t.Errorf("%s: New badge = %v, want %v", tc.src, got, tc.isNew)
		}
		if strings.Contains(body, "It asks for no permissions") {
			t.Errorf("%s: says it asks for nothing", tc.src)
		}
	}
}
