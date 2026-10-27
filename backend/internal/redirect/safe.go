package redirect

import (
	"net/url"
	"strings"
	"unicode"
)

// SafeLocation reports whether a computed redirect target may be sent as a
// Location header. A target is safe when it is
//
//   - a single-slash relative path ("/x", never "//x" or "/\x"), or
//   - an absolute http(s) URL, allowed only when the owner configured an
//     absolute target (absoluteOK), never one a request could have steered.
//
// Browsers treat "\" as "/" and drop tabs and newlines, so a backslash, a
// control character or a Unicode slash look-alike right after the leading "/"
// turns "/x" into a protocol-relative URL. Every check runs on the raw and the
// percent-decoded form, so an encoded payload is rejected as well.
func SafeLocation(target string, absoluteOK bool) bool {
	if target == "" {
		return false
	}
	if isAbsoluteExternal(target) {
		return absoluteOK && cleanChars(target) && !strings.Contains(target, `\`)
	}
	forms := []string{target}
	if dec, err := url.PathUnescape(target); err == nil {
		forms = append(forms, dec)
	} else {
		return false
	}
	for _, f := range forms {
		if !strings.HasPrefix(f, "/") || !cleanChars(f) || strings.Contains(f, `\`) {
			return false
		}
		if len(f) > 1 {
			next := []rune(f[1:])[0]
			if next == '/' || next == '\\' || isSlashLike(next) {
				return false
			}
		}
	}
	return true
}

func cleanChars(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) && r != ' ' {
			return false
		}
	}
	return true
}

// isSlashLike matches the Unicode characters that normalise or render as a
// slash or backslash.
func isSlashLike(r rune) bool {
	switch r {
	case '\u2044', '\u2215', '\u29F8', '\uFF0F', '\u2216', '\u29F5', '\u29F9', '\uFF3C', '\u2571', '\u2572':
		return true
	}
	return false
}

// escapeCapture path-escapes each segment of a capture, keeping "/" as the
// separator, so a capture can never introduce a backslash, a control
// character or a stray "?" or "#" into the expanded target.
func escapeCapture(c string) string {
	if c == "" {
		return ""
	}
	parts := strings.Split(c, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
