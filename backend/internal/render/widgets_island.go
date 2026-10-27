package render

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"html/template"
	"slices"
	"strings"
)

// IslandPrefix is the path below AssetPrefix under which widget islands are served.
const IslandPrefix = "builder/"

// lightboxKind names the lightbox island in builderCSS.kinds and widgetSet.island.
// It is not a widget kind: no template, CSS file or palette entry.
const lightboxKind = "lightbox"

//go:embed islands/lightbox.js
var lightboxJS []byte

// widgetIsland is a widget's island script, served under a content-hash name
// so it can be cached forever.
type widgetIsland struct {
	name string // <kind>.<hash8>.js
	body []byte
	// integrity is the sha256-<base64> of body, set for plugin islands only
	// (the tag of an embedded island stays as it always was).
	integrity string
}

func newWidgetIsland(kind string, js []byte) widgetIsland {
	sum := sha256.Sum256(js)
	return widgetIsland{name: kind + "." + hex.EncodeToString(sum[:])[:8] + ".js", body: js}
}

// islandByName finds the island served as name (below IslandPrefix).
func (ws *widgetSet) islandByName(name string) ([]byte, bool) {
	for _, is := range ws.island {
		if is.name == name {
			return is.body, true
		}
	}
	return nil, false
}

// islandTags is the <script type="module"> markup for the islands of the
// present kinds, in registry order. Public renders only; same-origin, no inline code.
func (ws *widgetSet) islandTags(present map[string]bool) template.HTML {
	var b strings.Builder
	for _, k := range append(slices.Clone(ws.kinds), lightboxKind) {
		if is, ok := ws.island[k]; ok && present[k] {
			integrity := ""
			if is.integrity != "" {
				integrity = ` integrity="` + is.integrity + `"`
			}
			b.WriteString(`<script type="module" src="` + AssetPrefix + IslandPrefix + is.name + `"` + integrity + `></script>` + "\n")
		}
	}
	return template.HTML(b.String()) // #nosec G203 -- fixed prefix, hex names and base64 digests
}
