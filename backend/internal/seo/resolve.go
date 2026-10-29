package seo

import "strings"

// Kind says which template a page uses.
type Kind string

// Page kinds.
const (
	KindPage     Kind = "page"
	KindSingle   Kind = "single"
	KindHome     Kind = "home"
	KindArchive  Kind = "archive"
	KindTaxonomy Kind = "taxonomy"
	KindAuthor   Kind = "author"
	KindSearch   Kind = "search"
	KindDate     Kind = "date"
	KindNotFound Kind = "notfound"
)

// siteSuffixTemplate is the tail that makes a template "title, then the site".
const siteSuffixTemplate = " %sep% %sitename%"

// Input describes one page to resolve.
type Input struct {
	Kind       Kind
	SchemaSlug string // content type, for KindPage and KindSingle
	TermKind   string // taxonomy slug, for KindTaxonomy
	Vars       Vars

	// HandTitle and HandDescription are the values an editor wrote on the
	// entry. TitleSet and DescriptionSet say whether they were written at all,
	// since a stored value can equal the entry name.
	HandTitle, HandDescription string
	TitleSet, DescriptionSet   bool
}

// Result is the resolved head text. An empty Description means no opinion: the
// caller keeps its own fallback.
type Result struct {
	Title       string
	Description string
}

// titleTemplate returns the title template for in, or DefaultTitle.
func titleTemplate(s *Settings, in Input) string {
	var tpl string
	switch in.Kind {
	case KindHome:
		tpl = s.Home
	case KindPage, KindSingle:
		tpl = s.Types[in.SchemaSlug].Title
	case KindTaxonomy:
		tpl = s.Taxonomy[in.TermKind]
	case KindArchive:
		tpl = s.Archives.Archive
	case KindAuthor:
		tpl = s.Archives.Author
	case KindSearch:
		tpl = s.Archives.Search
	case KindDate:
		tpl = s.Archives.Date
	case KindNotFound:
		tpl = s.Archives.NotFound
	}
	if tpl == "" {
		return DefaultTitle
	}
	return tpl
}

// IsLegacyTitle reports whether the page title is what the head produced before
// templates existed: the default template with the default separator. It does
// not allocate.
func IsLegacyTitle(s *Settings, in Input) bool {
	return s.Separator == DefaultSeparator && titleTemplate(s, in) == DefaultTitle
}

// HasDescriptionTemplate reports whether in has a description template that
// would be used when the entry has no written description.
func HasDescriptionTemplate(s *Settings, in Input) bool {
	return !in.DescriptionSet && (in.Kind == KindPage || in.Kind == KindSingle) &&
		s.Types[in.SchemaSlug].Description != ""
}

// Resolve returns the title and description for a page. A value an editor wrote
// on the entry is kept as written (the title still gets the site suffix); the
// templates only fill in what was left empty.
func Resolve(s *Settings, in Input) Result {
	v := in.Vars
	if v.Custom == nil {
		v.Custom = s.Variables
	}
	sep := s.Separator
	if sep == "" {
		sep = DefaultSeparator
	}

	var r Result
	if in.TitleSet {
		r.Title = TitleWithSite(in.HandTitle, v.SiteName, sep)
	} else {
		r.Title = expandTitle(titleTemplate(s, in), v, sep)
	}

	switch {
	case in.DescriptionSet:
		r.Description = in.HandDescription
	case in.Kind == KindPage || in.Kind == KindSingle:
		if tpl := s.Types[in.SchemaSlug].Description; tpl != "" {
			r.Description = Expand(tpl, v, sep)
		}
	}
	return r
}

// expandTitle expands a title template. A template that ends with the site
// name behaves like TitleWithSite: a title that already carries the site name
// is not suffixed twice.
func expandTitle(tpl string, v Vars, sep string) string {
	site := strings.TrimSpace(v.SiteName)
	head, ok := strings.CutSuffix(tpl, siteSuffixTemplate)
	if !ok {
		return Expand(tpl, v, sep)
	}
	head = Expand(head, v, sep)
	if site == "" || head == site || HasSiteSuffix(head, site) {
		return head
	}
	return Expand(tpl, v, sep)
}
