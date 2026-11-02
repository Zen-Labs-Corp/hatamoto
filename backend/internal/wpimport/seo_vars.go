package wpimport

import (
	"regexp"
	"strings"

	"hatamoto.dev/core/internal/seo"
)

// Plugin variable -> Hatamoto variable (without percent signs). Variables
// that are absent are not mapped and get dropped from the template.
var (
	yoastVars = map[string]string{
		"title": "title", "post_title": "title", "page_title": "title",
		"sitename": "sitename", "sitedesc": "tagline", "sep": "sep",
		"page": "page", "pagenumber": "page", "pagetotal": "pagetotal",
		"currentyear": "currentyear",
		"excerpt":     "excerpt", "excerpt_only": "excerpt",
		"name": "author", "author_name": "author",
		"date": "date", "modified": "date",
		"term_title": "term", "category_title": "term", "category": "term", "tag": "term",
		"searchphrase":  "searchphrase",
		"archive_title": "archive", "pt_plural": "archive",
	}
	rankMathVars = map[string]string{
		"title": "title", "seo_title": "title", "post_title": "title",
		"sitename": "sitename", "site_title": "sitename", "sitedesc": "tagline", "sep": "sep",
		"page": "page", "pagenumber": "page", "pagetotal": "pagetotal",
		"currentyear": "currentyear", "year": "currentyear",
		"excerpt": "excerpt", "excerpt_only": "excerpt",
		"name": "author", "author": "author",
		"date": "date", "modified": "date",
		"term": "term", "category": "term", "categories": "term", "tag": "term",
		"search_query": "searchphrase", "searchphrase": "searchphrase",
		"archive_title": "archive", "pt_plural": "archive",
	}
	tsfVars = map[string]string{
		"title": "title", "blogname": "sitename", "site_title": "sitename",
		"description": "tagline", "site_description": "tagline", "tagline": "tagline",
		"separator": "sep", "sep": "sep",
		"page": "page", "year": "currentyear",
		"excerpt": "excerpt",
		"author":  "author", "author_name": "author",
		"date": "date",
		"term": "term", "category": "term",
		"search_term": "searchphrase", "query": "searchphrase",
		"archive_title": "archive",
	}
)

// lossyVars map to a Hatamoto variable that renders something different.
var lossyVars = map[string]map[string]bool{
	"yoast": {"modified": true, "page": true, "category": true, "tag": true, "pt_plural": true, "pagenumber": true},
	"rankmath": {"modified": true, "category": true, "categories": true, "tag": true,
		"pt_plural": true, "page": true},
	"tsf": {"description": true, "category": true},
}

var (
	yoastTokenRE    = regexp.MustCompile(`%%([A-Za-z0-9_]+)%%`)
	rankMathTokenRE = regexp.MustCompile(`%([A-Za-z_]+)(\([^)]*\))?%`)
	tsfTokenRE      = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_]+)\s*\}\}`)
)

// TranslateTemplate converts a plugin title/description template to Hatamoto's.
// plugin is "yoast" (%%x%%), "rankmath" (%x% and %x(arg)%) or "tsf" ({{x}}).
// notMapped lists the tokens that were dropped; lossy lists the tokens that
// were mapped to a variable that renders slightly differently. Both hold the
// tokens as written, once each. An empty out means nothing is left.
func TranslateTemplate(plugin, tpl string) (out string, notMapped []string, lossy []string) {
	var re *regexp.Regexp
	var table map[string]string
	switch plugin {
	case "yoast":
		re, table = yoastTokenRE, yoastVars
	case "rankmath":
		re, table = rankMathTokenRE, rankMathVars
	case "tsf":
		re, table = tsfTokenRE, tsfVars
	default:
		return "", nil, nil
	}
	seenNM, seenLossy := map[string]bool{}, map[string]bool{}
	out = re.ReplaceAllStringFunc(tpl, func(tok string) string {
		m := re.FindStringSubmatch(tok)
		name := strings.ToLower(m[1])
		target, ok := table[name]
		// Rank Math arguments (%customfield(x)%) have no equivalent.
		if ok && len(m) > 2 && m[2] != "" {
			ok = false
		}
		// Defence in depth: never emit a variable Hatamoto would reject.
		if ok && len((seo.Settings{}).UnknownVars("%"+target+"%")) > 0 {
			ok = false
		}
		if !ok {
			if !seenNM[tok] {
				seenNM[tok] = true
				notMapped = append(notMapped, tok)
			}
			return ""
		}
		if lossyVars[plugin][name] && !seenLossy[tok] {
			seenLossy[tok] = true
			lossy = append(lossy, tok)
		}
		return "%" + target + "%"
	})
	out = strings.Join(strings.Fields(out), " ")
	return out, notMapped, lossy
}
