// Package consent holds the cookie consent settings, the category-set version
// that decides when a stored choice is stale, the region rule and the daily
// choice counter. It stores no visitor data.
package consent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Limits on the stored values.
const (
	MaxTitleLen = 80
	MaxTextLen  = 500
)

// Region modes.
const (
	RegionAll = "all"
	RegionEU  = "eu_uk"
)

// Choice values the counter accepts.
const (
	ChoiceAll    = "all"
	ChoiceNone   = "none"
	ChoiceCustom = "custom"
)

// Categories lists the optional categories a visitor can decide on, in display order.
var Categories = []string{"analytics", "marketing", "functional"}

// Settings is the owner's cookie consent configuration.
type Settings struct {
	Enabled    bool     `json:"enabled"`
	Region     string   `json:"region"`
	Title      string   `json:"title"`
	Text       string   `json:"text"`
	Categories []string `json:"categories"`
}

// FieldError names a setting and what is wrong with it.
type FieldError struct {
	Field   string
	Message string
}

// Defaults is what a site gets before anyone saves the page: banner off.
func Defaults() Settings {
	return Settings{
		Enabled:    false,
		Region:     RegionAll,
		Title:      DefaultStrings["title"],
		Text:       DefaultStrings["text"],
		Categories: append([]string(nil), Categories...),
	}
}

// Decode parses a stored document. Missing keys keep their defaults, so a
// row written by an older version still reads.
func Decode(raw []byte) (Settings, error) {
	s := Defaults()
	if len(raw) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return Defaults(), fmt.Errorf("decode consent settings: %w", err)
	}
	return s, nil
}

// Validate checks every field and returns one error per problem.
func (s Settings) Validate() []FieldError {
	var errs []FieldError
	if s.Region != RegionAll && s.Region != RegionEU {
		errs = append(errs, FieldError{"region", "Choose where the banner shows."})
	}
	if utf8.RuneCountInString(s.Title) > MaxTitleLen {
		errs = append(errs, FieldError{"title", fmt.Sprintf("The title can be at most %d characters.", MaxTitleLen)})
	}
	if utf8.RuneCountInString(s.Text) > MaxTextLen {
		errs = append(errs, FieldError{"text", fmt.Sprintf("The text can be at most %d characters.", MaxTextLen)})
	}
	seen := map[string]bool{}
	for _, c := range s.Categories {
		if !contains(Categories, c) {
			errs = append(errs, FieldError{"categories", "Unknown category " + c + "."})
			break
		}
		if seen[c] {
			errs = append(errs, FieldError{"categories", "Each category can be listed once."})
			break
		}
		seen[c] = true
	}
	return errs
}

// Version identifies the enabled category set: a stored choice made under a
// different set is asked again. It is the first 8 hex digits of a hash of the
// sorted category list.
func (s Settings) Version() string {
	c := append([]string(nil), s.Categories...)
	sort.Strings(c)
	sum := sha256.Sum256([]byte(strings.Join(c, ",")))
	return hex.EncodeToString(sum[:])[:8]
}

// EUUK lists the ISO country codes the "EU/UK" region mode covers: the 27 EU
// states, Iceland, Liechtenstein, Norway and the United Kingdom.
var EUUK = map[string]bool{
	"AT": true, "BE": true, "BG": true, "HR": true, "CY": true, "CZ": true, "DK": true,
	"EE": true, "FI": true, "FR": true, "DE": true, "GR": true, "HU": true, "IE": true,
	"IT": true, "LV": true, "LT": true, "LU": true, "MT": true, "NL": true, "PL": true,
	"PT": true, "RO": true, "SK": true, "SI": true, "ES": true, "SE": true,
	"IS": true, "LI": true, "NO": true, "GB": true,
}

// RegionShow reports whether the banner shows for a visitor from country (an
// ISO code from a CDN header). Mode "all" always shows; "eu_uk" shows for the
// listed countries and when the country is unknown.
func RegionShow(mode, country string) bool {
	if mode != RegionEU {
		return true
	}
	country = strings.ToUpper(strings.TrimSpace(country))
	if country == "" {
		return true
	}
	return EUUK[country]
}

// DefaultStrings are the built-in labels, keyed by id. The owner's title and
// text override "title" and "text" only.
var DefaultStrings = map[string]string{
	"title":           "Cookies on this site",
	"text":            "We use cookies to measure how the site is used and to improve it. You can accept all, reject all or choose.",
	"accept":          "Accept all",
	"reject":          "Reject all",
	"preferences":     "Preferences",
	"save":            "Save choices",
	"close":           "Close",
	"dialog_title":    "Cookie preferences",
	"necessary":       "Necessary",
	"necessary_desc":  "Needed for the site to work. Always on.",
	"analytics":       "Analytics",
	"analytics_desc":  "Helps us understand how the site is used.",
	"marketing":       "Marketing",
	"marketing_desc":  "Used to show relevant ads and measure them.",
	"functional":      "Functional",
	"functional_desc": "Remembers your settings and preferences.",
	"banner_label":    "Cookie notice",
	"reopen":          "Cookie preferences",
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
