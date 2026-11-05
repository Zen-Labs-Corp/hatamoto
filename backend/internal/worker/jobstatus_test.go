package worker

import (
	"errors"
	"strings"
	"testing"
)

func TestCleanError(t *testing.T) {
	if got := cleanError(nil); got != "" {
		t.Fatalf("nil: %q", got)
	}
	if got := cleanError(errors.New("  a\nb\x00c\td\x1b[0m ")); got != "a b c d [0m" {
		t.Fatalf("control chars: %q", got)
	}
	if got := []rune(cleanError(errors.New(strings.Repeat("x", 600)))); len(got) != 500 {
		t.Fatalf("long: %d runes", len(got))
	}
	got := cleanError(errors.New(strings.Repeat("é日", 400)))
	if r := []rune(got); len(r) != 500 || !strings.HasPrefix(got, "é日é") {
		t.Fatalf("multibyte: %d runes", len(r))
	}
}
