package adminpath

import (
	"context"
	"math"
	"strings"
)

// ReservedPrefixes are the first segments the site always serves (or must
// keep free) at the top level. The server test checks that every fixed route
// prefix appears here or is exempt. "admin" is deliberately absent: it is the
// legacy default and may be chosen as a weak address.
var ReservedPrefixes = []string{
	"api", "p", "theme", "media", ".well-known", "sitemap.xml", "robots.txt", "health", "ready",
	"llms.txt", "plugin-ui", "feed", "feed.xml", "atom.xml", "sitemap", "forms", "auth", "-", "favicon.ico",
}

const (
	minOKLen      = 10
	minStrongLen  = 12
	minStrongBits = 50
	minNameLen    = 4 // a site name or domain label shorter than this is not matched
	wordBits      = 12
)

// Assess ranks an admin address. site may be nil. The order is: a shape that
// is not a single segment is invalid; a first segment the site serves is
// reserved (checked before the length rule, so "/api" is reserved, not just
// too short); then the format rules; then the weak rules; then ok or strong.
func Assess(ctx context.Context, path string, site Site) Result {
	res := Result{Path: strings.TrimSpace(path)}
	lower := strings.ToLower(res.Path)
	if !strings.HasPrefix(lower, "/") || strings.Contains(lower[1:], "/") || lower == "/" {
		_, err := Normalize(path)
		if err == nil {
			err = ErrFormat
		}
		res.Reasons = []Reason{{"format", err.Error()}}
		return res
	}
	seg := lower[1:]

	for _, r := range ReservedPrefixes {
		if seg == r {
			res.Path, res.Level = lower, LevelReserved
			res.Reasons = []Reason{{"reserved", "the site serves /" + r}}
			return res
		}
	}
	if site != nil {
		if ok, what := site.Serves(ctx, lower); ok {
			res.Path, res.Level = lower, LevelReserved
			res.Reasons = []Reason{{"served", what}}
			return res
		}
	}

	norm, err := Normalize(path)
	if err != nil {
		res.Reasons = []Reason{{"format", err.Error()}}
		return res
	}
	res.Path = norm
	seg = norm[1:]

	var weak []Reason
	if onScannerList(seg) {
		weak = append(weak, Reason{"scanner", "on the scanner list"})
	}
	if len(seg) < minOKLen {
		weak = append(weak, Reason{"short", "shorter than 10 characters"})
	}
	if isDictionaryWord(seg) {
		weak = append(weak, Reason{"dictionary", "a common word or name with digits or dashes added"})
	}
	if site != nil {
		if r, ok := siteNameReason(seg, site); ok {
			weak = append(weak, r)
		}
	}
	res.Bits = estimateBits(seg)
	if len(weak) > 0 {
		res.Level, res.Reasons = LevelWeak, weak
		return res
	}
	if len(seg) >= minStrongLen && res.Bits >= minStrongBits {
		res.Level = LevelStrong
		return res
	}
	res.Level = LevelOK
	res.Reasons = []Reason{{"variety", "easy to guess compared with a random address"}}
	return res
}

func isDictionaryWord(seg string) bool {
	for _, c := range weakCandidates(seg) {
		if isWord(c) {
			return true
		}
	}
	return false
}

// siteNameReason reports a seg that contains the site name or the first label
// of the site domain (4 or more characters of it).
func siteNameReason(seg string, site Site) (Reason, bool) {
	flat := strings.ReplaceAll(seg, "-", "")
	if n := alnumLower(site.Name()); len(n) >= minNameLen && strings.Contains(flat, n) {
		return Reason{"site-name", "contains the site name"}, true
	}
	host := strings.ToLower(site.Domain())
	host = strings.TrimPrefix(host, "www.")
	if label := alnumLower(strings.SplitN(host, ".", 2)[0]); len(label) >= minNameLen && strings.Contains(flat, label) {
		return Reason{"site-name", "contains the site domain"}, true
	}
	return Reason{}, false
}

func alnumLower(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 'a' - 'A'
		}
		return -1
	}, s)
}

// estimateBits is a rough guessing-cost estimate for seg, in bits.
//
//   - The alphabet size comes from the classes used: letters 26, digits +10,
//     dash +1. Each letter or digit is worth log2(alphabet) bits; a dash is a
//     separator and is worth nothing.
//   - A character equal to the one before it (aa), or that continues a run of
//     steps in one direction (the c of abc, the 3 of 123), is worth nothing.
//     A single step (ab) is not penalised: random text has those too often
//     (3 raw draws in 100 would fall below strong), so the generator would
//     retry needlessly.
//   - A dictionary word of 4 or more letters found in seg is worth a flat
//     12 bits instead of its characters.
func estimateBits(seg string) float64 {
	alphabet := 0
	var hasLetter, hasDigit, hasDash bool
	for i := 0; i < len(seg); i++ {
		switch c := seg[i]; {
		case c >= 'a' && c <= 'z':
			hasLetter = true
		case c >= '0' && c <= '9':
			hasDigit = true
		case c == '-':
			hasDash = true
		}
	}
	if hasLetter {
		alphabet += 26
	}
	if hasDigit {
		alphabet += 10
	}
	if hasDash {
		alphabet++
	}
	if alphabet < 2 {
		return 0
	}
	per := math.Log2(float64(alphabet))

	bits := 0.0
	for i := 0; i < len(seg); {
		if n := wordAt(seg, i); n > 0 {
			bits += wordBits
			i += n
			continue
		}
		c := seg[i]
		if c != '-' && !(i > 0 && (seg[i-1] == c || i > 1 && runStep(seg[i-2], seg[i-1], c))) {
			bits += per
		}
		i++
	}
	return bits
}

// wordAt returns the length of the longest dictionary word (4+ letters)
// starting at seg[i], or 0.
func wordAt(seg string, i int) int {
	for n := min(len(seg)-i, 12); n >= 4; n-- {
		if isWord(seg[i : i+n]) {
			return n
		}
	}
	return 0
}

// runStep reports a, b, c stepping by one in the same direction.
func runStep(a, b, c byte) bool {
	return a+1 == b && b+1 == c || a == b+1 && b == c+1
}
