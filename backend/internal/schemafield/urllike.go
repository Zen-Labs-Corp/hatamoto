package schemafield

import (
	"regexp"
	"strings"
)

var (
	// urlSchemeRe finds a known scheme anywhere a reader could take it as one
	// (at the start, or after punctuation or markup), so "Doors: 7pm" and
	// "19:30" are not schemes but "javascript:x" and `href="mailto:a@b"` are.
	// mailto and tel are included: they never render as a picture, but a link
	// of any kind is something a person should look at.
	urlSchemeRe = regexp.MustCompile(`(?:^|[^a-z0-9+.-])(?:https?|ftp|ftps|ws|wss|file|data|blob|javascript|vbscript|mailto|tel|sms|about)\s*:`)
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
// lowercased). Plain prose, numbers, times ("19:30"), labels ("Doors: 7pm")
// and fractions ("page 3/4") do not match. It is deliberately broad: a false
// positive only makes a change wait for a person.
func LooksLikeURLOrPath(s string) bool {
	if strings.TrimSpace(s) == "" {
		return false
	}
	return looksLikeURLOrPath(strings.ToLower(s)) || looksLikeURLOrPath(NormalizeValue(s))
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
