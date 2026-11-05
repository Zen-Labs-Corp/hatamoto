package render

import (
	"html/template"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// FontFace is one installed font file a page uses. It is plain data: the
// render package does not know where fonts are stored.
type FontFace struct {
	Family       string
	Weight       int
	Style        string // normal or italic
	Hash         string // sha256 hex of the file, served at /fonts/<hash>.woff2
	UnicodeRange string // optional
}

var (
	fontFamilyRE = regexp.MustCompile(`^[A-Za-z0-9 ._-]{1,60}$`)
	fontHashRE   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	fontRangeRE  = regexp.MustCompile(`^[Uu+0-9A-Fa-f?, -]{1,300}$`)
)

func (f FontFace) valid() bool {
	return fontFamilyRE.MatchString(f.Family) && fontHashRE.MatchString(f.Hash) &&
		f.Weight >= 100 && f.Weight <= 900 && f.Weight%100 == 0 && (f.Style == "normal" || f.Style == "italic") &&
		(f.UnicodeRange == "" || fontRangeRE.MatchString(f.UnicodeRange))
}

// fontHead returns the head markup for the faces a page uses: one @font-face
// per face with font-display swap, and one preload for the face whose hash is
// preload (none when empty or not among the valid faces). A face with any
// field outside its charset is skipped. No faces, no markup.
func fontHead(faces []FontFace, preload string) template.HTML {
	var ok []FontFace
	for _, f := range faces {
		if f.valid() {
			ok = append(ok, f)
		}
	}
	if len(ok) == 0 {
		return ""
	}
	sort.Slice(ok, func(i, j int) bool {
		a, b := ok[i], ok[j]
		if a.Family != b.Family {
			return a.Family < b.Family
		}
		if a.Weight != b.Weight {
			return a.Weight < b.Weight
		}
		if a.Style != b.Style {
			return a.Style < b.Style
		}
		if a.UnicodeRange != b.UnicodeRange {
			return a.UnicodeRange < b.UnicodeRange
		}
		return a.Hash < b.Hash
	})
	var w strings.Builder
	if fontHashRE.MatchString(preload) {
		for _, f := range ok {
			if f.Hash == preload {
				w.WriteString(`<link rel="preload" href="/fonts/` + preload + `.woff2" as="font" type="font/woff2" crossorigin>`)
				break
			}
		}
	}
	w.WriteString(`<style>`)
	for _, f := range ok {
		w.WriteString(`@font-face{font-family:"` + f.Family + `";font-weight:` + strconv.Itoa(f.Weight) +
			`;font-style:` + f.Style + `;font-display:swap;src:url(/fonts/` + f.Hash + `.woff2) format("woff2")`)
		if f.UnicodeRange != "" {
			w.WriteString(`;unicode-range:` + f.UnicodeRange)
		}
		w.WriteString(`}`)
	}
	w.WriteString(`</style>`)
	return template.HTML(w.String()) // #nosec G203 -- every field matched an allow-list pattern above
}
