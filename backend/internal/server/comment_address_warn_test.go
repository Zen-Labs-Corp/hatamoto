package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestWarnCommentsWithoutAddress(t *testing.T) {
	const msg = "comments are on but no site address is set"
	cases := []struct {
		name string
		on   bool
		err  error
		base string
		want bool
	}{
		{"on, empty", true, nil, "", true},
		{"on, address", true, nil, "https://example.com", false},
		{"off, empty", false, nil, "", false},
		{"read error", true, errors.New("db down"), "", false},
		{"on, blank", true, nil, "  ", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, nil))
			got := warnCommentsWithoutAddress(context.Background(), log,
				func(context.Context) (bool, error) { return c.on, c.err },
				func(context.Context) string { return c.base })
			if got != c.want {
				t.Fatalf("returned %v, want %v", got, c.want)
			}
			n := strings.Count(buf.String(), msg)
			if (n == 1) != c.want || n > 1 {
				t.Fatalf("logged %d times, want logged=%v: %s", n, c.want, buf.String())
			}
		})
	}
}
