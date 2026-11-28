package wpimport

import (
	"encoding/base64"
	"errors"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Bounds on what a body can make the WPBakery pass do.
const (
	maxVCDepth = 64       // nested containers
	maxVCTags  = 5000     // vc_* tags per body
	maxVCSpan  = 16 << 10 // bytes of one tag's attributes

	maxRawHTMLDecoded = 256 << 10 // decoded bytes of one vc_raw_html
	maxRawHTMLBody    = 1 << 20   // decoded bytes of all vc_raw_html in one body
)

var (
	errRawTooLarge = errors.New("raw HTML too large")
	errRawBad      = errors.New("raw HTML unreadable")
)

var vcIframeSrcRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`(?i)src\s*=\s*["']([^"']+)["']`)
})

// vcMapHosts are the hosts a vc_gmaps link may point to.
var vcMapHosts = map[string]bool{"www.google.com": true, "google.com": true, "maps.google.com": true}

var vcRawPairRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`\[vc_raw_(?:html|js)(?:\s[^\[\]]*)?\][\s\S]*?\[/vc_raw_(?:html|js)\]`)
})

var vcTagRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`\[(/?)(vc_[a-zA-Z0-9_-]{1,40})((?:\s[^\[\]]*)?)\]`)
})

// vcContainers only lay content out: their tags are removed and the content kept.
var vcContainers = map[string]bool{
	"vc_row": true, "vc_row_inner": true, "vc_column": true, "vc_column_inner": true,
	"vc_section": true, "vc_column_text": true,
}

var vcKeyRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`([a-zA-Z0-9_-]+)\s*=\s*`)
})

// vcQuote is one way a WordPress body can write a quote: plain, as an entity
// (texturized rendered content) or as a curly character. dbl tells a double
// quote from a single one, form the writing, so a curly quote inside a value
// is not mistaken for the delimiter that is written another way.
type vcQuote struct {
	tok  string
	dbl  bool
	form int
}

var vcQuotes = []vcQuote{
	{"&#8220;", true, 1}, {"&#8221;", true, 1}, {"&#8243;", true, 1}, {"&#8242;", true, 1}, {"&quot;", true, 1}, {"&#34;", true, 1},
	{"&#039;", false, 1}, {"&#39;", false, 1}, {"&#8217;", false, 1}, {"&#8216;", false, 1},
	{"\u201c", true, 2}, {"\u201d", true, 2}, {"\u2033", true, 2}, {"\u2032", true, 2},
	{"\u2019", false, 2}, {"\u2018", false, 2},
	{`"`, true, 0}, {"'", false, 0},
}

func quoteAt(s string, i int) (vcQuote, bool) {
	for _, q := range vcQuotes {
		if strings.HasPrefix(s[i:], q.tok) {
			return q, true
		}
	}
	return vcQuote{}, false
}

// vcSafe undoes WPBakery's escaping of a value: a double quote is stored as
// two backticks, which a texturized body shows as &#8220;; [ and ] are stored
// as `{` and `}`.
var vcSafe = strings.NewReplacer("``", `"`, "&#8220;", `"`, "`{`", "[", "`}`", "]")

// vcKind is the quote family a value was opened with.
type vcKind struct {
	dbl  bool
	form int
}

var vcNextKeyRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`^[a-zA-Z0-9_-]+\s*=`)
})

// vcClosesAt reports whether a quote ending at end closes a value: it is
// followed by the end of the span, or by whitespace and then the next key (or
// only more whitespace). A quote inside the text is followed by a word.
func vcClosesAt(span string, end int) bool {
	if end == len(span) {
		return true
	}
	j := end
	for j < len(span) && strings.IndexByte(" \t\r\n", span[j]) >= 0 {
		j++
	}
	if j == end {
		return false
	}
	return j == len(span) || vcNextKeyRe().MatchString(span[j:])
}

// vcValue reads the value starting at span[i:]. A quote only opens a value
// here, and only closes it where vcClosesAt says so, so quotes inside the
// value stay. noCloser remembers the quote kinds with no closer in the rest of
// the span (a closer is valid wherever it sits, so none exists after a failed
// search either), which keeps the parse linear. It returns the value and where
// reading goes on.
func vcValue(span string, i int, noCloser map[vcKind]bool) (string, int) {
	if q, ok := quoteAt(span, i); ok && !noCloser[vcKind{q.dbl, q.form}] {
		for k := i + len(q.tok); k < len(span); k++ {
			c, ok := quoteAt(span, k)
			if !ok || c.dbl != q.dbl || c.form != q.form {
				continue
			}
			if end := k + len(c.tok); vcClosesAt(span, end) {
				return vcSafe.Replace(span[i+len(q.tok) : k]), end
			}
		}
		noCloser[vcKind{q.dbl, q.form}] = true
	}
	end := i
	for end < len(span) && !strings.ContainsRune(" \t\r\n", rune(span[end])) {
		end++
	}
	return vcSafe.Replace(span[i:end]), end
}

// convertOpts carries the optional inputs of Convert.
type convertOpts struct {
	media map[string]Media
	raw   bool // the body is raw (paragraphs not wrapped yet)
	// forms maps a Contact Form 7 id or hash to the slug of the imported form.
	forms map[string]string
}

// vcBreak is the block boundary written where a container tag was removed.
// WPBakery stores tags back to back, so without it adjacent columns would run
// into one paragraph. A raw body is split by autop on blank lines; a rendered
// one is already inside a <p> that has to be closed and reopened.
func (o *convertOpts) vcBreak() string {
	if o.raw {
		return "\n\n"
	}
	return "</p><p>"
}

// ConvertOption configures Convert.
type ConvertOption func(*convertOpts)

// WithMedia lets Convert resolve WPBakery image ids to source URLs.
func WithMedia(byID map[string]Media) ConvertOption {
	return func(o *convertOpts) { o.media = byID }
}

// WithForms lets Convert replace [contact-form-7] shortcodes with the form
// shortcode of the imported form. byIDOrHash maps a CF7 post id or 7-character
// hash to the form slug. Without it the shortcode is removed.
func WithForms(byIDOrHash map[string]string) ConvertOption {
	return func(o *convertOpts) { o.forms = byIDOrHash }
}

// MediaByID indexes the dump's media by WordPress attachment id. Build it once
// per run.
func MediaByID(ms []Media) map[string]Media {
	out := make(map[string]Media, len(ms))
	for _, m := range ms {
		out[m.ID] = m
	}
	return out
}

// vcAttrs parses the attribute span of a shortcode from a raw or a texturized
// (REST rendered) body. Keys are lower-cased; values are not unescaped, the
// handlers do that. A repeated key keeps its last value.
func vcAttrs(span string) map[string]string {
	span = tagRe().ReplaceAllString(span, " ") // wpautop leaves <br /> inside a span
	out := map[string]string{}
	if len(span) > maxVCSpan {
		return out
	}
	noCloser := map[vcKind]bool{}
	for pos := 0; pos < len(span); {
		m := vcKeyRe().FindStringSubmatchIndex(span[pos:])
		if m == nil {
			break
		}
		key := strings.ToLower(span[pos+m[2] : pos+m[3]])
		var v string
		v, pos = vcValue(span, pos+m[1], noCloser)
		out[key] = v
	}
	return out
}

// wpbakery rewrites the vc_* shortcodes of s into plain HTML in reading order.
// Layout (rows, columns, spacing, icons) is dropped, content (headings, images,
// buttons, separators) is converted. Tags it cannot convert are removed and
// their names added to removed, so the scan can tell them from converted ones.
func wpbakery(s string, o *convertOpts, res *Converted, removed map[string]bool) string {
	locs := vcTagRe().FindAllStringSubmatchIndex(s, maxVCTags+1)
	tooMany := len(locs) > maxVCTags
	if tooMany {
		locs = locs[:maxVCTags]
		res.Dropped = append(res.Dropped, "WPBakery (too many shortcodes)")
	}
	var b strings.Builder
	var stack []string
	var missing []string
	deepReported := false
	spanReported := false
	noCloser := map[string]bool{} // raw tags with no closing tag in the rest of the body
	unclosedReported := false
	rawTotal := 0
	last := 0
	for _, m := range locs {
		if m[0] < last {
			continue // inside a raw payload already consumed
		}
		b.WriteString(s[last:m[0]])
		last = m[1]
		closing := s[m[2]:m[3]] == "/"
		name := s[m[4]:m[5]]
		span := s[m[6]:m[7]]
		container := vcContainers[name] || strings.HasPrefix(name, "vc_tta_")
		var payload string
		hasPayload := false
		if !closing && (name == "vc_raw_html" || name == "vc_raw_js") {
			// The payload is everything up to the closing tag; none of it is
			// tokenised. Without a closing tag only the tag itself is removed.
			// A closer missing once is missing for every later opener too.
			end := "[/" + name + "]"
			if noCloser[name] {
				// nothing to find
			} else if i := strings.Index(s[m[1]:], end); i >= 0 {
				payload, hasPayload = s[m[1]:m[1]+i], true
				last = m[1] + i + len(end)
			} else {
				noCloser[name] = true
			}
		}
		if len(span) > maxVCSpan {
			if !spanReported {
				spanReported = true
				res.Dropped = append(res.Dropped, "WPBakery (shortcode too large)")
			}
			continue
		}
		switch {
		case name == "vc_raw_html" && !closing:
			if !hasPayload {
				if !unclosedReported {
					unclosedReported = true
					res.Dropped = append(res.Dropped, "WPBakery raw HTML (unclosed)")
				}
				break
			}
			dec, err := vcRawPayload(payload, maxRawHTMLBody-rawTotal)
			switch {
			case errors.Is(err, errRawTooLarge):
				res.Dropped = append(res.Dropped, "WPBakery raw HTML (too large)")
			case err != nil:
				res.Dropped = append(res.Dropped, "WPBakery raw HTML (unreadable)")
			default:
				rawTotal += len(dec)
				b.WriteString("\n\n" + dec + "\n\n")
				res.WPBakery++
			}
		case name == "vc_raw_js" && !closing:
			removed[name] = true
		case name == "vc_posts_slider" && !closing:
			removed[name] = true
		case name == "vc_gmaps" && !closing:
			if link, ok := vcMapsLink(vcAttrs(span)["link"]); ok {
				b.WriteString("\n\n<p><a href=\"" + html.EscapeString(link) + "\">View on Google Maps</a></p>\n\n")
				res.WPBakery++
			} else {
				res.Dropped = append(res.Dropped, "Google Maps (unrecognised link)")
			}
		case container && closing:
			b.WriteString(o.vcBreak())
			// Like HTML: pop up to the matching open, ignore a close with none.
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i] == name {
					stack = stack[:i]
					break
				}
			}
		case container:
			if len(stack) >= maxVCDepth {
				if !deepReported {
					deepReported = true
					res.Dropped = append(res.Dropped, "WPBakery (nesting too deep)")
				}
				continue
			}
			b.WriteString(o.vcBreak())
			if name == "vc_tta_section" || name == "vc_tta_tab" || name == "vc_tta_panel" {
				if title := vcText(vcAttrs(span), "title"); title != "" {
					b.WriteString("\n\n<h3>" + html.EscapeString(title) + "</h3>\n\n")
				}
			}
			stack = append(stack, name)
			res.WPBakery++
		case closing:
			removed[name] = true
		case name == "vc_empty_space" || name == "vc_icon":
			res.WPBakery++
		case name == "vc_separator" || (name == "vc_text_separator" && vcText(vcAttrs(span), "title") == ""):
			b.WriteString("\n\n<hr>\n\n")
			res.WPBakery++
		case name == "vc_text_separator":
			b.WriteString("\n\n<h3>" + html.EscapeString(vcText(vcAttrs(span), "title")) + "</h3>\n\n")
			res.WPBakery++
		case name == "vc_custom_heading":
			b.WriteString(vcHeading(vcAttrs(span)))
			res.WPBakery++
		case name == "vc_btn":
			b.WriteString(vcButton(vcAttrs(span)))
			res.WPBakery++
		case name == "vc_single_image":
			img, miss := vcImage(vcAttrs(span), o)
			b.WriteString(img)
			if miss != "" {
				missing = append(missing, miss)
			}
			res.WPBakery++
		default:
			removed[name] = true
		}
	}
	rest := s[last:]
	if tooMany { // the tail was never tokenised: strip its tags, keep the text
		rest = vcRawPairRe().ReplaceAllString(rest, "") // payloads are base64, not text
		rest = vcTagRe().ReplaceAllString(rest, "")
	}
	b.WriteString(rest)
	sort.Strings(missing)
	for i, id := range missing {
		if i == 0 || id != missing[i-1] {
			res.MissingImages = append(res.MissingImages, id)
		}
	}
	return b.String()
}

// vcText returns an attribute as plain text (entities decoded).
func vcText(attrs map[string]string, key string) string {
	return strings.TrimSpace(html.UnescapeString(attrs[key]))
}

// vcLink parses WPBakery's link value, "url:<encoded>|title:<encoded>|target:..",
// or a bare URL.
func vcLink(v string) (href, title string) {
	v = strings.TrimSpace(html.UnescapeString(v))
	if v == "" {
		return "", ""
	}
	if !strings.Contains(v, "url:") && !strings.Contains(v, "title:") {
		return v, ""
	}
	for _, part := range strings.Split(v, "|") {
		k, val, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		if dec, err := url.PathUnescape(val); err == nil {
			val = dec
		}
		switch strings.TrimSpace(k) {
		case "url":
			href = strings.TrimSpace(val)
		case "title":
			title = strings.TrimSpace(val)
		}
	}
	return href, title
}

func vcHeading(attrs map[string]string) string {
	text := vcText(attrs, "text")
	if text == "" {
		return ""
	}
	tag, align := "h2", ""
	for _, part := range strings.Split(html.UnescapeString(attrs["font_container"]), "|") {
		k, v, _ := strings.Cut(part, ":")
		v, _ = url.PathUnescape(strings.TrimSpace(v))
		switch strings.TrimSpace(k) {
		case "tag":
			switch v {
			case "h1", "h2", "h3", "h4", "h5", "h6":
				tag = v
			case "p", "div":
				tag = "p"
			}
		case "text_align":
			if v == "left" || v == "center" || v == "right" {
				align = v
			}
		}
	}
	inner := html.EscapeString(text)
	if href, _ := vcLink(attrs["link"]); href != "" {
		inner = `<a href="` + html.EscapeString(href) + `">` + inner + `</a>`
	}
	cls := ""
	if align != "" {
		cls = ` class="has-text-align-` + align + `"`
	}
	return "\n\n<" + tag + cls + ">" + inner + "</" + tag + ">\n\n"
}

func vcButton(attrs map[string]string) string {
	href, ltitle := vcLink(attrs["link"])
	title := vcText(attrs, "title")
	if title == "" {
		title = ltitle
	}
	if title == "" {
		title = href
	}
	if title == "" {
		return ""
	}
	if href == "" {
		return "\n\n<p>" + html.EscapeString(title) + "</p>\n\n"
	}
	return "\n\n<p><a href=\"" + html.EscapeString(href) + "\">" + html.EscapeString(title) + "</a></p>\n\n"
}

// vcImageID reports whether v can be an attachment id: digits only, at most 20.
func vcImageID(v string) bool {
	if len(v) > 20 {
		return false
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// vcImage returns the markup for a vc_single_image and, when the image cannot
// be resolved, what to report as missing (an id, "featured" or "external").
func vcImage(attrs map[string]string, o *convertOpts) (string, string) {
	var src, alt string
	switch source := attrs["source"]; source {
	case "external_link":
		src = vcText(attrs, "custom_src")
		if src == "" {
			return "", "external"
		}
	case "featured_image":
		return "", "featured"
	default:
		id := strings.TrimSpace(attrs["image"])
		if id == "" {
			return "", "unknown"
		}
		if !vcImageID(id) {
			return "", "invalid" // never echo free text into a report
		}
		m, ok := o.media[id]
		if !ok || m.URL == "" {
			return "", id
		}
		src, alt = m.URL, m.Alt
	}
	img := `<img src="` + html.EscapeString(src) + `" alt="` + html.EscapeString(alt) + `"`
	switch attrs["alignment"] {
	case "center", "right", "left":
		img += ` class="align` + attrs["alignment"] + `"`
	}
	img += ">"
	if attrs["onclick"] == "" || attrs["onclick"] == "custom_link" {
		if href, _ := vcLink(attrs["link"]); href != "" {
			img = `<a href="` + html.EscapeString(href) + `">` + img + `</a>`
		}
	}
	return "\n\n<p>" + img + "</p>\n\n", ""
}

// vcRawPayload decodes the body of a vc_raw_html: base64 of URL-encoded HTML,
// possibly texturized or wrapped in tags by the editor. room is what is left of
// the per-body budget; sizes are checked before decoding.
func vcRawPayload(p string, room int) (string, error) {
	p = tagRe().ReplaceAllString(p, "")
	p = strings.NewReplacer("&#215;", "x", "\u00d7", "x").Replace(p)
	p = strings.Join(strings.Fields(p), "")
	limit := min(maxRawHTMLDecoded, room)
	if base64.StdEncoding.DecodedLen(len(p)) > limit+3 {
		return "", errRawTooLarge
	}
	raw, err := base64.StdEncoding.DecodeString(p)
	if err != nil {
		if raw, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(p, "=")); err != nil {
			return "", errRawBad
		}
	}
	dec, err := url.QueryUnescape(string(raw))
	if err != nil {
		if dec, err = url.PathUnescape(string(raw)); err != nil {
			return "", errRawBad
		}
	}
	if len(dec) > limit {
		return "", errRawTooLarge
	}
	return dec, nil
}

// vcMapsLink extracts the map URL from a vc_gmaps link attribute ("#E-8_" and
// base64 of the URL-encoded embed code). Only https Google Maps URLs pass.
func vcMapsLink(link string) (string, bool) {
	link = strings.TrimPrefix(strings.TrimSpace(html.UnescapeString(link)), "#E-8_")
	dec, err := vcRawPayload(link, maxRawHTMLDecoded)
	if err != nil {
		return "", false
	}
	target := strings.TrimSpace(dec)
	if strings.Contains(strings.ToLower(target), "<iframe") {
		m := vcIframeSrcRe().FindStringSubmatch(target)
		if m == nil {
			return "", false
		}
		target = html.UnescapeString(m[1])
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || !vcMapHosts[u.Host] || !strings.HasPrefix(u.Path, "/maps") {
		return "", false
	}
	return u.String(), true
}
