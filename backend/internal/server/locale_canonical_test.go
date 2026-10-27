package server

import (
	"testing"

	"hatamoto.dev/core/internal/entity"
)

// The public filter compares an entity's locale to the site language as
// strings, so the site-language canonicalizer and entity.NormalizeLocale must
// agree on every tag.
func TestEntityLocaleMatchesSiteLanguageCanonical(t *testing.T) {
	for _, tag := range []string{"en", "EN", "en_au", "en-au", "fr", "fr-CA", "iw", "in", "tl", "sh", "mo", "zh-hans", "zh-Hant-TW", "es-419", "pt_BR", "xx", "x", "english"} {
		site, siteOK := canonicalLanguage(tag)
		ent, err := entity.NormalizeLocale(tag)
		if siteOK != (err == nil) || site != ent {
			t.Errorf("%q: site language %q (ok=%v), entity locale %q (err=%v)", tag, site, siteOK, ent, err)
		}
	}
}
