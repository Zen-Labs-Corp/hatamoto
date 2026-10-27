package entity

import (
	"strings"

	"golang.org/x/net/html"
)

// plainBlockTags separate words: "<p>a</p><p>b</p>" reads "a b", not "ab".
var plainBlockTags = map[string]bool{
	"p": true, "div": true, "br": true, "hr": true, "li": true, "ul": true, "ol": true, "dl": true, "dt": true, "dd": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "blockquote": true, "pre": true,
	"table": true, "thead": true, "tbody": true, "tr": true, "td": true, "th": true, "figure": true, "figcaption": true,
	"section": true, "article": true, "header": true, "footer": true, "aside": true, "nav": true, "form": true,
}

// PlainText turns stored HTML into plain text for a card excerpt: block
// boundaries become spaces, script and style content is dropped, entities are
// decoded and whitespace is collapsed. It keeps at most n runes. The result is
// only ever rendered escaped.
func PlainText(markup string, n int) string {
	var b strings.Builder
	z := html.NewTokenizer(strings.NewReader(markup))
	skip := 0 // inside script/style
	for b.Len() < 4*n {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if tag == "script" || tag == "style" {
				switch tt {
				case html.StartTagToken:
					skip++
				case html.EndTagToken:
					skip = max(skip-1, 0)
				}
			}
			if plainBlockTags[tag] {
				b.WriteByte(' ')
			}
		}
	}
	return truncateRunes(strings.Join(strings.Fields(b.String()), " "), n)
}

// truncateRunes cuts s to at most n runes.
func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// personDescriptionMax bounds a person's JSON-LD description in runes.
const personDescriptionMax = 300

// PersonDescription is the plain-text description of a person: the short bio,
// else the bio with its markup removed, cut to 300 runes.
func PersonDescription(shortBio, bio string) string {
	if s := strings.TrimSpace(shortBio); s != "" {
		return truncateRunes(s, personDescriptionMax)
	}
	return PlainText(bio, personDescriptionMax)
}
