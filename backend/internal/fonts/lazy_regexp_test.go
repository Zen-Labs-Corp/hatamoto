package fonts

import (
	"regexp"
	"testing"
)

// TestLazyRegexpsCompile forces every lazily compiled pattern, so a bad one
// still fails the test run instead of the first use.
func TestLazyRegexpsCompile(t *testing.T) {
	lazy := map[string]func() *regexp.Regexp{
		"rangeRe":   rangeRe,
		"srcRe":     srcRe,
		"stretchRe": stretchRe,
		"subsetRe":  subsetRe,
		"weightRe":  weightRe,
	}
	for name, get := range lazy {
		if get() == nil {
			t.Errorf("%s compiled to nil", name)
		}
	}
}
