package admin

import (
	"testing"

	"hatamoto.dev/core/internal/auth"
)

func TestScopeLabelSEO(t *testing.T) {
	t.Parallel()

	if got := scopeLabel("read:seo"); got != "Read SEO settings" {
		t.Errorf("read:seo label = %q", got)
	}
	if got := scopeLabel("write:seo"); got != "Change SEO settings" {
		t.Errorf("write:seo label = %q", got)
	}
	for _, s := range auth.ValidScopes() {
		if scopeLabel(string(s)) == "" || scopeHelp[string(s)] == "" {
			t.Errorf("scope %s lacks a label or help text", s)
		}
	}
}
