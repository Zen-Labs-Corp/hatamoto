package wpimport

import (
	"fmt"
	"strings"

	"golang.org/x/text/language"

	"hatamoto.dev/core/internal/entity"
)

// wpmlMetaKeys are the postmeta keys a WPML aware export may carry the item
// language in, in the order they are tried. A plain WordPress export of a WPML
// site usually has none of them.
var wpmlMetaKeys = []string{"_icl_lang_code", "wpml_language", "_wpml_language"}

// itemLang picks the language of a WXR item: postmeta first, then the Polylang term.
func itemLang(meta map[string]string, polylangTerm string) string {
	for _, k := range wpmlMetaKeys {
		if v := strings.TrimSpace(meta[k]); v != "" {
			return v
		}
	}
	return strings.TrimSpace(polylangTerm)
}

// localeFor maps a raw source language tag to the locale the entity is stored
// with. An empty tag is the site language. An invalid tag becomes the site
// language and a warning is returned. Two forms map to the site language
// string so the site's own default language stays public:
//
//   - a bare language tag ("en") whose language is the site language's
//     primary subtag (an "en-AU" site);
//   - a script-only tag ("zh-Hans", WPML's default code for Simplified
//     Chinese) when the site shares the primary subtag and the site's own
//     script (its likely script, so "zh-CN" is Hans) is that script. A
//     different script ("zh-Hant" on a "zh-CN" site) stays another language,
//     and a tag with a region is never mapped: "zh-SG" and "zh-CN" are
//     different locales.
//
// Everything else keeps its normalized tag and is stored but not public.
func localeFor(raw, site string) (loc, warn string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return site, ""
	}
	n, err := entity.NormalizeLocale(raw)
	if err != nil {
		return site, fmt.Sprintf("language %q is not a valid language tag: imported in the site language", raw)
	}
	if sameSiteLanguage(n, site) {
		return site, ""
	}
	return n, ""
}

// sameSiteLanguage reports whether the normalized tag n has no region and is
// the site language by the rules in localeFor.
func sameSiteLanguage(n, site string) bool {
	nt, err := language.Parse(n)
	if err != nil {
		return false
	}
	st, err := language.Parse(site)
	if err != nil {
		return false
	}
	nb, _ := nt.Base()
	sb, _ := st.Base()
	if nb != sb {
		return false
	}
	if _, conf := nt.Region(); conf == language.Exact {
		return false
	}
	ns, nconf := nt.Script()
	if nconf != language.Exact {
		return true // bare language
	}
	ss, _ := st.Script()
	return ns == ss
}

// languageCounts tallies the items per source language for the scan report.
// It returns nil when no item carries a language.
func languageCounts(items []Item, site string) map[string]int {
	out := map[string]int{}
	tagged := false
	for _, it := range items {
		if it.Status == wpTrash {
			continue
		}
		lang := strings.TrimSpace(it.Lang)
		if lang != "" {
			tagged = true
			if n, err := entity.NormalizeLocale(lang); err == nil {
				lang = n
			}
		} else {
			lang = firstNonEmpty(site, "default")
		}
		out[lang]++
	}
	if !tagged {
		return nil
	}
	return out
}
