package admin

import (
	"fmt"
	"html/template"
	"maps"
	"strings"
)

// icons holds 24x24 outline glyphs, 1.5 stroke, Lucide (ISC licence). Everything
// comes from the generated lucideIcons (tools/gen-icons/gen-icons.mjs) except
// the brand mark, which is hand-drawn. Inline SVG: no icon font or CDN. Values
// are constants; they are the only template.HTML besides schemaform output.
var icons = func() map[string]string {
	m := maps.Clone(lucideIcons)
	m["mark"] = `<path d="M4 20V8l8-4 8 4v12"/><path d="M9 20v-6h6v6"/>`
	return m
}()

// iconSVG renders an inline, decorative SVG (aria-hidden; the control that
// holds it carries the accessible name). An unknown name is an error, so the
// template render fails instead of silently dropping the glyph.
func iconSVG(name string) (template.HTML, error) {
	body, ok := icons[name]
	if !ok {
		return "", fmt.Errorf("admin: unknown icon %q", name)
	}
	var b strings.Builder
	b.WriteString(`<svg class="hm-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">`)
	b.WriteString(body)
	b.WriteString(`</svg>`)
	return template.HTML(b.String()), nil //nolint:gosec // constants above
}
