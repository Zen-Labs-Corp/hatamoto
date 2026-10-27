package entity

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

// EmbedHostsEnv names the environment variable that lists the hosts an iframe
// may embed (comma-separated host names, no scheme or port).
const EmbedHostsEnv = "HATAMOTO_EMBED_HOSTS"

// defaultEmbedPaths are the embed hosts of a stock install, each with the one
// path prefix its player lives under: YouTube (also the no-cookie domain) and
// Vimeo. A host from HATAMOTO_EMBED_HOSTS that is not here may be embedded at
// any path.
var defaultEmbedPaths = map[string]string{
	"www.youtube.com":          "/embed/",
	"youtube.com":              "/embed/",
	"www.youtube-nocookie.com": "/embed/",
	"youtube-nocookie.com":     "/embed/",
	"player.vimeo.com":         "/video/",
}

// embedHostRE is a plausible DNS name with at least one dot. Anything else
// (a scheme, a port, a path, a wildcard, userinfo) is not a host.
var embedHostRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// newIframeSrcRE builds the iframe src allowlist from the value of
// HATAMOTO_EMBED_HOSTS. With no usable host in it the defaults apply, so a
// typo never opens the policy. The result matches only https URLs whose host
// is listed exactly (the host must be followed by "/", so userinfo, a port or
// a look-alike suffix never matches) and, for the stock hosts, under their
// player path. Names are sorted so the pattern is stable.
func newIframeSrcRE(env string) *regexp.Regexp {
	paths := map[string]string{}
	for h := range strings.SplitSeq(env, ",") {
		h = strings.ToLower(strings.TrimSpace(h))
		switch {
		case h == "":
		case !embedHostRE.MatchString(h):
			slog.Warn("ignoring invalid host in "+EmbedHostsEnv, "host", h)
		default:
			paths[h] = defaultEmbedPaths[h]
			if paths[h] == "" {
				paths[h] = "/"
			}
		}
	}
	if len(paths) == 0 {
		paths = defaultEmbedPaths
	}
	hosts := make([]string, 0, len(paths))
	for h := range paths {
		hosts = append(hosts, h)
	}
	slices.Sort(hosts)
	alts := make([]string, len(hosts))
	for i, h := range hosts {
		alts[i] = regexp.QuoteMeta(h + paths[h])
	}
	return regexp.MustCompile(`^https://(` + strings.Join(alts, "|") + `)`)
}

// iframeSrcAllowlist is the allowlist both policies use, built once from the
// environment: iframes may only embed the configured hosts (default YouTube
// and Vimeo). Everything else is stripped.
var iframeSrcAllowlist = sync.OnceValue(func() *regexp.Regexp {
	return newIframeSrcRE(os.Getenv(EmbedHostsEnv))
})

// IframeSrcAllowed reports whether src is an iframe source the sanitiser
// policies keep.
func IframeSrcAllowed(src string) bool {
	return iframeSrcAllowlist().MatchString(strings.TrimSpace(src))
}

var (
	richTextPolicy     *bluemonday.Policy
	richTextPolicyOnce sync.Once
)

// newRichTextPolicy builds a bluemonday policy that matches the frontend
// DOMPurify allowlist (lib/sanitize.ts): the same tag set, no `style`
// attribute, and iframe `src` restricted to the embed host allowlist. This is the
// server-side defense-in-depth applied at the write boundary so malicious HTML
// never reaches storage even if it bypassed the client.
func newRichTextPolicy(iframeSrc *regexp.Regexp) *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	p.AllowElements(
		"h1", "h2", "h3", "h4", "h5", "h6",
		"p", "br", "hr", "div", "span",
		"ul", "ol", "li",
		"strong", "em", "b", "i", "u", "s",
		"blockquote", "code", "pre",
		"figure", "figcaption",
	)
	p.AllowLists()
	p.AllowTables()

	// class is used throughout (prose, entity-link, embed wrappers).
	p.AllowAttrs("class").Globally()

	// Links. Relative (internal) and standard schemes only; no javascript:.
	p.AllowAttrs("href", "target", "rel", "title").OnElements("a")
	// v2 linker bridge attributes.
	p.AllowAttrs("data-entity-id", "data-target-slug", "data-target-schema").OnElements("a")

	// Images.
	p.AllowAttrs("src", "alt", "title", "width", "height", "loading").OnElements("img")

	// Iframes: only YouTube/Vimeo srcs survive.
	p.AllowAttrs("src").Matching(iframeSrc).OnElements("iframe")
	p.AllowAttrs("width", "height", "frameborder", "allow", "allowfullscreen", "loading", "title").OnElements("iframe")

	// Include tel:/sms: so phone links in editor content survive the save.
	p.AllowURLSchemes("http", "https", "mailto", "tel", "sms")
	p.AllowRelativeURLs(true)
	p.RequireNoFollowOnLinks(false)

	return p
}

var (
	inlinePolicy     *bluemonday.Policy
	inlinePolicyOnce sync.Once
)

// newInlinePolicy builds the profile of the heading/text `html` prop: inline
// marks and links only. rel is never taken from input (SanitizeInlineHTML
// regenerates it), and there is no class, style, id or event handler.
func newInlinePolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("strong", "em", "b", "i", "u", "s", "code", "br", "a")
	p.AllowAttrs("href").OnElements("a")
	p.AllowAttrs("target").Matching(regexp.MustCompile(`^_blank$`)).OnElements("a")
	p.AllowURLSchemes("http", "https", "mailto", "tel", "sms")
	p.AllowRelativeURLs(true)
	p.RequireParseableURLs(true)
	p.RequireNoFollowOnLinks(false)
	return p
}

func inlineText() *bluemonday.Policy {
	inlinePolicyOnce.Do(func() { inlinePolicy = newInlinePolicy() })
	return inlinePolicy
}

// styleBackstop is the regexp handed to bluemonday (which only accepts a
// positive regexp): no CSS escapes and no tag breakouts. The real policy is
// styleAllowed, applied by dropUnsafeStyles before bluemonday runs.
var styleBackstop = regexp.MustCompile(`^[^\\<>]*$`)

var (
	styleDeniedRe = regexp.MustCompile(`(?i)expression\s*\(|javascript:|vbscript:|@import|-moz-binding|(?:^|[;{\s])behavior\s*:`)
	styleURLRe    = regexp.MustCompile(`(?i)url\(`)
	styleURLArgRe = regexp.MustCompile(`(?i)^url\(\s*(?:"([^"]*)"|'([^']*)'|([^"'()\s]*))\s*\)`)
	styleSchemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)
	styleDataRe   = regexp.MustCompile(`(?i)^data:image/(?:png|jpe?g|gif|webp)[;,]`)
)

// styleAllowed is the inline-`style` policy for html sections (denylist; the
// frontend isSafeStyle in lib/sanitize.ts implements the same rules and both
// are checked against testdata/style-cases.json). Any CSS function is fine
// except the dangerous ones. Rejected: backslashes (CSS escapes), angle
// brackets, expression(), javascript:, vbscript:, @import, -moz-binding, a
// `behavior:` property, and url() whose target is not http(s), root-relative,
// relative or a raster data:image (png/jpeg/gif/webp; svg+xml is excluded).
func styleAllowed(v string) bool {
	if !styleBackstop.MatchString(v) || styleDeniedRe.MatchString(v) {
		return false
	}
	rest := v
	for {
		loc := styleURLRe.FindStringIndex(rest)
		if loc == nil {
			return true
		}
		m := styleURLArgRe.FindStringSubmatch(rest[loc[0]:])
		if m == nil {
			return false
		}
		if !styleURLTargetAllowed(m[1] + m[2] + m[3]) {
			return false
		}
		rest = rest[loc[0]+len(m[0]):]
	}
}

func styleURLTargetAllowed(t string) bool {
	t = strings.TrimSpace(t)
	for _, r := range t {
		if r < 0x20 || r == 0x7f { // browsers strip tabs/newlines inside URLs
			return false
		}
	}
	lower := strings.ToLower(t)
	switch {
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"), strings.HasPrefix(t, "/"):
		return true
	case styleDataRe.MatchString(t):
		return true
	}
	return !styleSchemeRe.MatchString(t)
}

// dropUnsafeStyles removes every style attribute that fails styleAllowed,
// leaving all other bytes untouched (token-level rewrite, no tree rebuild).
// bluemonday drops a whole attribute on mismatch but cannot express a denylist.
func dropUnsafeStyles(src string) string {
	if !strings.Contains(strings.ToLower(src), "style") {
		return src
	}
	var out strings.Builder
	z := html.NewTokenizer(strings.NewReader(src))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return out.String()
		}
		raw := string(z.Raw())
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			out.WriteString(raw)
			continue
		}
		tok := z.Token()
		kept := tok.Attr[:0:0]
		for _, a := range tok.Attr {
			if strings.EqualFold(a.Key, "style") && !styleAllowed(a.Val) {
				continue
			}
			kept = append(kept, a)
		}
		if len(kept) == len(tok.Attr) {
			out.WriteString(raw)
			continue
		}
		tok.Attr = kept
		out.WriteString(tok.String())
	}
}

var (
	htmlSectionPolicy     *bluemonday.Policy
	htmlSectionPolicyOnce sync.Once
)

// newHTMLSectionPolicy is the rich policy for page_sections[type=html]
// content.body: semantic containers, details/summary, buttons, inline SVG, id/role/style,
// data-* and a fixed set of aria-* attributes. Never script, style blocks,
// forms, event handlers or javascript: URLs. (bluemonday has no attribute-name
// wildcard, so aria-* is an explicit list.)
func newHTMLSectionPolicy(iframeSrc *regexp.Regexp) *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	p.AllowElements(
		"h1", "h2", "h3", "h4", "h5", "h6",
		"p", "br", "hr", "div", "span",
		"strong", "em", "b", "i", "u", "s",
		"blockquote", "code", "pre",
		"figure", "figcaption",
		"section", "aside", "article", "header", "footer", "nav",
		"details", "summary", "button",
		"small", "sup", "sub", "mark", "time",
		"svg", "g", "path", "circle", "rect", "line", "polyline", "polygon", "defs",
	)
	p.AllowLists()
	p.AllowTables()
	p.AllowDataAttributes()

	p.AllowAttrs("class", "id", "role").Globally()
	p.AllowAttrs("style").Matching(styleBackstop).Globally()
	p.AllowAttrs(
		"aria-label", "aria-labelledby", "aria-describedby", "aria-hidden", "aria-expanded",
		"aria-controls", "aria-current", "aria-live", "aria-haspopup", "aria-selected",
		"aria-pressed", "aria-checked", "aria-disabled", "aria-level", "aria-valuenow",
		"aria-valuemin", "aria-valuemax", "aria-atomic", "aria-busy", "aria-modal",
	).Globally()

	p.AllowAttrs("type").Matching(regexp.MustCompile(`^(button|submit|reset)$`)).OnElements("button")
	p.AllowAttrs("open").OnElements("details")
	p.AllowAttrs("datetime").OnElements("time")

	p.AllowAttrs("href", "target", "rel", "title").OnElements("a")
	p.AllowAttrs("src", "alt", "title", "width", "height", "loading").OnElements("img")
	p.AllowAttrs("src").Matching(iframeSrc).OnElements("iframe")
	p.AllowElements("iframe")
	p.AllowAttrs("width", "height", "frameborder", "allow", "allowfullscreen", "loading", "title").OnElements("iframe")

	svgEls := []string{"svg", "g", "path", "circle", "rect", "line", "polyline", "polygon", "defs"}
	p.AllowAttrs(
		"viewBox", "xmlns", "width", "height", "d", "fill", "stroke", "stroke-width",
		"stroke-linecap", "stroke-linejoin", "cx", "cy", "r", "x", "y", "x1", "y1", "x2", "y2",
		"points", "transform", "fill-rule", "clip-rule",
	).OnElements(svgEls...)

	p.AllowURLSchemes("http", "https", "mailto", "tel", "sms")
	p.AllowRelativeURLs(true)
	p.RequireNoFollowOnLinks(false)

	return p
}

// sanitizeHTMLBody applies the html-section policy to one content.body.
func sanitizeHTMLBody(body string) string {
	return htmlSection().Sanitize(dropUnsafeStyles(body))
}

func htmlSection() *bluemonday.Policy {
	htmlSectionPolicyOnce.Do(func() { htmlSectionPolicy = newHTMLSectionPolicy(iframeSrcAllowlist()) })
	return htmlSectionPolicy
}

func richText() *bluemonday.Policy {
	richTextPolicyOnce.Do(func() { richTextPolicy = newRichTextPolicy(iframeSrcAllowlist()) })
	return richTextPolicy
}

// maxSanitizeDepth bounds recursion into attribute JSON. Anything deeper is
// left as-is (pathological nesting, not real editor content).
const maxSanitizeDepth = 64

// sanitizeAttributes sanitizes every rich-text `content` string inside an
// attributes blob (create path: all input is new). See sanitizeAttributesAgainst.
func sanitizeAttributes(raw json.RawMessage) (json.RawMessage, error) {
	return sanitizeAttributesAgainst(raw, nil)
}

// SanitizeAttributesAgainst is the exported form of sanitizeAttributesAgainst
// for write paths outside this package (the render tests use it).
func SanitizeAttributesAgainst(raw, stored json.RawMessage) (json.RawMessage, error) {
	return sanitizeAttributesAgainst(raw, stored)
}

// sanitizeAttributesAgainst sanitizes the rich-text `content` strings and
// html-section bodies of raw that differ from the stored blob at the same JSON
// path (sections: matched by `id`, else by index). Values equal to the stored
// ones were accepted when they were written and are left alone, so an
// unchanged save never rewrites legacy content; new or changed input is always
// sanitized. A nil/empty stored blob sanitizes everything.
//
// It decodes once, mutates in place (O(size), not O(depth*size)), and
// re-marshals only if something changed, so an unchanged blob returns
// byte-identical (no spurious entity version diff). Numbers are preserved
// exactly via json.Number.
func sanitizeAttributesAgainst(raw, stored json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return raw, nil
	}
	v, ok := decodeJSON(raw)
	if !ok {
		return raw, nil // malformed: leave to schema validation, don't corrupt
	}
	var old any
	if len(bytes.TrimSpace(stored)) > 0 {
		old, _ = decodeJSON(stored)
	}
	// page_doc is sanitised by SanitizeDocComponents against the stored doc and
	// sections (see normalizeDoc); a path walk here has no baseline for it.
	root, _ := v.(map[string]any)
	doc, hasDoc := root[keyDoc]
	delete(root, keyDoc)
	changed := false
	sanitizeValue(v, old, 0, &changed)
	sanitizeHTMLSections(v, old, &changed)
	sanitizeSchemaFields(v, old, &changed)
	if hasDoc {
		root[keyDoc] = doc
	}
	if !changed {
		return raw, nil
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw, nil
	}
	return out, nil
}

func decodeJSON(raw []byte) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	return v, true
}

// sanitizeValue walks a decoded JSON value in place, sanitizing the string
// value of any key named "content" unless old (the stored value at the same
// path, nil when absent) holds the identical string. Maps/slices are
// reference types, so in-place mutation is visible to the caller.
func sanitizeValue(v, old any, depth int, changed *bool) {
	if depth > maxSanitizeDepth {
		return
	}
	switch t := v.(type) {
	case map[string]any:
		oldMap, _ := old.(map[string]any)
		for k, child := range t {
			if s, ok := child.(string); ok && k == "content" {
				if prev, same := oldMap[k].(string); same && prev == s {
					continue
				}
				if clean := richText().Sanitize(s); clean != s {
					t[k] = clean
					*changed = true
				}
				continue
			}
			sanitizeValue(child, oldMap[k], depth+1, changed)
		}
	case []any:
		// Array elements are never treated as content strings (avoids escaping a
		// plain-text string array that happens to sit under a "content" key).
		oldArr, _ := old.([]any)
		for i, child := range t {
			var oc any
			if i < len(oldArr) {
				oc = oldArr[i]
			}
			sanitizeValue(child, oc, depth+1, changed)
		}
	}
}

// htmlSectionAliasesEnv lists extra section types (comma separated) that are
// stored as html but not literally typed "html" (e.g. legacy types a plugin
// aliases to html on the frontend). Default empty. The backend has no view of
// the frontend alias registry, so this is its schema-independent equivalent.
const htmlSectionAliasesEnv = "HTML_SECTION_TYPE_ALIASES"

// isHTMLSectionType reports whether sections of this type carry html in
// content.body: "html" itself or a type listed in HTML_SECTION_TYPE_ALIASES.
func isHTMLSectionType(t string) bool {
	if t == "html" {
		return true
	}
	for a := range strings.SplitSeq(os.Getenv(htmlSectionAliasesEnv), ",") {
		if a = strings.TrimSpace(a); a != "" && a == t {
			return true
		}
	}
	return false
}

// sanitizeHTMLSections applies the rich html-section policy to
// content.body of every attributes.page_sections[] element whose type is
// "html" or listed in HTML_SECTION_TYPE_ALIASES. The generic walk above never
// touches `body` (only keys named "content"), so this is the only
// sanitisation those bodies get on save.
func sanitizeHTMLSections(v, old any, changed *bool) {
	root, ok := v.(map[string]any)
	if !ok {
		return
	}
	sections, ok := root["page_sections"].([]any)
	if !ok {
		return
	}
	var oldSections []any
	if oldRoot, ok := old.(map[string]any); ok {
		oldSections, _ = oldRoot["page_sections"].([]any)
	}
	for i, item := range sections {
		sec, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := sec["type"].(string); !isHTMLSectionType(typ) {
			continue
		}
		content, ok := sec["content"].(map[string]any)
		if !ok {
			continue
		}
		body, ok := content["body"].(string)
		if !ok {
			continue
		}
		if prev, same := storedSectionBody(oldSections, sec, i); same && prev == body {
			continue
		}
		if clean := sanitizeHTMLBody(body); clean != body {
			content["body"] = clean
			*changed = true
		}
	}
}

// storedSectionBody returns the stored html body of the section matching sec:
// the one with the same `id`, else the one at the same index. The match only
// counts when the stored section was itself an html-type section: bodies of
// other types are never sanitised on save, so they are no baseline for "unchanged".
func storedSectionBody(oldSections []any, sec map[string]any, index int) (string, bool) {
	match := matchStoredSection(oldSections, sec, index)
	if typ, _ := match["type"].(string); !isHTMLSectionType(typ) {
		return "", false
	}
	oc, _ := match["content"].(map[string]any)
	body, ok := oc["body"].(string)
	return body, ok
}

// matchStoredSection returns the stored section matching sec: the one with the
// same `id`, else the one at the same index (nil when none).
func matchStoredSection(oldSections []any, sec map[string]any, index int) map[string]any {
	if id, _ := sec["id"].(string); id != "" {
		for _, o := range oldSections {
			if om, ok := o.(map[string]any); ok && om["id"] == id {
				return om
			}
		}
	}
	if index < len(oldSections) {
		m, _ := oldSections[index].(map[string]any)
		return m
	}
	return nil
}
