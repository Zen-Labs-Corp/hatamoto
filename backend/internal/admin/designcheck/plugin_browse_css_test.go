package designcheck

import (
	"os"
	"strings"
	"testing"
)

// The filters collapse on a narrow screen; the markup keeps them open for wide screens and no-JS.
func TestBrowseFiltersCollapseOnNarrowScreens(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../web/admin/js/pluginpanel.js")
	if err != nil {
		t.Fatal(err)
	}
	if src := string(b); !strings.Contains(src, "matchMedia(NARROW)") || !strings.Contains(src, `removeAttribute("open")`) || !strings.Contains(src, "hm-pluginbrowse__facets") {
		t.Error("pluginpanel.js must collapse the browse filters on a narrow screen")
	}
	css, err := os.ReadFile("../../../web/admin/css/pages/pluginbrowse.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), "(min-width: 900px)") {
		t.Error("pluginbrowse.css breakpoint moved: update NARROW in pluginpanel.js")
	}
}
