package menu

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrInvalidAttrs is returned (wrapped, with the reason) when the per-item
// settings or the CSS classes of a menu item are not acceptable.
var ErrInvalidAttrs = errors.New("invalid menu item settings")

const (
	maxAttrLen    = 100
	maxClassesLen = 200
)

var (
	// A preset id as a theme declares it; also the shape of a stored Style, Color and Icon.
	presetIDRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
	htmlIDRe   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_:.-]{0,99}$`)
)

// allowedRel lists the link relations an item may carry.
var allowedRel = map[string]bool{"nofollow": true, "sponsored": true, "ugc": true, "me": true}

// MenuItemAttrs are the per-item settings beyond the link itself. Style,
// Color and Icon hold preset ids declared by the theme (an id the active theme
// does not know is kept and ignored when rendering); everything else is
// validated text.
type MenuItemAttrs struct {
	Style     string   `json:"style,omitempty"`
	Color     string   `json:"color,omitempty"`
	Badge     string   `json:"badge,omitempty"`
	Icon      string   `json:"icon,omitempty"`
	ShowOn    string   `json:"show_on,omitempty"` // "", desktop or mobile ("all" is stored as "")
	ID        string   `json:"id,omitempty"`
	Title     string   `json:"title,omitempty"`
	AriaLabel string   `json:"aria_label,omitempty"`
	Rel       []string `json:"rel,omitempty"`
}

// Normalize returns the attrs in their stored form: show_on "all" becomes "".
func (a MenuItemAttrs) Normalize() MenuItemAttrs {
	if a.ShowOn == "all" {
		a.ShowOn = ""
	}
	return a
}

// Validate checks every field against its limit.
func (a MenuItemAttrs) Validate() error {
	for _, f := range []struct{ name, val string }{{"style", a.Style}, {"color", a.Color}, {"icon", a.Icon}, {"badge", a.Badge}} {
		if f.val != "" && !presetIDRe.MatchString(f.val) {
			return fmt.Errorf("%w: %s must be 1 to 32 lowercase letters, digits or dashes", ErrInvalidAttrs, f.name)
		}
	}
	for _, f := range []struct{ name, val string }{{"title", a.Title}, {"aria label", a.AriaLabel}} {
		if utf8.RuneCountInString(f.val) > maxAttrLen {
			return fmt.Errorf("%w: %s is longer than %d characters", ErrInvalidAttrs, f.name, maxAttrLen)
		}
		if strings.IndexFunc(f.val, unicode.IsControl) >= 0 {
			return fmt.Errorf("%w: %s has control characters", ErrInvalidAttrs, f.name)
		}
	}
	if a.ID != "" && !htmlIDRe.MatchString(a.ID) {
		return fmt.Errorf("%w: id must start with a letter and use at most 100 letters, digits or _ : . -", ErrInvalidAttrs)
	}
	switch a.ShowOn {
	case "", "all", "desktop", "mobile":
	default:
		return fmt.Errorf("%w: show on must be all, desktop or mobile", ErrInvalidAttrs)
	}
	for _, r := range a.Rel {
		if !allowedRel[r] {
			return fmt.Errorf("%w: rel %q is not allowed", ErrInvalidAttrs, r)
		}
	}
	return nil
}

// ValidateCSSClasses checks a space separated class list and returns it with
// single spaces between the classes. Class names are free-form (Tailwind style
// names such as md:hidden or w-1/2 are fine; the template escapes the
// attribute): only control characters and the quote, backtick and angle
// bracket characters are refused.
func ValidateCSSClasses(s string) (string, error) {
	out := strings.Join(strings.Fields(s), " ")
	if len(out) > maxClassesLen {
		return "", fmt.Errorf("%w: classes are longer than %d characters", ErrInvalidAttrs, maxClassesLen)
	}
	if strings.IndexFunc(out, func(r rune) bool { return unicode.IsControl(r) || strings.ContainsRune("<>\"'`", r) }) >= 0 {
		return "", fmt.Errorf("%w: classes cannot contain control characters, quotes or angle brackets", ErrInvalidAttrs)
	}
	return out, nil
}
