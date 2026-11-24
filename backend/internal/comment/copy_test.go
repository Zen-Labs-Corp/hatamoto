package comment

import (
	"strings"
	"testing"
)

func TestCopyPinned(t *testing.T) {
	for _, c := range []struct{ name, got, want string }{
		{"EnableWarning", EnableWarning, "Turning comments on will invite spam. Bots and spammers find comment forms within days. Every comment with a link and every first comment will wait in your moderation queue."},
		{"EnableConfirmWarning", EnableConfirmWarning, "Are you sure? Turning comments on will invite spam to this site. You will need to check the moderation queue regularly."},
		{"EnableTick", EnableTick, "I understand this will invite spam"},
		{"NoAddressMsg", NoAddressMsg, "Set your site address in Settings > General before turning comments on."},
	} {
		if c.got != c.want {
			t.Errorf("%s changed: %q", c.name, c.got)
		}
	}
}

func TestCopySentences(t *testing.T) {
	for name, s := range map[string]string{"EnableWarning": EnableWarning, "NoAddressMsg": NoAddressMsg} {
		if !strings.HasSuffix(s, ".") {
			t.Errorf("%s should end with a full stop", name)
		}
	}
}
