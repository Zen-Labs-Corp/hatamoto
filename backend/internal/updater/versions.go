package updater

import (
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/packages"
)

// majorMinor returns the numeric major and minor of a valid version.
func majorMinor(v string) (major, minor int, ok bool) {
	if !packages.ValidSemver(v) {
		return 0, 0, false
	}
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	if len(parts) < 3 {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	return major, minor, err1 == nil && err2 == nil
}

// SameMajor reports whether a and b are valid versions of the same major.
func SameMajor(a, b string) bool {
	x, _, ok1 := majorMinor(a)
	y, _, ok2 := majorMinor(b)
	return ok1 && ok2 && x == y
}

// PinMatches reports whether v lies inside the pin ("vX.Y" allows vX.Y.*).
// An empty pin allows everything; an invalid pin or version allows nothing.
func PinMatches(pin, v string) bool {
	if pin == "" {
		return true
	}
	if !pinPattern.MatchString(pin) || !packages.ValidSemver(v) {
		return false
	}
	pm, pn, _ := majorMinor(pin + ".0")
	vm, vn, ok := majorMinor(v)
	return ok && pm == vm && pn == vn
}
