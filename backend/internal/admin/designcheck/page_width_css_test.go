package designcheck

import (
	"strings"
	"testing"
)

// Narrow and compact pages keep the 760px column; an earlier merge dropped the cap and 36
// pages went full width. Every other page fills the space beside the sidebar. Beside the
// docked subnav a page keeps its own width, left-aligned, and compact ones stay narrow.
func TestPageWidthCSS(t *testing.T) {
	t.Parallel()

	base := readCSSNormalised(t, "../../../web/admin/css/base.css")
	const cap = `.hm-page[data-width="narrow"], .hm-page[data-width="compact"] { max-width: var(--page-narrow); }`
	if !strings.Contains(base, cap) {
		t.Errorf("base.css lacks the narrow+compact cap rule %q", cap)
	}
	if !strings.Contains(base, `.hm-page:not([data-width="narrow"], [data-width="compact"]) { max-width: none; }`) {
		t.Error("base.css: pages that are not narrow or compact must fill the content column")
	}
	const centred = `width: 100%; max-width: var(--page-wide); margin-inline: auto;`
	if !strings.Contains(base, ".hm-page { "+centred) {
		t.Errorf("base.css: .hm-page must centre at --page-wide (%q)", centred)
	}
	if !strings.Contains(base, `.hm-page[data-width="full"] { max-width: none; }`) {
		t.Error("base.css: data-width=full must stay uncapped")
	}

	settings := readCSSNormalised(t, "../../../web/admin/css/partials/settings.css")
	if !strings.Contains(settings, ".hm-settings { display: grid; grid-template-columns: var(--subnav-w) minmax(0, 1fr);") {
		t.Error("settings.css: the subnav must dock beside the page in a two-column grid")
	}
	if strings.Contains(settings, "margin-inline: auto") {
		t.Error("settings.css: the subnav and page group must not centre; the subnav touches the sidebar")
	}
	const wide = `.hm-settings > .hm-page[data-width] { max-width: var(--page-wide); }`
	const compact = `.hm-settings > .hm-page[data-width="compact"] { max-width: var(--page-narrow); }`
	w, c := strings.Index(settings, wide), strings.Index(settings, compact)
	if w < 0 || c < 0 || c < w {
		t.Errorf("settings.css must hold the compact rule after the wide override (wide at %d, compact at %d)", w, c)
	}
}
