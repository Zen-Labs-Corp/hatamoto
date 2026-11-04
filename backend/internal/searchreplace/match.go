// Package searchreplace finds and replaces plain text or a URL across entity
// attributes, page builder text, SEO fields and menus. Matching is plain text
// or whole word; there is no regex.
package searchreplace

import (
	"strings"
	"unicode"
)

// Options says what to find and how.
type Options struct {
	Find      string
	Replace   string
	WholeWord bool
	MatchCase bool
}

// Hit is one occurrence of the search text. Index is its number within the
// value it was found in, counted in document order from zero.
type Hit struct {
	Index   int
	Context string
}

const contextRunes = 40

// span is a match as rune offsets, [start, end).
type span struct{ start, end int }

// runeEq compares two runes, folding case unless matchCase is set.
func runeEq(a, b rune, matchCase bool) bool {
	if a == b {
		return true
	}
	if matchCase {
		return false
	}
	for f := unicode.SimpleFold(a); f != a; f = unicode.SimpleFold(f) {
		if f == b {
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// findAll returns the non-overlapping matches of o.Find in text, left to
// right. An empty Find matches nothing.
func (o Options) findAll(text []rune) []span {
	find := []rune(o.Find)
	if len(find) == 0 {
		return nil
	}
	var out []span
	for i := 0; i+len(find) <= len(text); {
		if !o.matchAt(text, find, i) {
			i++
			continue
		}
		out = append(out, span{i, i + len(find)})
		i += len(find)
	}
	return out
}

func (o Options) matchAt(text, find []rune, i int) bool {
	for j, f := range find {
		if !runeEq(text[i+j], f, o.MatchCase) {
			return false
		}
	}
	if !o.WholeWord {
		return true
	}
	end := i + len(find)
	if i > 0 && isWordRune(text[i-1]) && isWordRune(find[0]) {
		return false
	}
	if end < len(text) && isWordRune(text[end]) && isWordRune(find[len(find)-1]) {
		return false
	}
	return true
}

// context is the text around a match, about contextRunes either side, with
// whitespace collapsed.
func context(text []rune, sp span) string {
	from, to := max(0, sp.start-contextRunes), min(len(text), sp.end+contextRunes)
	return strings.Join(strings.Fields(string(text[from:to])), " ")
}
