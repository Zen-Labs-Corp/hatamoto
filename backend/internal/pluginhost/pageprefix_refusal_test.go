package pluginhost

import (
	"strconv"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func TestPagePathPrefixesRefusalNamesPrefix(t *testing.T) {
	for _, p := range []string{"/", "/p/", "/api/", "/uploads/", "/theme/", "/-/", "/admin/", "/mcp/",
		"/locale/", "/sitemap.xsl/", "/favicon-32.png/", "/f/", "/preview/", "/api/v1/"} {
		err := PagePathPrefixesRefusal(sdk.Manifest{Name: "board", PagePathPrefixes: []string{"/item/", p}})
		if err == nil || !strings.Contains(err.Error(), strconv.Quote(p)) {
			t.Errorf("%s: err = %v, want a refusal naming it", p, err)
		}
	}
	if err := PagePathPrefixesRefusal(sdk.Manifest{Name: "board", PagePathPrefixes: []string{"/item/", "/news/", "/item/sub/", "/apis/"}}); err != nil {
		t.Errorf("allowed prefixes refused: %v", err)
	}
}
