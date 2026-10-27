package siteconfig

import (
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

// Theme types relocated from the deleted tenant package (Path B PR 3).
// These are pure shape definitions used by public-site rendering and the
// admin theme editor. They marshal to/from the theme_extra JSONB column
// of hatamoto_site_config.

// ThemeColors holds color design tokens.
type ThemeColors struct {
	Primary    string `json:"primary,omitempty"`
	Secondary  string `json:"secondary,omitempty"`
	Accent     string `json:"accent,omitempty"`
	Background string `json:"background,omitempty"`
	Text       string `json:"text,omitempty"`
}

// ThemeTypography holds typography design tokens.
type ThemeTypography struct {
	FontFamily    string `json:"fontFamily,omitempty"`
	HeadingFamily string `json:"headingFamily,omitempty"`
}

// ThemeBorderRadius holds border radius design tokens.
type ThemeBorderRadius struct {
	Sm string `json:"sm,omitempty"`
	Md string `json:"md,omitempty"`
	Lg string `json:"lg,omitempty"`
}

// ThemeCTA is a call-to-action button (label and target) shown by themes.
type ThemeCTA struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

// ThemeConfig holds all design tokens for CSS variable injection.
type ThemeConfig struct {
	Colors       ThemeColors       `json:"colors"`
	Typography   ThemeTypography   `json:"typography"`
	BorderRadius ThemeBorderRadius `json:"borderRadius"`
	// HeaderCTA is the optional header call-to-action. Nil (omitted from JSON)
	// means the theme renders no header button.
	HeaderCTA *ThemeCTA `json:"headerCta,omitempty"`
}

// DefaultThemeConfig returns the default theme configuration.
func DefaultThemeConfig() ThemeConfig {
	return ThemeConfig{
		Colors: ThemeColors{
			Primary:    "#0ea5e9",
			Secondary:  "#64748b",
			Accent:     "#f59e0b",
			Background: "#ffffff",
			Text:       "#1e293b",
		},
		Typography: ThemeTypography{
			FontFamily:    "Inter, sans-serif",
			HeadingFamily: "Inter, sans-serif",
		},
		BorderRadius: ThemeBorderRadius{
			Sm: "4px",
			Md: "8px",
			Lg: "16px",
		},
	}
}

// Scan implements the sql.Scanner interface for ThemeConfig so callers can
// decode the theme_extra JSONB column directly into the typed shape.
func (t *ThemeConfig) Scan(src any) error {
	if src == nil {
		*t = DefaultThemeConfig()
		return nil
	}
	switch v := src.(type) {
	case []byte:
		if len(v) == 0 {
			*t = DefaultThemeConfig()
			return nil
		}
		return json.Unmarshal(v, t)
	case string:
		if v == "" {
			*t = DefaultThemeConfig()
			return nil
		}
		return json.Unmarshal([]byte(v), t)
	default:
		return errors.New("unsupported type for ThemeConfig")
	}
}

// PublicSettings is the slim, public-facing slice of admin-configurable
// settings rendered into PublicSiteData. Pre-Path-B this lived in the
// tenant package as tenant.TenantSettings; relocated here because all
// fields ultimately derive from hatamoto_site_config.
type PublicSettings struct {
	DefaultEntitySchema string     `json:"default_entity_schema,omitempty"`
	NotificationEmail   string     `json:"notification_email,omitempty"`
	Timezone            string     `json:"timezone,omitempty"`
	HomepageEntityID    *uuid.UUID `json:"homepage_entity_id,omitempty"`
}
