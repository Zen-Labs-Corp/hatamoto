package entity

import (
	"fmt"
	"regexp"
	"strings"
)

// maxLocaleSegmentLen bounds a locale URL segment.
const maxLocaleSegmentLen = 32

var localeSegmentRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// LocaleEntry is one public locale: a BCP 47 tag and the free URL segment
// that prefixes its paths.
type LocaleEntry struct {
	Tag     string
	Segment string
}

// LocaleSet is the validated view of the multi-locale site settings. Entry
// order is the hreflang order. A nil or empty set means a single-locale install:
// every method is safe on a nil receiver and reports "no locale routing".
type LocaleSet struct {
	def       string
	xDefault  string
	prefixAll bool
	entries   []LocaleEntry // normalized tags, settings order
	segByTag  map[string]string
}

// NewLocaleSet validates the settings. An empty entries list yields an empty
// set (single-locale). Otherwise def must be one of the tags, tags and
// segments must be unique, and every segment must match
// ^[a-z0-9]+(-[a-z0-9]+)*$ (max 32). The default locale may have an empty
// segment only when prefixAll is false; every other locale needs a segment.
// xDefault must be one of the tags or empty. Errors wrap ErrValidationFailed.
// Reserved paths, redirects and slugs are checked by the caller, not here.
func NewLocaleSet(def string, entries []LocaleEntry, prefixAll bool, xDefault string) (*LocaleSet, error) {
	if len(entries) == 0 {
		return &LocaleSet{}, nil
	}
	defTag, err := NormalizeLocale(def)
	if err != nil {
		return nil, fmt.Errorf("default locale: %w", err)
	}
	s := &LocaleSet{
		def:       defTag,
		prefixAll: prefixAll,
		entries:   make([]LocaleEntry, 0, len(entries)),
		segByTag:  make(map[string]string, len(entries)),
	}
	segs := make(map[string]string, len(entries))
	for _, e := range entries {
		tag, err := NormalizeLocale(e.Tag)
		if err != nil {
			return nil, err
		}
		if _, dup := s.segByTag[tag]; dup {
			return nil, fmt.Errorf("%w: locale %q is listed twice", ErrValidationFailed, tag)
		}
		seg := e.Segment
		if seg == "" {
			if tag != defTag || prefixAll {
				return nil, fmt.Errorf("%w: locale %q needs a URL segment", ErrValidationFailed, tag)
			}
		} else if len(seg) > maxLocaleSegmentLen || !localeSegmentRe.MatchString(seg) {
			return nil, fmt.Errorf("%w: segment %q must be lowercase letters, digits and single hyphens, at most %d characters", ErrValidationFailed, seg, maxLocaleSegmentLen)
		}
		if other, dup := segs[seg]; dup {
			return nil, fmt.Errorf("%w: locales %q and %q share the segment %q", ErrValidationFailed, other, tag, seg)
		}
		segs[seg] = tag
		s.segByTag[tag] = seg
		s.entries = append(s.entries, LocaleEntry{Tag: tag, Segment: seg})
	}
	if _, ok := s.segByTag[defTag]; !ok {
		return nil, fmt.Errorf("%w: default locale %q is not in the locale list", ErrValidationFailed, defTag)
	}
	if xDefault != "" {
		x, err := NormalizeLocale(xDefault)
		if err != nil {
			return nil, fmt.Errorf("x-default locale: %w", err)
		}
		if _, ok := s.segByTag[x]; !ok {
			return nil, fmt.Errorf("%w: x-default locale %q is not in the locale list", ErrValidationFailed, x)
		}
		s.xDefault = x
	}
	return s, nil
}

// Multi reports whether two or more locales are configured.
func (s *LocaleSet) Multi() bool { return s != nil && len(s.entries) >= 2 }

// Tags returns the locale tags in settings order (a copy).
func (s *LocaleSet) Tags() []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s.entries))
	for i, e := range s.entries {
		out[i] = e.Tag
	}
	return out
}

// Default returns the default locale tag, or "" for an empty set.
func (s *LocaleSet) Default() string {
	if s == nil {
		return ""
	}
	return s.def
}

// XDefault returns the configured x-default locale tag, or "".
func (s *LocaleSet) XDefault() string {
	if s == nil {
		return ""
	}
	return s.xDefault
}

// Resolve splits a request path into its locale and the path without the
// segment: "/fr/x" gives ("fr", "/x"), "/fr" gives ("fr", "/"). ok is false
// when the set is not Multi or the first path segment is not a configured
// segment ("/frx" never matches "fr"). An unprefixed default locale is not a
// match; the caller treats it as the default.
func (s *LocaleSet) Resolve(path string) (tag, rest string, ok bool) {
	if !s.Multi() || !strings.HasPrefix(path, "/") {
		return "", "", false
	}
	first, tail, _ := strings.Cut(path[1:], "/")
	if first == "" {
		return "", "", false
	}
	for _, e := range s.entries {
		if e.Segment == first {
			return e.Tag, "/" + tail, true
		}
	}
	return "", "", false
}

// Prefix returns the URL prefix of tag, such as "/fr", or "" for shared
// content, an unknown tag, a single-locale set and the default locale when
// prefixAll is off.
func (s *LocaleSet) Prefix(tag string) string {
	if !s.Multi() || tag == LocaleShared {
		return ""
	}
	seg, ok := s.segByTag[tag]
	if !ok || seg == "" || (tag == s.def && !s.prefixAll) {
		return ""
	}
	return "/" + seg
}
