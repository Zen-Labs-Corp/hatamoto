package searchreplace

import (
	"html"
	"io"
	"strings"

	xhtml "golang.org/x/net/html"
)

// transform numbers the occurrences of o.Find in s in document order.
// With pick == nil it only collects hits. Otherwise it replaces the
// occurrences pick accepts and returns the new string and how many were
// replaced; s is returned as is when nothing was.
//
// isHTML says the field holds markup (a rich-text body, a builder html prop).
// Such a value is edited token by token: only text and href/src values can
// change, raw-text bodies and every other byte stay as they were. Any other
// value is plain text and is never parsed or escaped, whatever it contains.
func transform(s string, o Options, isHTML bool, pick func(i int) bool) (out string, hits []Hit, replaced int) {
	if o.Find == "" {
		return s, nil, 0
	}
	sc := &scanner{o: o, pick: pick}
	if isHTML {
		out = sc.markup(s)
	} else {
		out = sc.text(s)
	}
	if sc.replaced == 0 {
		out = s
	}
	return out, sc.hits, sc.replaced
}

// scanner carries the occurrence counter across the pieces of one value.
type scanner struct {
	o        Options
	pick     func(i int) bool
	n        int
	hits     []Hit
	replaced int
}

// text scans one run of plain text and returns it with the picked matches
// replaced.
func (sc *scanner) text(s string) string {
	rs := []rune(s)
	spans := sc.o.findAll(rs)
	if len(spans) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, sp := range spans {
		i := sc.n
		sc.n++
		sc.hits = append(sc.hits, Hit{Index: i, Context: snippet(rs, sp)})
		if sc.pick == nil || !sc.pick(i) {
			continue
		}
		b.WriteString(string(rs[last:sp.start]))
		b.WriteString(sc.o.Replace)
		last = sp.end
		sc.replaced++
	}
	b.WriteString(string(rs[last:]))
	return b.String()
}

// markup edits the text nodes and href/src values of an HTML fragment.
func (sc *scanner) markup(s string) string {
	z := xhtml.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	rawText := false // inside script, style or another raw-text element
	consumed := 0    // input bytes read by the tokens written so far
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			if z.Err() != io.EOF {
				sc.hits, sc.replaced = nil, 0
				return s
			}
			// An unfinished tag at the end ends the tokenizer without a
			// token: keep those bytes as they are.
			return b.String() + s[consumed:]
		}
		raw := string(z.Raw())
		consumed += len(raw)
		switch tt {
		case xhtml.TextToken:
			if rawText {
				break
			}
			plain := string(z.Text())
			if edited := sc.text(plain); edited != plain {
				raw = html.EscapeString(edited)
			}
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			var name string
			name, raw = sc.tag(z, tt, raw)
			rawText = tt == xhtml.StartTagToken && rawTextTags[name]
		case xhtml.EndTagToken:
			rawText = false
		}
		b.WriteString(raw)
	}
}

// tag returns the tag name and the raw start tag, rebuilt only when an href
// or src value in it was edited.
func (sc *scanner) tag(z *xhtml.Tokenizer, tt xhtml.TokenType, raw string) (string, string) {
	nameBytes, hasAttr := z.TagName()
	name := string(nameBytes)
	var b strings.Builder
	b.WriteString("<" + name)
	changed := false
	for hasAttr {
		var key, val []byte
		key, val, hasAttr = z.TagAttr()
		v := string(val)
		if k := string(key); k == "href" || k == "src" {
			if edited := sc.text(v); edited != v {
				v, changed = edited, true
			}
		}
		b.WriteString(" " + string(key) + `="` + html.EscapeString(v) + `"`)
	}
	if !changed {
		return name, raw
	}
	if tt == xhtml.SelfClosingTagToken {
		b.WriteString("/")
	}
	b.WriteString(">")
	return name, b.String()
}

// rawTextTags are the elements whose body the tokenizer returns as is, not as
// text: it is script, style or markup kept as data, never prose to edit.
var rawTextTags = map[string]bool{
	"script": true, "style": true, "iframe": true, "noscript": true,
	"noembed": true, "noframes": true, "xmp": true, "plaintext": true,
}
