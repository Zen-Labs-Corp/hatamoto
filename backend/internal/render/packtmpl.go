package render

import (
	"fmt"
	"strings"
	"text/template/parse"
)

// This file judges where a pack template's actions sit in its markup. The
// template text is walked in the order a browser would read it, with a small
// HTML tokenizer state, so a validation never depends on which {{if}} branch a
// sample render happens to take.
//
// The rule: an action may print in text content, or inside a quoted attribute
// value of an allowlisted attribute. {{.Attrs}} (the whole action) is allowed
// between attributes. Nothing else may be dynamic: no tag name, attribute name,
// unquoted value, comment, or URL scheme.

type mkKind int

const (
	mkText    mkKind = iota
	mkLt             // after "<"
	mkLtSlash        // after "</"
	mkTagName
	mkInTag // between attributes
	mkAttrName
	mkAfterName
	mkBeforeVal
	mkValQ // inside a quoted value
	mkValU // inside an unquoted value
)

// mkState is the comparable part of the scanner: the state two branches of an
// {{if}} must agree on at their end.
type mkState struct {
	kind mkKind
	q    byte   // quote of mkValQ
	attr string // lowercased attribute of mkValQ
}

// packMixedAttrs may mix literal text and actions in a quoted value: they carry
// no URL, script or style. data-* and aria-* are allowed too.
var packMixedAttrs = map[string]bool{
	"class": true, "title": true, "alt": true, "lang": true, "dir": true, "role": true,
	"width": true, "height": true, "colspan": true, "rowspan": true, "tabindex": true,
	"placeholder": true, "value": true, "type": true, "target": true, "rel": true,
	"loading": true, "decoding": true, "datetime": true, "for": true, "open": true,
	"checked": true, "disabled": true, "selected": true,
}

// packURLAttrs take an action only as the entire quoted value, so html/template
// filters the scheme (javascript:, data: and other schemes become #ZgotmplZ).
var packURLAttrs = map[string]bool{"href": true, "src": true}

func attrMixed(a string) bool {
	return packMixedAttrs[a] || strings.HasPrefix(a, "data-") || strings.HasPrefix(a, "aria-")
}

type tmplScan struct {
	file   string
	tr     *parse.Tree
	st     mkState
	name   strings.Builder
	valLen int  // bytes of the current quoted value seen so far
	acted  bool // the current quoted value is one action already
	failed error
}

func isMkSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }

func (s *tmplScan) fail(n parse.Node, rule, format string, a ...any) {
	if s.failed == nil {
		loc := "template text"
		if n != nil {
			loc, _ = s.tr.ErrorContext(n)
		}
		s.failed = packErr(s.file, rule, "%s: %s", loc, fmt.Sprintf(format, a...))
	}
}

func (s *tmplScan) text(str string) {
	for i := 0; i < len(str); i++ {
		c := str[i]
	again:
		switch s.st.kind {
		case mkText:
			if c == '<' {
				s.st.kind = mkLt
			}
		case mkLt:
			switch {
			case c == '/':
				s.st.kind = mkLtSlash
			case c == '!' || c == '?':
				s.fail(nil, "markup", "<%c constructs (comments, doctype, processing instructions) are not allowed", c)
				return
			case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
				s.st.kind = mkTagName
			default:
				s.st.kind = mkText
				goto again
			}
		case mkLtSlash:
			switch {
			case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
				s.st.kind = mkTagName
			default:
				s.fail(nil, "markup", "</ must be followed by a letter")
				return
			}
		case mkTagName:
			switch {
			case c == '>':
				s.st.kind = mkText
			case isMkSpace(c) || c == '/':
				s.st.kind = mkInTag
			}
		case mkInTag:
			switch {
			case c == '>':
				s.st.kind = mkText
			case isMkSpace(c) || c == '/':
			default:
				s.st.kind = mkAttrName
				s.name.Reset()
				s.name.WriteByte(c)
			}
		case mkAttrName:
			switch {
			case c == '>':
				s.st.kind = mkText
			case c == '=':
				s.st.kind = mkBeforeVal
			case c == '/':
				s.st.kind = mkInTag
			case isMkSpace(c):
				s.st.kind = mkAfterName
			default:
				s.name.WriteByte(c)
			}
		case mkAfterName:
			switch {
			case c == '>':
				s.st.kind = mkText
			case c == '=':
				s.st.kind = mkBeforeVal
			case c == '/':
				s.st.kind = mkInTag
			case isMkSpace(c):
			default:
				s.st.kind = mkAttrName
				s.name.Reset()
				s.name.WriteByte(c)
			}
		case mkBeforeVal:
			switch {
			case c == '"' || c == '\'':
				s.st = mkState{kind: mkValQ, q: c, attr: strings.ToLower(s.name.String())}
				s.valLen, s.acted = 0, false
			case c == '>':
				s.st.kind = mkText
			case isMkSpace(c):
			default:
				s.st.kind = mkValU
			}
		case mkValU:
			switch {
			case c == '>':
				s.st.kind = mkText
			case isMkSpace(c):
				s.st.kind = mkInTag
			}
		case mkValQ:
			switch {
			case c == s.st.q:
				s.st = mkState{kind: mkInTag}
			case s.acted:
				s.fail(nil, "action-context", "attribute %q: an action must be the entire value", s.st.attr)
			default:
				s.valLen++
			}
		}
	}
}

func isAttrsAction(a *parse.ActionNode) bool {
	if a.Pipe == nil || len(a.Pipe.Cmds) != 1 || len(a.Pipe.Decl) != 0 {
		return false
	}
	c := a.Pipe.Cmds[0]
	if len(c.Args) != 1 {
		return false
	}
	f, ok := c.Args[0].(*parse.FieldNode)
	return ok && len(f.Ident) == 1 && f.Ident[0] == "Attrs"
}

var mkNames = map[mkKind]string{
	mkLt: "right after <", mkLtSlash: "right after </",
	mkTagName: "a tag name", mkInTag: "a tag, outside an attribute", mkAttrName: "an attribute name",
	mkAfterName: "an attribute name", mkBeforeVal: "an unquoted attribute value", mkValU: "an unquoted attribute value",
}

func (s *tmplScan) action(n *parse.ActionNode) {
	switch s.st.kind {
	case mkText:
	case mkInTag:
		if !isAttrsAction(n) {
			s.fail(n, "action-context", "an action between attributes would write attribute names; only {{.Attrs}} may")
		}
	case mkValQ:
		a := s.st.attr
		switch {
		case isAttrsAction(n):
			s.fail(n, "action-context", "{{.Attrs}} is only allowed between attributes")
		case attrMixed(a):
			s.valLen++
		case packURLAttrs[a]:
			if s.valLen > 0 || s.acted {
				s.fail(n, "action-context", "attribute %q: an action must be the entire value (no text before the URL)", a)
			}
			s.acted = true
		default:
			s.fail(n, "action-context", "attribute %q may not hold an action; allowed: class, title, alt, aria-*, data-* and similar, and href/src as a whole value", a)
		}
	default:
		s.fail(n, "action-context", "an action inside %s is not allowed", mkNames[s.st.kind])
	}
}

// ctl scans an {{if}}/{{range}}/{{with}}: both bodies start in the current
// state and must end in it, so the state after the node is known.
func (s *tmplScan) ctl(n parse.Node, lists ...*parse.ListNode) {
	k := s.st.kind
	if k != mkText && k != mkInTag && !(k == mkValQ && attrMixed(s.st.attr)) {
		s.fail(n, "action-context", "a control action inside %s is not allowed", mkNames[k]+attrNote(s.st))
		return
	}
	start := s.st
	for _, l := range lists {
		s.st = start
		s.list(l)
		if s.st != start {
			s.fail(n, "action-context", "a branch ends inside a tag or attribute it did not start in; open and close markup in the same branch")
			break
		}
	}
	s.st = start
}

func attrNote(st mkState) string {
	if st.kind == mkValQ {
		return fmt.Sprintf(" (attribute %q)", st.attr)
	}
	return ""
}

func (s *tmplScan) list(l *parse.ListNode) {
	if l == nil {
		return
	}
	for _, n := range l.Nodes {
		if s.failed != nil {
			return
		}
		switch x := n.(type) {
		case *parse.TextNode:
			s.text(string(x.Text))
		case *parse.ActionNode:
			s.action(x)
		case *parse.IfNode:
			s.ctl(x, x.List, x.ElseList)
		case *parse.RangeNode:
			s.ctl(x, x.List, x.ElseList)
		case *parse.WithNode:
			s.ctl(x, x.List, x.ElseList)
		}
	}
}

// checkPackActions walks the parse tree of the (single) template.
func checkPackActions(file string, tr *parse.Tree) error {
	s := &tmplScan{file: file, tr: tr}
	s.list(tr.Root)
	if s.failed == nil && s.st.kind != mkText {
		s.failed = packErr(file, "action-context", "the template ends inside a tag")
	}
	return s.failed
}

// packBranches calls fn with the flattened text of every if/else/range/with
// body in the tree, each on its own, so a body is judged without the help (or
// the damage) of its neighbours.
func packBranches(l *parse.ListNode, repl string, fn func(string) error) error {
	if l == nil {
		return nil
	}
	for _, n := range l.Nodes {
		var lists []*parse.ListNode
		switch x := n.(type) {
		case *parse.IfNode:
			lists = []*parse.ListNode{x.List, x.ElseList}
		case *parse.RangeNode:
			lists = []*parse.ListNode{x.List, x.ElseList}
		case *parse.WithNode:
			lists = []*parse.ListNode{x.List, x.ElseList}
		}
		for _, b := range lists {
			if b == nil {
				continue
			}
			var sb strings.Builder
			flattenPack(b, repl, &sb)
			if err := fn(sb.String()); err != nil {
				return err
			}
			if err := packBranches(b, repl, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// flattenPack joins the text of every branch of the tree. Each action becomes
// repl, so the result shows how the markup tokenizes with the dynamic parts
// removed ("") and with them in place ("x").
func flattenPack(l *parse.ListNode, repl string, b *strings.Builder) {
	if l == nil {
		return
	}
	for _, n := range l.Nodes {
		switch x := n.(type) {
		case *parse.TextNode:
			b.Write(x.Text)
		case *parse.ActionNode:
			b.WriteString(repl)
		case *parse.IfNode:
			flattenPack(x.List, repl, b)
			flattenPack(x.ElseList, repl, b)
		case *parse.RangeNode:
			flattenPack(x.List, repl, b)
			flattenPack(x.ElseList, repl, b)
		case *parse.WithNode:
			flattenPack(x.List, repl, b)
			flattenPack(x.ElseList, repl, b)
		}
	}
}
