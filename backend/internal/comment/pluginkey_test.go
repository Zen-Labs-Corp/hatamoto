package comment

import (
	"testing"

	"github.com/google/uuid"
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
