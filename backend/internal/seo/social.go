package seo

// SocialTitle returns the social title template for in, expanded, or "" when
// none applies and the caller keeps its bare title. The result is not
// suffixed with the site name.
func SocialTitle(s *Settings, in Input) string {
	var tpl string
	switch in.Kind {
	case KindPage, KindSingle:
		tpl = s.Social.Types[in.SchemaSlug].Title
	case KindTaxonomy:
		tpl = s.Social.Taxonomy[in.TermKind]
	}
	if tpl == "" || tpl == "%title%" {
		return ""
	}
	v := in.Vars
	if v.Custom == nil {
		v.Custom = s.Variables
	}
	sep := s.Separator
	if sep == "" {
		sep = DefaultSeparator
	}
	return Expand(tpl, v, sep)
}

// SocialImage returns the default share image of the content type of in, or ""
// for any other kind.
func SocialImage(s *Settings, in Input) string {
	if in.Kind == KindPage || in.Kind == KindSingle {
		return s.Social.Types[in.SchemaSlug].Image
	}
	return ""
}
