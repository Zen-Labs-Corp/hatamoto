package render

import "strings"

// LocaleLinks is what the locale-aware link rule needs about a page: its own
// URL segment, every configured segment (to spot a link that is already
// prefixed) and the shared-path predicate.
type LocaleLinks struct {
	Segment  string
	Segments []string
	Shared   func(path string) bool
}

// LocaleLinks returns the link rule inputs of the page. It is the zero value
// (no rewriting) on a one-locale site and on unprefixed pages.
func (v *PageView) LocaleLinks() LocaleLinks {
	if v == nil {
		return LocaleLinks{}
	}
	l := LocaleLinks{Segment: v.Segment, Shared: v.LocaleShared}
	for _, loc := range v.Locales {
		if loc.Segment != "" {
			l.Segments = append(l.Segments, loc.Segment)
		}
	}
	return l
}

// LocalizeURL prefixes the page's locale segment onto a site-relative path.
// Everything else comes back unchanged: absolute URLs, "//host", anchors,
// mailto:/tel:, empty or relative values, paths already starting with a
// configured segment and shared paths. Query and fragment survive; "/" becomes
// the bare segment.
func LocalizeURL(l LocaleLinks, raw string) string {
	if l.Segment == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, `/\`) {
		return raw
	}
	path, rest := raw, ""
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		path, rest = raw[:i], raw[i:]
	}
	first, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	first = strings.ToLower(first)
	for _, seg := range l.Segments {
		if first == strings.ToLower(seg) {
			return raw
		}
	}
	if l.Shared != nil && l.Shared(path) {
		return raw
	}
	if path == "/" {
		return "/" + l.Segment + rest
	}
	return "/" + l.Segment + raw
}
