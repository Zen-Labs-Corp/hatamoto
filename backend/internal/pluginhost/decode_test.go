package pluginhost

import (
	"encoding/json"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// sitemap.urls returns additions only, and at most sdk.MaxSitemapAdditions.
func TestSitemapOutputIsBounded(t *testing.T) {
	in, _ := json.Marshal(sdk.SitemapURLs{Group: "misc"})
	entries := func(n int) []byte {
		return []byte(`{"add":[` + strings.TrimSuffix(strings.Repeat(`{"loc":"https://x.test/a"},`, n), ",") + `]}`)
	}
	got, err := decodeOutput(sdk.SitemapURLs{Group: "misc"}, in, entries(sdk.MaxSitemapAdditions))
	if err != nil || len(got.(sdk.SitemapURLs).Add) != sdk.MaxSitemapAdditions {
		t.Fatalf("at the limit: %v", err)
	}
	if got.(sdk.SitemapURLs).Group != "misc" {
		t.Fatal("a field the plugin left out lost its input value")
	}
	if _, err := decodeOutput(sdk.SitemapURLs{Group: "misc"}, in, entries(sdk.MaxSitemapAdditions+1)); err == nil {
		t.Fatal("more additions than the limit were accepted")
	}
}
