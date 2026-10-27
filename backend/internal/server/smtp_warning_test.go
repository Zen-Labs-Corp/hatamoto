package server

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestWarnUnsendableNotifications(t *testing.T) {
	count := func(n int) func(context.Context) (int, error) {
		return func(context.Context) (int, error) { return n, nil }
	}
	for _, tc := range []struct {
		name, env  string
		configured bool
		forms      int
		wantWarn   bool
	}{
		{"production, no smtp, forms notify", "production", false, 2, true},
		{"production, no forms notify", "production", false, 0, false},
		{"production, smtp set", "production", true, 2, false},
		{"development", "development", false, 2, false},
	} {
		var buf bytes.Buffer
		warnUnsendableNotifications(context.Background(), slog.New(slog.NewTextHandler(&buf, nil)), tc.env, tc.configured, count(tc.forms))
		if got := strings.Contains(buf.String(), "level=WARN"); got != tc.wantWarn {
			t.Errorf("%s: warned = %v, want %v (%q)", tc.name, got, tc.wantWarn, buf.String())
		}
		if tc.wantWarn && !strings.Contains(buf.String(), "Settings > Email") {
			t.Errorf("%s: warning must name Settings > Email, got %q", tc.name, buf.String())
		}
	}
}
