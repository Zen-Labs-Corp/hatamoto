package render

import (
	"io/fs"
	"maps"
	"slices"

	"encoding/json/jsontext"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/themestrings"
)

// chainStrings merges the "strings" declared by theme.json of every layer,
// lowest precedence first, so a layer's declaration wins over the same key
// below it. A layer whose theme.json is absent or unreadable adds nothing.
func chainStrings(c Chain) themestrings.Decl {
	var out themestrings.Decl
	for i := len(c) - 1; i >= 0; i-- {
		b, err := fs.ReadFile(c[i], "theme.json")
		if err != nil {
			continue
		}
		var m struct {
			Strings jsontext.Value `json:"strings"`
		}
		if jsonx.Unmarshal(b, &m, jsonx.StoredLoose) != nil {
			continue
		}
		layer := themestrings.Lenient(m.Strings)
		if len(layer) == 0 {
			continue
		}
		if out == nil {
			out = themestrings.Decl{}
		}
		maps.Copy(out, layer)
	}
	return out
}

// resolveStrings lays the request's overrides over the theme defaults. The
// defaults map is shared (read-only) when no override applies; undeclared
// override keys are ignored.
func resolveStrings(def themestrings.Decl, ov map[string]string) map[string]string {
	var out map[string]string
	for k, v := range ov {
		if _, ok := def[k]; !ok {
			continue
		}
		if out == nil {
			out = maps.Clone(def)
		}
		out[k] = v
	}
	if out == nil {
		return def
	}
	return out
}

// tFunc is the "t" template func: {{t . "key"}} or {{t . "min_read" 5}}. v is
// the page data (the template's "." or "$"), a section, or a dict carrying a
// "Strings" entry. Without defaults a missing key, nil map or unknown v
// yields the key itself. Compiled themes use newTFunc instead: there those
// cases yield the theme's default text, so a partial called without "Strings"
// prints the default, never a translation.
// The result is plain text: html/template escapes it where it is printed.
var tFunc = newTFunc(nil)

// newTFunc is tFunc with a fallback: when v carries no Strings map (a default
// partial called the pre-"t" way, without "Strings"), or the map lacks the
// key, the chain's theme default text is used before the key itself.
func newTFunc(def themestrings.Decl) func(any, string, ...any) string {
	return func(v any, key string, args ...any) string {
		s, ok := stringsOf(v)[key]
		if !ok {
			s, ok = def[key]
		}
		if !ok {
			return key
		}
		return themestrings.Interpolate(s, args...)
	}
}

func stringsOf(v any) map[string]string {
	switch p := v.(type) {
	case *pageData:
		return p.Strings
	case pageData:
		return p.Strings
	case *preparedSection:
		return p.Page.Strings
	case preparedSection:
		return p.Page.Strings
	case *sectionPage:
		return p.Strings
	case sectionPage:
		return p.Strings
	case map[string]any:
		m, _ := p["Strings"].(map[string]string)
		return m
	}
	return nil
}

// homeCrumbKey is the theme string that names the home crumb when the owner
// saved no label for it (Settings > SEO > Breadcrumbs) and it is not a locale
// name. The built-in theme declares it as "Home", and every theme inherits it.
const homeCrumbKey = "breadcrumb_home"

// localizeHomeCrumb names the home crumb with the theme string of the page's
// locale when v.HomeCrumbDefault is set, so a site in another language prints
// its own word in the visible trail and the BreadcrumbList JSON-LD (#413).
// It replaces the trail rather than writing into it: the slice may be shared.
func localizeHomeCrumb(v *PageView) {
	if !v.HomeCrumbDefault || len(v.Breadcrumb) == 0 {
		return
	}
	label := v.strs[homeCrumbKey]
	if label == "" || label == v.Breadcrumb[0].Name {
		return
	}
	trail := slices.Clone(v.Breadcrumb)
	trail[0].Name = label
	v.Breadcrumb = trail
}
