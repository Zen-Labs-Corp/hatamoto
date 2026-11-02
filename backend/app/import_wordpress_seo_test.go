package app

import (
	"bytes"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/wpimport"
)

func TestPrintSEOResultStripsControlCharacters(t *testing.T) {
	var out bytes.Buffer
	printSEOResult(&out, &wpimport.SEOResult{
		Applied:   []wpimport.SEORow{{Label: "L\x1b[2J", Value: "v\x07"}},
		NotMapped: []string{"robots.txt line: \x1b]0;pwned\x07Disallow: /x"},
	})
	if strings.ContainsAny(out.String(), "\x1b\x07") {
		t.Errorf("control characters in output: %q", out.String())
	}
}
