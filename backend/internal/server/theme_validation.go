package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/siteconfig"
)

func validColor(v string) bool  { return render.ValidColor(v) }
func validFont(v string) bool   { return render.ValidFont(v) }
func validRadius(v string) bool { return render.ValidRadius(v) }

// validSiteURL reports whether v is empty, an http(s)/mailto/tel URL, a
// protocol-relative or relative URL, a path, a #anchor or a ?query. Any other
// scheme (javascript:, data:, vbscript:, ...) and control characters are
// rejected. Both renderers emit it as an href/src attribute, which is escaped.
func validSiteURL(v string) bool {
	if v == "" {
		return true
	}
	if len(v) > 2048 || strings.ContainsAny(v, " \"'<>\\`") {
		return false
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	u, err := url.Parse(v)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "":
		return true
	case "http", "https":
		return u.Host != ""
	case "mailto", "tel":
		return u.Opaque != ""
	}
	return false
}

// validateThemePayload checks the known theme keys of a PUT body. Unknown
// keys pass through untouched (they are merged, not rendered as CSS), except
// socialLinks whose values are URLs.
func validateThemePayload(sent map[string]json.RawMessage) error {
	var t siteconfig.ThemeConfig
	for _, key := range []string{"colors", "typography", "borderRadius", "headerCta"} {
		raw, ok := sent[key]
		if !ok {
			continue
		}
		var dst any
		switch key {
		case "colors":
			dst = &t.Colors
		case "typography":
			dst = &t.Typography
		case "borderRadius":
			dst = &t.BorderRadius
		default:
			dst = &t.HeaderCTA
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			return fmt.Errorf("invalid %s", key)
		}
	}
	if raw, ok := sent["builder"]; ok && string(raw) != "null" {
		// The design system is read back at render time; refuse what would be dropped.
		blob, _ := json.Marshal(map[string]json.RawMessage{"builder": raw})
		if _, err := builder.ParseDesign(blob); err != nil {
			return fmt.Errorf("invalid builder")
		}
	}
	for name, v := range map[string]string{
		"colors.primary": t.Colors.Primary, "colors.secondary": t.Colors.Secondary,
		"colors.accent": t.Colors.Accent, "colors.background": t.Colors.Background, "colors.text": t.Colors.Text,
	} {
		if !validColor(v) {
			return fmt.Errorf("invalid %s", name)
		}
	}
	for name, v := range map[string]string{
		"typography.fontFamily": t.Typography.FontFamily, "typography.headingFamily": t.Typography.HeadingFamily,
	} {
		if !validFont(v) {
			return fmt.Errorf("invalid %s", name)
		}
	}
	for name, v := range map[string]string{
		"borderRadius.sm": t.BorderRadius.Sm, "borderRadius.md": t.BorderRadius.Md, "borderRadius.lg": t.BorderRadius.Lg,
	} {
		if !validRadius(v) {
			return fmt.Errorf("invalid %s", name)
		}
	}
	if c := t.HeaderCTA; c != nil && !validSiteURL(c.URL) {
		return errors.New("invalid headerCta.url")
	}
	if raw, ok := sent["socialLinks"]; ok {
		var links map[string]string
		if err := json.Unmarshal(raw, &links); err != nil {
			return errors.New("invalid socialLinks")
		}
		for name, u := range links {
			if !validSiteURL(u) {
				return fmt.Errorf("invalid socialLinks.%s", name)
			}
		}
	}
	return nil
}
