package spam

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Verdict is the outcome of the spam pipeline for one submission.
type Verdict int

// Pipeline outcomes.
const (
	Allow       Verdict = iota // store as a normal submission
	Spam                       // store with status spam, send nothing
	Refuse                     // reject the submission
	CaptchaFail                // the captcha was missing, wrong or could not be checked
)

// Reasons EvaluateRules reports. They name the rule only, never user content.
const (
	ReasonLinks  = "too many links"
	ReasonWord   = "blocked word"
	ReasonScript = "blocked script"
)

// linkRe matches only the URL prefix, so links glued together are each counted.
var linkRe = regexp.MustCompile(`(?i)https?://(?:www\.)?|\bwww\.`)

var scriptTables = map[string]*unicode.RangeTable{
	"cyrillic":   unicode.Cyrillic,
	"arabic":     unicode.Arabic,
	"hebrew":     unicode.Hebrew,
	"thai":       unicode.Thai,
	"devanagari": unicode.Devanagari,
}

// cjkTables are the scripts the "cjk" setting covers.
var cjkTables = []*unicode.RangeTable{unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul}

// EvaluateRules checks the submitted values against the content rules and
// reports the first rule that trips. Control fields must already be stripped.
func EvaluateRules(s Settings, data map[string]interface{}) (reason string, tripped bool) {
	var texts []string
	for _, v := range data {
		collectStrings(v, &texts)
	}

	links := 0
	for _, t := range texts {
		links += len(linkRe.FindAllStringIndex(t, -1))
	}
	if links > s.MaxLinks {
		return ReasonLinks, true
	}

	matchers := wordMatchers(s.BlockedWords)
	for _, t := range texts {
		lower := strings.ToLower(t)
		for _, m := range matchers {
			if m(lower) {
				return ReasonWord, true
			}
		}
	}

	if len(s.BlockedScripts) > 0 {
		tables := scriptRanges(s.BlockedScripts)
		for _, t := range texts {
			for _, r := range t {
				if unicode.IsOneOf(tables, r) {
					return ReasonScript, true
				}
			}
		}
	}
	return "", false
}

// wordMatchers returns one matcher per blocked word. A word matches whole
// words only, so "ass" does not block "class"; a phrase matches as a run of
// whole words. An edge that is punctuation (".ru") needs no boundary. Scripts written without spaces (CJK, Thai) match as substrings.
func wordMatchers(blocked []string) []func(lower string) bool {
	var out []func(string) bool
	for _, w := range blocked {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" {
			continue
		}
		if strings.IndexFunc(w, isUnspacedScript) >= 0 {
			out = append(out, func(lower string) bool { return strings.Contains(lower, w) })
			continue
		}
		// A side needs a word boundary only when the entry's edge is a letter or
		// digit, so ".ru" and "@mail.ru" still match inside "x.ru".
		pattern := regexp.QuoteMeta(w)
		if first, _ := utf8.DecodeRuneInString(w); isWordRune(first) {
			pattern = `(?:^|[^\p{L}\p{N}])` + pattern
		}
		if last, _ := utf8.DecodeLastRuneInString(w); isWordRune(last) {
			pattern += `(?:$|[^\p{L}\p{N}])`
		}
		re := regexp.MustCompile(pattern)
		out = append(out, re.MatchString)
	}
	return out
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }

func isUnspacedScript(r rune) bool {
	return unicode.IsOneOf(cjkTables, r) || unicode.Is(unicode.Thai, r)
}

func scriptRanges(names []string) []*unicode.RangeTable {
	var out []*unicode.RangeTable
	for _, n := range names {
		if n == "cjk" {
			out = append(out, cjkTables...)
		} else if t, ok := scriptTables[n]; ok {
			out = append(out, t)
		}
	}
	return out
}

// collectStrings gathers every string inside v, including list and nested map values.
func collectStrings(v interface{}, out *[]string) {
	switch x := v.(type) {
	case string:
		*out = append(*out, x)
	case []string:
		*out = append(*out, x...)
	case []interface{}:
		for _, e := range x {
			collectStrings(e, out)
		}
	case map[string]interface{}:
		for _, e := range x {
			collectStrings(e, out)
		}
	}
}
