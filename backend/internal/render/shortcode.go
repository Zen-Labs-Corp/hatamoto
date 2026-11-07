package render

import (
	"bytes"
	"html/template"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/shortcode"
)

// expandShortcodes expands the shortcodes of already sanitised rich text. It
// must run after the sanitiser: the markup a shortcode produces (a <form>) is
// not something the rich-text policy lets through. A nil view leaves the text
// alone (a render path that has no page data).
func expandShortcodes(sanitised string, v *PageView) template.HTML {
	if v != nil {
		sanitised = shortcode.Expand(sanitised, shortcodeRegistry(v))
	}
	return template.HTML(sanitised) // #nosec G203 -- sanitised by the caller, handlers emit template-escaped widget markup
}

// shortcodeRegistry is where a second shortcode name is added.
func shortcodeRegistry(v *PageView) shortcode.Registry {
	return shortcode.Registry{
		"form": func(attrs map[string]string) (string, bool, bool) {
			html, ok := formShortcode(v, attrs["slug"])
			return html, true, ok
		},
	}
}

// formShortcode renders the form widget for slug, the same markup a form node
// gives. A paused, archived or unknown form (absent from v.Forms) renders
// nothing; the canvas gets the widget's placeholder.
func formShortcode(v *PageView, slug string) (string, bool) {
	ws := widgets()
	t, prep := ws.tmpl["form"], ws.prep["form"]
	if t == nil || prep == nil || slug == "" {
		return "", false
	}
	n := &builder.Node{ID: "sc-" + slug, T: "form", P: map[string]any{"slug": slug}}
	ctx := &widgetCtx{View: v, N: n, P: n.P, Canvas: v.Canvas, Class: "hm-form"}
	x, err := prep(ctx)
	if err != nil {
		return "", false
	}
	var ab bytes.Buffer
	nodeAttrs(&ab, v, n, ctx.Class, false)
	var out bytes.Buffer
	d := widgetData{P: n.P, X: x, Attrs: template.HTMLAttr(ab.String())} // #nosec G203 -- class from the registry, id from a validated slug
	if err := t.Execute(&out, d); err != nil {
		return "", false
	}
	if _, found := v.Forms[slug]; found {
		v.formIsland = true // the page needs the form's CSS and island
	}
	return out.String(), true
}
