// Package seo holds the site-wide SEO settings, the %variable% template engine
// and the resolver that turns a template into a page title or description.
// The public head, the admin preview and the editor panel all call Resolve, so
// what an editor sees is what a visitor gets.
package seo

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Limits on the stored values.
const (
	MaxTemplateLen = 300
	MaxVariables   = 20
	MaxVariableLen = 200
)

// DefaultTitle is the title template a site starts with. With the default
// separator it reproduces the title the head rendered before templates existed.
const DefaultTitle = "%title% %sep% %sitename%"

// DefaultSeparator is the separator a site starts with.
const DefaultSeparator = "|"

// Separators lists the valid separator values.
var Separators = []string{"|", "-", "•", "»", "·", "–", "—"}

// KnownVars lists the built-in variable names, without the percent signs.
// Owner-defined variables are written %cv_<name>%.
var KnownVars = []string{
	"title", "sitename", "tagline", "sep", "page", "pagetotal", "currentyear",
	"excerpt", "author", "date", "term", "searchphrase", "archive",
}

// CustomVarPrefix starts the name of an owner-defined variable.
const CustomVarPrefix = "cv_"

var customNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,23}$`)

// TypeTemplates are the templates for one content type, keyed by schema slug.
type TypeTemplates struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// ArchiveTemplates are the title templates for the generated archive pages.
type ArchiveTemplates struct {
	Archive  string `json:"archive"`
	Author   string `json:"author"`
	Date     string `json:"date"`
	Search   string `json:"search"`
	NotFound string `json:"notfound"`
}

// SocialCards lists the valid Social.Card values. "" means large when an image
// exists and no image card otherwise.
var SocialCards = []string{"", "large", "summary"}

// Limits on the social values.
const maxSocialURLLen = 2048

var (
	twitterHandleRE = regexp.MustCompile(`^@[A-Za-z0-9_]{1,15}$`)
	fbAppIDRE       = regexp.MustCompile(`^[0-9]{1,32}$`)
)

// SocialType holds the social overrides for one content type.
type SocialType struct {
	Title string `json:"title,omitempty"`
	Image string `json:"image,omitempty"`
}

// Social configures the Open Graph and X tags. Every field is zero by default,
// which keeps the public head exactly as it was before this key existed.
type Social struct {
	Image       string                `json:"image,omitempty"`        // site default share image
	TwitterSite string                `json:"twitter_site,omitempty"` // "@name"
	Card        string                `json:"card,omitempty"`         // see SocialCards
	FBAppID     string                `json:"fb_app_id,omitempty"`
	FBPage      string                `json:"fb_page,omitempty"`   // article:publisher
	SiteTags    bool                  `json:"site_tags,omitempty"` // og:site_name + og:locale
	Articles    bool                  `json:"articles,omitempty"`  // article:* on posts
	Types       map[string]SocialType `json:"types,omitempty"`
	Taxonomy    map[string]string     `json:"taxonomy,omitempty"` // title templates only
}

// IsZero reports whether no social setting is saved.
func (s Social) IsZero() bool {
	return s.Image == "" && s.TwitterSite == "" && s.Card == "" && s.FBAppID == "" &&
		s.FBPage == "" && !s.SiteTags && !s.Articles && len(s.Types) == 0 && len(s.Taxonomy) == 0
}

// Settings is the SEO configuration. It is stored as one jsonb document so a
// later tab can add keys without a migration.
type Settings struct {
	Separator string                   `json:"separator"`
	Home      string                   `json:"home"`
	Types     map[string]TypeTemplates `json:"types"`
	Taxonomy  map[string]string        `json:"taxonomy"`
	Archives  ArchiveTemplates         `json:"archives"`
	Variables map[string]string        `json:"variables"`
	Social    Social                   `json:"social"`
}

// FieldError names a setting and what is wrong with it.
type FieldError struct {
	Field   string
	Message string
}

// Defaults is what a site gets before anyone saves the page. Types and Taxonomy
// are empty: a missing entry means DefaultTitle and no description template.
func Defaults() Settings {
	return Settings{
		Separator: DefaultSeparator,
		Home:      DefaultTitle,
		Types:     map[string]TypeTemplates{},
		Taxonomy:  map[string]string{},
		Archives: ArchiveTemplates{
			Archive: DefaultTitle, Author: DefaultTitle, Date: DefaultTitle,
			Search: DefaultTitle, NotFound: DefaultTitle,
		},
		Variables: map[string]string{},
	}
}

// Decode reads stored settings. Unknown keys are ignored and missing keys keep
// their default, so a document written by an older or newer version still loads.
func Decode(raw []byte) (Settings, error) {
	s := Defaults()
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s); err != nil {
			return Defaults(), fmt.Errorf("decode seo settings: %w", err)
		}
	}
	if s.Types == nil {
		s.Types = map[string]TypeTemplates{}
	}
	if s.Taxonomy == nil {
		s.Taxonomy = map[string]string{}
	}
	if s.Variables == nil {
		s.Variables = map[string]string{}
	}
	return s, nil
}

// Clone returns a copy that shares no map with s.
func (s Settings) Clone() Settings {
	c := s
	c.Types = make(map[string]TypeTemplates, len(s.Types))
	for k, v := range s.Types {
		c.Types[k] = v
	}
	c.Taxonomy = make(map[string]string, len(s.Taxonomy))
	for k, v := range s.Taxonomy {
		c.Taxonomy[k] = v
	}
	c.Variables = make(map[string]string, len(s.Variables))
	for k, v := range s.Variables {
		c.Variables[k] = v
	}
	if s.Social.Types != nil {
		c.Social.Types = make(map[string]SocialType, len(s.Social.Types))
		for k, v := range s.Social.Types {
			c.Social.Types[k] = v
		}
	}
	if s.Social.Taxonomy != nil {
		c.Social.Taxonomy = make(map[string]string, len(s.Social.Taxonomy))
		for k, v := range s.Social.Taxonomy {
			c.Social.Taxonomy[k] = v
		}
	}
	return c
}

// Validate checks every field and returns one error per problem.
func (s Settings) Validate() []FieldError {
	var errs []FieldError
	if !containsString(Separators, s.Separator) {
		errs = append(errs, FieldError{"separator", "Choose one of the listed separators."})
	}
	if len(s.Variables) > MaxVariables {
		errs = append(errs, FieldError{"variables", fmt.Sprintf("At most %d custom variables.", MaxVariables)})
	}
	for _, name := range sortedKeys(s.Variables) {
		field := "variable[" + name + "]"
		if !customNameRE.MatchString(name) {
			errs = append(errs, FieldError{field, "Use a lowercase name of letters, digits and underscores, starting with a letter (24 characters at most)."})
		}
		if utf8.RuneCountInString(s.Variables[name]) > MaxVariableLen {
			errs = append(errs, FieldError{field, fmt.Sprintf("A value can be at most %d characters.", MaxVariableLen)})
		}
	}
	check := func(field, tpl string) {
		if utf8.RuneCountInString(tpl) > MaxTemplateLen {
			errs = append(errs, FieldError{field, fmt.Sprintf("A template can be at most %d characters.", MaxTemplateLen)})
			return
		}
		for _, name := range s.UnknownVars(tpl) {
			errs = append(errs, FieldError{field, "Unknown variable %" + name + "%."})
		}
	}
	check("home", s.Home)
	check("archives[archive]", s.Archives.Archive)
	check("archives[author]", s.Archives.Author)
	check("archives[date]", s.Archives.Date)
	check("archives[search]", s.Archives.Search)
	check("archives[notfound]", s.Archives.NotFound)
	for _, slug := range sortedKeys(s.Types) {
		check("type["+slug+"][title]", s.Types[slug].Title)
		check("type["+slug+"][description]", s.Types[slug].Description)
	}
	for _, kind := range sortedKeys(s.Taxonomy) {
		check("taxonomy["+kind+"]", s.Taxonomy[kind])
	}
	errs = append(errs, s.validateSocial(check)...)
	return errs
}

// validateSocial checks the social key. check validates one template.
func (s Settings) validateSocial(check func(field, tpl string)) []FieldError {
	var errs []FieldError
	so := s.Social
	url := func(field, v string) {
		if v == "" {
			return
		}
		if len(v) > maxSocialURLLen || !ValidCanonical(v) {
			errs = append(errs, FieldError{field, "Enter a root-relative path or an http(s) address."})
		}
	}
	if !containsString(SocialCards, so.Card) {
		errs = append(errs, FieldError{"social[card]", "Choose one of the listed card types."})
	}
	if so.TwitterSite != "" && !twitterHandleRE.MatchString(so.TwitterSite) {
		errs = append(errs, FieldError{"social[twitter_site]", "Use an X handle such as @name (letters, digits, underscore; 15 at most)."})
	}
	if so.FBAppID != "" && !fbAppIDRE.MatchString(so.FBAppID) {
		errs = append(errs, FieldError{"social[fb_app_id]", "Use digits only."})
	}
	url("social[fb_page]", so.FBPage)
	url("social[image]", so.Image)
	for _, slug := range sortedKeys(so.Types) {
		check("social["+slug+"][title]", so.Types[slug].Title)
		url("social["+slug+"][image]", so.Types[slug].Image)
	}
	for _, kind := range sortedKeys(so.Taxonomy) {
		check("social_taxonomy["+kind+"]", so.Taxonomy[kind])
	}
	return errs
}

// UnknownVars returns the variable names in tpl that are neither built in nor
// a defined custom variable.
func (s Settings) UnknownVars(tpl string) []string {
	var bad []string
	for _, name := range varNames(tpl) {
		if containsString(KnownVars, name) {
			continue
		}
		if custom, ok := strings.CutPrefix(name, CustomVarPrefix); ok {
			if _, defined := s.Variables[custom]; defined {
				continue
			}
		}
		bad = append(bad, name)
	}
	return bad
}

// varNames lists the %name% tokens in tpl, in order.
func varNames(tpl string) []string {
	var out []string
	for i := 0; i < len(tpl); i++ {
		if tpl[i] != '%' {
			continue
		}
		n := tokenLen(tpl[i+1:])
		if n == 0 {
			continue
		}
		out = append(out, tpl[i+1:i+1+n])
		i += n + 1
	}
	return out
}

// tokenLen returns the length of the variable name that starts rest and is
// closed by a percent sign, or 0 when rest does not start a variable.
func tokenLen(rest string) int {
	for j := 0; j < len(rest); j++ {
		c := rest[j]
		switch {
		case c == '%':
			return j
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
		default:
			return 0
		}
	}
	return 0
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
