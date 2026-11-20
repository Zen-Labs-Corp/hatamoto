package wpimport

import (
	"bytes"
	"regexp"
	"strings"
	"sync"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var manyNewlinesRe = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`\n{3,}`) })

// commentHTMLToText turns comment HTML as WordPress stores it into the plain
// comment format commentfmt.Render reads: paragraphs, **bold**, *italic*,
// `code`, "> " quote lines. A link keeps its URL as text ("label (https://x)")
// so Render autolinks it and sets HasLinks. Every other tag is dropped and
// entities are decoded. The result is never HTML.
func commentHTMLToText(s string) string {
	var (
		bufs   = []*bytes.Buffer{{}} // one per open blockquote
		open   = map[string]int{}    // open **, *, ` markers
		skip   int                   // depth inside script, style, iframe
		hrefs  []string              // open links: href, "" when not http(s)
		starts []int                 // open links: start offset in the current buffer
		cur    = func() *bytes.Buffer { return bufs[len(bufs)-1] }
		para   = func() { cur().WriteString("\n\n") }
		marker = func(m string, on bool) {
			switch {
			case on:
				open[m]++
				cur().WriteString(m)
			case open[m] > 0:
				open[m]--
				cur().WriteString(m)
			}
		}
		markers = map[atom.Atom]string{atom.B: "**", atom.Strong: "**", atom.I: "*", atom.Em: "*", atom.Code: "`"}
	)
	z := xhtml.NewTokenizer(strings.NewReader(s))
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			break
		}
		switch tt {
		case xhtml.TextToken:
			if skip == 0 {
				cur().Write(z.Text())
			}
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			a := atom.Lookup(name)
			if a == atom.Script || a == atom.Style || a == atom.Iframe {
				if tt == xhtml.StartTagToken {
					skip++
				}
				continue
			}
			if skip > 0 {
				continue
			}
			switch a {
			case atom.Br:
				cur().WriteByte('\n')
			case atom.P, atom.Div, atom.Ul, atom.Ol, atom.Li, atom.Pre, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
				para()
			case atom.Blockquote:
				para()
				bufs = append(bufs, &bytes.Buffer{})
			case atom.A:
				href := ""
				for hasAttr {
					var k, v []byte
					k, v, hasAttr = z.TagAttr()
					if string(k) == "href" {
						href = strings.TrimSpace(string(v))
					}
				}
				if l := strings.ToLower(href); !strings.HasPrefix(l, "http://") && !strings.HasPrefix(l, "https://") {
					href = ""
				}
				hrefs = append(hrefs, href)
				starts = append(starts, cur().Len())
			default:
				if m, ok := markers[a]; ok && tt == xhtml.StartTagToken {
					marker(m, true)
				}
			}
		case xhtml.EndTagToken:
			name, _ := z.TagName()
			a := atom.Lookup(name)
			if a == atom.Script || a == atom.Style || a == atom.Iframe {
				skip = max(skip-1, 0)
				continue
			}
			if skip > 0 {
				continue
			}
			switch a {
			case atom.P, atom.Div, atom.Ul, atom.Ol, atom.Li, atom.Pre, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
				para()
			case atom.Blockquote:
				if len(bufs) > 1 {
					q := tidyComment(bufs[len(bufs)-1].String())
					bufs = bufs[:len(bufs)-1]
					para()
					cur().WriteString(quoteLines(q))
					para()
				}
			case atom.A:
				if len(hrefs) == 0 {
					continue
				}
				href, start := hrefs[len(hrefs)-1], starts[len(starts)-1]
				hrefs, starts = hrefs[:len(hrefs)-1], starts[:len(starts)-1]
				if href == "" || start > cur().Len() {
					continue
				}
				if label := strings.TrimSpace(string(cur().Bytes()[start:])); label == href {
					continue
				} else if label == "" {
					cur().WriteString(href)
				} else {
					cur().WriteString(" (" + href + ")")
				}
			default:
				if m, ok := markers[a]; ok {
					marker(m, false)
				}
			}
		}
	}
	for len(bufs) > 1 { // unclosed blockquote
		q := tidyComment(bufs[len(bufs)-1].String())
		bufs = bufs[:len(bufs)-1]
		para()
		cur().WriteString(quoteLines(q))
	}
	for m, n := range open { // unclosed formatting
		cur().WriteString(strings.Repeat(m, n))
	}
	return tidyComment(cur().String())
}

// quoteLines prefixes every line with "> " (">" for blank ones, so the quote
// stays one block for Render).
func quoteLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight("> "+l, " ")
	}
	return strings.Join(lines, "\n")
}

// tidyComment normalises newlines, de-indents lines (Render would turn four
// spaces or a tab into a code block), collapses blank runs and trims.
func tidyComment(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		l = strings.ReplaceAll(l, "\u00a0", " ")
		if strings.HasPrefix(l, "    ") || strings.HasPrefix(l, "\t") {
			l = strings.TrimLeft(l, " \t")
		}
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(manyNewlinesRe().ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}
