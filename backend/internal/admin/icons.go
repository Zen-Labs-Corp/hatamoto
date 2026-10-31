package admin

import (
	"fmt"
	"html/template"
	"maps"
	"strings"
)

// icons holds 24x24 outline glyphs, 1.5 stroke, Lucide (ISC licence). Everything
// comes from the generated lucideIcons (tools/gen-icons/gen-icons.mjs) except
// the brand mark and the seal, which are drawn by hand or outlined from a font. Inline SVG: no icon font or CDN. Values
// are constants; they are the only template.HTML besides schemaform output.
var icons = func() map[string]string {
	m := maps.Clone(lucideIcons)
	m["mark"] = `<path d="M4 20V8l8-4 8 4v12"/><path d="M9 20v-6h6v6"/>`
	// The seal is the outline of the character 旗 (hata, "flag") from Noto Sans
	// CJK JP Bold 2.004 (SIL Open Font License 1.1, (c) Adobe), fitted to 20 of
	// the 24 units. It is a filled path, not text, so it never needs a CJK font
	// on the viewer's machine.
	m["seal"] = `<path fill="currentColor" stroke="none" d="M12.49 2.21C12.11 3.41 11.49 4.6 10.72 5.57V5.39H7.94V2.21H5.55V5.39H2.44V7.69H4.6V11.16C4.6 13.92 4.31 17.33 2 20.23C2.58 20.63 3.39 21.25 3.83 21.75C6.22 18.78 6.71 15.17 6.78 11.97H8.19C8.11 16.97 8 18.76 7.73 19.22C7.57 19.45 7.42 19.53 7.15 19.53C6.86 19.53 6.34 19.51 5.76 19.45C6.09 20.05 6.32 21 6.36 21.65C7.15 21.69 7.9 21.67 8.4 21.56C8.96 21.46 9.33 21.27 9.73 20.69C10.25 19.92 10.35 17.45 10.45 10.68C10.45 10.39 10.45 9.71 10.45 9.71H6.78V7.69H10.72V7.05C11.12 7.3 11.49 7.59 11.74 7.79C12.2 7.32 12.65 6.74 13.09 6.07H21.65V4.1H14.17C14.36 3.64 14.54 3.18 14.69 2.73ZM16.83 18.8C17.97 19.69 19.45 20.98 20.15 21.79L22 20.63C21.27 19.88 19.96 18.82 18.88 18.01H21.81V16.06H20.01V9.81H21.44V7.9H20.01V6.67H17.72V7.9H14.96V6.67H12.72V7.9H11.12V9.81H12.72V16.06H10.72V18.01H13.42C12.59 18.82 11.3 19.74 10.12 20.26C10.62 20.63 11.33 21.27 11.72 21.69C13.11 21.07 14.75 19.84 15.75 18.76L14.46 18.01H18.18ZM14.96 9.81H17.72V10.81H14.96ZM14.96 12.38H17.72V13.4H14.96ZM14.96 14.96H17.72V16.06H14.96Z"/>`
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
