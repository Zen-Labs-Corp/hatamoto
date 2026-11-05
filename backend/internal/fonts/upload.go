package fonts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Limits. MaxUploads is how many files one owner may add by upload.
const (
	MaxFileBytes = 2 << 20
	MaxFonts     = 60
	MaxUploads   = 20
)

// Errors carry the wording shown to the owner.
var (
	ErrNotWOFF2  = errors.New("that file is not a .woff2 font")
	ErrTooBig    = errors.New("fonts can be at most 2 MB")
	ErrBadFamily = errors.New("the family name can use letters, digits, spaces, dashes and underscores, up to 60 characters")
	ErrBadWeight = errors.New("the weight must be 100, 200, up to 900")
	ErrBadStyle  = errors.New("the style must be normal or italic")
)

// familyRe is a family name as stored: no quotes and no commas, so it can be
// put inside a quoted CSS string without escaping.
var familyRe = regexp.MustCompile(`^[A-Za-z0-9 _-]{1,60}$`)

// ValidFamily reports whether family is acceptable as a stored family name.
func ValidFamily(family string) bool { return familyRe.MatchString(family) }

// ValidWeight reports whether w is 100 to 900 in steps of 100.
func ValidWeight(w int) bool { return w >= 100 && w <= 900 && w%100 == 0 }

// ValidStyle reports whether s is normal or italic.
func ValidStyle(s string) bool { return s == "normal" || s == "italic" }

// ValidateUpload checks an uploaded font and returns its bytes and sha256 hex.
func ValidateUpload(family string, weight int, style string, r io.Reader) ([]byte, string, error) {
	if !ValidFamily(family) {
		return nil, "", ErrBadFamily
	}
	if !ValidWeight(weight) {
		return nil, "", ErrBadWeight
	}
	if !ValidStyle(style) {
		return nil, "", ErrBadStyle
	}
	b, err := io.ReadAll(io.LimitReader(r, MaxFileBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("fonts: read upload: %w", err)
	}
	if len(b) > MaxFileBytes {
		return nil, "", ErrTooBig
	}
	if !bytes.HasPrefix(b, []byte("wOF2")) {
		return nil, "", ErrNotWOFF2
	}
	return b, hashOf(b), nil
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// FirstFamily returns the first family of a font stack without its quotes:
// `"Brand Sans", sans-serif` gives Brand Sans.
func FirstFamily(stack string) string {
	first, _, _ := strings.Cut(stack, ",")
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(first), `"'`))
}
