package designcheck

import (
	"os"
	"strings"
	"testing"
)

// Browse has one toolbar (hm/filterbar), not a facet column: the old facet
// class and its narrow-screen collapse are gone, and the cards fill the width.
func TestBrowseToolbarReplacesFacetColumn(t *testing.T) {
	t.Parallel()

	js, err := os.ReadFile("../../../web/admin/js/pluginpanel.js")
	if err != nil {
		t.Fatal(err)
	}
	css, err := os.ReadFile("../../../web/admin/css/pages/pluginbrowse.css")
	if err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{"pluginpanel.js": string(js), "pluginbrowse.css": string(css)} {
		if strings.Contains(src, "hm-pluginbrowse__facets") {
			t.Errorf("%s still mentions hm-pluginbrowse__facets", name)
		}
	}
	if !strings.Contains(string(css), "repeat(auto-fill, minmax(min(240px, 100%), 1fr))") {
		t.Error("pluginbrowse.css: the cards grid must be repeat(auto-fill, minmax(240px, 1fr))")
	}
}
