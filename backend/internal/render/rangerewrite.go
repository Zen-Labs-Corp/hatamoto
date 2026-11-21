package render

import (
	"html/template"
	"strconv"
	"text/template/parse"
)

// rangeBudgetVar is the name the rewrite gives the budget variable. The
// template lexer cannot produce a variable with a hyphen, so plugin source can
// neither read nor shadow it: the budget cannot be bypassed from a template.
const rangeBudgetVar = "$hm-budget"

// rangeDecl declares the budget variable and rangeGuard stops a range body
// once the budget is spent. Both come from one static snippet, parsed once at
// package init, so the rewrite itself cannot fail at run time.
var rangeDecl, rangeGuard parse.Node

// rangeRun is the empty {{if $hm-budget.Run}}{{end}} that the rewrite fills with
// the original tree, so a template the breaker paused renders nothing.
var rangeRun *parse.IfNode

func init() {
	const snippet = `{{$hmbudget := ` + rangeBudgetFunc + ` "p" "t"}}` +
		`{{range 1}}{{if $hmbudget.Spend}}{{break}}{{end}}{{end}}` +
		`{{if $hmbudget.Run}}{{end}}`
	funcs := map[string]any{rangeBudgetFunc: func(string, string) *rangeHandle { return nil }}
	trees, err := parse.Parse("hm-range-guard", snippet, "", "", funcs)
	if err != nil {
		panic("render: range guard snippet: " + err.Error())
	}
	root := trees["hm-range-guard"].Root
	decl := root.Nodes[0].(*parse.ActionNode)
	decl.Pipe.Decl[0].Ident[0] = rangeBudgetVar
	guard := root.Nodes[1].(*parse.RangeNode).List.Nodes[0].(*parse.IfNode)
	guard.Pipe.Cmds[0].Args[0].(*parse.VariableNode).Ident[0] = rangeBudgetVar
	run := root.Nodes[2].(*parse.IfNode)
	run.Pipe.Cmds[0].Args[0].(*parse.VariableNode).Ident[0] = rangeBudgetVar
	rangeDecl, rangeGuard, rangeRun = decl, guard, run
}

// guardRanges prepends the guard to the body of every range under l, at any
// depth (inside if, with and range bodies and their else lists), and reports
// whether it saw a range. The range pipeline is never touched, so every
// shape keeps text/template's own iteration semantics, including break,
// continue and {{else}}.
func guardRanges(l *parse.ListNode) bool {
	if l == nil {
		return false
	}
	found := false
	for _, n := range l.Nodes {
		switch n := n.(type) {
		case *parse.RangeNode:
			found = true
			guardRanges(n.List)
			guardRanges(n.ElseList)
			n.List.Nodes = append([]parse.Node{rangeGuard.Copy()}, n.List.Nodes...)
		case *parse.IfNode:
			found = guardRanges(n.List) || found
			found = guardRanges(n.ElseList) || found
		case *parse.WithNode:
			found = guardRanges(n.List) || found
			found = guardRanges(n.ElseList) || found
		}
	}
	return found
}

// rangeRewritten reports whether the root already starts with the budget
// declaration. A later source that redefines a name with an empty body leaves
// the earlier tree in place, so the same tree can come up for a second rewrite,
// and a second guard would halve its budget.
func rangeRewritten(root *parse.ListNode) bool {
	if len(root.Nodes) == 0 {
		return false
	}
	a, ok := root.Nodes[0].(*parse.ActionNode)
	return ok && a.Pipe != nil && len(a.Pipe.Decl) > 0 && a.Pipe.Decl[0].Ident[0] == rangeBudgetVar
}

// rewriteRangeTree guards tree t and, if it holds a range, declares the
// budget variable first in the tree. plugin and name travel as string
// literals so a budget hit can be logged against the right template.
func rewriteRangeTree(t *parse.Tree, plugin, name string) {
	if t == nil || t.Root == nil || rangeRewritten(t.Root) || !guardRanges(t.Root) {
		return
	}
	decl := rangeDecl.Copy().(*parse.ActionNode)
	args := decl.Pipe.Cmds[0].Args
	for i, text := range []string{plugin, name} {
		s := args[i+1].(*parse.StringNode)
		s.Text, s.Quoted = text, strconv.Quote(text)
	}
	run := rangeRun.Copy().(*parse.IfNode)
	run.List.Nodes = t.Root.Nodes
	t.Root.Nodes = []parse.Node{decl, run}
}

// definedNames lists the template names src defines, or nil if it does not parse.
func definedNames(rootName, src string) []string {
	t := parse.New(rootName)
	t.Mode = parse.SkipFuncCheck
	set := map[string]*parse.Tree{}
	if _, err := t.Parse(src, "", "", set); err != nil {
		return nil
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	return names
}

// rewritePluginRanges bounds every range in the templates that the plugin
// source src defined in root. Callers run it right after parsing src and
// before any Clone or Execute, once per source. Templates that are missing or
// have no tree are skipped.
func rewritePluginRanges(root *template.Template, rootName, src, plugin string) {
	for _, n := range definedNames(rootName, src) {
		if t := root.Lookup(n); t != nil {
			rewriteRangeTree(t.Tree, plugin, n)
		}
	}
}
