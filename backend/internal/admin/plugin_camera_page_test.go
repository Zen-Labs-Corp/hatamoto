package admin

import (
	"strings"
	"testing"
)

// A plugin page with the camera granted opens the camera to the admin origin
// only, marks its frame host and carries core's capture dialog. An ungranted
// page keeps the global header and has none of it. The frame stays sealed.
func TestPluginPageCameraPolicyFollowsTheGrant(t *testing.T) {
	t.Parallel()

	g, _, fp := newPackageRig(t)
	fp.list[0].UI = &PluginUIInfo{Title: "Hello board", Entry: "index.html", Camera: true}

	rec := g.do("GET", "/admin/plugins/hello/settings", "owner-token", nil, nil)
	body := rec.Body.String()
	if got := rec.Header().Get("Permissions-Policy"); got != "geolocation=(), camera=(self), microphone=()" {
		t.Errorf("granted page Permissions-Policy = %q", got)
	}
	for _, want := range []string{` data-camera="1"`, `id="plugin-camera"`, `data-hm-camera-capture`, `data-hm-camera-video`, "Camera: hello", "The plugin receives one photo."} {
		if !strings.Contains(body, want) {
			t.Errorf("granted page lacks %q", want)
		}
	}
	if !strings.Contains(body, `<iframe class="hm-pluginframe" sandbox="allow-scripts" title="Hello board" referrerpolicy="no-referrer">`) {
		t.Error("the frame must stay sealed: sandbox allow-scripts, no allow attribute")
	}

	rec = g.do("GET", "/admin/plugins/board/settings", "owner-token", nil, nil)
	body = rec.Body.String()
	if got := rec.Header().Get("Permissions-Policy"); got != "" && strings.Contains(got, "camera=(self)") {
		t.Errorf("ungranted page Permissions-Policy = %q", got)
	}
	for _, bad := range []string{"data-camera", "plugin-camera", "hm-camera", "Camera: "} {
		if strings.Contains(body, bad) {
			t.Errorf("ungranted page mentions %q", bad)
		}
	}
}
