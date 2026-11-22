package llmstxt

import (
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// skipTags are dropped with their content.
var skipTags = map[string]bool{
	"script": true, "style": true, "iframe": true, "form": true, "noscript": true,
	"template": true, "svg": true, "head": true, "object": true, "embed": true,
}

// blockTags start and end a block; everything else is inline.
var blockTags = map[string]bool{
	"p": true, "div": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"ul": true, "ol": true, "li": true, "pre": true, "blockquote": true, "table": true, "thead": true,
	"tbody": true, "tfoot": true, "tr": true, "td": true, "th": true, "hr": true, "section": true,
	"article": true, "header": true, "footer": true, "aside": true, "nav": true, "main": true,
	"html": true, "body": true, "figure": true, "figcaption": true, "dl": true, "dt": true, "dd": true, "details": true, "summary": true,
}

// HTMLToMarkdown converts stored HTML to Markdown: headings, paragraphs, line
// breaks, nested lists, links, images, code, quotes, emphasis and tables (as
// plain rows). Scripts, styles, frames and forms are dropped.
func HTMLToMarkdown(markup string) string {
	root, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(renderBlocks(root, "\n\n"))
}

// renderBlocks renders the children of n as blocks joined by sep.
func renderBlocks(n *html.Node, sep string) string {
	var blocks []string
	var run strings.Builder
	flush := func() {
		if s := tidyInline(run.String()); s != "" {
			blocks = append(blocks, s)
		}
		run.Reset()
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (blockTags[c.Data] || skipTags[c.Data]) {
			flush()
			if s := renderBlock(c); s != "" {
				blocks = append(blocks, s)
			}
			continue
		}
		run.WriteString(inline(c))
	}
	flush()
	return strings.Join(blocks, sep)
}

func renderBlock(n *html.Node) string {
	if skipTags[n.Data] {
		return ""
	}
	switch n.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(n.Data[1] - '0')
		text := strings.Join(strings.Fields(inlineChildren(n)), " ")
		if text == "" {
			return ""
		}
		return strings.Repeat("#", level) + " " + text
	case "ul", "ol":
		return renderList(n)
	case "pre":
		code := strings.Trim(rawText(n), "\n")
		if strings.TrimSpace(code) == "" {
			return ""
		}
		return "```\n" + code + "\n```"
	case "blockquote":
		inner := renderBlocks(n, "\n\n")
		if inner == "" {
			return ""
		}
		lines := strings.Split(inner, "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight("> "+l, " ")
		}
		return strings.Join(lines, "\n")
	case "table":
		return renderTable(n)
	case "hr":
		return ""
	}
	return renderBlocks(n, "\n\n")
}

func renderList(n *html.Node) string {
	var items []string
	num := 1
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Data != "li" {
			continue
		}
		body := renderBlocks(c, "\n")
		if body == "" {
			continue
		}
		prefix := "- "
		if n.Data == "ol" {
			prefix = strconv.Itoa(num) + ". "
		}
		num++
		pad := strings.Repeat(" ", len(prefix))
		lines := strings.Split(body, "\n")
		for i := 1; i < len(lines); i++ {
			if lines[i] != "" {
				lines[i] = pad + lines[i]
			}
		}
		items = append(items, prefix+strings.Join(lines, "\n"))
	}
	return strings.Join(items, "\n")
}

func renderTable(n *html.Node) string {
	var rows []string
	var walk func(*html.Node)
	walk = func(p *html.Node) {
		for c := p.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.Data {
			case "tr":
				var cells []string
				for d := c.FirstChild; d != nil; d = d.NextSibling {
					if d.Type == html.ElementNode && (d.Data == "td" || d.Data == "th") {
						cells = append(cells, strings.Join(strings.Fields(inlineChildren(d)), " "))
					}
				}
				if row := strings.Join(cells, " | "); strings.Trim(row, " |") != "" {
					rows = append(rows, row)
				}
			case "thead", "tbody", "tfoot":
				walk(c)
			}
		}
	}
	walk(n)
	return strings.Join(rows, "\n")
}

// inline renders one node in inline context. A line break is "\n".
func inline(n *html.Node) string {
	switch n.Type {
	case html.TextNode:
		return collapseSpace(n.Data)
	case html.ElementNode:
	default:
		return ""
	}
	if skipTags[n.Data] {
		return ""
	}
	switch n.Data {
	case "br":
		return "\n"
	case "img":
		src, ok := linkDest(attr(n, "src"))
		if !ok {
			return ""
		}
		return imageMarkdown(attr(n, "alt"), src)
	case "a":
		text := strings.TrimSpace(inlineChildren(n))
		if text == "" {
			return ""
		}
		href, ok := linkDest(attr(n, "href"))
		if !ok {
			return text
		}
		return "[" + text + "](" + href + ")"
	case "strong", "b":
		return wrap(inlineChildren(n), "**")
	case "em", "i":
		return wrap(inlineChildren(n), "_")
	case "code":
		return wrap(rawText(n), "`")
	}
	s := inlineChildren(n)
	if blockTags[n.Data] {
		return " " + s + " "
	}
	return s
}

func inlineChildren(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(inline(c))
	}
	return b.String()
}

// wrap puts mark around s, keeping the edge spaces outside.
func wrap(s, mark string) string {
	core := strings.TrimSpace(s)
	if core == "" {
		return s
	}
	lead := s[:len(s)-len(strings.TrimLeft(s, " "))]
	trail := s[len(strings.TrimRight(s, " ")):]
	return lead + mark + core + mark + trail
}

// tidyInline trims every line of an inline run and drops empty lines.
func tidyInline(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func collapseSpace(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' || r == '\f' {
			space = true
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	if space {
		b.WriteByte(' ')
	}
	return b.String()
}

func rawText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(p *html.Node) {
		if p.Type == html.TextNode {
			b.WriteString(p.Data)
		}
		for c := p.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

// unsafeSchemes are never written as a link or image destination.
var unsafeSchemes = map[string]bool{"javascript": true, "vbscript": true, "data": true}

// linkDest makes a raw href or src fit a Markdown link destination. ok is false
// for an empty value or a script-bearing scheme. Browsers ignore whitespace and
// control characters inside a scheme, so they are removed before the check
// ("java\tscript:" is javascript:). In the output, characters that would end or
// split the destination are percent-encoded.
func linkDest(raw string) (dest string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	squeezed := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return -1
		}
		return r
	}, raw)
	if scheme, _, found := strings.Cut(squeezed, ":"); found && unsafeSchemes[strings.ToLower(scheme)] {
		return "", false
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case unicode.IsSpace(r), unicode.IsControl(r), r == '(', r == ')', r == '<', r == '>':
			for _, c := range []byte(string(r)) {
				b.WriteString("%" + strings.ToUpper(strconv.FormatInt(int64(c)+256, 16)[1:]))
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String(), true
}

var bracketEscaper = strings.NewReplacer("[", `\[`, "]", `\]`)

// imageMarkdown writes an image whose alt text cannot close the brackets.
func imageMarkdown(alt, dest string) string {
	alt = bracketEscaper.Replace(strings.Join(strings.Fields(alt), " "))
	return "![" + alt + "](" + dest + ")"
}
