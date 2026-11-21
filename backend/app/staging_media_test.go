package app

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/media"
)

func TestWarnStagingMedia(t *testing.T) {
	for _, tc := range []struct {
		storage string
		want    bool
	}{{"s3", true}, {"", false}, {"local", false}} {
		var buf bytes.Buffer
		warnStagingMedia(slog.New(slog.NewTextHandler(&buf, nil)), func(k string) string {
			if k == media.EnvStorage {
				return tc.storage
			}
			return ""
		})
		if got := strings.Contains(buf.String(), "give this copy its own bucket"); got != tc.want {
			t.Errorf("storage %q: warned=%v, want %v (%q)", tc.storage, got, tc.want, buf.String())
		}
	}
}
