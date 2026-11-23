package wpimport

import (
	"regexp"
	"strings"
	"sync"
)

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
