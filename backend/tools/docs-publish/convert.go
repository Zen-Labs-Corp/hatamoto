package main

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// Convert renders the documentation markdown subset to HTML that only holds
// tags and attributes the server's rich-text policy keeps, so nothing is
// stripped after the save. linkFor maps a relative .md link
// ("../themes/layout.md#x") to a site path ("/docs/themes/layout#x"); nil
// leaves such links alone.
//
// Supported: ##, ### and #### headings, paragraphs, bullet and numbered lists
// (nested by indentation), fenced code, inline code, bold, italic, links,
// images, blockquotes, pipe tables and rules. Anything else (raw HTML, h1,
// indented code, unknown link schemes) is an error
// that names the line within the body; nothing is passed through.
func Convert(md string, linkFor func(href string) (string, error)) (string, error) {
	c := &converter{linkFor: linkFor}
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var b strings.Builder
	if err := c.blocks(&b, lines, 0); err != nil {
		return "", err
	}
	return b.String(), nil
}

type converter struct {
	linkFor func(string) (string, error)
}

var (
	headingRe   = regexp.MustCompile(`^(#{1,6})[ \t]+(.*\S)[ \t]*$`)
	ruleRe      = regexp.MustCompile(`^(-{3,}|\*{3,}|_{3,})[ \t]*$`)
	listRe      = regexp.MustCompile(`^( *)([-*+]|\d{1,9}\.)[ \t]+(.*)$`)
	fenceOpenRe = regexp.MustCompile("^(```|~~~)[ \t]*([A-Za-z0-9_+.-]*)[ \t]*$")
	tableSepRe  = regexp.MustCompile(`^\|?[ \t]*:?-+:?[ \t]*(\|[ \t]*:?-+:?[ \t]*)*\|?[ \t]*$`)
	rawHTMLRe   = regexp.MustCompile(`<[A-Za-z/!?]`)
	schemeRe    = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.-]*):`)
)

func lineErr(n int, format string, a ...any) error {
	return fmt.Errorf("line %d: %s", n, fmt.Sprintf(format, a...))
}

// blocks converts lines; base is the number of body lines before lines[0].
func (c *converter) blocks(b *strings.Builder, lines []string, base int) error {
	for i := 0; i < len(lines); {
		line := lines[i]
		n := base + i + 1
		trim := strings.TrimSpace(line)
		switch {
		case trim == "":
			i++
		case fenceOpenRe.MatchString(trim) && !strings.HasPrefix(line, " "):
			next, err := c.fence(b, lines, i, base)
			if err != nil {
				return err
			}
			i = next
		case strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~"):
			return lineErr(n, "unsupported code fence %q: use a plain fence with an optional language word", trim)
		case strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "    ") && !listRe.MatchString(line):
			return lineErr(n, "indented code blocks are not supported, use a fenced block")
		case headingRe.MatchString(line):
			m := headingRe.FindStringSubmatch(line)
			if err := c.heading(b, len(m[1]), m[2], n); err != nil {
				return err
			}
			i++
		case ruleRe.MatchString(trim):
			b.WriteString("<hr>\n")
			i++
		case strings.HasPrefix(trim, ">"):
			next, err := c.quote(b, lines, i, base)
			if err != nil {
				return err
			}
			i = next
		case strings.HasPrefix(trim, "|") && i+1 < len(lines) && tableSepRe.MatchString(strings.TrimSpace(lines[i+1])) && strings.Contains(lines[i+1], "-"):
			next, err := c.table(b, lines, i, base)
			if err != nil {
				return err
			}
			i = next
		case listRe.MatchString(line):
			next, err := c.list(b, lines, i, base, len(listRe.FindStringSubmatch(line)[1]))
			if err != nil {
				return err
			}
			i = next
		default:
			next, err := c.paragraph(b, lines, i, base)
			if err != nil {
				return err
			}
			i = next
		}
	}
	return nil
}

func (c *converter) heading(b *strings.Builder, level int, text string, n int) error {
	if level == 1 {
		return lineErr(n, "h1 is not allowed in the body; the title comes from front matter, start at ##")
	}
	if level > 4 {
		return lineErr(n, "headings deeper than #### are not supported")
	}
	s, err := c.inline(text, n)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "<h%d>%s</h%d>\n", level, s, level)
	return nil
}

func (c *converter) fence(b *strings.Builder, lines []string, start, base int) (int, error) {
	open := strings.TrimSpace(lines[start])
	m := fenceOpenRe.FindStringSubmatch(open)
	marker, lang := m[1], m[2]
	for j := start + 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == marker {
			if lang != "" {
				fmt.Fprintf(b, "<pre><code class=\"language-%s\">", html.EscapeString(lang))
			} else {
				b.WriteString("<pre><code>")
			}
			b.WriteString(html.EscapeString(strings.Join(lines[start+1:j], "\n")))
			b.WriteString("</code></pre>\n")
			return j + 1, nil
		}
	}
	return 0, lineErr(base+start+1, "code fence is never closed")
}

func (c *converter) quote(b *strings.Builder, lines []string, start, base int) (int, error) {
	var inner []string
	j := start
	for ; j < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[j]), ">"); j++ {
		t := strings.TrimPrefix(strings.TrimSpace(lines[j]), ">")
		inner = append(inner, strings.TrimPrefix(t, " "))
	}
	b.WriteString("<blockquote>\n")
	if err := c.blocks(b, inner, base+start); err != nil {
		return 0, err
	}
	b.WriteString("</blockquote>\n")
	return j, nil
}

func (c *converter) paragraph(b *strings.Builder, lines []string, start, base int) (int, error) {
	j := start
	var parts []string
	for ; j < len(lines); j++ {
		l := lines[j]
		t := strings.TrimSpace(l)
		if t == "" || j > start && startsBlock(lines, j) {
			break
		}
		parts = append(parts, t)
	}
	s, err := c.inline(strings.Join(parts, "\n"), base+start+1)
	if err != nil {
		return 0, err
	}
	fmt.Fprintf(b, "<p>%s</p>\n", s)
	return j, nil
}

// startsBlock reports whether lines[j] opens a block that ends a paragraph.
func startsBlock(lines []string, j int) bool {
	l := lines[j]
	t := strings.TrimSpace(l)
	return fenceOpenRe.MatchString(t) || headingRe.MatchString(l) || ruleRe.MatchString(t) ||
		strings.HasPrefix(t, ">") || listRe.MatchString(l)
}

// dedent removes up to col leading spaces from every line.
func dedent(lines []string, col int) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		k := 0
		for k < col && k < len(l) && l[k] == ' ' {
			k++
		}
		out[i] = l[k:]
	}
	return out
}

// list renders the list whose first marker is at lines[start] with the given
// indent and returns the index of the first line after it. Each item's body is
// every following line indented deeper than the marker (plus the blank lines
// between them), dedented to the item's content column and rendered with the
// normal block renderer, so any block can appear inside an item.
func (c *converter) list(b *strings.Builder, lines []string, start, base, indent int) (int, error) {
	first := listRe.FindStringSubmatch(lines[start])
	ordered := first[2][0] >= '0' && first[2][0] <= '9'
	tag := "ul"
	if ordered {
		tag = "ol"
	}
	fmt.Fprintf(b, "<%s>\n", tag)
	j := start
	for j < len(lines) {
		l := lines[j]
		n := base + j + 1
		if strings.HasPrefix(l, "\t") {
			return 0, lineErr(n, "tabs are not supported in lists, indent with spaces")
		}
		m := listRe.FindStringSubmatch(l)
		if m == nil || len(m[1]) != indent {
			break
		}
		if (m[2][0] >= '0' && m[2][0] <= '9') != ordered {
			break
		}
		col := len(m[0]) - len(m[3])
		body := []string{m[3]}
		end := j + 1
		for k := j + 1; k < len(lines); k++ {
			t := lines[k]
			if strings.TrimSpace(t) == "" {
				body = append(body, "")
				continue
			}
			if strings.HasPrefix(t, "\t") {
				return 0, lineErr(base+k+1, "tabs are not supported in lists, indent with spaces")
			}
			if len(t)-len(strings.TrimLeft(t, " ")) <= indent {
				break
			}
			if ind := len(t) - len(strings.TrimLeft(t, " ")); ind < col && fenceOpenRe.MatchString(strings.TrimSpace(t)) {
				return 0, lineErr(base+k+1, "a code block or table in a list item must be indented to the item text")
			}
			body = append(body, t)
			end = k + 1
		}
		body = append(body[:1], dedent(body[1:end-j], col)...)
		var inner strings.Builder
		if err := c.blocks(&inner, body, base+j); err != nil {
			return 0, err
		}
		out := strings.TrimRight(inner.String(), "\n")
		if strings.HasSuffix(out, "</ul>") || strings.HasSuffix(out, "</ol>") {
			out += "\n"
		}
		if m[3] != "" && strings.HasPrefix(out, "<p>") {
			out = strings.TrimPrefix(out, "<p>")
			out = strings.Replace(out, "</p>", "", 1)
		}
		b.WriteString("<li>" + out + "</li>\n")
		j = end
		for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
			j++
		}
		if j < len(lines) && listRe.MatchString(lines[j]) && len(listRe.FindStringSubmatch(lines[j])[1]) == indent {
			continue
		}
		j = end
		break
	}
	fmt.Fprintf(b, "</%s>\n", tag)
	return j, nil
}

// splitRow splits a pipe table row into trimmed cells.
func splitRow(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	row = strings.TrimSuffix(row, "|")
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(row); i++ {
		switch {
		case row[i] == '\\' && i+1 < len(row) && row[i+1] == '|':
			cur.WriteByte('|')
			i++
		case row[i] == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(row[i])
		}
	}
	return append(cells, strings.TrimSpace(cur.String()))
}

func (c *converter) table(b *strings.Builder, lines []string, start, base int) (int, error) {
	head := splitRow(lines[start])
	if len(splitRow(lines[start+1])) != len(head) {
		return 0, lineErr(base+start+2, "table separator has a different column count than the header")
	}
	row := func(tag string, cells []string, n int) error {
		b.WriteString("<tr>")
		for _, cell := range cells {
			s, err := c.inline(cell, n)
			if err != nil {
				return err
			}
			fmt.Fprintf(b, "<%s>%s</%s>", tag, s, tag)
		}
		b.WriteString("</tr>\n")
		return nil
	}
	b.WriteString("<table>\n<thead>\n")
	if err := row("th", head, base+start+1); err != nil {
		return 0, err
	}
	b.WriteString("</thead>\n<tbody>\n")
	j := start + 2
	for ; j < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[j]), "|"); j++ {
		cells := splitRow(lines[j])
		if len(cells) != len(head) {
			return 0, lineErr(base+j+1, "table row has %d cells, header has %d", len(cells), len(head))
		}
		if err := row("td", cells, base+j+1); err != nil {
			return 0, err
		}
	}
	b.WriteString("</tbody>\n</table>\n")
	return j, nil
}

// inline renders code spans, emphasis, links and images and escapes the rest.
func (c *converter) inline(s string, n int) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		ch := s[i]
		switch {
		case ch == '\\' && i+1 < len(s) && strings.IndexByte("\\`*_[]()#+-.!|<>{}", s[i+1]) >= 0:
			b.WriteString(html.EscapeString(string(s[i+1])))
			i += 2
		case ch == '`':
			end := strings.IndexByte(s[i+1:], '`')
			if end < 0 {
				return "", lineErr(n, "unclosed inline code span")
			}
			code := s[i+1 : i+1+end]
			b.WriteString("<code>" + html.EscapeString(code) + "</code>")
			i += end + 2
		case ch == '<':
			if i+1 < len(s) && rawHTMLRe.MatchString(s[i:i+2]) {
				return "", lineErr(n, "raw HTML is not allowed (%q)", clip(s[i:]))
			}
			b.WriteString("&lt;")
			i++
		case ch == '!' && strings.HasPrefix(s[i:], "!["):
			alt, dest, next, ok := linkParts(s, i+1)
			if !ok {
				b.WriteString("!")
				i++
				continue
			}
			src, err := c.imageSrc(dest, n)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, `<img src="%s" alt="%s">`, html.EscapeString(src), html.EscapeString(plain(alt)))
			i = next
		case ch == '[':
			label, dest, next, ok := linkParts(s, i)
			if !ok {
				b.WriteString("[")
				i++
				continue
			}
			href, title, err := c.linkHref(dest, n)
			if err != nil {
				return "", err
			}
			inner, err := c.inline(label, n)
			if err != nil {
				return "", err
			}
			t := ""
			if title != "" {
				t = ` title="` + html.EscapeString(title) + `"`
			}
			fmt.Fprintf(&b, `<a href="%s"%s>%s</a>`, html.EscapeString(href), t, inner)
			i = next
		case ch == '*' || ch == '_':
			if out, next, ok, err := c.emphasis(s, i, n); err != nil {
				return "", err
			} else if ok {
				b.WriteString(out)
				i = next
				continue
			}
			b.WriteByte(ch)
			i++
		case ch == '\n':
			b.WriteString("\n")
			i++
		default:
			b.WriteString(html.EscapeString(string(ch)))
			i++
		}
	}
	return b.String(), nil
}

// emphasis handles **bold**, *italic* and _italic_ starting at s[i]. Underscores
// only count at word edges so snake_case names stay literal.
func (c *converter) emphasis(s string, i, n int) (string, int, bool, error) {
	ch := s[i]
	width, tag := 1, "em"
	if ch == '*' && strings.HasPrefix(s[i:], "**") {
		width, tag = 2, "strong"
	}
	if ch == '_' && i > 0 && isWord(s[i-1]) {
		return "", 0, false, nil
	}
	delim := strings.Repeat(string(ch), width)
	rest := s[i+width:]
	if rest == "" || rest[0] == ' ' || rest[0] == '\n' {
		return "", 0, false, nil
	}
	for from := 0; ; {
		k := strings.Index(rest[from:], delim)
		if k < 0 {
			return "", 0, false, nil
		}
		k += from
		after := k + width
		okEdge := k > 0 && rest[k-1] != ' ' && rest[k-1] != '\n'
		if ch == '_' && after < len(rest) && isWord(rest[after]) {
			okEdge = false
		}
		if width == 1 && ch == '*' && after < len(rest) && rest[after] == '*' {
			okEdge = false
		}
		if okEdge {
			inner, err := c.inline(rest[:k], n)
			if err != nil {
				return "", 0, false, err
			}
			return "<" + tag + ">" + inner + "</" + tag + ">", i + width + after, true, nil
		}
		from = k + 1
	}
}

func isWord(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// linkParts parses "[label](dest)" at s[i] ('['). Brackets nest one level.
func linkParts(s string, i int) (label, dest string, next int, ok bool) {
	depth := 0
	j := i
	for ; j < len(s); j++ {
		if s[j] == '\\' {
			j++
			continue
		}
		if s[j] == '`' {
			if e := strings.IndexByte(s[j+1:], '`'); e >= 0 {
				j += e + 1
			}
			continue
		}
		if s[j] == '[' {
			depth++
		}
		if s[j] == ']' {
			depth--
			if depth == 0 {
				break
			}
		}
	}
	if j >= len(s) || j+1 >= len(s) || s[j+1] != '(' {
		return "", "", 0, false
	}
	end := strings.IndexByte(s[j+2:], ')')
	if end < 0 {
		return "", "", 0, false
	}
	return s[i+1 : j], s[j+2 : j+2+end], j + 2 + end + 1, true
}

// linkHref validates a link destination ("url" or `url "title"`).
func (c *converter) linkHref(dest string, n int) (href, title string, err error) {
	dest = strings.TrimSpace(dest)
	if k := strings.Index(dest, ` "`); k >= 0 && strings.HasSuffix(dest, `"`) {
		title = dest[k+2 : len(dest)-1]
		dest = strings.TrimSpace(dest[:k])
	}
	switch {
	case dest == "":
		return "", "", lineErr(n, "empty link destination")
	case strings.ContainsAny(dest, " \t\n"):
		return "", "", lineErr(n, "link destination %q contains whitespace", dest)
	}
	if m := schemeRe.FindStringSubmatch(dest); m != nil {
		switch strings.ToLower(m[1]) {
		case "https", "http", "mailto":
			return dest, title, nil
		}
		return "", "", lineErr(n, "link scheme %q is not allowed", m[1])
	}
	if strings.HasPrefix(dest, "#") || strings.HasPrefix(dest, "/") {
		return dest, title, nil
	}
	if strings.Contains(strings.SplitN(dest, "#", 2)[0], ".md") && c.linkFor != nil {
		out, err := c.linkFor(dest)
		if err != nil {
			return "", "", lineErr(n, "link %q: %v", dest, err)
		}
		return out, title, nil
	}
	return dest, title, nil
}

func (c *converter) imageSrc(dest string, n int) (string, error) {
	dest = strings.TrimSpace(dest)
	if m := schemeRe.FindStringSubmatch(dest); m != nil && !strings.EqualFold(m[1], "https") {
		return "", lineErr(n, "image scheme %q is not allowed, use https or a relative path", m[1])
	}
	if dest == "" || strings.ContainsAny(dest, " \t\n\"") {
		return "", lineErr(n, "invalid image source %q", dest)
	}
	return dest, nil
}

// plain strips markdown emphasis characters from alt text.
func plain(s string) string {
	return strings.NewReplacer("*", "", "`", "", "\\", "").Replace(s)
}

func clip(s string) string {
	if len(s) > 20 {
		return s[:20]
	}
	return s
}
