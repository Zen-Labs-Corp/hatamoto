package sdk

import (
	"fmt"
	"strconv"
	"strings"
)

type semver struct {
	major, minor, patch int
	pre                 []string // prerelease identifiers; build metadata is dropped
}

// cmp orders by semver precedence: numeric parts first, then a version with a
// prerelease sorts before the same version without one, then prerelease
// identifiers left to right (numeric < alphanumeric, shorter set < longer).
func (a semver) cmp(b semver) int {
	for _, p := range [][2]int{{a.major, b.major}, {a.minor, b.minor}, {a.patch, b.patch}} {
		switch {
		case p[0] < p[1]:
			return -1
		case p[0] > p[1]:
			return 1
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		if c := cmpPreID(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.pre) < len(b.pre):
		return -1
	case len(a.pre) > len(b.pre):
		return 1
	}
	return 0
}

func isNumericID(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func cmpPreID(a, b string) int {
	an, bn := isNumericID(a), isNumericID(b)
	switch {
	case an && bn:
		x, _ := strconv.ParseUint(a, 10, 64)
		y, _ := strconv.ParseUint(b, 10, 64)
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	case an:
		return -1
	case bn:
		return 1
	}
	return strings.Compare(a, b)
}

// validIDs checks dot-separated prerelease/build identifiers: non-empty,
// [0-9A-Za-z-] only; numeric prerelease identifiers have no leading zeros.
func validIDs(s string, prerelease bool) ([]string, bool) {
	ids := strings.Split(s, ".")
	for _, id := range ids {
		if id == "" {
			return nil, false
		}
		for _, r := range id {
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-') {
				return nil, false
			}
		}
		if prerelease && isNumericID(id) && len(id) > 1 && id[0] == '0' {
			return nil, false
		}
	}
	return ids, true
}

// parseSemver parses "1", "1.2" or "1.2.3" (optional leading "v"; missing
// parts are 0), optionally followed by "-prerelease" and "+build" ("1.2.3-rc.1+abc").
// parts reports how many numeric components were present.
func parseSemver(s string) (v semver, parts int, err error) {
	t := strings.TrimPrefix(strings.TrimSpace(s), "v")
	if t == "" {
		return v, 0, fmt.Errorf("sdk: empty version")
	}
	bad := fmt.Errorf("sdk: invalid version %q", s)
	if core, build, ok := strings.Cut(t, "+"); ok {
		if _, valid := validIDs(build, false); !valid {
			return v, 0, bad
		}
		t = core
	}
	if core, pre, ok := strings.Cut(t, "-"); ok {
		ids, valid := validIDs(pre, true)
		if !valid {
			return v, 0, bad
		}
		v.pre = ids
		t = core
	}
	fields := strings.Split(t, ".")
	if len(fields) > 3 {
		return v, 0, bad
	}
	nums := [3]int{}
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return v, 0, bad
		}
		nums[i] = n
	}
	v.major, v.minor, v.patch = nums[0], nums[1], nums[2]
	return v, len(fields), nil
}

type comparator struct {
	op string // ">=", ">", "<=", "<", "="
	v  semver
}

func (c comparator) match(v semver) bool {
	r := v.cmp(c.v)
	switch c.op {
	case ">=":
		return r >= 0
	case ">":
		return r > 0
	case "<=":
		return r <= 0
	case "<":
		return r < 0
	}
	return r == 0
}

// parseRange turns a space-separated AND list into comparators. Supported
// terms: ">=x >x <=x <x =x", bare "x", "^x" and "~x".
func parseRange(rng string) ([]comparator, error) {
	terms := strings.Fields(rng)
	if len(terms) == 0 {
		return nil, fmt.Errorf("sdk: empty version range")
	}
	var out []comparator
	for _, t := range terms {
		cs, err := parseTerm(t)
		if err != nil {
			return nil, err
		}
		out = append(out, cs...)
	}
	return out, nil
}

func parseTerm(t string) ([]comparator, error) {
	switch {
	case strings.HasPrefix(t, "^"):
		v, parts, err := parseSemver(t[1:])
		if err != nil {
			return nil, err
		}
		// npm caret: the leftmost non-zero component is frozen; "^0" (major
		// only) therefore allows the whole 0.x line.
		upper := semver{major: v.major + 1}
		if v.major == 0 && parts >= 2 {
			upper = semver{minor: v.minor + 1}
			if v.minor == 0 && parts == 3 {
				upper = semver{patch: v.patch + 1}
			}
		}
		return []comparator{{">=", v}, {"<", upper}}, nil
	case strings.HasPrefix(t, "~"):
		v, parts, err := parseSemver(t[1:])
		if err != nil {
			return nil, err
		}
		upper := semver{major: v.major, minor: v.minor + 1}
		if parts == 1 {
			upper = semver{major: v.major + 1}
		}
		return []comparator{{">=", v}, {"<", upper}}, nil
	}
	op := "="
	for _, o := range []string{">=", "<=", ">", "<", "="} {
		if strings.HasPrefix(t, o) {
			op = o
			t = t[len(o):]
			break
		}
	}
	v, _, err := parseSemver(t)
	if err != nil {
		return nil, err
	}
	return []comparator{{op, v}}, nil
}

// Satisfies reports whether version lies within the range rng. Ranges are
// space-separated AND comparators: "^0.1", ">=0.1.0 <1.0.0", "~0.1.2",
// "=0.1.0". Caret and tilde follow npm semantics (^0.1 means >=0.1.0 <0.2.0).
func Satisfies(version, rng string) (bool, error) {
	v, _, err := parseSemver(version)
	if err != nil {
		return false, err
	}
	cs, err := parseRange(rng)
	if err != nil {
		return false, err
	}
	for _, c := range cs {
		if !c.match(v) {
			return false, nil
		}
	}
	return true, nil
}
