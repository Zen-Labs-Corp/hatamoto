package admin

import (
	"strings"
	"testing"
)

// Narrow pages keep the 760px column outside Settings; an earlier merge dropped the
// cap and 36 pages went full width. Settings widens every page except compact ones.
func TestPageWidthCSS(t *testing.T) {
	t.Parallel()

	base := readCSSNormalised(t, "../../web/admin/css/base.css")
	const cap = `.hm-page[data-width="narrow"], .hm-page[data-width="compact"] { max-width: var(--page-narrow); }`
	if !strings.Contains(base, cap) {
		t.Errorf("base.css lacks the narrow+compact cap rule %q", cap)
	}
	const uncap = `.hm-page:not([data-width="narrow"], [data-width="compact"]) { max-width: none; }`
	if !strings.Contains(base, uncap) {
		t.Errorf("base.css lacks the uncapped rule %q", uncap)
	}

	settings := readCSSNormalised(t, "../../web/admin/css/partials/settings.css")
	const wide = `.hm-settings > .hm-page[data-width] { max-width: var(--page-wide); }`
	const compact = `.hm-settings > .hm-page[data-width="compact"] { max-width: var(--page-narrow); }`
	w, c := strings.Index(settings, wide), strings.Index(settings, compact)
	if w < 0 || c < 0 || c < w {
		t.Errorf("settings.css must hold the compact rule after the wide override (wide at %d, compact at %d)", w, c)
	}
}
