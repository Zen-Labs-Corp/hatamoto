package seo

import (
	"slices"
	"strings"
	"unicode"
)

const (
	// MaxIgnorePatterns404 caps the ignore list.
	MaxIgnorePatterns404 = 50
	// DefaultRetention404 is the retention in days when none is saved.
	DefaultRetention404 = 90

	maxIgnorePatternLen404 = 200
)

// Retention404Options lists the retention periods (days) an owner may pick.
var Retention404Options = []int{30, 90, 180, 365}

// Monitor404 holds the 404 monitor settings. The zero value is today's
// behaviour: logging on, 90 days, nothing ignored. Logging is stored as an
// inverse flag so documents saved before this key existed keep logging.
type Monitor404 struct {
	Disabled       bool     `json:"disabled,omitzero"`
	RetentionDays  int      `json:"retention_days,omitzero"` // 0 => DefaultRetention404
	IgnorePatterns []string `json:"ignore_patterns,omitempty"`
}

// IsZero reports whether no 404 monitor setting is saved.
func (m Monitor404) IsZero() bool {
	return !m.Disabled && m.RetentionDays == 0 && len(m.IgnorePatterns) == 0
}

// Days is the retention in days; zero or an unknown value gives the default.
func (m Monitor404) Days() int {
	if slices.Contains(Retention404Options, m.RetentionDays) {
		return m.RetentionDays
	}
	return DefaultRetention404
}

// validateMonitor404 checks the retention and the ignore patterns.
func validateMonitor404(m Monitor404) []FieldError {
	var errs []FieldError
	if m.RetentionDays != 0 && !slices.Contains(Retention404Options, m.RetentionDays) {
		errs = append(errs, FieldError{"monitor404[retention_days]", "Choose 30, 90, 180 or 365 days."})
	}
	if len(m.IgnorePatterns) > MaxIgnorePatterns404 {
		errs = append(errs, FieldError{"monitor404[ignore_patterns]", "Use at most 50 patterns."})
		return errs
	}
	for _, p := range m.IgnorePatterns {
		switch {
		case strings.TrimSpace(p) == "":
			errs = append(errs, FieldError{"monitor404[ignore_patterns]", "Remove blank patterns."})
		case len(p) > maxIgnorePatternLen404:
			errs = append(errs, FieldError{"monitor404[ignore_patterns]", "Keep each pattern under 200 characters."})
		case p[0] != '/' && p[0] != '*':
			errs = append(errs, FieldError{"monitor404[ignore_patterns]", "Start each pattern with / or *."})
		case strings.IndexFunc(p, unicode.IsControl) >= 0:
			errs = append(errs, FieldError{"monitor404[ignore_patterns]", "Patterns cannot contain control characters."})
		default:
			continue
		}
		break
	}
	return errs
}
