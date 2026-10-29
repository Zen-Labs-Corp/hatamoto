package seo

import (
	"strconv"
	"strings"
	"time"
)

// Vars holds the values a template can use. Empty fields expand to nothing.
type Vars struct {
	Title        string
	SiteName     string
	Tagline      string
	Excerpt      string
	Author       string
	Date         string
	Term         string
	SearchPhrase string
	Archive      string
	Page         int // current page; 0 leaves %page% empty
	PageTotal    int // number of pages; 0 leaves %pagetotal% empty
	Year         int // 0 means the current year (UTC)
	Custom       map[string]string
}

// Expand replaces every %name% in tpl. %sep% becomes sep. A name that is not a
// known variable is left as written. When a variable expands to nothing, the
// spaces around it are collapsed and the result is trimmed.
func Expand(tpl string, v Vars, sep string) string {
	if !strings.Contains(tpl, "%") {
		return tpl
	}
	var b strings.Builder
	b.Grow(len(tpl) + len(v.Title) + len(v.SiteName))
	empty := false
	for i := 0; i < len(tpl); i++ {
		if tpl[i] != '%' {
			b.WriteByte(tpl[i])
			continue
		}
		n := tokenLen(tpl[i+1:])
		if n == 0 {
			b.WriteByte('%')
			continue
		}
		name := tpl[i+1 : i+1+n]
		val, ok := v.lookup(name, sep)
		if !ok {
			b.WriteByte('%')
			continue
		}
		if val == "" {
			empty = true
		}
		b.WriteString(val)
		i += n + 1
	}
	out := b.String()
	if empty {
		out = strings.Join(strings.Fields(out), " ")
	}
	return out
}

func (v Vars) lookup(name, sep string) (string, bool) {
	switch name {
	case "title":
		return v.Title, true
	case "sitename":
		return v.SiteName, true
	case "tagline":
		return v.Tagline, true
	case "sep":
		return sep, true
	case "page":
		return positive(v.Page), true
	case "pagetotal":
		return positive(v.PageTotal), true
	case "currentyear":
		if v.Year > 0 {
			return strconv.Itoa(v.Year), true
		}
		return strconv.Itoa(time.Now().UTC().Year()), true
	case "excerpt":
		return v.Excerpt, true
	case "author":
		return v.Author, true
	case "date":
		return v.Date, true
	case "term":
		return v.Term, true
	case "searchphrase":
		return v.SearchPhrase, true
	case "archive":
		return v.Archive, true
	}
	if custom, ok := strings.CutPrefix(name, CustomVarPrefix); ok {
		val, defined := v.Custom[custom]
		return val, defined
	}
	return "", false
}

func positive(n int) string {
	if n > 0 {
		return strconv.Itoa(n)
	}
	return ""
}

// siteSuffixes are the separators after which a title may already carry the
// site name (imported SEO titles keep the old template).
var siteSuffixes = [...]string{" | ", " - ", " – ", " — ", " · ", " : "}

// HasSiteSuffix reports whether title already ends with a separator and site.
func HasSiteSuffix(title, site string) bool {
	for _, sep := range siteSuffixes {
		if strings.HasSuffix(title, sep+site) {
			return true
		}
	}
	return false
}

// TitleWithSite appends " <sep> <site>" to a title. The site name alone, and a
// title that already ends with it after a separator, are left as they are.
func TitleWithSite(title, site, sep string) string {
	site = strings.TrimSpace(site)
	if site == "" || title == site || HasSiteSuffix(title, site) {
		return title
	}
	return title + " " + sep + " " + site
}
