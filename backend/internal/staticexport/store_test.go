package staticexport

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateError(t *testing.T) {
	// "é" is two bytes, so the cut at byte 500 would fall inside a rune.
	long := "x" + strings.Repeat("é", 400)
	got := truncateError(long)
	if !utf8.ValidString(got) || len(got) > maxErrorLen {
		t.Fatalf("len %d valid %v", len(got), utf8.ValidString(got))
	}
	if short := "short"; truncateError(short) != short {
		t.Error("a short error changed")
	}
	if ascii := strings.Repeat("a", 600); len(truncateError(ascii)) != maxErrorLen {
		t.Error("an ASCII error is not cut at the limit")
	}
}
