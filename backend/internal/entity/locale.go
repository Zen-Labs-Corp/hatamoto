package entity

import (
	"fmt"
	"strings"

	"golang.org/x/text/language"
)

// maxLocaleLen is the longest tag NormalizeLocale accepts (the RFC 5646
// recommendation), so an oversized value is a validation error and never a
// database error.
const maxLocaleLen = 35

// NormalizeLocale returns the canonical BCP 47 form of tag, exactly as the site
// language setting canonicalizes it (language.Parse then String): "_" becomes
// "-", case is fixed and deprecated codes are replaced ("iw" is "he", "tl" is
// "fil"). The public filter compares entity locale to the site language as
// strings, so the two must never disagree. An invalid or over-long tag wraps
// ErrValidationFailed.
func NormalizeLocale(tag string) (string, error) {
	t := strings.ReplaceAll(strings.TrimSpace(tag), "_", "-")
	if len(t) > maxLocaleLen {
		return "", fmt.Errorf("%w: locale is longer than %d characters", ErrValidationFailed, maxLocaleLen)
	}
	parsed, err := language.Parse(t)
	if err != nil || parsed == language.Und {
		return "", fmt.Errorf("%w: locale %q is not a BCP 47 tag", ErrValidationFailed, tag)
	}
	return parsed.String(), nil
}
