package wpimport

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"

	"hatamoto.dev/core/internal/seo"
	"hatamoto.dev/core/internal/taxonomy"
)

// seoField is one setting the import would write. Path is the prefix the
// validator uses for this field's errors (see seo.FieldError).
type seoField struct {
	Key, Label string
	Path       string
	Apply      func(*seo.Settings)
	Show       func(seo.Settings) string
}

// seoPlan is everything the mapper found, before validation.
type seoPlan struct {
	Plugin    string
	Fields    []seoField
	NotMapped []string
	Notes     []string // lossy translations
}

// seoSpec names the options of one plugin's settings file: scalar options by
// logical name and regexes whose first group is the post type or taxonomy.
type seoSpec struct {
	opt                           map[string]string
	typeTitle, typeDesc, taxTitle *regexp.Regexp
	typeNoindex, taxNoindex       *regexp.Regexp
	notMappedKeys                 map[string]string // option key prefix -> label
	notMappedNoindex              map[string]string // option key -> label, when noindex
	customRobots                  bool              // *_robots only applies when *_custom_robots is on (Rank Math)
}

var seoSpecs = map[string]seoSpec{
	"rankmath": {
		customRobots: true,
		opt: map[string]string{
			"sep": "title_separator", "home": "homepage_title",
			"author": "author_archive_title", "date": "date_archive_title",
			"search": "search_title", "notfound": "404_title", "author_noindex": "author_robots",
			"twitter": "twitter_author_names", "card": "twitter_card_type", "fbpage": "social_url_facebook",
			"image": "open_graph_image", "robots_txt": "robots_txt_content", "robots_global": "robots_global",
			"google": "google_verify", "bing": "bing_verify", "yandex": "yandex_verify",
			"baidu": "baidu_verify", "pinterest": "pinterest_verify",
		},
		typeTitle: regexp.MustCompile(`^pt_(.+)_title$`), typeDesc: regexp.MustCompile(`^pt_(.+)_description$`),
		taxTitle:    regexp.MustCompile(`^tax_(.+)_title$`),
		typeNoindex: regexp.MustCompile(`^pt_(.+)_robots$`), taxNoindex: regexp.MustCompile(`^tax_(.+)_robots$`),
		notMappedKeys:    map[string]string{"local_": "Local SEO", "phone_numbers": "Local SEO", "opening_hours": "Local SEO", "redirections": "Redirections", "indexnow": "IndexNow key", "rank_math_indexnow": "IndexNow key"},
		notMappedNoindex: map[string]string{"date_archive_robots": "Noindex on date archives", "404_robots": "Noindex on 404 pages"},
	},
	"yoast": {
		opt: map[string]string{
			"sep": "separator", "home": "title-home-wpseo",
			"author": "title-author-wpseo", "date": "title-archive-wpseo",
			"search": "title-search-wpseo", "notfound": "title-404-wpseo", "author_noindex": "noindex-author-wpseo",
			"twitter": "twitter_site", "card": "twitter_card_type", "fbapp": "fbadminapp", "fbpage": "facebook_site",
			"image": "og_default_image", "sitetags": "opengraph",
			"google": "googleverify", "bing": "msverify", "yandex": "yandexverify",
			"baidu": "baiduverify", "pinterest": "pinterestverify",
		},
		typeTitle: regexp.MustCompile(`^title-(.+)$`), typeDesc: regexp.MustCompile(`^metadesc-(.+)$`),
		taxTitle:    regexp.MustCompile(`^title-tax-(.+)$`),
		typeNoindex: regexp.MustCompile(`^noindex-(.+)$`), taxNoindex: regexp.MustCompile(`^noindex-tax-(.+)$`),
		notMappedKeys:    map[string]string{"indexnow": "IndexNow key", "enable_index_now": "IndexNow key", "redirect": "Redirections"},
		notMappedNoindex: map[string]string{"noindex-archive-wpseo": "Noindex on date archives", "noindex-404-wpseo": "Noindex on 404 pages"},
	},
	"tsf": {
		opt: map[string]string{
			"sep": "title_separator", "home": "homepage_title", "author_noindex": "author_noindex",
			"twitter": "twitter_site", "card": "twitter_card", "fbapp": "facebook_appid", "fbpage": "facebook_publisher",
			"image": "social_image_fb_url", "sitetags": "og_tags",
			"google": "google_verification", "bing": "bing_verification", "yandex": "yandex_verification",
			"baidu": "baidu_verification", "pinterest": "pint_verification",
		},
		typeNoindex: regexp.MustCompile(`^(.+)_noindex$`), taxNoindex: regexp.MustCompile(`^(category|post_tag)_noindex$`),
		notMappedNoindex: map[string]string{"date_noindex": "Noindex on date archives"},
	},
}

var seoSeparatorNames = map[string]map[string]string{
	"yoast": {"sc-dash": "-", "sc-ndash": "–", "sc-mdash": "—", "sc-middot": "·", "sc-bull": "•", "sc-pipe": "|", "sc-raquo": "»"},
	"tsf":   {"pipe": "|", "hyphen": "-", "dash": "-", "ndash": "–", "mdash": "—", "bull": "•", "middot": "·"},
}

var (
	metaContentRE = regexp.MustCompile(`(?i)content\s*=\s*["']([^"']*)["']`)
	twitterURLRE  = regexp.MustCompile(`(?i)^(?:https?://)?(?:www\.)?(?:twitter|x)\.com/`)
)

// buildSEOPlan turns the parsed settings file and the live read into fields.
// The file wins per key, the live read fills the gaps. schemaOf resolves a
// WordPress post type to its Hatamoto schema slug; plugin is the fallback
// display plugin (from the scan).
func buildSEOPlan(file *SEOFile, live *seoLive, schemaOf func(wpType string) (slug string, ok bool), plugin string) seoPlan {
	b := &seoBuilder{seen: map[string]bool{}}
	b.plan.Plugin = pluginKey(plugin)
	if live != nil && live.Plugin != "" {
		b.plan.Plugin = pluginKey(live.Plugin)
	}
	if file != nil {
		b.plan.Plugin = pluginKey(file.Plugin)
		if spec, ok := seoSpecs[file.Plugin]; ok {
			b.fromFile(file, spec, schemaOf)
		}
	}
	if live != nil {
		b.fromLive(live)
	}
	return b.plan
}

// pluginKey normalises a plugin name or key to the display key.
func pluginKey(name string) string {
	n := strings.ToLower(strings.ReplaceAll(name, " ", ""))
	switch {
	case strings.Contains(n, "yoast"):
		return "yoast"
	case strings.Contains(n, "rankmath"):
		return "rankmath"
	case strings.Contains(n, "seoframework"), n == "tsf":
		return "tsf"
	case strings.Contains(n, "seopress"):
		return "seopress"
	case strings.Contains(n, "allinone"), n == "aioseo":
		return "aioseo"
	}
	return n
}

type seoBuilder struct {
	plan seoPlan
	seen map[string]bool
}

func (b *seoBuilder) add(f seoField) {
	if b.seen[f.Key] {
		return
	}
	b.seen[f.Key] = true
	b.plan.Fields = append(b.plan.Fields, f)
}

func (b *seoBuilder) notMapped(s string) {
	for _, n := range b.plan.NotMapped {
		if n == s {
			return
		}
	}
	b.plan.NotMapped = append(b.plan.NotMapped, s)
}

func (b *seoBuilder) str(key, label, path, val string, get func(seo.Settings) string, set func(*seo.Settings, string)) {
	b.add(seoField{Key: key, Label: label, Path: path,
		Apply: func(s *seo.Settings) { set(s, val) }, Show: get})
}

func (b *seoBuilder) flag(key, label, path string, get func(seo.Settings) bool, set func(*seo.Settings)) {
	b.add(seoField{Key: key, Label: label, Path: path, Apply: set,
		Show: func(s seo.Settings) string { return yesNo(get(s)) }})
}

func yesNo(v bool) string {
	if v {
		return "Yes"
	}
	return "No"
}

// template translates a plugin template and adds the field when something is left.
func (b *seoBuilder) template(plugin, key, label, path, tpl string, get func(seo.Settings) string, set func(*seo.Settings, string)) {
	if strings.TrimSpace(tpl) == "" { // empty means "use the default"
		return
	}
	out, nm, lossy := TranslateTemplate(plugin, tpl)
	for _, tok := range nm {
		b.notMapped(fmt.Sprintf("%s in %s", tok, label))
	}
	for _, tok := range lossy {
		b.plan.Notes = append(b.plan.Notes, fmt.Sprintf("%s in %s is approximated", tok, label))
	}
	if out == "" {
		return
	}
	b.str(key, label, path, out, get, set)
}

func (b *seoBuilder) fromFile(file *SEOFile, spec seoSpec, schemaOf func(string) (string, bool)) {
	plugin, vals := file.Plugin, file.Values
	opt := func(name string) any { return vals[spec.opt[name]] }
	optStr := func(name string) string { return valStr(opt(name)) }

	// Separator.
	if raw := optStr("sep"); raw != "" {
		sep := raw
		if names := seoSeparatorNames[plugin]; names != nil {
			sep = names[strings.ToLower(raw)]
		}
		sep = html.UnescapeString(sep)
		if containsSep(sep) {
			b.str("separator", "Separator", "separator", sep,
				func(s seo.Settings) string { return s.Separator }, func(s *seo.Settings, v string) { s.Separator = v })
		} else {
			b.notMapped("Separator " + raw + " (not available here)")
		}
	}
	b.template(plugin, "home", "Home title", "home", optStr("home"),
		func(s seo.Settings) string { return s.Home }, func(s *seo.Settings, v string) { s.Home = v })
	archives := []struct {
		name, label string
		get         func(seo.Settings) string
		set         func(*seo.Settings, string)
	}{
		{"author", "Author archive title", func(s seo.Settings) string { return s.Archives.Author }, func(s *seo.Settings, v string) { s.Archives.Author = v }},
		{"date", "Date archive title", func(s seo.Settings) string { return s.Archives.Date }, func(s *seo.Settings, v string) { s.Archives.Date = v }},
		{"search", "Search title", func(s seo.Settings) string { return s.Archives.Search }, func(s *seo.Settings, v string) { s.Archives.Search = v }},
		{"notfound", "Not found title", func(s seo.Settings) string { return s.Archives.NotFound }, func(s *seo.Settings, v string) { s.Archives.NotFound = v }},
	}
	for _, a := range archives {
		if spec.opt[a.name] != "" {
			b.template(plugin, "archive."+a.name, a.label, "archives["+a.name+"]", optStr(a.name), a.get, a.set)
		}
	}

	// Per-type and per-taxonomy options, found by scanning the option names.
	for _, k := range sortedSEOKeys(vals) {
		if m := matchName(spec.typeTitle, k); m != "" && !strings.HasPrefix(m, "tax-") {
			if slug, ok := schemaOf(m); ok {
				b.typeTemplate(plugin, slug, "title", valStr(vals[k]))
			}
		}
		if m := matchName(spec.typeDesc, k); m != "" {
			if slug, ok := schemaOf(m); ok {
				b.typeTemplate(plugin, slug, "description", valStr(vals[k]))
			}
		}
		if m := matchName(spec.taxTitle, k); m != "" {
			if kind := termKind(m); taxonomy.ValidKind(kind) {
				b.template(plugin, "taxonomy."+kind, "Title template: "+kind, "taxonomy["+kind+"]", valStr(vals[k]),
					func(s seo.Settings) string { return s.Taxonomy[kind] },
					func(s *seo.Settings, v string) {
						if s.Taxonomy == nil {
							s.Taxonomy = map[string]string{}
						}
						s.Taxonomy[kind] = v
					})
			}
		}
		robotsOn := !spec.customRobots || truthy(vals[strings.TrimSuffix(k, "_robots")+"_custom_robots"])
		if m := matchName(spec.typeNoindex, k); m != "" && !strings.HasPrefix(m, "tax-") && robotsOn {
			if slug, ok := schemaOf(m); ok && hasRobot(vals[k], "noindex") {
				b.flag("noindex.type."+slug, "Noindex: "+slug, "index[noindex_types]",
					func(s seo.Settings) bool { return s.Index.NoIndexTypes[slug] },
					func(s *seo.Settings) {
						if s.Index.NoIndexTypes == nil {
							s.Index.NoIndexTypes = map[string]bool{}
						}
						s.Index.NoIndexTypes[slug] = true
					})
			}
		}
		if m := matchName(spec.taxNoindex, k); m != "" && robotsOn && hasRobot(vals[k], "noindex") {
			if kind := termKind(m); taxonomy.ValidKind(kind) {
				b.flag("noindex.taxonomy."+kind, "Noindex: "+kind, "index[noindex_taxonomy]",
					func(s seo.Settings) bool { return s.Index.NoIndexTaxonomy[kind] },
					func(s *seo.Settings) {
						if s.Index.NoIndexTaxonomy == nil {
							s.Index.NoIndexTaxonomy = map[string]bool{}
						}
						s.Index.NoIndexTaxonomy[kind] = true
					})
			}
		}
		if hasRobot(vals[k], "nofollow") {
			b.notMapped("nofollow settings")
		}
	}
	if hasRobot(opt("author_noindex"), "noindex") {
		b.flag("noindex.author", "Noindex: author archives", "index[noindex_author]",
			func(s seo.Settings) bool { return s.Index.NoIndexAuthor }, func(s *seo.Settings) { s.Index.NoIndexAuthor = true })
	}
	for k, label := range spec.notMappedNoindex {
		if hasRobot(vals[k], "noindex") {
			b.notMapped(label)
		}
	}
	for _, k := range sortedSEOKeys(vals) {
		for prefix, label := range spec.notMappedKeys {
			if strings.HasPrefix(k, prefix) && !isEmptySEOValue(vals[k]) {
				b.notMapped(label)
			}
		}
	}

	// Social.
	if h := twitterHandle(optStr("twitter")); h != "" {
		b.socialTwitter(h)
	}
	switch strings.ToLower(optStr("card")) {
	case "summary_large_image":
		b.card("large")
	case "summary":
		b.card("summary")
	}
	if v := optStr("fbapp"); v != "" {
		b.str("social.fb_app_id", "Facebook app ID", "social[fb_app_id]", v,
			func(s seo.Settings) string { return s.Social.FBAppID }, func(s *seo.Settings, v string) { s.Social.FBAppID = v })
	}
	if v := optStr("fbpage"); v != "" {
		b.str("social.fb_page", "Facebook page", "social[fb_page]", v,
			func(s seo.Settings) string { return s.Social.FBPage }, func(s *seo.Settings, v string) { s.Social.FBPage = v })
	}
	if v := optStr("image"); v != "" {
		b.str("social.image", "Default share image", "social[image]", v,
			func(s seo.Settings) string { return s.Social.Image }, func(s *seo.Settings, v string) { s.Social.Image = v })
	}
	if truthy(opt("sitetags")) {
		b.siteTags()
	}

	// Verification codes.
	for _, name := range []string{"google", "bing", "yandex", "baidu", "pinterest"} {
		if v := verificationCode(optStr(name)); v != "" {
			b.verification(name, v)
		}
	}

	// Robots.
	if raw := optStr("robots_txt"); raw != "" {
		if lines := robotsExtra(raw); len(lines) > 0 {
			b.robotsExtra(lines)
		}
	}
	if list, _ := opt("robots_global").([]string); len(list) > 0 {
		if hasRobot(list, "noarchive") {
			b.flag("robots.noarchive", "Robots: noarchive", "robots", func(s seo.Settings) bool { return s.Robots.NoArchive },
				func(s *seo.Settings) { s.Robots.NoArchive = true })
		}
		if hasRobot(list, "nosnippet") {
			b.flag("robots.nosnippet", "Robots: nosnippet", "robots", func(s seo.Settings) bool { return s.Robots.NoSnippet },
				func(s *seo.Settings) { s.Robots.NoSnippet = true })
		}
	}
}

func (b *seoBuilder) typeTemplate(plugin, slug, which, tpl string) {
	label := strings.ToUpper(which[:1]) + which[1:] + " template: " + slug
	get := func(s seo.Settings) string {
		if which == "title" {
			return s.Types[slug].Title
		}
		return s.Types[slug].Description
	}
	set := func(s *seo.Settings, v string) {
		if s.Types == nil {
			s.Types = map[string]seo.TypeTemplates{}
		}
		t := s.Types[slug]
		if which == "title" {
			t.Title = v
		} else {
			t.Description = v
		}
		s.Types[slug] = t
	}
	b.template(plugin, "type."+slug+"."+which, label, "type["+slug+"]["+which+"]", tpl, get, set)
}

func (b *seoBuilder) socialTwitter(handle string) {
	b.str("social.twitter_site", "X handle", "social[twitter_site]", handle,
		func(s seo.Settings) string { return s.Social.TwitterSite }, func(s *seo.Settings, v string) { s.Social.TwitterSite = v })
}

func (b *seoBuilder) card(v string) {
	b.str("social.card", "X card type", "social[card]", v,
		func(s seo.Settings) string { return s.Social.Card }, func(s *seo.Settings, v string) { s.Social.Card = v })
}

func (b *seoBuilder) siteTags() {
	b.flag("social.site_tags", "Site name and locale tags", "social[site_tags]",
		func(s seo.Settings) bool { return s.Social.SiteTags }, func(s *seo.Settings) { s.Social.SiteTags = true })
}

func (b *seoBuilder) robotsExtra(lines []string) {
	keep, dropped := dropRobotsRules(lines, func(l string) bool { return strings.Contains(strings.ToLower(l), "/admin") })
	for _, l := range dropped {
		b.notMapped("robots.txt line: " + l)
	}
	if len(keep) == 0 {
		return
	}
	b.add(seoField{Key: "robots.extra", Label: "Extra robots.txt lines", Path: "robots[extra]",
		Apply: func(s *seo.Settings) { s.Robots.ExtraLines = append([]string(nil), keep...) },
		Show:  func(s seo.Settings) string { return strings.Join(s.Robots.ExtraLines, "; ") }})
}

func (b *seoBuilder) verification(name, code string) {
	ptr := func(s *seo.Settings) *string {
		switch name {
		case "google":
			return &s.Verification.Google
		case "bing":
			return &s.Verification.Bing
		case "yandex":
			return &s.Verification.Yandex
		case "baidu":
			return &s.Verification.Baidu
		case "pinterest":
			return &s.Verification.Pinterest
		}
		return &s.Verification.Facebook
	}
	b.str("verification."+name, strings.ToUpper(name[:1])+name[1:]+" verification", "verification["+name+"]", code,
		func(s seo.Settings) string { return *ptr(&s) }, func(s *seo.Settings, v string) { *ptr(s) = v })
}

func (b *seoBuilder) fromLive(l *seoLive) {
	if l.Separator != "" {
		b.str("separator", "Separator", "separator", l.Separator,
			func(s seo.Settings) string { return s.Separator }, func(s *seo.Settings, v string) { s.Separator = v })
	}
	if h := twitterHandle(l.TwitterSite); h != "" {
		b.socialTwitter(h)
	}
	if l.SiteName != "" {
		b.siteTags()
	}
	for _, name := range []string{"google", "bing", "yandex", "baidu", "pinterest", "facebook"} {
		if v := verificationCode(l.Verification[name]); v != "" {
			b.verification(name, v)
		}
	}
	if l.Discouraged {
		b.notMapped("Extra robots.txt lines and archive noindex: the source site discourages search engines, so they were not imported")
		return
	}
	if len(l.RobotsExtra) > 0 {
		b.robotsExtra(l.RobotsExtra)
	}
	if l.Noindex["archive"] {
		b.flag("noindex.archive", "Noindex: archives", "index[noindex_archive]",
			func(s seo.Settings) bool { return s.Index.NoIndexArchive }, func(s *seo.Settings) { s.Index.NoIndexArchive = true })
	}
}

// pruneSEOPlan drops every field the validator rejects, so the rest can be
// written (Merge is all-or-nothing). dropped explains each removed field.
func pruneSEOPlan(cur seo.Settings, p seoPlan) (kept []seoField, dropped []string) {
	baseline := map[string]bool{}
	for _, e := range cur.Validate() {
		baseline[e.Field+"\x00"+e.Message] = true
	}
	kept = append([]seoField(nil), p.Fields...)
	for round := 0; round < 20; round++ {
		s := cur.Clone()
		for _, f := range kept {
			f.Apply(&s)
		}
		var bad []seo.FieldError
		for _, e := range s.Validate() {
			if !baseline[e.Field+"\x00"+e.Message] {
				bad = append(bad, e)
			}
		}
		if len(bad) == 0 {
			return kept, dropped
		}
		next := kept[:0:0]
		for _, f := range kept {
			var hit *seo.FieldError
			for i := range bad {
				if strings.HasPrefix(bad[i].Field, f.Path) {
					hit = &bad[i]
					break
				}
			}
			if hit != nil {
				dropped = append(dropped, f.Label+": "+hit.Message)
				continue
			}
			next = append(next, f)
		}
		if len(next) == len(kept) {
			return kept, dropped
		}
		kept = next
	}
	return kept, dropped
}

func matchName(re *regexp.Regexp, key string) string {
	if re == nil {
		return ""
	}
	m := re.FindStringSubmatch(key)
	if m == nil {
		return ""
	}
	n := m[1]
	if strings.HasSuffix(n, "-wpseo") || n == "subpages" || strings.HasPrefix(n, "ptarchive-") || strings.HasSuffix(n, "_archive") {
		return ""
	}
	return n
}

func sortedSEOKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func containsSep(s string) bool {
	for _, v := range seo.Separators {
		if v == s {
			return true
		}
	}
	return false
}

// valStr reads a scalar option value as text.
func valStr(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return t.String()
	}
	return ""
}

func isEmptySEOValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == "" || t == "off"
	case bool:
		return !t
	case []string:
		return len(t) == 0
	}
	return false
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "1", "on", "true", "yes":
			return true
		}
	case json.Number:
		return t.String() != "0"
	case float64:
		return t != 0
	}
	return false
}

// hasRobot reports whether a robots option holds the directive: a list that
// contains it, or a plain switch when asking for noindex.
func hasRobot(v any, directive string) bool {
	switch t := v.(type) {
	case []string:
		for _, s := range t {
			if strings.EqualFold(s, directive) {
				return true
			}
		}
		return false
	case string:
		if strings.EqualFold(strings.TrimSpace(t), directive) {
			return true
		}
	}
	return directive == "noindex" && truthy(v)
}

// twitterHandle normalises a handle or profile URL to "@name".
func twitterHandle(v string) string {
	v = strings.TrimSpace(v)
	v = twitterURLRE.ReplaceAllString(v, "")
	v = strings.Trim(strings.TrimPrefix(v, "@"), "/ ")
	if v == "" {
		return ""
	}
	return "@" + v
}

// verificationCode extracts the code from a value that may be a whole meta tag.
func verificationCode(v string) string {
	v = strings.TrimSpace(v)
	if m := metaContentRE.FindStringSubmatch(v); m != nil {
		return strings.TrimSpace(m[1])
	}
	return v
}
