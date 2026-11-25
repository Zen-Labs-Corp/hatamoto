package entity

import (
	"fmt"
	"strings"
	"unicode"
)

// Site search and scripts written without spaces (#412).
//
// The 'simple' text search parser splits words on spaces and punctuation, so
// Japanese, Chinese and Korean text becomes one lexeme per run between
// punctuation marks, and a word inside a run never matches. The search vector
// (migration 202611251600_entity_search_cjk.sql) therefore indexes every run of
// these scripts as overlapping bigrams: the token at each character is that
// character and the next one in the run, and the last character of a run
// stands alone. "上映会" is indexed as 上映 映会 会. Every other text is passed
// through unchanged, so Latin-script vectors and their ranking are what they
// were.
//
// A query with such a run is matched the same way: the run becomes the phrase
// of its bigrams with the last character as a prefix ("上映会" is
// 上映 <-> 映会 <-> 会:*), which matches the run wherever it starts or ends in
// the indexed text; a single character is that character as a prefix. No
// Postgres extension is needed.

// segmentlessRanges are the characters indexed as bigrams: Han (with its
// iteration and numeral marks, radicals and compatibility ideographs), kana,
// Bopomofo and Hangul. The SQL class in the migration is generated from this
// table and a test keeps the two equal; never change one without a new
// migration that rebuilds the column.
var segmentlessRanges = []struct{ Lo, Hi rune }{
	{0x1100, 0x11FF},   // Hangul Jamo
	{0x2E80, 0x2FDF},   // CJK and Kangxi radicals
	{0x3005, 0x3007},   // 々 〆 〇
	{0x3021, 0x3029},   // Hangzhou numerals
	{0x3031, 0x3035},   // kana repeat marks
	{0x3041, 0x3096},   // Hiragana
	{0x309D, 0x309F},   // Hiragana iteration marks, ゟ
	{0x30A1, 0x30FA},   // Katakana
	{0x30FC, 0x30FF},   // ー and Katakana iteration marks, ヿ (not the ・ separator)
	{0x3105, 0x312F},   // Bopomofo
	{0x3131, 0x318E},   // Hangul compatibility Jamo
	{0x31A0, 0x31BF},   // Bopomofo extended
	{0x31F0, 0x31FF},   // Katakana phonetic extensions
	{0x3400, 0x4DBF},   // CJK extension A
	{0x4E00, 0x9FFF},   // CJK unified ideographs
	{0xA960, 0xA97F},   // Hangul Jamo extended A
	{0xAC00, 0xD7AF},   // Hangul syllables
	{0xD7B0, 0xD7FF},   // Hangul Jamo extended B
	{0xF900, 0xFAFF},   // CJK compatibility ideographs
	{0xFF66, 0xFF9F},   // halfwidth Katakana
	{0xFFA0, 0xFFDC},   // halfwidth Hangul
	{0x20000, 0x3134F}, // CJK extensions B to G, compatibility supplement
}

// isSegmentless reports whether r is indexed as bigrams.
func isSegmentless(r rune) bool {
	for _, g := range segmentlessRanges {
		if r >= g.Lo && r <= g.Hi {
			return true
		}
	}
	return false
}

// segmentlessClassSQL is the body of the Postgres bracket expression that
// matches the same characters as isSegmentless ("\u1100-\u11ff..."), in the
// form the migration spells it.
func segmentlessClassSQL() string {
	var b strings.Builder
	esc := func(r rune) string {
		if r > 0xFFFF {
			return fmt.Sprintf(`\U%08x`, r)
		}
		return fmt.Sprintf(`\u%04x`, r)
	}
	for _, g := range segmentlessRanges {
		b.WriteString(esc(g.Lo))
		if g.Hi != g.Lo {
			b.WriteString("-" + esc(g.Hi))
		}
	}
	return b.String()
}

// segmentlessTSQuery turns a websearch-syntax query that contains a
// segmentless character into to_tsquery input matching the bigram index.
// It reads the websearch syntax: words, "quoted phrases", a leading - for
// NOT and the word or between two terms. Each term is the phrase of its
// pieces: a segmentless run becomes its bigram phrase, any other text a
// quoted operand the 'simple' parser splits like the indexed text. It returns
// "" when the query has no segmentless character or nothing to search for;
// the caller then uses websearch_to_tsquery as before.
func segmentlessTSQuery(q string) string {
	if !strings.ContainsFunc(q, isSegmentless) {
		return ""
	}
	type term struct {
		text   string
		not    bool
		phrase bool
	}
	var (
		terms []term
		ops   []string // ops[i] joins terms[i] and terms[i+1]
		or    bool
	)
	rs := []rune(q)
	for i := 0; i < len(rs); {
		if unicode.IsSpace(rs[i]) {
			i++
			continue
		}
		t := term{}
		if rs[i] == '-' && i+1 < len(rs) && !unicode.IsSpace(rs[i+1]) {
			t.not = true
			i++
		}
		start := i
		if rs[i] == '"' {
			t.phrase = true
			i++
			start = i
			for i < len(rs) && rs[i] != '"' {
				i++
			}
			t.text = string(rs[start:i])
			if i < len(rs) {
				i++ // the closing quote
			}
		} else {
			for i < len(rs) && !unicode.IsSpace(rs[i]) && rs[i] != '"' {
				i++
			}
			t.text = string(rs[start:i])
		}
		if !t.not && !t.phrase && strings.EqualFold(t.text, "or") {
			or = len(terms) > 0
			continue
		}
		expr := termTSQuery(t.text)
		if expr == "" {
			continue
		}
		if t.not {
			expr = "!" + expr
		}
		if len(terms) > 0 {
			if or {
				ops = append(ops, " | ")
			} else {
				ops = append(ops, " & ")
			}
		}
		or = false
		t.text = expr
		terms = append(terms, t)
	}
	var b strings.Builder
	for i, t := range terms {
		if i > 0 {
			b.WriteString(ops[i-1])
		}
		b.WriteString(t.text)
	}
	return b.String()
}

// termTSQuery is one term as a parenthesised phrase of its pieces, or "" when
// it holds nothing searchable.
func termTSQuery(text string) string {
	var pieces []string
	rs := []rune(text)
	for i := 0; i < len(rs); {
		j := i
		seg := isSegmentless(rs[i])
		for j < len(rs) && isSegmentless(rs[j]) == seg {
			j++
		}
		run := rs[i:j]
		i = j
		if seg {
			for k := range run {
				if k == len(run)-1 {
					pieces = append(pieces, tsLexeme(string(run[k]))+":*")
				} else {
					pieces = append(pieces, tsLexeme(string(run[k:k+2])))
				}
			}
			continue
		}
		if strings.ContainsFunc(string(run), func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
			pieces = append(pieces, tsLexeme(string(run)))
		}
	}
	if len(pieces) == 0 {
		return ""
	}
	return "(" + strings.Join(pieces, " <-> ") + ")"
}

// tsLexeme quotes s as one to_tsquery operand.
func tsLexeme(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `''`).Replace(s) + "'"
}
