package render

import (
	"io/fs"
	"maps"

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
// "Strings" entry. A missing key, nil map or unknown v yields the key itself.
// The result is plain text: html/template escapes it where it is printed.
func tFunc(v any, key string, args ...any) string {
	var m map[string]string
	switch p := v.(type) {
	case *pageData:
		m = p.Strings
	case pageData:
		m = p.Strings
	case *preparedSection:
		m = p.Page.Strings
	case preparedSection:
		m = p.Page.Strings
	case *sectionPage:
		m = p.Strings
	case sectionPage:
		m = p.Strings
	case map[string]any:
		m, _ = p["Strings"].(map[string]string)
	}
	s, ok := m[key]
	if !ok {
		return key
	}
	return themestrings.Interpolate(s, args...)
}
