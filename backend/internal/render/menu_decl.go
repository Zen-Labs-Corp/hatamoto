package render

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits of a theme's menu declaration. Anything past them is dropped.
const (
	maxMenuPresets = 24
	maxMenuClasses = 64
	maxPresetLabel = 40
)

var (
	menuPresetIDRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
	menuClassRe    = regexp.MustCompile(`^-?[_a-zA-Z][_a-zA-Z0-9-]*$`)
	menuSwatchRe   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// MenuPreset is one choice a theme offers for a menu item setting. Items store
// the ID; the theme's Class is applied when the menu is rendered, so switching
// themes never leaves a stale class behind.
type MenuPreset struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Class  string `json:"class,omitempty"`  // space separated class names; unused for icons
	Swatch string `json:"swatch,omitempty"` // #rrggbb, colours only
}

// MenuDecl is what a theme declares for menu items (theme.json "menu"): the
// styles, colours, badges and icons the editor offers and the class names it
// suggests. The zero value means the theme declares nothing.
type MenuDecl struct {
	Styles  []MenuPreset `json:"styles,omitempty"`
	Colors  []MenuPreset `json:"colors,omitempty"`
	Badges  []MenuPreset `json:"badges,omitempty"`
	Icons   []MenuPreset `json:"icons,omitempty"`
	Classes []string     `json:"classes,omitempty"`
}

// IsZero reports whether the theme declares nothing.
func (d MenuDecl) IsZero() bool {
	return len(d.Styles)+len(d.Colors)+len(d.Badges)+len(d.Icons)+len(d.Classes) == 0
}

// ClassFor is the class of the preset with the given id: kind is "style",
// "color" or "badge". It is empty when the theme does not declare the id.
func (d MenuDecl) ClassFor(kind, id string) string {
	var list []MenuPreset
	switch kind {
	case "style":
		list = d.Styles
	case "color":
		list = d.Colors
	case "badge":
		list = d.Badges
	}
	for _, p := range list {
		if p.ID == id {
			return p.Class
		}
	}
	return ""
}

// parseMenuDecl reads the "menu" object of theme.json. It never fails: a
// malformed value or entry is dropped, so a theme with a typo still loads.
func parseMenuDecl(raw json.RawMessage) MenuDecl {
	var m struct {
		Styles  []MenuPreset `json:"styles"`
		Colors  []MenuPreset `json:"colors"`
		Badges  []MenuPreset `json:"badges"`
		Icons   []MenuPreset `json:"icons"`
		Classes []string     `json:"classes"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return MenuDecl{}
	}
	return MenuDecl{
		Styles:  cleanPresets(m.Styles, true, false),
		Colors:  cleanPresets(m.Colors, true, true),
		Badges:  cleanPresets(m.Badges, true, false),
		Icons:   cleanPresets(m.Icons, false, false),
		Classes: cleanClassList(m.Classes),
	}
}

func cleanPresets(in []MenuPreset, needClass, swatch bool) []MenuPreset {
	var out []MenuPreset
	seen := map[string]bool{}
	for _, p := range in {
		if len(out) == maxMenuPresets {
			break
		}
		p.Label = strings.TrimSpace(p.Label)
		classes, classOK := cleanClassString(p.Class)
		switch {
		case !menuPresetIDRe.MatchString(p.ID), seen[p.ID]:
			continue
		case p.Label == "", utf8.RuneCountInString(p.Label) > maxPresetLabel, strings.IndexFunc(p.Label, unicode.IsControl) >= 0:
			continue
		case !classOK, needClass && classes == "":
			continue
		case swatch && !menuSwatchRe.MatchString(p.Swatch), !swatch && p.Swatch != "":
			continue
		}
		seen[p.ID] = true
		p.Class = classes
		if !needClass {
			p.Class = ""
		}
		out = append(out, p)
	}
	return out
}

// cleanClassString checks every class name of a space separated list and
// returns it with single spaces.
func cleanClassString(s string) (string, bool) {
	fields := strings.Fields(s)
	for _, c := range fields {
		if !menuClassRe.MatchString(c) {
			return "", false
		}
	}
	return strings.Join(fields, " "), true
}

func cleanClassList(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range in {
		if len(out) == maxMenuClasses {
			break
		}
		if !menuClassRe.MatchString(c) || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}
