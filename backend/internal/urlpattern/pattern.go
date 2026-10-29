// Package urlpattern implements the URL pattern mini-language used for
// per-schema public routing: parsing, rendering an entity to a path, matching
// a path back to its variables, and a Router that resolves a path to the
// schemas that may serve it.
//
// A pattern is a slash-separated path whose segments are either literals or
// one of the whole-segment tokens {parent}, {slug}, {path}, {year}, {month}
// and {day}:
//
//	/post/{slug}
//	/blog/{slug}
//	/docs/{parent}/{slug}
//	/{year}/{month}/{slug}
//	/{path}
//
// The last segment is either {slug} or {path}, exactly one of them. {path} is
// the entity's whole ancestor chain of slugs plus its own, joined by '/'
// (services/seo/local), so it spans one to MaxPathDepth URL segments.
// {parent} is optional at render and match time: when the parent is empty its
// segment is omitted (it cannot be combined with {path}). {year} ("2024"),
// {month} ("05") and {day} ("09") are the entity's publication date.
//
// Locale. Content carries a locale (a BCP 47 tag) but patterns have no
// language token and Parse, Match and Render never see one. On an install with
// several public locales the locale segment ("/fr") is added in front of the
// rendered path and stripped before matching, both by the entity package
// (entity.LocaleSet), so a pattern is the same in every locale. Vars.Locale
// only tells the caller which prefix to add.
//
// The package is pure (no I/O) so it can be unit tested exhaustively.
package urlpattern

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Token identifies a variable segment.
type Token int

const (
	// TokenNone marks a literal segment.
	TokenNone Token = iota
	TokenParent
	TokenSlug
	TokenPath
	TokenYear
	TokenMonth
	TokenDay
)

// MaxPathDepth caps the segments a {path} value may have (ancestors plus the
// entity), so a parent cycle or a hostile URL can never grow without bound.
const MaxPathDepth = 8

func (t Token) String() string {
	switch t {
	case TokenParent:
		return "{parent}"
	case TokenSlug:
		return "{slug}"
	case TokenPath:
		return "{path}"
	case TokenYear:
		return "{year}"
	case TokenMonth:
		return "{month}"
	case TokenDay:
		return "{day}"
	}
	return ""
}

var tokenNames = map[string]Token{
	"{parent}": TokenParent, "{slug}": TokenSlug, "{path}": TokenPath,
	"{year}": TokenYear, "{month}": TokenMonth, "{day}": TokenDay,
}

// ErrInvalidPattern is wrapped by every Parse failure.
var ErrInvalidPattern = errors.New("invalid url pattern")

// ErrMissingVar is returned by Render when a required variable is empty.
var ErrMissingVar = errors.New("missing url variable")

// Segment is one path segment: a literal, or a token when Token != TokenNone.
type Segment struct {
	Literal string
	Token   Token
}

// Vars carries the values substituted into / extracted from a pattern.
type Vars struct {
	Slug   string
	Parent string
	// Path is the slash-joined slug chain ending with Slug ("a/b/slug").
	// Render falls back to Slug when it is empty.
	Path string
	// Year, Month and Day are zero-padded ("2024", "05", "09").
	Year, Month, Day string
	// Locale is the entity's locale tag. Render ignores it; the caller that
	// builds the public path (entity.URLSnapshot.Locate) reads it to add the
	// locale prefix.
	Locale string
}

// SplitPath returns the segments of a {path} value.
func SplitPath(path string) []string { return strings.Split(path, "/") }

// Pattern is a parsed URL pattern.
type Pattern struct {
	raw      string
	segments []Segment
}

// Parse validates and parses a pattern.
func Parse(pattern string) (*Pattern, error) {
	fail := func(format string, a ...any) (*Pattern, error) {
		return nil, fmt.Errorf("%w %q: %s", ErrInvalidPattern, pattern, fmt.Sprintf(format, a...))
	}
	if !strings.HasPrefix(pattern, "/") {
		return fail("must start with '/'")
	}
	if pattern == "/" {
		return fail("must contain {slug} or {path}")
	}
	parts := strings.Split(pattern[1:], "/")
	p := &Pattern{raw: pattern, segments: make([]Segment, 0, len(parts))}
	seen := map[Token]bool{}
	for i, part := range parts {
		if part == "" {
			return fail("empty segment")
		}
		var seg Segment
		if tok, ok := tokenNames[part]; ok {
			seg.Token = tok
		} else {
			if strings.ContainsAny(part, "{}") {
				return fail("segment %q: tokens must be a whole segment and one of {parent}, {slug}, {path}, {year}, {month}, {day}", part)
			}
			seg.Literal = part
		}
		if seg.Token != TokenNone {
			if seen[seg.Token] {
				return fail("duplicate token %s", seg.Token)
			}
			seen[seg.Token] = true
		}
		if (seg.Token == TokenSlug || seg.Token == TokenPath) && i != len(parts)-1 {
			return fail("%s must be the last segment", seg.Token)
		}
		p.segments = append(p.segments, seg)
	}
	switch {
	case seen[TokenSlug] && seen[TokenPath]:
		return fail("use {slug} or {path}, not both")
	case !seen[TokenSlug] && !seen[TokenPath]:
		return fail("must contain {slug} or {path}")
	case seen[TokenPath] && seen[TokenParent]:
		return fail("{parent} is redundant with {path}")
	}
	return p, nil
}

// String returns the original pattern text.
func (p *Pattern) String() string { return p.raw }

// Segments returns a copy of the parsed segments.
func (p *Pattern) Segments() []Segment { return append([]Segment(nil), p.segments...) }

// HasPath reports whether the pattern ends in {path}.
func (p *Pattern) HasPath() bool { return p.has(TokenPath) }

// HasDate reports whether the pattern has a {year}, {month} or {day} token.
func (p *Pattern) HasDate() bool {
	return p.has(TokenYear) || p.has(TokenMonth) || p.has(TokenDay)
}

// HasParent reports whether the pattern has an (optional) {parent} token.
func (p *Pattern) HasParent() bool { return p.has(TokenParent) }

// FirstLiteral returns the first path segment when it is a literal (for
// example "post" for /post/{slug}), or "" when the pattern starts with a token.
func (p *Pattern) FirstLiteral() string { return p.segments[0].Literal }

// LiteralCount is the number of literal segments (higher = more specific).
func (p *Pattern) LiteralCount() int {
	n := 0
	for _, s := range p.segments {
		if s.Token == TokenNone {
			n++
		}
	}
	return n
}

// TokenCount is the number of token segments.
func (p *Pattern) TokenCount() int { return len(p.segments) - p.LiteralCount() }

func (p *Pattern) has(t Token) bool {
	for _, s := range p.segments {
		if s.Token == t {
			return true
		}
	}
	return false
}

// Render substitutes vars into the pattern. {slug} (or {path}) is required;
// an empty {parent} omits its segment. Values may not contain '/', except a
// {path} value, whose segments must be non-empty and at most MaxPathDepth.
func Render(p *Pattern, v Vars) (string, error) {
	var b strings.Builder
	for _, s := range p.segments {
		var val string
		switch s.Token {
		case TokenNone:
			val = s.Literal
		case TokenSlug:
			val = v.Slug
		case TokenParent:
			if v.Parent == "" {
				continue
			}
			val = v.Parent
		case TokenPath:
			val = v.Path
			if val == "" {
				val = v.Slug
			}
			if val == "" || slices.Contains(SplitPath(val), "") || len(SplitPath(val)) > MaxPathDepth {
				return "", fmt.Errorf("%w: {path} value %q for pattern %s", ErrMissingVar, val, p.raw)
			}
			b.WriteByte('/')
			b.WriteString(val)
			continue
		case TokenYear:
			val = v.Year
		case TokenMonth:
			val = v.Month
		case TokenDay:
			val = v.Day
		}
		if val == "" {
			return "", fmt.Errorf("%w: %s for pattern %s", ErrMissingVar, s.Token, p.raw)
		}
		if strings.Contains(val, "/") {
			return "", fmt.Errorf("%w: %s value %q contains '/'", ErrMissingVar, s.Token, val)
		}
		b.WriteByte('/')
		b.WriteString(val)
	}
	return b.String(), nil
}

// Match extracts variables from path. The path must be absolute; a single
// trailing slash is ignored. A pattern with {parent} also matches the path
// without that segment (Parent left empty).
func Match(p *Pattern, path string) (Vars, bool) {
	path = strings.TrimSuffix(path, "/")
	if !strings.HasPrefix(path, "/") {
		return Vars{}, false
	}
	parts := strings.Split(path[1:], "/")
	if slices.Contains(parts, "") {
		return Vars{}, false
	}
	if p.HasPath() {
		// {path} is last and takes one to MaxPathDepth segments.
		if len(parts) < len(p.segments) || len(parts)-len(p.segments)+1 > MaxPathDepth {
			return Vars{}, false
		}
		return matchSegments(p.segments, parts, false)
	}
	switch {
	case len(parts) == len(p.segments):
		return matchSegments(p.segments, parts, false)
	case len(parts) == len(p.segments)-1 && p.HasParent():
		return matchSegments(p.segments, parts, true)
	}
	return Vars{}, false
}

func matchSegments(segs []Segment, parts []string, skipParent bool) (Vars, bool) {
	var v Vars
	i := 0
	for _, s := range segs {
		if s.Token == TokenParent && skipParent {
			continue
		}
		part := parts[i]
		i++
		switch s.Token {
		case TokenPath:
			v.Path = strings.Join(parts[i-1:], "/")
			v.Slug = parts[len(parts)-1]
			return v, true
		case TokenYear:
			if !digits(part, 4) {
				return Vars{}, false
			}
			v.Year = part
		case TokenMonth:
			if !digits(part, 2) {
				return Vars{}, false
			}
			v.Month = part
		case TokenDay:
			if !digits(part, 2) {
				return Vars{}, false
			}
			v.Day = part
		case TokenNone:
			if part != s.Literal {
				return Vars{}, false
			}
		case TokenParent:
			v.Parent = part
		case TokenSlug:
			v.Slug = part
		}
	}
	return v, true
}

// digits reports whether s is exactly n ASCII digits.
func digits(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
