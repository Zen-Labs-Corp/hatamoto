package tags

import (
	"regexp"
	"testing"
)

// TestLazyPatternsCompile forces every lazily compiled pattern, so a bad one
// still fails the test run instead of the first validation.
func TestLazyPatternsCompile(t *testing.T) {
	for _, v := range Vendors {
		if v.Pattern.regexp() == nil {
			t.Errorf("%s: pattern did not compile", v.ID)
		}
		if v.Aux != nil && v.Aux.Pattern.regexp() == nil {
			t.Errorf("%s: aux pattern did not compile", v.ID)
		}
	}
	for name, get := range map[string]func() *regexp.Regexp{
		"exactHostRE": exactHostRE, "pluginNameRE": pluginNameRE, "stemRE": stemRE, "wildHostRE": wildHostRE,
	} {
		if get() == nil {
			t.Errorf("%s compiled to nil", name)
		}
	}
}
