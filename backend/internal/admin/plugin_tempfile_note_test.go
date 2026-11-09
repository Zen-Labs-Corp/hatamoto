package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

// TestPluginSettingsTempFileNote: the note shows only for a plugin whose
// database role runs without temp_file_limit.
func TestPluginSettingsTempFileNote(t *testing.T) {
	const note = "Temp file cap: not applied."
	for _, off := range []bool{true, false} {
		fp := &fakePlugins{settings: map[string]map[string]any{}, list: []PluginInfo{{Name: "hello", Version: "1.0.0", Loaded: true, TempFileLimitOff: off}}}
		g := newRig(t, func(d *Deps) { d.Growth.Plugins = fp })
		rec := g.do("GET", "/admin/plugins/hello/settings", "owner-token", nil, nil)
		if rec.Code != 200 {
			t.Fatalf("off=%v: status %d", off, rec.Code)
		}
		body := rec.Body.String()
		if got := strings.Contains(body, note); got != off {
			t.Errorf("off=%v: note present = %v", off, got)
		}
		if off {
			if !strings.Contains(body, `<p class="hm-muted">Temp file cap`) {
				t.Errorf("note is not a muted line")
			}
			if issues := a11y.Check(normalizePage(body), a11y.Options{Page: true}); len(issues) > 0 {
				t.Errorf("a11y: %s", strings.Join(issues, "; "))
			}
		}
	}
}
