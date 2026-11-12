package fonts

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Face is one @font-face block of a Google Fonts stylesheet, reduced to the
// fields Hatamoto keeps. The stylesheet text itself is never kept.
type Face struct {
	Family       string
	Style        string
	Weight       int
	Variable     bool // the stylesheet gave a weight range; Weight is its first value
	Subset       string
	URL          string
	UnicodeRange string
}

var (
	srcRe = sync.OnceValue(func() *regexp.Regexp {
		return regexp.MustCompile(`^url\(\s*(https://[A-Za-z0-9._~%/:@+=&?-]{1,500})\s*\)\s*format\(\s*['"]?woff2['"]?\s*\)$`)
	})
	rangeRe   = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^[Uu+0-9A-Fa-f?, -]+$`) })
	subsetRe  = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^[A-Za-z0-9 _-]{1,40}$`) })
	weightRe  = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^(\d{3})(?:\s+(\d{3}))?$`) })
	stretchRe = sync.OnceValue(func() *regexp.Regexp {
		return regexp.MustCompile(`^(normal|(?:condensed|expanded|(?:ultra|extra|semi)-(?:condensed|expanded))|\d{1,3}(?:\.\d+)?%(?:\s+\d{1,3}(?:\.\d+)?%)?)$`)
	})
	errSheet    = errors.New("Google Fonts answered with a stylesheet this server does not understand")
	maxBlocks   = 200  // parser bound on @font-face blocks in one stylesheet
	maxFaces    = 40   // font files one download may fetch, after the Latin filter
	maxRangeLen = 4000 // Google sends lists past 1500 bytes
	allowedKV   = map[string]bool{
		"font-family": true, "font-style": true, "font-weight": true,
		"font-display": true, "font-stretch": true, "src": true, "unicode-range": true,
	}
)

func sheetErr(why string) error { return fmt.Errorf("%w (%s)", errSheet, why) }

// ParseFaces reads a Google Fonts stylesheet with a strict whitelist: only
// comments and @font-face blocks, only the declarations in allowedKV, one
// https woff2 url per face. Anything else is an error.
func ParseFaces(css string) ([]Face, error) {
	var faces []Face
	subset := ""
	s := css
	for {
		s = strings.TrimSpace(s)
		if s == "" {
			break
		}
		if strings.HasPrefix(s, "/*") {
			end := strings.Index(s, "*/")
			if end < 0 {
				return nil, sheetErr("unterminated comment")
			}
			c := strings.TrimSpace(s[2:end])
			if subsetRe().MatchString(c) {
				subset = c
			} else {
				subset = ""
			}
			s = s[end+2:]
			continue
		}
		if !strings.HasPrefix(s, "@font-face") {
			return nil, sheetErr("unexpected content")
		}
		s = strings.TrimSpace(s[len("@font-face"):])
		if !strings.HasPrefix(s, "{") {
			return nil, sheetErr("missing block")
		}
		end := strings.Index(s, "}")
		if end < 0 {
			return nil, sheetErr("unterminated block")
		}
		f, err := parseBlock(s[1:end])
		if err != nil {
			return nil, err
		}
		f.Subset = subset
		subset = ""
		faces = append(faces, f)
		if len(faces) > maxBlocks {
			return nil, sheetErr("too many font blocks")
		}
		s = s[end+1:]
	}
	if len(faces) == 0 {
		return nil, sheetErr("no fonts")
	}
	return faces, nil
}

func parseBlock(body string) (Face, error) {
	var f Face
	seen := map[string]bool{}
	for _, decl := range strings.Split(body, ";") {
		decl = strings.TrimSpace(decl)
		if decl == "" {
			continue
		}
		k, v, ok := strings.Cut(decl, ":")
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		if !ok || !allowedKV[k] {
			return f, sheetErr("unknown declaration")
		}
		if seen[k] {
			return f, sheetErr("repeated declaration")
		}
		seen[k] = true
		switch k {
		case "font-family":
			f.Family = strings.Trim(v, `'"`)
			if !ValidFamily(f.Family) {
				return f, sheetErr("family")
			}
		case "font-style":
			if !ValidStyle(v) {
				return f, sheetErr("style")
			}
			f.Style = v
		case "font-weight":
			m := weightRe().FindStringSubmatch(v)
			if m == nil {
				return f, sheetErr("weight")
			}
			f.Weight, _ = strconv.Atoi(m[1])
			f.Variable = m[2] != ""
			if !ValidWeight(f.Weight) {
				return f, sheetErr("weight")
			}
		case "font-stretch":
			// Google sends font-stretch: 100% for width-axis families (Open
			// Sans, Roboto). Checked, then ignored like font-display.
			if !stretchRe().MatchString(v) {
				return f, sheetErr("stretch")
			}
		case "font-display":
			// Accepted and ignored: the site always emits swap.
		case "src":
			m := srcRe().FindStringSubmatch(v)
			if m == nil {
				return f, sheetErr("src")
			}
			f.URL = m[1]
		case "unicode-range":
			if len(v) > maxRangeLen || !rangeRe().MatchString(v) {
				return f, sheetErr("unicode-range")
			}
			f.UnicodeRange = v
		}
	}
	if f.Family == "" || f.Style == "" || f.Weight == 0 || f.URL == "" {
		return f, sheetErr("incomplete face")
	}
	return f, nil
}
