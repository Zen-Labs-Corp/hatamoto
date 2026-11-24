package comment

import (
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/pluginkey"
)

func TestPluginCommenterKey(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	got := PluginCommenterKey("test-secret", "example", id)
	if want := "dX9T97uNMqjzuL7R-3z4fBGDzUERpiUHEp40tpQsjqI"; got != want {
		t.Errorf("PluginCommenterKey = %q, want %q", got, want)
	}
	if got == PluginCommenterKey("test-secret", "other", id) {
		t.Error("two plugins got the same key")
	}
	if got == PluginCommenterKey("other-secret", "example", id) {
		t.Error("two secrets gave the same key")
	}
}

// set_viewer matches the keys threads and events carry only while both agree.
func TestPluginCommenterKeyMatchesDeriver(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	d := pluginkey.New("test-secret")
	for plugin, want := range map[string]string{
		"plugin-a": "YbImxQ55xbM-bwQrFm5Z61w-K60_HEd-Uhx7ytt7ML0",
		"plugin-b": "EKKbBPWObivTPGQgGnQK2_6PBXbB0lH7CX-NRETUU_8",
	} {
		if got := PluginCommenterKey("test-secret", plugin, id); got != want || got != d.Commenter(plugin, id) {
			t.Errorf("%s: PluginCommenterKey = %q, Deriver = %q, want %q", plugin, got, d.Commenter(plugin, id), want)
		}
	}
}
