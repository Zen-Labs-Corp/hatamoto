package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync/atomic"
	"time"
)

func init() {
	// Not in Go's built-in table; the embedded Geist fonts need the right type.
	_ = mime.AddExtensionType(".woff2", "font/woff2")
	_ = mime.AddExtensionType(".ico", "image/x-icon")
}

// publicAPIPrefix, when set, replaces /api/v1/public (and /api/v1/track) in the
// served hm.js. Set once at boot, before the engine is built.
var publicAPIPrefix atomic.Pointer[string]

// SetPublicAPIPrefix makes hm.js call prefix instead of /api/v1/public, and
// prefix+"/track" instead of /api/v1/track. "" restores the defaults.
func SetPublicAPIPrefix(prefix string) {
	if prefix == "" {
		publicAPIPrefix.Store(nil)
		return
	}
	publicAPIPrefix.Store(&prefix)
}

// rewriteAPIPrefix points the default API paths in hm.js at the configured prefix.
func rewriteAPIPrefix(js []byte) []byte {
	p := publicAPIPrefix.Load()
	if p == nil {
		return js
	}
	js = bytes.ReplaceAll(js, []byte("/api/v1/public/"), []byte(*p+"/"))
	return bytes.ReplaceAll(js, []byte("/api/v1/track"), []byte(*p+"/track"))
}

// trackEngagementFn reports whether the served beacon may send engagement
// events; nil means yes.
var trackEngagementFn atomic.Pointer[func() bool]

// SetTrackEngagement installs the engagement opt-out: when f returns false the
// served hm-track.js is rendered without engagement events. The script is
// generated per request (5 minute cache), so a change shows within the cache
// lifetime. nil restores the default (engagement on).
func SetTrackEngagement(f func() bool) {
	if f == nil {
		trackEngagementFn.Store(nil)
		return
	}
	trackEngagementFn.Store(&f)
}

func trackEngagement() bool {
	if f := trackEngagementFn.Load(); f != nil {
		return (*f)()
	}
	return true
}

// hasLegacyBeacon reports whether a theme's hm.js (after the API prefix
// rewrite) still posts the pre-core pageview beacon.
func hasLegacyBeacon(js []byte) bool {
	if bytes.Contains(js, []byte("/api/v1/track")) {
		return true
	}
	p := publicAPIPrefix.Load()
	return p != nil && bytes.Contains(js, []byte(*p+"/track"))
}

// AssetPrefix is the URL prefix under which AssetHandler serves theme assets.
const AssetPrefix = "/theme/assets/"

// IconDir is the theme asset directory holding the site icons the server also
// answers at the site root (/favicon.ico, /apple-icon.png, ...). A theme
// overrides them by shipping its own files there.
const IconDir = "icons/"

// RootIcons are the root paths served from IconDir, without the leading slash.
var RootIcons = []string{"favicon.ico", "favicon-16.png", "favicon-32.png", "apple-icon.png", "icon-192.png", "icon-512.png"}

const (
	cacheImmutable = "public, max-age=31536000, immutable"
	cacheShort     = "public, max-age=300"
	// cacheIcons is for the unfingerprinted site icons: they change rarely,
	// and the ETag revalidates them when they do.
	cacheIcons = "public, max-age=2592000"
)

// buildBundle concatenates assets/css/base.css, the other sorted
// assets/css/*.css (wp-compat.css, blog.css, ...) and the sorted
// assets/css/sections/*.css into one fingerprinted stylesheet, and returns the
// JS entry point assets/js/hm.js. cssName is theme.<hash>.css.
func buildBundle(t *Theme) (css []byte, js []byte, cssName string) {
	if b, err := t.chain.ReadFile("assets/css/base.css"); err == nil {
		css = append(css, b...)
		css = append(css, '\n')
	}
	extra, _ := t.chain.Glob("assets/css/*.css")
	sections, _ := t.chain.Glob("assets/css/sections/*.css")
	for _, names := range [][]string{extra, sections} {
		for _, n := range names {
			if n == "assets/css/base.css" {
				continue
			}
			if b, err := t.chain.ReadFile(n); err == nil {
				css = append(css, b...)
				css = append(css, '\n')
			}
		}
	}
	js, _ = t.chain.ReadFile("assets/js/hm.js")
	js = rewriteAPIPrefix(js)
	sum := sha256.Sum256(css)
	return css, js, "theme." + hex.EncodeToString(sum[:])[:12] + ".css"
}

// AssetHandler serves the theme's assets under AssetPrefix: the fingerprinted
// CSS bundle (immutable cache), hm.js and any other file below assets/ from
// the lookup chain (5 minute cache). Unknown paths and directories are 404.
func (t *Theme) AssetHandler() http.Handler {
	css, js, cssName := buildBundle(t)
	return &assetHandler{theme: t, css: css, js: js, cssName: cssName}
}

type assetHandler struct {
	theme   *Theme
	css, js []byte
	cssName string
}

func (h *assetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, AssetPrefix)
	name = strings.TrimPrefix(name, "/")
	if name == "" || !fs.ValidPath(name) {
		http.NotFound(w, r)
		return
	}
	switch {
	case name == h.cssName:
		h.serve(w, r, name, h.css, cacheImmutable)
	case name == TrackScriptName:
		h.serve(w, r, name, trackScript(trackEngagement()), cacheShort)
	case name == "hm.js" && len(h.js) > 0:
		h.serve(w, r, name, h.js, cacheShort)
	case strings.HasPrefix(name, IslandPrefix):
		js, ok := widgets().islandByName(strings.TrimPrefix(name, IslandPrefix))
		if !ok {
			http.NotFound(w, r)
			return
		}
		h.serve(w, r, name, js, cacheImmutable)
	default:
		b, err := h.theme.chain.ReadFile("assets/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		cc := cacheShort
		if strings.HasPrefix(name, IconDir) {
			cc = cacheIcons
		}
		h.serve(w, r, name, b, cc)
	}
}

func (h *assetHandler) serve(w http.ResponseWriter, r *http.Request, name string, body []byte, cacheControl string) {
	sum := sha256.Sum256(body)
	hdr := w.Header()
	hdr.Set("ETag", `"`+hex.EncodeToString(sum[:])[:16]+`"`)
	hdr.Set("Cache-Control", cacheControl)
	hdr.Set("X-Content-Type-Options", "nosniff")
	// Public static files; the sandboxed canvas iframe has an opaque origin
	// and needs CORS to load fonts.
	hdr.Set("Access-Control-Allow-Origin", "*")
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		hdr.Set("Content-Type", ct)
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
}
