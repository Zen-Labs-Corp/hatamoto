package designcheck

import (
	"strings"
	"testing"
)

// Narrow pages keep the 760px column outside Settings; an earlier merge dropped the
// cap and 36 pages went full width. Every page centres in the --page-wide group
// unless it is data-width="full"; Settings centres subnav and page as one group and
// lets every page fill the content column except compact ones.
func TestPageWidthCSS(t *testing.T) {
	t.Parallel()

	base := readCSSNormalised(t, "../../../web/admin/css/base.css")
	const cap = `.hm-page[data-width="narrow"], .hm-page[data-width="compact"] { max-width: var(--page-narrow); }`
	if !strings.Contains(base, cap) {
		t.Errorf("base.css lacks the narrow+compact cap rule %q", cap)
	}
	if strings.Contains(base, `.hm-page:not([data-width="narrow"], [data-width="compact"]) { max-width: none; }`) {
		t.Error("base.css uncaps every non-narrow page again: wide pages would not centre at --page-wide")
	}
	const centred = `width: 100%; max-width: var(--page-wide); margin-inline: auto;`
	if !strings.Contains(base, ".hm-page { "+centred) {
		t.Errorf("base.css: .hm-page must centre at --page-wide (%q)", centred)
	}
	if !strings.Contains(base, `.hm-page[data-width="full"] { max-width: none; }`) {
		t.Error("base.css: data-width=full must stay uncapped")
	}

	settings := readCSSNormalised(t, "../../../web/admin/css/partials/settings.css")
	if !strings.Contains(settings, ".hm-settings { display: grid;") || !strings.Contains(settings, "max-width: var(--page-wide); margin-inline: auto;") {
		t.Error("settings.css: the subnav+page group must centre at --page-wide")
	}
	const wide = `.hm-settings > .hm-page[data-width] { max-width: none; }`
	const compact = `.hm-settings > .hm-page[data-width="compact"] { max-width: var(--page-narrow); }`
	w, c := strings.Index(settings, wide), strings.Index(settings, compact)
	if w < 0 || c < 0 || c < w {
		t.Errorf("settings.css must hold the compact rule after the wide override (wide at %d, compact at %d)", w, c)
	}
}
