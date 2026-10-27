package render

import (
	"fmt"
	"strconv"
	"strings"
	"text/template/parse"

	"golang.org/x/net/html"
)

// checkPluginActions is the structural rule for a plugin's sections and
// partials without site.scripts. A template action that prints may not sit
// where a tag name, an attribute name or an unquoted value is parsed:
// html/template escapes an action in text and in a quoted value, but elsewhere
// it writes the name itself, and the markup scans cannot see what a template
// prints from data (<scr{{.Content.t}}>, <p {{.Content.a}}>).
//
//   - a printing action may stand in text (outside raw-text elements such as
//     <style>) or entirely inside a quoted attribute value;
//   - if/range/with/break/continue and {{template}} may stand in text only;
//   - every branch (the template body and each if/else/range/with body) starts
//     and ends in text, and is judged on its own.
//
// Where a tag begins and ends is not modelled here: each branch gets an inert
// token in place of every action and is run through the HTML tokenizer, which
// follows the HTML specification for comments, raw-text elements, doctype and
// quoting. Widget packs are judged by the stricter scanner in packtmpl.go.
func checkPluginActions(what string, tr *parse.Tree) error {
	if tr == nil || tr.Root == nil {
		return nil
	}
	c := &markupCheck{prefix: "hmact"}
	for c.usedIn(tr.Root) {
		c.prefix += "z"
	}
	if err := c.branch(tr.Root); err != nil {
		return fmt.Errorf("markup: action-context: %w (%s)", err, what)
	}
	return nil
}

type actKind int

const (
	actPrint actKind = iota
	actControl
	actTemplate
)

var actNames = [...]string{"an action", "a control action", "a template call"}

type markupCheck struct {
	prefix string // lower-case letters: survives the tokenizer's lower-casing of names
	kinds  []actKind
}

// usedIn reports whether the placeholder prefix occurs in the literal text.
func (c *markupCheck) usedIn(l *parse.ListNode) bool {
	if l == nil {
		return false
	}
	for _, n := range l.Nodes {
		switch x := n.(type) {
		case *parse.TextNode:
			if strings.Contains(strings.ToLower(string(x.Text)), c.prefix) {
				return true
			}
		case *parse.IfNode:
			if c.usedIn(x.List) || c.usedIn(x.ElseList) {
				return true
			}
		case *parse.RangeNode:
			if c.usedIn(x.List) || c.usedIn(x.ElseList) {
				return true
			}
		case *parse.WithNode:
			if c.usedIn(x.List) || c.usedIn(x.ElseList) {
				return true
			}
		}
	}
	return false
}

func (c *markupCheck) placeholder(k actKind) string {
	c.kinds = append(c.kinds, k)
	return c.prefix + fmt.Sprintf("%04d", len(c.kinds)-1) + "x"
}

// branch judges one list, then each body nested in it on its own. A nested
// control structure is a single token in its parent.
func (c *markupCheck) branch(l *parse.ListNode) error {
	if l == nil {
		return nil
	}
	c.kinds = c.kinds[:0]
	var b strings.Builder
	var bodies []*parse.ListNode
	for _, n := range l.Nodes {
		switch x := n.(type) {
		case *parse.TextNode:
			b.Write(x.Text)
		case *parse.ActionNode:
			b.WriteString(c.placeholder(actPrint))
		case *parse.TemplateNode:
			b.WriteString(c.placeholder(actTemplate))
		case *parse.IfNode:
			b.WriteString(c.placeholder(actControl))
			bodies = append(bodies, x.List, x.ElseList)
		case *parse.RangeNode:
			b.WriteString(c.placeholder(actControl))
			bodies = append(bodies, x.List, x.ElseList)
		case *parse.WithNode:
			b.WriteString(c.placeholder(actControl))
			bodies = append(bodies, x.List, x.ElseList)
		case *parse.BreakNode, *parse.ContinueNode:
			b.WriteString(c.placeholder(actControl))
		}
	}
	if err := c.tokens(b.String()); err != nil {
		return err
	}
	for _, body := range bodies {
		if err := c.branch(body); err != nil {
			return err
		}
	}
	return nil
}

// rawTextTags are the elements whose content the tokenizer returns as one raw
// text token: nothing in them is markup, and nothing dynamic may be in them.
var rawTextTags = map[string]bool{
	"script": true, "style": true, "textarea": true, "title": true, "xmp": true,
	"iframe": true, "noembed": true, "noframes": true, "noscript": true, "plaintext": true,
}

func (c *markupCheck) tokens(src string) error {
	z := html.NewTokenizer(strings.NewReader(src))
	raw := false // inside a raw-text element: its content is one text token
	for {
		tt := z.Next()
		tok := string(z.Raw())
		switch tt {
		case html.ErrorToken:
			switch {
			case tok != "":
				return fmt.Errorf("a branch ends inside a tag; open and close markup in the same branch")
			case raw:
				return fmt.Errorf("a branch ends inside a raw-text element (<style>, <textarea>, ...); close it in the same branch")
			}
			return nil
		case html.TextToken:
			if raw {
				if err := c.refuseAny(tok, "inside a raw-text element (<style>, <textarea>, ...)"); err != nil {
					return err
				}
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			raw = rawTextTags[string(name)]
			if err := c.tag(tok); err != nil {
				return err
			}
		case html.EndTagToken:
			raw = false
			if err := c.refuseAny(tok, "inside an end tag"); err != nil {
				return err
			}
		case html.CommentToken, html.DoctypeToken:
			if !closed(tok) {
				return fmt.Errorf("a branch ends inside a comment; close it in the same branch")
			}
			if err := c.refuseAny(tok, "inside a comment or doctype"); err != nil {
				return err
			}
		}
	}
}

// closed reports whether a comment or doctype token ran to its end rather than
// to the end of the branch.
func closed(tok string) bool {
	if strings.HasPrefix(tok, "<!--") {
		return strings.HasSuffix(tok, "-->") || strings.HasSuffix(tok, "--!>")
	}
	return strings.HasSuffix(tok, ">")
}

func (c *markupCheck) refuseAny(tok, where string) error {
	if occ := c.found(tok); len(occ) > 0 {
		return fmt.Errorf("%s %s is not allowed without site.scripts: actions belong in text content or in a quoted attribute value", actNames[occ[0].kind], where)
	}
	return nil
}

// tag judges a start tag: only a printing action lying entirely between the
// quotes of a quoted attribute value may be in it.
func (c *markupCheck) tag(tok string) error {
	spans := quotedValues(tok)
	for _, o := range c.found(tok) {
		in := false
		for _, sp := range spans {
			in = in || (o.pos >= sp[0] && o.end <= sp[1])
		}
		if o.kind != actPrint {
			return fmt.Errorf("%s inside a tag is not allowed without site.scripts: control actions and template calls belong in text content", actNames[o.kind])
		}
		if !in {
			return fmt.Errorf("an action inside a tag name, an attribute name or an unquoted value is not allowed without site.scripts: outside text it may only sit inside a quoted attribute value")
		}
	}
	return nil
}

func isWS(c byte) bool {
	return c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == '\f'
}

// quotedValues returns the [start, end) byte ranges of the quoted attribute
// values of a complete start tag token. It follows the tokenizer's own reading
// of a tag (readTag, readTagAttrKey, readTagAttrVal in x/net/html), over a token
// whose extent the tokenizer has already fixed.
func quotedValues(tok string) [][2]int {
	var out [][2]int
	i, n := 1, len(tok)
	skip := func() {
		for i < n && isWS(tok[i]) {
			i++
		}
	}
	for i < n && !isWS(tok[i]) && tok[i] != '/' && tok[i] != '>' { // tag name
		i++
	}
	skip()
	for i < n && tok[i] != '>' {
		// attribute name: a leading '=' belongs to it
		start := i
		for i < n {
			c := tok[i]
			if c == '=' && i == start {
				i++
				continue
			}
			if c == '=' || isWS(c) || c == '/' || c == '>' {
				break
			}
			i++
		}
		skip()
		switch {
		case i < n && tok[i] == '/':
			i++
		case i < n && tok[i] == '=':
			i++
			skip()
			switch {
			case i >= n || tok[i] == '>':
			case tok[i] == '"' || tok[i] == '\'':
				q := tok[i]
				j := strings.IndexByte(tok[i+1:], q)
				if j < 0 {
					return out
				}
				out = append(out, [2]int{i + 1, i + 1 + j})
				i += j + 2
			default: // unquoted
				for i < n && !isWS(tok[i]) && tok[i] != '>' {
					i++
				}
			}
		}
		skip()
	}
	return out
}

type occurrence struct {
	pos, end int
	kind     actKind
}

// found lists the placeholders in s, in order.
func (c *markupCheck) found(s string) []occurrence {
	var out []occurrence
	for off := 0; ; {
		i := strings.Index(s[off:], c.prefix)
		if i < 0 {
			return out
		}
		start := off + i
		d := start + len(c.prefix)
		j := strings.IndexByte(s[d:], 'x')
		if j < 0 {
			return out
		}
		if k, err := strconv.Atoi(s[d : d+j]); err == nil && k < len(c.kinds) {
			out = append(out, occurrence{start, d + j + 1, c.kinds[k]})
		}
		off = d + j + 1
	}
}
