package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/runenv"
)

// The banner is on the sign-in (bare), shell and editor layouts in staging
// and on none of them otherwise. No t.Parallel: it changes the environment.
func TestStagingBanner(t *testing.T) {
	const marker = "data-hm-staging-banner"
	pages := func(t *testing.T) map[string]string {
		r := newBlogRig(t)
		return map[string]string{
			"bare":   r.do("GET", "/admin/login", "", nil, nil).Body.String(),
			"shell":  r.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(),
			"editor": r.get(t, "owner-token", r.path(), ""),
		}
	}

	t.Setenv(runenv.EnvVar, "staging")
	for name, body := range pages(t) {
		if !strings.Contains(body, marker) || !strings.Contains(body, runenv.Banner) {
			t.Errorf("%s layout misses the staging banner", name)
		}
	}

	t.Setenv(runenv.EnvVar, "")
	for name, body := range pages(t) {
		if strings.Contains(body, marker) || strings.Contains(body, runenv.Banner) {
			t.Errorf("%s layout shows the staging banner in production", name)
		}
	}
}
