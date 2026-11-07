//go:build integration

package server

import (
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/themestrings"
)

// A 404 under a locale segment renders that locale's string overrides: the
// real handler resolves the locale from the path.
func TestNotFoundUsesLocaleStrings(t *testing.T) {
	ctx := context.Background()
	e := newWireEnv(t, nil)
	if _, err := e.db.Pool().Exec(ctx, `UPDATE hatamoto_site_config SET language='en',
		locales='[{"tag":"en","segment":""},{"tag":"fr","segment":"fr"}]'::jsonb`); err != nil {
		t.Fatal(err)
	}
	s, _ := e.server(t, "/admin")
	theme := s.renderEngine.ThemeName()
	raw, _ := jsonx.Marshal(map[string]map[string]map[string]string{theme: {
		"en": {"not_found_title": "Missing EN"},
		"fr": {"not_found_title": "Missing FR"},
	}})
	if err := s.siteConfig.PatchThemeExtra(ctx, themestrings.ExtraKey, raw); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"/fr/missing": "Missing FR", "/missing": "Missing EN"} {
		rec := get(s, path)
		if rec.Code != 404 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d, want 404 containing %q", path, rec.Code, want)
		}
	}
}
