package seo

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Limits on the robots and verification values.
const (
	MaxRobotsLines     = 50
	MaxRobotsLineLen   = 200
	MaxCustomMetaTags  = 10
	MaxMetaContentLen  = 300
	maxMetaNameLen     = 64
	robotsExtraField   = "robots[extra]"
	verificationPrefix = "verification"
)

// MaxImagePreviews lists the valid Robots.MaxImagePreview values; "" means not set.
var MaxImagePreviews = []string{"", "none", "standard", "large"}

var (
	metaNameRE      = regexp.MustCompile(`^[a-z0-9:._-]{1,64}$`)
	robotsDirective = map[string]bool{"user-agent": true, "allow": true, "disallow": true, "crawl-delay": true, "sitemap": true}
)

// Index holds the noindex switches for content types and archive kinds. The
// zero value reproduces the historic output: everything is indexed except
// search results.
type Index struct {
	NoIndexTypes    map[string]bool `json:"noindex_types,omitempty"`    // schema slug
	NoIndexTaxonomy map[string]bool `json:"noindex_taxonomy,omitempty"` // taxonomy kind
	NoIndexAuthor   bool            `json:"noindex_author,omitzero"`
	NoIndexArchive  bool            `json:"noindex_archive,omitzero"` // schema archives
	IndexSearch     bool            `json:"index_search,omitzero"`    // false: search results stay noindex
}

// IsZero reports whether no index switch is saved.
func (i Index) IsZero() bool {
	return len(i.NoIndexTypes) == 0 && len(i.NoIndexTaxonomy) == 0 &&
		!i.NoIndexAuthor && !i.NoIndexArchive && !i.IndexSearch
}

// Robots holds the site-wide robots defaults and the extra robots.txt lines.
type Robots struct {
	MaxSnippet      *int     `json:"max_snippet,omitempty"`
	MaxImagePreview string   `json:"max_image_preview,omitempty"` // see MaxImagePreviews
	NoArchive       bool     `json:"noarchive,omitzero"`
	NoSnippet       bool     `json:"nosnippet,omitzero"`
	ExtraLines      []string `json:"extra_lines,omitempty"`
}

// IsZero reports whether no robots setting is saved.
func (r Robots) IsZero() bool {
	return r.MaxSnippet == nil && r.MaxImagePreview == "" && !r.NoArchive && !r.NoSnippet && len(r.ExtraLines) == 0
}

// MetaTag is one <meta name content> pair.
type MetaTag struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// Verification holds the webmaster verification codes.
type Verification struct {
	Google    string    `json:"google,omitempty"`
	Bing      string    `json:"bing,omitempty"`
	Yandex    string    `json:"yandex,omitempty"`
	Baidu     string    `json:"baidu,omitempty"`
	Pinterest string    `json:"pinterest,omitempty"`
	Facebook  string    `json:"facebook,omitempty"`
	Custom    []MetaTag `json:"custom,omitempty"`
}

// IsZero reports whether no verification tag is saved.
func (v Verification) IsZero() bool {
	return v.Google == "" && v.Bing == "" && v.Yandex == "" && v.Baidu == "" &&
		v.Pinterest == "" && v.Facebook == "" && len(v.Custom) == 0
}

// reservedMetaNames are head tags the page already owns: a custom tag with one
// of these names would duplicate or override them (robots above all).
var reservedMetaNames = map[string]bool{
	"robots": true, "googlebot": true, "bingbot": true, "description": true,
	"viewport": true, "referrer": true, "generator": true, "theme-color": true,
	"geo.region": true, "google-site-verification": true, "msvalidate.01": true,
	"yandex-verification": true, "baidu-site-verification": true,
	"p:domain_verify": true, "facebook-domain-verification": true,
}

// ReservedMetaName reports whether a custom verification tag may not use name.
func ReservedMetaName(name string) bool {
	return reservedMetaNames[name] || strings.HasPrefix(name, "og:") || strings.HasPrefix(name, "twitter:")
}

// Metas lists the tags to render, built-in services first, then the custom
// tags. Empty values are skipped.
func (v Verification) Metas() []MetaTag {
	all := []MetaTag{
		{"google-site-verification", v.Google},
		{"msvalidate.01", v.Bing},
		{"yandex-verification", v.Yandex},
		{"baidu-site-verification", v.Baidu},
		{"p:domain_verify", v.Pinterest},
		{"facebook-domain-verification", v.Facebook},
	}
	for _, m := range v.Custom {
		if !ReservedMetaName(m.Name) {
			all = append(all, m)
		}
	}
	var out []MetaTag
	for _, m := range all {
		if m.Name != "" && m.Content != "" {
			out = append(out, m)
		}
	}
	return out
}

// NoIndexType reports whether every page of the content type is noindex. A nil
// receiver means nothing is saved.
func (s *Settings) NoIndexType(slug string) bool {
	return s != nil && s.Index.NoIndexTypes[slug]
}

// NoIndexTypeSlugs lists the noindex content types, sorted.
func (s *Settings) NoIndexTypeSlugs() []string {
	if s == nil {
		return nil
	}
	var out []string
	for slug, on := range s.Index.NoIndexTypes {
		if on {
			out = append(out, slug)
		}
	}
	sort.Strings(out)
	return out
}

// NoIndexView reports whether a rendered page of the given kind is noindex by
// setting. kind is the render kind ("single", "page", "home", "archive",
// "taxonomy", "author", "search"); schemaSlug is the content type and termKind
// the taxonomy kind. A nil receiver reproduces the historic behaviour: only
// search results are noindex.
func (s *Settings) NoIndexView(kind, schemaSlug, termKind string) bool {
	if kind == string(KindSearch) {
		return s == nil || !s.Index.IndexSearch
	}
	if s == nil {
		return false
	}
	switch Kind(kind) {
	case KindPage, KindSingle, KindHome:
		return s.Index.NoIndexTypes[schemaSlug]
	case KindArchive:
		return s.Index.NoIndexArchive || s.Index.NoIndexTypes[schemaSlug]
	case KindTaxonomy:
		return s.Index.NoIndexTaxonomy[termKind]
	case KindAuthor:
		return s.Index.NoIndexAuthor || s.Index.NoIndexTypes[schemaSlug]
	}
	return false
}

// EntryNoIndex reports whether an entity asks search engines to skip it:
// metadata.no_index, attributes.no_index, or attributes.seo.robots containing
// "noindex". Invalid JSON counts as not set. It is the Go twin of
// entity.NoIndexSQL.
func EntryNoIndex(metadata, attrs json.RawMessage) bool {
	flag := func(raw json.RawMessage) bool {
		s := strings.TrimSpace(string(raw))
		return s == "true" || s == `"true"`
	}
	var m struct {
		NoIndex json.RawMessage `json:"no_index"`
	}
	if len(metadata) > 0 && json.Unmarshal(metadata, &m) == nil && flag(m.NoIndex) {
		return true
	}
	var a struct {
		NoIndex json.RawMessage `json:"no_index"`
		SEO     struct {
			Robots string `json:"robots"`
		} `json:"seo"`
	}
	if len(attrs) == 0 || json.Unmarshal(attrs, &a) != nil {
		return false
	}
	return flag(a.NoIndex) || strings.Contains(strings.ToLower(a.SEO.Robots), "noindex")
}

// Directives is a parsed robots meta value.
type Directives struct {
	NoIndex, NoFollow, NoArchive, NoSnippet bool
	MaxSnippet                              *int
	MaxImagePreview                         string
	Other                                   []string // unknown tokens, kept verbatim
}

// ParseDirectives reads a comma separated robots value. The redundant "index"
// and "follow" tokens are dropped; unknown tokens are kept.
func ParseDirectives(raw string) Directives {
	var d Directives
	for _, tok := range strings.Split(raw, ",") {
		tok = strings.TrimSpace(tok)
		low := strings.ToLower(tok)
		switch {
		case tok == "", low == "index", low == "follow":
		case low == "noindex":
			d.NoIndex = true
		case low == "nofollow":
			d.NoFollow = true
		case low == "noarchive":
			d.NoArchive = true
		case low == "nosnippet":
			d.NoSnippet = true
		case strings.HasPrefix(low, "max-snippet:"):
			if n, err := strconv.Atoi(strings.TrimPrefix(low, "max-snippet:")); err == nil && n >= -1 {
				d.MaxSnippet = &n
				continue
			}
			d.Other = append(d.Other, tok)
		case strings.HasPrefix(low, "max-image-preview:"):
			if v := strings.TrimPrefix(low, "max-image-preview:"); v != "" && containsString(MaxImagePreviews, v) {
				d.MaxImagePreview = v
				continue
			}
			d.Other = append(d.Other, tok)
		default:
			d.Other = append(d.Other, tok)
		}
	}
	return d
}

// String writes the directives as the stored "a, b" value.
func (d Directives) String() string {
	var t []string
	if d.NoIndex {
		t = append(t, "noindex")
	}
	if d.NoFollow {
		t = append(t, "nofollow")
	}
	if d.NoArchive {
		t = append(t, "noarchive")
	}
	if d.NoSnippet {
		t = append(t, "nosnippet")
	}
	if d.MaxSnippet != nil {
		t = append(t, fmt.Sprintf("max-snippet:%d", *d.MaxSnippet))
	}
	if d.MaxImagePreview != "" {
		t = append(t, "max-image-preview:"+d.MaxImagePreview)
	}
	return strings.Join(append(t, d.Other...), ", ")
}

// ComposeRobots builds the content of the robots meta tag from the entry's
// directives, the site defaults and whether the page is noindex by setting. It
// returns "" when nothing differs from a page with no robots meta. A noindex
// page keeps the historic "noindex,follow" (or "noindex,nofollow" when the
// entry says so); the other directives only apply to indexed pages.
func ComposeRobots(entry Directives, def *Robots, noindex bool) string {
	if noindex || entry.NoIndex {
		if entry.NoFollow {
			return "noindex,nofollow"
		}
		return "noindex,follow"
	}
	var d Robots
	if def != nil {
		d = *def
	}
	t := []string{"index"}
	if entry.NoFollow {
		t = append(t, "nofollow")
	}
	if entry.NoArchive || d.NoArchive {
		t = append(t, "noarchive")
	}
	if entry.NoSnippet || d.NoSnippet {
		t = append(t, "nosnippet")
	}
	ms := d.MaxSnippet
	if entry.MaxSnippet != nil {
		ms = entry.MaxSnippet
	}
	if ms != nil {
		t = append(t, fmt.Sprintf("max-snippet:%d", *ms))
	}
	img := d.MaxImagePreview
	if entry.MaxImagePreview != "" {
		img = entry.MaxImagePreview
	}
	if img != "" {
		t = append(t, "max-image-preview:"+img)
	}
	if len(t) == 1 {
		return ""
	}
	return strings.Join(t, ",")
}

// ValidateRobotsLines checks the extra robots.txt lines: only the directives
// User-agent, Allow, Disallow, Crawl-delay and Sitemap, or comments; no
// control characters; no line naming adminPath (when non-empty).
func ValidateRobotsLines(lines []string, adminPath string) []FieldError {
	bad := func(msg string) []FieldError { return []FieldError{{robotsExtraField, msg}} }
	if len(lines) > MaxRobotsLines {
		return bad(fmt.Sprintf("At most %d lines.", MaxRobotsLines))
	}
	admin := strings.ToLower(strings.TrimSpace(adminPath))
	for i, raw := range lines {
		n := i + 1
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) > MaxRobotsLineLen {
			return bad(fmt.Sprintf("Line %d is longer than %d characters.", n, MaxRobotsLineLen))
		}
		if hasControl(line) {
			return bad(fmt.Sprintf("Line %d contains a control character.", n))
		}
		if admin != "" && strings.Contains(strings.ToLower(line), admin) {
			return bad(fmt.Sprintf("Line %d names the admin address, which robots.txt must not reveal.", n))
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		name = strings.ToLower(strings.TrimSpace(name))
		if !ok || !robotsDirective[name] {
			return bad(fmt.Sprintf("Line %d: use User-agent, Allow, Disallow, Crawl-delay, Sitemap or a # comment.", n))
		}
		value = strings.TrimSpace(value)
		if name == "sitemap" && !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
			return bad(fmt.Sprintf("Line %d: a Sitemap must be an http(s) address.", n))
		}
		if (name == "user-agent" || name == "crawl-delay") && value == "" {
			return bad(fmt.Sprintf("Line %d: %s needs a value.", n, line[:strings.Index(line, ":")]))
		}
	}
	return nil
}

// validateRobotsVerification checks the robots and verification keys.
func (s Settings) validateRobotsVerification() []FieldError {
	var errs []FieldError
	r := s.Robots
	if r.MaxSnippet != nil && *r.MaxSnippet < -1 {
		errs = append(errs, FieldError{"robots[max_snippet]", "Use -1, 0 or a positive number."})
	}
	if !containsString(MaxImagePreviews, r.MaxImagePreview) {
		errs = append(errs, FieldError{"robots[max_image_preview]", "Choose one of the listed sizes."})
	}
	errs = append(errs, ValidateRobotsLines(r.ExtraLines, "")...)

	v := s.Verification
	code := func(field, val string) {
		switch {
		case utf8.RuneCountInString(val) > MaxMetaContentLen:
			errs = append(errs, FieldError{field, fmt.Sprintf("At most %d characters.", MaxMetaContentLen)})
		case hasControl(val):
			errs = append(errs, FieldError{field, "Remove the control characters."})
		}
	}
	code(verificationPrefix+"[google]", v.Google)
	code(verificationPrefix+"[bing]", v.Bing)
	code(verificationPrefix+"[yandex]", v.Yandex)
	code(verificationPrefix+"[baidu]", v.Baidu)
	code(verificationPrefix+"[pinterest]", v.Pinterest)
	code(verificationPrefix+"[facebook]", v.Facebook)
	if len(v.Custom) > MaxCustomMetaTags {
		errs = append(errs, FieldError{verificationPrefix + "[custom]", fmt.Sprintf("At most %d custom tags.", MaxCustomMetaTags)})
	}
	for i, m := range v.Custom {
		base := fmt.Sprintf("%s[custom][%d]", verificationPrefix, i)
		if len(m.Name) > maxMetaNameLen || !metaNameRE.MatchString(m.Name) {
			errs = append(errs, FieldError{base + "[name]", "Use lowercase letters, digits and : . _ - (64 characters at most)."})
		} else if ReservedMetaName(m.Name) {
			errs = append(errs, FieldError{base + "[name]", "This name is used by the page itself. Choose another."})
		}
		code(base+"[content]", m.Content)
	}
	return errs
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
