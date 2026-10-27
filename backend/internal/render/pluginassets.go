package render

import (
	"html/template"
	"sort"
	"strings"
	"sync"
)

// pluginAssetSet is the public files of installed plugins that every page
// loads: one stylesheet and one script each, at most. The URLs are the
// plugin's own /p/<plugin>/assets/... paths.
type pluginAssetSet struct {
	mu      sync.RWMutex
	byOwner map[string]pluginAssetLinks
	// scriptsAllowed says whether a plugin's script may be linked. With no
	// gate installed nothing is: a script is never linked by default.
	scriptsAllowed func(plugin string) bool
}

type pluginAssetLinks struct{ css, js string }

var defaultPluginAssets = &pluginAssetSet{byOwner: map[string]pluginAssetLinks{}}

// SetPluginAssets makes pages load the plugin's stylesheet and script (either
// may be ""). The URLs must be site-relative paths; others are ignored.
func SetPluginAssets(plugin, cssURL, jsURL string) {
	defaultPluginAssets.mu.Lock()
	defer defaultPluginAssets.mu.Unlock()
	l := pluginAssetLinks{css: localPath(cssURL), js: localPath(jsURL)}
	if l == (pluginAssetLinks{}) {
		delete(defaultPluginAssets.byOwner, plugin)
		return
	}
	defaultPluginAssets.byOwner[plugin] = l
}

// SetPluginScriptGate installs the check pages make, on every render, before
// linking a plugin's script: the owner's site.scripts grant. The stylesheet
// is not gated.
func SetPluginScriptGate(allowed func(plugin string) bool) {
	defaultPluginAssets.mu.Lock()
	defer defaultPluginAssets.mu.Unlock()
	defaultPluginAssets.scriptsAllowed = allowed
}

func localPath(u string) string {
	if strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") && !strings.ContainsAny(u, "\\\"'<>\r\n ") {
		return u
	}
	return ""
}

// pluginAssetMarkup returns the PluginHead and PluginBody slots: links the
// host builds from its own validated paths, escaped, in plugin-name order.
// With scripts false (editor frames) only the stylesheet links are returned.
func pluginAssetMarkup(scripts bool) (head, body template.HTML) {
	s := defaultPluginAssets
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.byOwner))
	for n := range s.byOwner {
		names = append(names, n)
	}
	sort.Strings(names)
	var h, b strings.Builder
	for _, n := range names {
		l := s.byOwner[n]
		if l.css != "" {
			h.WriteString(`<link rel="stylesheet" href="` + template.HTMLEscapeString(l.css) + `">` + "\n")
		}
		if scripts && l.js != "" && s.scriptsAllowed != nil && s.scriptsAllowed(n) {
			b.WriteString(`<script src="` + template.HTMLEscapeString(l.js) + `" defer></script>` + "\n")
		}
	}
	return template.HTML(h.String()), template.HTML(b.String()) // #nosec G203 -- built from validated local paths, escaped
}

// pluginScriptsAllowed returns the installed script gate (nil denies).
func pluginScriptsAllowed() func(string) bool {
	defaultPluginAssets.mu.RLock()
	defer defaultPluginAssets.mu.RUnlock()
	return defaultPluginAssets.scriptsAllowed
}
