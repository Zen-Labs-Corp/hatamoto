package render

import (
	"encoding/json"
	"html"
	"html/template"
	"sync"
)

// svgAttrs is what every icon svg shares: 24-unit stroke glyphs that take the
// text colour.
const svgAttrs = `xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"`

var (
	iconSet     map[string]string // name -> inner svg paths, from widgets/icon/icons.json
	iconSetOnce sync.Once
)

// icons returns the closed icon set; the builder schema enum lists the same names.
func icons() map[string]string {
	iconSetOnce.Do(func() {
		raw, err := widgetFS.ReadFile("widgets/icon/icons.json")
		if err == nil {
			err = json.Unmarshal(raw, &iconSet)
		}
		if err != nil {
			panic("render: widgets/icon/icons.json: " + err.Error())
		}
	})
	return iconSet
}

// iconPaths is the inner markup of an icon; "" for a name outside the set. The
// data is embedded, never user input.
func iconPaths(name string) template.HTML {
	return template.HTML(icons()[name]) // #nosec G203 -- embedded glyph data, keyed by an allowlist
}

// iconInline is a complete decorative svg, for use inside another widget.
func iconInline(name, class string) template.HTML {
	p := icons()[name]
	if p == "" {
		return ""
	}
	return template.HTML(`<svg class="` + html.EscapeString(class) + `" ` + svgAttrs + ` aria-hidden="true">` + p + `</svg>`) // #nosec G203 -- embedded glyph data
}

func init() {
	registerWidget("icon", func(w *widgetCtx) (any, error) {
		w.Class = "hm-icon"
		return struct {
			SVG   template.HTMLAttr
			Paths template.HTML
			Label string
		}{
			SVG:   template.HTMLAttr(svgAttrs), // #nosec G203 -- constant
			Paths: iconPaths(str(w.P, "name")),
			Label: str(w.P, "label"),
		}, nil
	})
}
