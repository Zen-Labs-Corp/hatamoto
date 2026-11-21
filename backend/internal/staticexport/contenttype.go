package staticexport

import (
	"path"
	"regexp"
	"strings"
)

// The table is our own on purpose: mime.TypeByExtension reads OS files.
var contentTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".json":  "application/json",
	".xml":   "application/xml",
	".xsl":   "text/xsl",
	".txt":   "text/plain; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".avif":  "image/avif",
	".ico":   "image/x-icon",
	".woff2": "font/woff2",
	".woff":  "font/woff",
	".pdf":   "application/pdf",
}

// ContentType returns the media type for a path by extension, or
// application/octet-stream.
func ContentType(p string) string {
	if t, ok := contentTypes[strings.ToLower(path.Ext(p))]; ok {
		return t
	}
	return "application/octet-stream"
}

const (
	cacheImmutable = "public, max-age=31536000, immutable"
	cacheShort     = "public, max-age=300"
	cacheDay       = "public, max-age=86400"
)

var (
	fingerprintedAsset = regexp.MustCompile(`(^|/)theme\.[0-9a-f]+\.(css|js)$`)
	fingerprintedFont  = regexp.MustCompile(`(^|/)fonts/[0-9a-f]{64}\.woff2$`)
)

// CacheControl returns the Cache-Control value for an output path:
// immutable for fingerprinted assets and fonts, five minutes for documents
// that change with the content, a day for everything else.
func CacheControl(p string) string {
	p = strings.TrimPrefix(p, "/")
	if fingerprintedAsset.MatchString(p) || fingerprintedFont.MatchString(p) {
		return cacheImmutable
	}
	switch strings.ToLower(path.Ext(p)) {
	case ".html", ".xml", ".txt", ".json", ".xsl":
		return cacheShort
	}
	if p == "_redirects" || p == ManifestFile || p == strings.TrimPrefix(KnowledgeGraphPath, "/") {
		return cacheShort
	}
	return cacheDay
}
