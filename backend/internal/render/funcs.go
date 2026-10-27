package render

import (
	"fmt"
	"html"
	"html/template"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // zone database for formatDate's tz argument on minimal images

	"hatamoto.dev/core/internal/entity"
)

// nowFunc is the clock behind the "year" template func (swapped in tests).
var nowFunc = time.Now

// baseFuncs returns the template funcs available to every theme template.
// None of them returns template.HTML/JS/URL from untrusted input; marker
// helpers return template.HTMLAttr built from escaped values only.
func baseFuncs() template.FuncMap {
	fm := template.FuncMap{
		"hmsec":                sectionMarker,
		"hmf":                  fieldMarker,
		"hment":                entityMarker,
		"cta":                  safeHref,
		"safeurl":              safeHref,
		"safeHref":             safeHref,
		"telHref":              telHref,
		"embedAllowed":         embedAllowed,
		"imgsrc":               imgSrc,
		"dict":                 dict,
		"list":                 func(v ...any) []any { return v },
		"seq":                  seq,
		"join":                 join,
		"default":              defaultVal,
		"hasPrefix":            strings.HasPrefix,
		"hl":                   highlight,
		"year":                 func() int { return nowFunc().Year() },
		"sanitisedHTML":        sanitisedHTML,
		"sanitisedSectionHTML": sanitisedSectionHTML,
		"formatDate":           formatDate,
		"readingTime":          readingTime,
		"navctx":               navCtx,
		"navlink":              navLink,
	}
	for k, f := range extraFuncs() {
		fm[k] = f
	}
	return fm
}

var hlTokenRE = regexp.MustCompile("(?s)(<!--.*?-->|//[^\\n]*|#[^\\n]*|\"(?:\\\\.|[^\"\\\\\\n])*\"|'(?:\\\\.|[^'\\\\\\n])*'|`[^`]*`)|\\b(func|return|if|else|for|range|var|const|type|struct|interface|package|import|go|defer|select|switch|case|default|nil|true|false|let|function|new|async|await|class|export|from)\\b")

// highlight wraps strings, comments and a small keyword set of src in
// hm-tok spans. It tokenises the raw source and escapes every segment, so
// the result contains only escaped text plus the fixed span markup. Hash
// comments are recognised for languages other than go/js/html.
func highlight(langV, srcV any) template.HTML {
	lang, src := strVal(langV), strVal(srcV)
	hash := lang != "go" && lang != "js" && lang != "javascript" && lang != "html"
	var b strings.Builder
	last := 0
	for _, m := range hlTokenRE.FindAllStringSubmatchIndex(src, -1) {
		cls := "k"
		if m[2] >= 0 {
			cls = "s"
			if tok := src[m[0]:m[1]]; strings.HasPrefix(tok, "//") || strings.HasPrefix(tok, "<!--") || strings.HasPrefix(tok, "#") {
				cls = "c"
				if tok[0] == '#' && !hash {
					continue
				}
			}
		}
		b.WriteString(html.EscapeString(src[last:m[0]]))
		b.WriteString(`<span class="hm-tok hm-tok--` + cls + `">` + html.EscapeString(src[m[0]:m[1]]) + `</span>`)
		last = m[1]
	}
	b.WriteString(html.EscapeString(src[last:]))
	return template.HTML(b.String()) // #nosec G203 -- every segment of src is escaped
}

// strVal is v as a string; anything that is not a string (missing key, number,
// object: stored content is not schema-validated) is "".
func strVal(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case template.HTML:
		return string(t)
	case template.CSS:
		return string(t)
	}
	return ""
}

// safeHref is safeURL as a template.URL. html/template would otherwise
// rewrite tel:/sms: hrefs (not in its scheme allowlist) to #ZgotmplZ; the
// value is returned as template.URL only after safeURL validated it.
func safeHref(raw any) template.URL {
	return template.URL(safeURL(strVal(raw))) // #nosec G203 -- scheme allowlist enforced by safeURL
}

// telHref builds a tel: href from a phone number, keeping digits and a
// leading "+" only.
func telHref(phone any) template.URL {
	var b strings.Builder
	for i, r := range strings.TrimSpace(strVal(phone)) {
		if r >= '0' && r <= '9' || (r == '+' && i == 0) {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "#"
	}
	return safeHref("tel:" + b.String())
}

// mapEmbedPrefixes are the map embeds a theme may add to the video hosts of
// the sanitiser's iframe allowlist (https only).
var mapEmbedPrefixes = []string{
	"https://www.google.com/maps/embed",
	"https://www.openstreetmap.org/export/embed.html",
}

// embedAllowed reports whether raw is an iframe source a theme may embed: the
// sanitiser's host allowlist (HATAMOTO_EMBED_HOSTS; default YouTube and Vimeo)
// or a map embed.
func embedAllowed(raw any) bool {
	s := strings.TrimSpace(strVal(raw))
	if entity.IframeSrcAllowed(s) {
		return true
	}
	for _, p := range mapEmbedPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// safeURL allows http, https, mailto, tel, sms and relative references (path,
// ?query, #fragment); anything else becomes "#".
func safeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "#"
	}
	if strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, `/\`) {
		return "#"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "#"
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto", "tel", "sms":
		return raw
	case "":
		if u.Host != "" {
			return "#"
		}
		return raw
	}
	return "#"
}

var allowedDataImage = []string{"data:image/png;", "data:image/jpeg;", "data:image/gif;", "data:image/webp;"}

// imgSrc allows http(s), relative and raster data: image URLs; else "".
func imgSrc(v any) string {
	raw := strings.TrimSpace(strVal(v))
	lower := strings.ToLower(raw)
	for _, p := range allowedDataImage {
		if strings.HasPrefix(lower, p) {
			return raw
		}
	}
	if strings.HasPrefix(lower, "data:") {
		return ""
	}
	if safeURL(raw) == "#" && raw != "#" {
		return ""
	}
	return raw
}

func dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, fmt.Errorf("dict: odd number of arguments")
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %d is not a string", i/2)
		}
		m[k] = kv[i+1]
	}
	return m, nil
}

const maxSeq = 1000

// seq returns 0..n-1 (n capped at maxSeq).
func seq(n int) []int {
	if n < 0 {
		n = 0
	}
	if n > maxSeq {
		n = maxSeq
	}
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// join concatenates the elements of any slice with sep; nil -> "".
func join(elems any, sep string) string {
	if elems == nil {
		return ""
	}
	rv := reflect.ValueOf(elems)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return fmt.Sprint(elems)
	}
	parts := make([]string, rv.Len())
	for i := range parts {
		parts[i] = fmt.Sprint(rv.Index(i).Interface())
	}
	return strings.Join(parts, sep)
}

// defaultVal returns v unless it is empty, then def (usage: default "x" .V).
func defaultVal(def, v any) any {
	if isEmptyValue(v) {
		return def
	}
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Bool && !rv.Bool() {
		return def
	}
	return v
}

// sanitisedHTML runs rich text (a TipTap body attribute) through the same
// sanitiser as stored rich-text section fields. Non-string input gives "".
func sanitisedHTML(v any) template.HTML {
	return template.HTML(entity.SanitizeRichText(strVal(v))) // #nosec G203 -- sanitised above
}

// sanitisedSectionHTML runs an HTML fragment (a plugin func's output, say)
// through the policy html sections are saved with, and returns it as trusted
// markup. Unlike sanitisedHTML it keeps class, id, role, style, aria-* and
// data-* attributes and the section element set (details, button, inline svg);
// it drops script, event handlers, forms, javascript: URLs, unsafe styles,
// data-hm-* marker attributes and iframes off the embed allowlist.
func sanitisedSectionHTML(v any) template.HTML {
	return template.HTML(entity.SanitizeHTMLSectionBody(strVal(v))) // #nosec G203 -- sanitised above
}

// formatDate formats t (time.Time, *time.Time or an RFC3339 / YYYY-MM-DD
// string) with a Go time layout. Zero or unparseable values give "". Month and
// weekday names are English (Go has no localised names).
// By default the value's own zone is used (UTC for stored timestamps). An
// optional IANA zone name ("Australia/Sydney") converts first, so a late-UTC
// publish prints as the local calendar day; an unknown zone is ignored.
func formatDate(t any, layout string, tz ...string) string {
	var tm time.Time
	switch v := t.(type) {
	case time.Time:
		tm = v
	case *time.Time:
		if v != nil {
			tm = *v
		}
	case string:
		for _, l := range []string{time.RFC3339, "2006-01-02"} {
			if p, err := time.Parse(l, v); err == nil {
				tm = p
				break
			}
		}
	}
	if tm.IsZero() {
		return ""
	}
	if len(tz) > 0 && tz[0] != "" {
		if loc, err := time.LoadLocation(tz[0]); err == nil {
			tm = tm.In(loc)
		}
	}
	return tm.Format(layout)
}

// wordsPerMinute is the reading speed behind readingTime.
const wordsPerMinute = 200

// readingTime returns the whole minutes needed to read the text of an HTML
// string: at least 1 for any text, 0 for none.
func readingTime(v any) int { return ReadingTime(strVal(v)) }

// WordCount is the number of words in the text of an HTML string.
func WordCount(markup string) int {
	return len(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(markup, " "))))
}

// ReadingTime is the whole minutes needed to read the text of an HTML string:
// at least 1 for any text, 0 for none.
func ReadingTime(markup string) int {
	return ReadingMinutes(WordCount(markup))
}

// ReadingMinutes is the reading time of a text of that many words.
func ReadingMinutes(words int) int {
	if words == 0 {
		return 0
	}
	return (words + wordsPerMinute - 1) / wordsPerMinute
}

// navMenu is the normalised argument of the "nav" and "nav-items" partials.
type navMenu struct {
	Items []NavItem
	// Path is the current page path; it marks the matching link aria-current.
	Path string
}

// navEntry is the normalised argument of the "nav-link" partial.
type navEntry struct {
	Item NavItem
	Path string
}

// navCtx accepts both shapes the nav partials have been called with: a bare
// []NavItem (the original contract) or dict "Items" ... "Path" ....
func navCtx(v any) navMenu {
	switch x := v.(type) {
	case navMenu:
		return x
	case []NavItem:
		return navMenu{Items: x}
	case map[string]any:
		items, _ := x["Items"].([]NavItem)
		return navMenu{Items: items, Path: strVal(x["Path"])}
	}
	return navMenu{}
}

// navLink accepts a NavItem (the original contract) or dict "Item" ... "Path" ....
func navLink(v any) navEntry {
	switch x := v.(type) {
	case NavItem:
		return navEntry{Item: x}
	case *NavItem:
		if x != nil {
			return navEntry{Item: *x}
		}
	case map[string]any:
		switch it := x["Item"].(type) {
		case NavItem:
			return navEntry{Item: it, Path: strVal(x["Path"])}
		case *NavItem:
			if it != nil {
				return navEntry{Item: *it, Path: strVal(x["Path"])}
			}
		}
	}
	return navEntry{}
}
