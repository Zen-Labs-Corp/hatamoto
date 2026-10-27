package wpimport

import (
	"net/url"
	"strings"
	"time"
)

// InferPermalink derives the WordPress permalink structure
// ("/%year%/%monthnum%/%postname%/") from the link of one post, its slug, date
// and id. It returns "" when the link does not end in the slug (plain
// "?p=123" permalinks, or a post whose slug was changed after the link).
func InferPermalink(link, slug, id string, date time.Time) string {
	u, err := url.Parse(link)
	if err != nil || slug == "" {
		return ""
	}
	segs := splitPath(u.Path)
	if len(segs) == 0 || segs[len(segs)-1] != slug {
		return ""
	}
	out := make([]string, len(segs))
	prev := ""
	for i, s := range segs {
		tok := s // anything else (a fixed prefix like "blog") stays literal
		switch {
		case i == len(segs)-1:
			tok = "%postname%"
		case s == date.Format("2006") && len(s) == 4:
			tok = "%year%"
		case s == date.Format("01") && prev == "%year%":
			tok = "%monthnum%"
		case s == date.Format("02") && prev == "%monthnum%":
			tok = "%day%"
		case s == id && id != "":
			tok = "%post_id%"
		}
		out[i], prev = tok, tok
	}
	res := "/" + strings.Join(out, "/")
	if strings.HasSuffix(u.Path, "/") {
		res += "/"
	}
	return res
}

func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// PatternFromPermalink converts a WordPress structure to a Hatamoto URL
// pattern ("/{year}/{month}/{slug}"). ok is false when the structure uses
// something a pattern cannot express (post ids, categories, authors); the
// importer then keeps the schema's pattern and redirects every old URL.
func PatternFromPermalink(structure string) (pattern string, ok bool) {
	if structure == "" {
		return "", false
	}
	tokens := map[string]string{
		"%year%": "{year}", "%monthnum%": "{month}", "%day%": "{day}", "%postname%": "{slug}",
	}
	var out []string
	hasSlug := false
	for _, seg := range splitPath(structure) {
		if t, known := tokens[seg]; known {
			if t == "{slug}" {
				hasSlug = true
			}
			out = append(out, t)
			continue
		}
		if strings.Contains(seg, "%") {
			return "", false
		}
		out = append(out, seg)
	}
	if !hasSlug {
		return "", false
	}
	return "/" + strings.Join(out, "/"), true
}

// pathOf returns the URL path of a link ("" for an unparsable one), without
// query string and fragment.
func pathOf(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	return u.Path
}

// normPath is the comparison form of a path: leading slash, no trailing one
// (except the root).
func normPath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
		if p == "" {
			p = "/"
		}
	}
	return p
}
