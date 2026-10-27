package redirect

import (
	"errors"
	"sort"
	"strings"
)

// Pattern rules are DB redirects whose source_path contains wildcard segments.
//
//	*   matches exactly one path segment
//	**  as the LAST segment, matches zero or more remaining segments
//
// Each wildcard captures in order and is referenced from the target as $1,
// $2, ... ("**" captures the remaining segments joined with "/"). Exact rules
// always win over pattern rules, and among patterns the one with more literal
// segments wins. Matching is case-sensitive, like exact rules.

// ErrInvalidPattern is returned for a malformed wildcard source_path.
var ErrInvalidPattern = errors.New("invalid wildcard source_path: use whole-segment * or a trailing **")

// IsPatternSource reports whether a source_path contains a wildcard.
func IsPatternSource(source string) bool { return strings.Contains(source, "*") }

// ErrUnsafePattern is returned for a wildcard rule that would redirect the
// whole site or its own target.
var ErrUnsafePattern = errors.New("unsafe wildcard rule: needs a literal first segment and a target that does not match its own source")

// ValidatePatternSource checks a wildcard source_path. Sources without a
// wildcard are always valid.
func ValidatePatternSource(source string) error {
	if !IsPatternSource(source) {
		return nil
	}
	if !strings.HasPrefix(source, "/") {
		return ErrInvalidPattern
	}
	segs := strings.Split(strings.Trim(source, "/"), "/")
	for i, s := range segs {
		switch {
		case s == "*":
		case s == "**":
			if i != len(segs)-1 {
				return ErrInvalidPattern
			}
		case strings.Contains(s, "*"):
			return ErrInvalidPattern
		}
	}
	return nil
}

// ValidatePatternRule checks a source/target pair. Beyond
// ValidatePatternSource, a wildcard rule must start with a literal segment
// (no catch-alls) unless its target is an absolute external URL, and a
// relative target must not match its own source, which would loop.
func ValidatePatternRule(source, target string) error {
	if err := ValidatePatternSource(source); err != nil {
		return err
	}
	if !IsPatternSource(source) || isAbsoluteExternal(target) {
		return nil
	}
	if first, _, _ := strings.Cut(strings.Trim(source, "/"), "/"); first == "*" || first == "**" {
		return ErrUnsafePattern
	}
	for _, fill := range []string{"x", ""} {
		caps := []string{fill, fill, fill, fill, fill, fill, fill, fill, fill}
		out := ExpandTarget(target, caps)
		if i := strings.IndexAny(out, "?#"); i >= 0 {
			out = out[:i]
		}
		if _, ok := MatchPattern(source, Normalise(out)); ok {
			return ErrUnsafePattern
		}
	}
	return nil
}

func isAbsoluteExternal(target string) bool {
	return strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")
}

// patternRule is a wildcard rule with its source split once at cache load.
type patternRule struct {
	*Redirect
	segs []string
}

func newPatternRule(rd *Redirect) patternRule {
	return patternRule{Redirect: rd, segs: strings.Split(strings.Trim(rd.SourcePath, "/"), "/")}
}

// MatchPattern reports whether path matches source and returns the captures.
func MatchPattern(source, path string) ([]string, bool) {
	return matchSegments(strings.Split(strings.Trim(source, "/"), "/"), path)
}

func matchSegments(src []string, path string) ([]string, bool) {
	got := strings.Split(strings.Trim(path, "/"), "/")
	if strings.Trim(path, "/") == "" {
		got = nil
	}
	var caps []string
	for i, s := range src {
		if s == "**" {
			caps = append(caps, strings.Join(got[min(i, len(got)):], "/"))
			return caps, true
		}
		if i >= len(got) {
			return nil, false
		}
		if s == "*" {
			caps = append(caps, got[i])
		} else if s != got[i] {
			return nil, false
		}
	}
	if len(got) != len(src) {
		return nil, false
	}
	return caps, true
}

// ExpandTarget replaces $1..$9 with path-escaped captures (see escapeCapture).
// An out-of-range reference expands to the empty string.
func ExpandTarget(target string, caps []string) string {
	var b strings.Builder
	for i := 0; i < len(target); i++ {
		if target[i] == '$' && i+1 < len(target) && target[i+1] >= '1' && target[i+1] <= '9' {
			if n := int(target[i+1] - '1'); n < len(caps) {
				b.WriteString(escapeCapture(caps[n]))
			}
			i++
			continue
		}
		b.WriteByte(target[i])
	}
	return b.String()
}

// sortPatterns orders pattern rules most specific first: more literal
// segments, then longer source, then lexical for determinism.
func sortPatterns(rules []*Redirect) {
	literals := func(s string) int {
		n := 0
		for seg := range strings.SplitSeq(strings.Trim(s, "/"), "/") {
			if seg != "*" && seg != "**" {
				n++
			}
		}
		return n
	}
	sort.SliceStable(rules, func(i, j int) bool {
		a, b := rules[i].SourcePath, rules[j].SourcePath
		if la, lb := literals(a), literals(b); la != lb {
			return la > lb
		}
		if len(a) != len(b) {
			return len(a) > len(b)
		}
		return a < b
	})
}

// FindPattern returns the first pattern rule matching path, with the target
// expanded, or nil. A match whose expanded target is not SafeLocation (for
// example "/xx/%5Cevil.com") is dropped: the lookup answers nil, never an
// open redirect.
func FindPattern(rules []patternRule, path string) *Redirect {
	path = Normalise(path)
	for _, rule := range rules {
		if caps, ok := matchSegments(rule.segs, path); ok {
			out := *rule.Redirect
			out.TargetURL = ExpandTarget(rule.TargetURL, caps)
			if !SafeLocation(out.TargetURL, isAbsoluteExternal(rule.TargetURL)) {
				return nil
			}
			out.Expanded = true
			return &out
		}
	}
	return nil
}
