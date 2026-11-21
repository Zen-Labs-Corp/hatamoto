package staticexport

import (
	"strings"
	"testing"
)

func TestContentType(t *testing.T) {
	for p, want := range map[string]string{
		"index.html":       "text/html; charset=utf-8",
		"a/theme.css":      "text/css; charset=utf-8",
		"sitemap.xsl":      "text/xsl",
		"fonts/x.woff2":    "font/woff2",
		"media/A.JPG":      "image/jpeg",
		"feed.xml":         "application/xml",
		"api/v1/public/kg": "application/octet-stream",
		"unknown.zzz":      "application/octet-stream",
	} {
		if got := ContentType(p); got != want {
			t.Errorf("ContentType(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestCacheControl(t *testing.T) {
	font := "fonts/" + strings.Repeat("a", 64) + ".woff2"
	for p, want := range map[string]string{
		"theme/assets/theme.abc123.css": cacheImmutable,
		"theme/assets/theme.abc123.js":  cacheImmutable,
		font:                            cacheImmutable,
		"fonts/short.woff2":             cacheDay,
		"index.html":                    "public, max-age=300",
		"feed.xml":                      "public, max-age=300",
		"_redirects":                    "public, max-age=300",
		ManifestFile:                    "public, max-age=300",
		"api/v1/public/knowledge-graph": "public, max-age=300",
		"media/x.jpg":                   "public, max-age=86400",
	} {
		if got := CacheControl(p); got != want {
			t.Errorf("CacheControl(%q) = %q, want %q", p, got, want)
		}
	}
}
