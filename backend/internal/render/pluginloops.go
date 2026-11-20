package render

import (
	"fmt"
	"sort"
	"strings"
	"text/template/parse"
)

// Bounds on what one plugin's templates may do together. A template language
// with calls can run for ever with no range at all (a template that calls
// itself, or one that calls two others that each call two more), so the call
// graph of a plugin is checked as a whole: partials and sections are
// registered one by one, but they call each other.
const (
	maxPluginCalls      = 1000 // expanded {{template}} invocations per template
	maxPluginRangeDepth = 2    // re-entering ranges, also across {{template}} calls
)

// These limits are guards against accidental recursion, fan-out and nesting.
// They are not a bound on what a template can cost: template text is trusted
// for CPU and memory like theme markup.

// pluginCall is one {{template "name"}} in a tree and the number of ranges
// that enclose it.
type pluginCall struct {
	name  string
	depth int
}

// pluginTree is what the call graph needs of one parsed template.
type pluginTree struct {
	calls []pluginCall
	depth int // deepest range nesting inside the template itself
}

// reentersContent reports whether the range pipe starts again from the
// section's content ($.Content..., or .Content... at a template root). A range
// over the current item's children (dotItem) only walks the data once, so it
// adds no depth.
func reentersContent(p *parse.PipeNode, dot dotKind) bool {
	if p == nil || len(p.Cmds) == 0 || len(p.Cmds[0].Args) == 0 {
		return true
	}
	switch p.Cmds[0].Args[0].(type) {
	case *parse.FieldNode, *parse.DotNode:
		return dot != dotItem
	}
	return true
}

// collectCalls records the calls and the range depth of l. dot says what the
// dot is, as for a with (withDot).
func collectCalls(l *parse.ListNode, depth int, dot dotKind, t *pluginTree) {
	if l == nil {
		return
	}
	t.depth = max(t.depth, depth)
	for _, n := range l.Nodes {
		switch x := n.(type) {
		case *parse.TemplateNode:
			t.calls = append(t.calls, pluginCall{x.Name, depth})
		case *parse.RangeNode:
			d := depth
			if reentersContent(x.Pipe, dot) {
				d++
			}
			collectCalls(x.List, d, dotItem, t)
			collectCalls(x.ElseList, depth, dot, t)
		case *parse.IfNode:
			collectCalls(x.List, depth, dot, t)
			collectCalls(x.ElseList, depth, dot, t)
		case *parse.WithNode:
			collectCalls(x.List, depth, withDot(x.Pipe, dot), t)
			collectCalls(x.ElseList, depth, dot, t)
		}
	}
}

// checkPluginCalls takes every template source of one plugin, by root name,
// and refuses a call cycle (a template that reaches itself), more than
// maxPluginCalls expanded invocations from any template, and range nesting
// deeper than maxPluginRangeDepth, counting ranges that start again from the
// content ($.Content..., not a range over the current item) and ranges that
// enclose a call to a template that ranges itself. Calls to names the sources do not define (the
// theme's templates) are leaves. A source that does not parse is left to vet.
func checkPluginCalls(srcs map[string]string) error {
	graph := map[string]*pluginTree{}
	roots := make([]string, 0, len(srcs))
	for root := range srcs {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	for _, root := range roots {
		p := parse.New(root)
		p.Mode = parse.SkipFuncCheck
		trees := map[string]*parse.Tree{}
		if _, err := p.Parse(srcs[root], "", "", trees); err != nil {
			continue
		}
		for name, tr := range trees {
			if tr.Root == nil {
				continue
			}
			t := graph[name]
			if t == nil {
				t = &pluginTree{}
				graph[name] = t
			}
			collectCalls(tr.Root, 0, dotRoot, t)
		}
	}
	names := make([]string, 0, len(graph))
	for name := range graph {
		names = append(names, name)
	}
	sort.Strings(names)

	const (
		visiting = iota + 1
		done
	)
	state := map[string]int{}
	calls := map[string]int{}
	depth := map[string]int{}
	var visit func(name string) error
	visit = func(name string) error {
		switch state[name] {
		case visiting:
			return fmt.Errorf("template %q calls itself, directly or through other templates", name)
		case done:
			return nil
		}
		state[name] = visiting
		t := graph[name]
		n, d := 0, t.depth
		for _, c := range t.calls {
			n++
			if callee := graph[c.name]; callee != nil {
				if err := visit(c.name); err != nil {
					return err
				}
				n += calls[c.name]
				d = max(d, c.depth+depth[c.name])
			}
			n = min(n, maxPluginCalls+1) // no overflow on deep fan-out
		}
		state[name] = done
		calls[name], depth[name] = n, d
		return nil
	}
	for _, name := range names {
		if err := visit(name); err != nil {
			return err
		}
		if calls[name] > maxPluginCalls {
			return fmt.Errorf("template %q expands to more than %d template calls", name, maxPluginCalls)
		}
		if depth[name] > maxPluginRangeDepth {
			return fmt.Errorf("template %q nests ranges deeper than %d, counting the templates it calls", name, maxPluginRangeDepth)
		}
	}
	return nil
}

// callGraphFaults runs checkPluginCalls per plugin over the given partials and
// sections and returns the error of each plugin that fails.
func (s pluginSnapshot) callGraphFaults(partials, sections map[string]string) map[string]error {
	byPlugin := map[string]map[string]string{}
	add := func(plugin, root, src string) {
		if byPlugin[plugin] == nil {
			byPlugin[plugin] = map[string]string{}
		}
		byPlugin[plugin][root] = src
	}
	for name, src := range partials {
		add(s.owner["p:"+name], name, src)
	}
	for typ, src := range sections {
		add(s.owner["s:"+typ], "sections/"+typ+".html", src)
	}
	faults := map[string]error{}
	for plugin, srcs := range byPlugin {
		if err := checkPluginCalls(srcs); err != nil {
			faults[plugin] = err
		}
	}
	return faults
}

// foreignCall returns the first {{template}} in the trees that calls a template
// of another plugin, or "" when there is none. That is any "plugin/<other>/..."
// name, and any name owned (reports true) by a different plugin: its partials,
// "section-<type>" and "sections/<type>.html". prefix is the calling plugin's
// own "plugin/<ident>/".
func foreignCall(trees []*parse.Tree, prefix string, foreign func(name string) bool) string {
	for _, tr := range trees {
		if tr == nil || tr.Root == nil {
			continue
		}
		var t pluginTree
		collectCalls(tr.Root, 0, dotRoot, &t)
		for _, c := range t.calls {
			if strings.HasPrefix(c.name, "plugin/") && !strings.HasPrefix(c.name, prefix) || foreign(c.name) {
				return c.name
			}
		}
	}
	return ""
}
