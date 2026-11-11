package schemafield

import (
	"regexp"
	"strings"
)

var (
	// urlSchemeRe finds a known scheme anywhere a reader could take it as one
	// (at the start, or after punctuation or markup) with a non-space character
	// right after the colon, so "Doors: 7pm", "Tel: 020 7946 0000", "About: the
	// venue" and "19:30" are labels, not schemes, but "javascript:x",
	// "tel:+44" and `href="mailto:a@b"` are.
	// mailto and tel are included: they never render as a picture, but a link
	// of any kind is something a person should look at.
	urlSchemeRe = regexp.MustCompile(`(?:^|[^a-z0-9+.-])(?:https?|ftp|ftps|ws|wss|file|data|blob|javascript|vbscript|mailto|tel|sms|about)\s*:[^\s]`)
	// mediaExtRe finds a file name with an image, audio, video or document
	// extension, with or without a query or fragment after it.
	mediaExtRe = regexp.MustCompile(`\.(?:jpe?g|png|gif|webp|avif|svg|bmp|ico|tiff?|heic|mp4|webm|mov|mp3|ogg|wav|pdf)\b`)
	cssImageRe = regexp.MustCompile(`(?:url|image-set)\s*\(`)
)

// LooksLikeURLOrPath reports whether a string reads as a link, a path or a
// file: it has "://", a known scheme (http, data, javascript, mailto, ...), a
// leading slash or backslash, a leading "www.", a media or document file
// extension, or a CSS url( or image-set(. It looks at the string as written and
// as a reader would resolve it (decoded until stable, backslashes straightened,
// lowercased). Inside HTML markup, a link attribute (href, src, action, cite,
// data, xlink:href, data-entity-id, ...) with a non-blank value matches whatever
// the value is, and every other attribute value is checked like plain text
// (never parsed as markup again). Plain prose, numbers, times ("19:30"), labels
// ("Doors: 7pm") and fractions ("page 3/4") do not match. It is deliberately broad: a false
// positive only makes a change wait for a person.
func LooksLikeURLOrPath(s string) bool {
	if strings.TrimSpace(s) == "" {
		return false
	}
	l, n := strings.ToLower(s), NormalizeValue(s)
	return looksLikeURLOrPath(l) || looksLikeURLOrPath(n) ||
		anyAttr(l, markupLinkAttr) || anyAttr(n, markupLinkAttr)
}

// linkAttrs carry a link whatever their value: a relative one ("tickets",
// "?aff=x") is a live same-origin link too, and data-entity-id gets an href at
// render time (render/links.go).
var linkAttrs = map[string]bool{
	"href": true, "src": true, "srcset": true, "action": true, "formaction": true,
	"cite": true, "data": true, "poster": true, "background": true,
	"xlink:href": true, "ping": true, "longdesc": true, "manifest": true,
	"codebase": true, "data-entity-id": true,
}

// markupLinkAttr tests one attribute of a tag. Its value is checked once, as
// plain text (never as markup again), so nesting cannot multiply the work.
func markupLinkAttr(key, val string) bool {
	if strings.TrimSpace(val) == "" {
		return false
	}
	return linkAttrs[key] || looksLikeURLOrPath(strings.ToLower(val)) || looksLikeURLOrPath(NormalizeValue(val))
}

func looksLikeURLOrPath(s string) bool {
	t := strings.TrimSpace(s)
	return strings.Contains(t, "://") ||
		strings.HasPrefix(t, "/") || strings.HasPrefix(t, `\`) ||
		strings.HasPrefix(t, "www.") ||
		urlSchemeRe.MatchString(t) ||
		mediaExtRe.MatchString(t) ||
		cssImageRe.MatchString(t)
}
