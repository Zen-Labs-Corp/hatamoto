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
	maxVCDepth = 64   // nested containers
	maxVCTags  = 5000 // vc_* tags per body

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

var vcTagRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`\[(/?)(vc_[a-zA-Z0-9_-]{1,40})((?:\s[^\[\]]*)?)\]`)
})

// vcContainers only lay content out: their tags are removed and the content kept.
var vcContainers = map[string]bool{
	"vc_row": true, "vc_row_inner": true, "vc_column": true, "vc_column_inner": true,
	"vc_section": true, "vc_column_text": true,
}

var vcAttrRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`([a-zA-Z0-9_-]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|(\S+))`)
})

// vcQuotes maps the quote forms a WordPress body can carry (texturized
// entities from rendered REST content, curly characters, plain entities) to a
// plain double or single quote.
var vcQuotes = strings.NewReplacer(
	"&#8220;", `"`, "&#8221;", `"`, "&#8243;", `"`, "&#8242;", `"`, "&quot;", `"`, "&#34;", `"`,
	"\u201c", `"`, "\u201d", `"`, "\u2033", `"`, "\u2032", `"`,
	"&#039;", "'", "&#39;", "'", "&#8217;", "'", "&#8216;", "'", "\u2019", "'", "\u2018", "'",
)

// convertOpts carries the optional inputs of Convert.
type convertOpts struct {
	media map[string]Media
	raw   bool // the body is raw (paragraphs not wrapped yet)
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
	span = vcQuotes.Replace(span)
	out := map[string]string{}
	for _, m := range vcAttrRe().FindAllStringSubmatch(span, -1) {
		v := m[2]
		if v == "" {
			v = m[3]
		}
		if v == "" {
			v = m[4]
		}
		out[strings.ToLower(m[1])] = v
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
	tooDeep := false
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
			end := "[/" + name + "]"
			if i := strings.Index(s[m[1]:], end); i >= 0 {
				payload, hasPayload = s[m[1]:m[1]+i], true
				last = m[1] + i + len(end)
			}
		}
		if tooDeep {
			continue
		}
		switch {
		case name == "vc_raw_html" && !closing:
			if !hasPayload {
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
			if n := len(stack); n > 0 && stack[n-1] == name {
				stack = stack[:n-1]
			}
		case container:
			if len(stack) >= maxVCDepth {
				tooDeep = true
				res.Dropped = append(res.Dropped, "WPBakery (nesting too deep)")
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
		m, ok := o.media[id]
		if !ok || m.URL == "" {
			if id == "" {
				id = "unknown"
			}
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
