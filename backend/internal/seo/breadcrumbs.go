package seo

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxCrumbHomeLabel caps the home crumb label, in characters.
const MaxCrumbHomeLabel = 40

// CrumbSeparators lists the separators the breadcrumb trail may use. An
// empty separator keeps the theme default.
var CrumbSeparators = []string{"/", "›", "»", ">", "•", "·", "-"}

// Breadcrumbs holds the site-wide breadcrumb options. The zero value
// reproduces the historic output.
type Breadcrumbs struct {
	HomeLabel   string `json:"home_label,omitempty"`  // "" => "Home"
	Separator   string `json:"separator,omitempty"`   // "" => theme default
	HideHome    bool   `json:"hide_home,omitzero"`    // visible trail only
	HideCurrent bool   `json:"hide_current,omitzero"` // visible trail only
	ShowTerm    bool   `json:"show_term,omitzero"`    // add the primary term crumb
}

// IsZero reports whether no breadcrumb setting is saved.
func (b Breadcrumbs) IsZero() bool { return b == Breadcrumbs{} }

// Home is the label of the home crumb.
func (b Breadcrumbs) Home() string {
	if b.HomeLabel == "" {
		return "Home"
	}
	return b.HomeLabel
}

// validateBreadcrumbs checks the breadcrumbs key.
func validateBreadcrumbs(b Breadcrumbs) []FieldError {
	var errs []FieldError
	if utf8.RuneCountInString(b.HomeLabel) > MaxCrumbHomeLabel {
		errs = append(errs, FieldError{"breadcrumbs[home_label]", "Use 40 characters or fewer."})
	} else if strings.ContainsFunc(b.HomeLabel, unicode.IsControl) {
		errs = append(errs, FieldError{"breadcrumbs[home_label]", "Remove control characters."})
	}
	if b.Separator != "" && !containsString(CrumbSeparators, b.Separator) {
		errs = append(errs, FieldError{"breadcrumbs[separator]", "Choose one of the listed separators."})
	}
	return errs
}
