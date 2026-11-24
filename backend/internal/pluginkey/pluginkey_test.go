package pluginkey

import (
	"testing"

	"github.com/google/uuid"
)

func TestCommenterKeyPinned(t *testing.T) {
	d := New("test-secret")
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	for plugin, want := range map[string]string{
		"plugin-a": "YbImxQ55xbM-bwQrFm5Z61w-K60_HEd-Uhx7ytt7ML0",
		"plugin-b": "EKKbBPWObivTPGQgGnQK2_6PBXbB0lH7CX-NRETUU_8",
	} {
		if got := d.Commenter(plugin, id); got != want {
			t.Errorf("%s: key = %q, want %q", plugin, got, want)
		}
	}
}

func TestCommenterKeyEmpty(t *testing.T) {
	id := uuid.New()
	if d := New(""); d != nil || d.Commenter("p", id) != "" {
		t.Error("an empty secret must derive nothing")
	}
	d := New("s")
	if d.Commenter("", id) != "" || d.Commenter("p", uuid.Nil) != "" {
		t.Error("empty plugin or nil id must derive nothing")
	}
}
