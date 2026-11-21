package staticexport

import (
	"bytes"
	"mime"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"

	"golang.org/x/net/html"
)

// The patterns are compiled on first use, so an idle server pays nothing.
var (
	cssURLRe    = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`url\(\s*(?:"([^"]*)"|'([^']*)'|([^)\s"']*))\s*\)`) })
	cssImportRe = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`@import\s+["']([^"']+)["']`) })
	absURLRe    = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`https?://[^\s"'<>)]+`) })
)

// Links returns the same-origin URL paths (with raw query, no fragment)
// referenced by body, sorted and without duplicates. base is the absolute URL
// of the document, used for relative references; the origin is its scheme
// and host.
func Links(base *url.URL, contentType string, body []byte) []string {
	mt, _, _ := mime.ParseMediaType(contentType)
	set := map[string]struct{}{}
	add := func(ref string) {
		if p, ok := sameOriginPath(base, ref); ok {
			set[p] = struct{}{}
		}
	}
	switch {
	case mt == "text/html" || mt == "application/xhtml+xml":
		htmlLinks(body, add)
	case mt == "text/css":
		cssLinks(string(body), add)
	case strings.HasPrefix(mt, "text/") || strings.Contains(mt, "xml") || strings.Contains(mt, "json"):
		for _, m := range absURLRe().FindAll(body, -1) {
			add(strings.TrimRight(html.UnescapeString(string(m)), ".,;:!"))
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// sameOriginPath resolves ref against base and returns path?query when the
// result is http(s) on the same scheme and host.
func sameOriginPath(base *url.URL, ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	u, err := base.Parse(ref)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	if u.Scheme != base.Scheme || !strings.EqualFold(u.Host, base.Host) {
		return "", false
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return p, true
}

func cssLinks(css string, add func(string)) {
	for _, m := range cssURLRe().FindAllStringSubmatch(css, -1) {
		for _, g := range m[1:] {
			if g != "" && !strings.HasPrefix(g, "data:") {
				add(g)
			}
		}
	}
	for _, m := range cssImportRe().FindAllStringSubmatch(css, -1) {
		add(m[1])
	}
}

func htmlLinks(body []byte, add func(string)) {
	z := html.NewTokenizer(bytes.NewReader(body))
	inStyle := false
	for {
		switch z.Next() {
		case html.ErrorToken:
			return
		case html.TextToken:
			if inStyle {
				cssLinks(string(z.Text()), add)
			}
		case html.EndTagToken:
			if name, _ := z.TagName(); string(name) == "style" {
				inStyle = false
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			tag := string(name)
			if tag == "style" {
				inStyle = true
			}
			if !hasAttr {
				continue
			}
			attrs := map[string]string{}
			for {
				k, v, more := z.TagAttr()
				attrs[string(k)] = string(v)
				if !more {
					break
				}
			}
			tagLinks(tag, attrs, add)
		}
	}
}

func tagLinks(tag string, attrs map[string]string, add func(string)) {
	if v, ok := attrs["style"]; ok {
		cssLinks(v, add)
	}
	switch tag {
	case "a", "link":
		add(attrs["href"])
	case "img", "source":
		add(attrs["src"])
		for part := range strings.SplitSeq(attrs["srcset"], ",") {
			if f := strings.Fields(part); len(f) > 0 {
				add(f[0])
			}
		}
	case "script", "iframe", "audio":
		add(attrs["src"])
	case "video":
		add(attrs["src"])
		add(attrs["poster"])
	case "meta":
		key := attrs["property"]
		if key == "" {
			key = attrs["name"]
		}
		if key == "og:image" || key == "twitter:image" {
			add(attrs["content"])
		}
	}
}
