package admin

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

const wantUICSP = "sandbox allow-scripts; default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'"

// The camera grant changes only the admin page around the frame (flag, dialog,
// header). The frame's sandbox and the framed document's CSP stay as they were.
func TestPluginCameraSealedFrameAndCSP(t *testing.T) {
	t.Parallel()

	if uiCSP != wantUICSP {
		t.Errorf("uiCSP changed:\n got %q\nwant %q", uiCSP, wantUICSP)
	}
	if pluginPagePolicy != "geolocation=(), camera=(self), microphone=()" {
		t.Errorf("pluginPagePolicy = %q", pluginPagePolicy)
	}
}

// The settings page and the runs page of a plugin both follow the grant.
func TestPluginCameraGrantedAndUngrantedPages(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, path string
		camera     bool
	}{
		{"settings granted", "/admin/plugins/hello/settings", true},
		{"settings ungranted", "/admin/plugins/hello/settings", false},
		{"runs granted", "/admin/plugins/hello/runs", true},
		{"runs ungranted", "/admin/plugins/hello/runs", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := runPlugin
			p.UI = &PluginUIInfo{Title: "Hello board", Entry: "index.html", Camera: tc.camera}
			g := newRig(t, func(d *Deps) {
				d.Growth.Plugins = &fakePlugins{settings: map[string]map[string]any{}, list: []PluginInfo{p}}
				d.Growth.RunReader, d.Growth.RunCancel, d.Growth.PluginRuns = &fakeRunReader{runs: map[uuid.UUID]RunView{}}, &fakeRunCanceller{}, &fakeRunStarter{}
			})
			rec := g.do("GET", tc.path, "owner-token", nil, nil)
			if rec.Code != 200 {
				t.Fatalf("%s: %d", tc.path, rec.Code)
			}
			body, policy := rec.Body.String(), rec.Header().Get("Permissions-Policy")
			if tc.camera != (policy == "geolocation=(), camera=(self), microphone=()") {
				t.Errorf("Permissions-Policy = %q, camera = %v", policy, tc.camera)
			}
			for _, marker := range []string{` data-camera="1"`, `id="plugin-camera"`, `data-hm-camera-capture`} {
				if got := strings.Contains(body, marker); got != tc.camera {
					t.Errorf("%q present = %v, want %v", marker, got, tc.camera)
				}
			}
			if !strings.Contains(body, `sandbox="allow-scripts"`) || strings.Contains(body, "allow-same-origin") || strings.Contains(body, `allow="camera`) {
				t.Error("the frame must stay sandbox=allow-scripts, with no allow-same-origin and no allow attribute")
			}
		})
	}
}
