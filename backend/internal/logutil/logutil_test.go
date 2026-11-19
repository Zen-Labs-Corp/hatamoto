package logutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCapField(t *testing.T) {
	if got := CapField("short"); got != "short" {
		t.Fatalf("short value changed: %q", got)
	}
	exact := strings.Repeat("a", MaxField)
	if got := CapField(exact); got != exact {
		t.Fatal("value at the limit was cut")
	}
	got := CapField(strings.Repeat("é", 32*1024))
	if len(got) > MaxField+len("...") || !strings.HasSuffix(got, "...") || !utf8.ValidString(got) {
		t.Fatalf("bad cap: len=%d valid=%v", len(got), utf8.ValidString(got))
	}
	// 3-byte rune straddling the limit must not be split.
	got = CapField("a" + strings.Repeat("€", 1000))
	if !utf8.ValidString(got) {
		t.Fatal("cut inside a rune")
	}
}
