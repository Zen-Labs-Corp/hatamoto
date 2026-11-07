// Package themestrings holds the shared rules for theme UI strings: the
// declaration a theme ships in theme.json, the per-locale overrides an owner
// stores in the site's theme_extra, the fallback chain between them and the
// "{n}" interpolation. It is a leaf package: render, packages and admin all
// import it, it imports none of them.
package themestrings

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"hatamoto.dev/core/internal/jsonx"
)

// ExtraKey is the top-level key of theme_extra that holds the overrides.
const ExtraKey = "themeStrings"

// Limits of a declaration and of a single text.
const (
	MaxKeys    = 500
	MaxTextLen = 300
)

var keyRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Decl maps a string key to its default text, as declared by a theme.
type Decl map[string]string

// ValidKey reports whether k is an acceptable string key.
func ValidKey(k string) bool { return keyRE.MatchString(k) }

// ValidText checks one default or override text: 1..300 runes, plain text
// (no <, > or &) and no control characters.
func ValidText(s string) error {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return errors.New("text is empty")
	}
	if n > MaxTextLen {
		return fmt.Errorf("text is longer than %d characters", MaxTextLen)
	}
	if strings.ContainsAny(s, "<>&") {
		return errors.New("text must not contain <, > or &")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return errors.New("text must not contain control characters")
		}
	}
	return nil
}

// ParseDecl strictly reads a theme.json "strings" value and returns the first
// problem. Absent (empty) input is valid and yields a nil Decl.
func ParseDecl(raw jsontext.Value) (Decl, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var m map[string]string
	if err := jsonx.Unmarshal(raw, &m, jsonx.StoredLoose); err != nil {
		return nil, errors.New(`"strings" must be an object of string to string`)
	}
	if len(m) > MaxKeys {
		return nil, fmt.Errorf(`"strings" has more than %d entries`, MaxKeys)
	}
	for k, v := range m {
		if !ValidKey(k) {
			return nil, fmt.Errorf("string key %q is invalid", k)
		}
		if err := ValidText(v); err != nil {
			return nil, fmt.Errorf("string %q: %w", k, err)
		}
	}
	return Decl(m), nil
}

// Lenient reads a "strings" value and drops entries that ParseDecl would
// refuse. Malformed input yields nil.
func Lenient(raw jsontext.Value) Decl {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]string
	if jsonx.Unmarshal(raw, &m, jsonx.StoredLoose) != nil {
		return nil
	}
	out := Decl{}
	for k, v := range m {
		if len(out) >= MaxKeys {
			break
		}
		if ValidKey(k) && ValidText(v) == nil {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Overrides is the stored document: theme -> locale tag -> key -> text.
type Overrides map[string]map[string]map[string]string

// ParseOverrides reads the ExtraKey entry of theme_extra. Missing or
// malformed data is the zero value.
func ParseOverrides(extra jsontext.Value) Overrides {
	var top map[string]jsontext.Value
	if jsonx.Unmarshal(extra, &top, jsonx.StoredLoose) != nil || len(top[ExtraKey]) == 0 {
		return nil
	}
	var o Overrides
	if jsonx.Unmarshal(top[ExtraKey], &o, jsonx.StoredLoose) != nil {
		return nil
	}
	return o
}

// For returns the overrides of one theme and locale tag (nil when none).
func (o Overrides) For(theme, tag string) map[string]string { return o[theme][tag] }

// Chain flattens the fallback chain for one request: the default-locale
// overrides overlaid by the locale's own. An empty localeTag means the
// default locale. Only declared keys with valid text are kept; the theme
// default is not included (the renderer applies it underneath).
func Chain(o Overrides, theme, localeTag, defaultTag string, decl Decl) map[string]string {
	if localeTag == "" {
		localeTag = defaultTag
	}
	var out map[string]string
	layer := func(m map[string]string) {
		for k, v := range m {
			if _, ok := decl[k]; !ok || ValidText(v) != nil {
				continue
			}
			if out == nil {
				out = map[string]string{}
			}
			out[k] = v
		}
	}
	layer(o.For(theme, defaultTag))
	if localeTag != defaultTag {
		layer(o.For(theme, localeTag))
	}
	return out
}

// Interpolate replaces every "{n}" in s with the first argument. Without
// arguments s is returned unchanged; no other placeholder is interpreted.
func Interpolate(s string, args ...any) string {
	if len(args) == 0 {
		return s
	}
	return strings.ReplaceAll(s, "{n}", fmt.Sprint(args[0]))
}
