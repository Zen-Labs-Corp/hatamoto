package adminpath

import (
	"errors"
	"fmt"
	"strings"
)

// ErrFormat is wrapped by every Normalize failure.
var ErrFormat = errors.New("not a valid admin address")

const (
	minSegment = 4
	maxSegment = 64
)

// Normalize returns the canonical form of an admin address: lower case, one
// segment of 4 to 64 letters, digits or dashes after a leading slash, with no
// dash at either end.
func Normalize(s string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(s))
	if !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("%w: must start with /", ErrFormat)
	}
	seg := p[1:]
	if strings.Contains(seg, "/") {
		return "", fmt.Errorf("%w: must be a single segment", ErrFormat)
	}
	if len(seg) < minSegment || len(seg) > maxSegment {
		return "", fmt.Errorf("%w: %d to %d characters after the slash", ErrFormat, minSegment, maxSegment)
	}
	if seg[0] == '-' || seg[len(seg)-1] == '-' {
		return "", fmt.Errorf("%w: must not start or end with a dash", ErrFormat)
	}
	for _, c := range seg {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return "", fmt.Errorf("%w: only lowercase letters, digits and dashes", ErrFormat)
		}
	}
	return p, nil
}
