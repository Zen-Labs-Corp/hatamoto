package staticexport

import (
	"errors"
	"net/url"
	"path"
	"strings"
)

// KnowledgeGraphPath is the one API URL that is exported: llms.txt links it.
const KnowledgeGraphPath = "/api/v1/public/knowledge-graph"

// FilePath maps a URL path to an output-relative file path. An HTML page
// without a file extension becomes <path>/index.html.
func FilePath(urlPath string, isHTML bool) (string, error) {
	p, err := url.PathUnescape(urlPath)
	if err != nil {
		return "", err
	}
	if strings.ContainsRune(p, 0) || strings.Contains(p, `\`) {
		return "", errors.New("unsafe path " + urlPath)
	}
	for seg := range strings.SplitSeq(p, "/") {
		if seg == ".." || strings.HasPrefix(seg, ManifestFile) {
			return "", errors.New("unsafe path " + urlPath)
		}
	}
	trailing := strings.HasSuffix(p, "/")
	clean := strings.Trim(path.Clean("/"+p), "/")
	if clean == "" {
		return "index.html", nil
	}
	if isHTML && (trailing || path.Ext(clean) == "") {
		return clean + "/index.html", nil
	}
	return clean, nil
}

// excludedPrefixes are never fetched, each matched on a segment boundary.
var excludedPrefixes = []string{
	"/auth", "/plugin-ui", "/-", "/api", "/preview", "/health", "/ready",
	"/forms", "/p", "/search", "/locale", "/.well-known",
}

func underPrefix(p, prefix string) bool {
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}

// Excluded reports whether a URL path is never exported: the admin address,
// authentication, plugin and API surfaces, previews, forms and search. The
// knowledge graph and a plugin's own assets are the exceptions.
//
// The path is tested as written and percent-decoded, because the router
// matches the decoded form (/%61dmin is /admin). A path that does not decode
// is excluded.
func Excluded(p, adminPrefix string) bool {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	decoded, err := url.PathUnescape(p)
	if err != nil {
		return true
	}
	return decoded != p && excluded(decoded, adminPrefix) || excluded(p, adminPrefix)
}

func excluded(p, adminPrefix string) bool {
	if p == KnowledgeGraphPath {
		return false
	}
	if adminPrefix != "" && adminPrefix != "/" && underPrefix(p, adminPrefix) {
		return true
	}
	if rest, ok := strings.CutPrefix(p, "/p/"); ok {
		_, after, _ := strings.Cut(rest, "/")
		return !strings.HasPrefix(after, "assets/")
	}
	for _, x := range excludedPrefixes {
		if underPrefix(p, x) {
			return true
		}
	}
	return false
}

// assetPrefixes are the directories of files that core links with a
// cache-busting query.
var assetPrefixes = []string{"/theme/assets", "/fonts", "/media"}

// isAssetPath reports whether a URL path is a static file under one of the
// asset directories.
func isAssetPath(p string) bool {
	for _, x := range assetPrefixes {
		if strings.HasPrefix(p, x+"/") {
			return true
		}
	}
	return false
}

// QueryKind says whether a URL with this raw query is skipped, and why. A
// file path cannot carry a query, so every query URL is skipped; the reason
// names the known cases. Asset URLs are exported by path before this is asked.
func QueryKind(rawQuery string) (skip bool, reason string) {
	if rawQuery == "" {
		return false, ""
	}
	q, err := url.ParseQuery(rawQuery)
	if err == nil {
		if _, ok := q["page"]; ok {
			return true, "?page=N"
		}
		if _, ok := q["schema"]; ok {
			return true, "?schema="
		}
	}
	return true, "query string"
}
