package adminpath

import (
	_ "embed"
	"strings"
)

// words.txt is a small list of common English words and first names (one per
// line, lower case). It is a floor for the weak check, not a dictionary. It
// holds no word made only of Crockford letters, so a generated address can
// never contain one.
//
//go:embed words.txt
var wordsRaw string

var wordSet = func() map[string]struct{} {
	m := map[string]struct{}{}
	for _, w := range strings.Fields(wordsRaw) {
		m[w] = struct{}{}
	}
	return m
}()

func isWord(s string) bool {
	_, ok := wordSet[s]
	return ok
}
