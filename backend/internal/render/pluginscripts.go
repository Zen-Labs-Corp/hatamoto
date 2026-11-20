package render

import (
	"bytes"
	"errors"
	"fmt"
	"hash/fnv"
	"html/template"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"text/template/parse"
)

// A plugin's partials and sections are markup the page emits as written. They
// carry the same script check as widget packs unless the owner granted the
// plugin site.scripts: otherwise a plugin with no permission could put
// <script> on every page by way of render.page and a section of its own.

// pluginMarkupRules are the exceptions a plugin's sections and partials get
// over the widget-pack check, and the only ones: a form that posts to an http(s)
// or relative URL, and inline style. Everything else a widget pack refuses
// (meta http-equiv, svg, math, link, template, noscript, dialog, ...) is refused
// here until the owner grants site.scripts.
var pluginMarkupRules = markupRules{allowForm: true, allowStyle: true}

func scanPluginScripts(what, markup string) error {
	return scanMarkup("markup", what, markup, pluginMarkupRules)
}

// checkPluginScripts scans every template a plugin source defines, with the
// actions cut out and with them replaced by a letter (as widget packs do), and
// each if/range/with body on its own, with no action where a tag or attribute
// name is parsed (checkPluginActions), then the output of a sample render. A
// source that does not parse is left to vet, which rejects it.
func checkPluginScripts(src string, funcs template.FuncMap) error {
	t, err := template.New("scan").Funcs(funcs).Parse(src)
	if err != nil {
		return nil
	}
	for _, d := range t.Templates() {
		if d.Tree == nil {
			continue
		}
		if err := checkPluginActions("template "+d.Name(), d.Tree); err != nil {
			return err
		}
		for _, repl := range []string{"", "x"} {
			var b strings.Builder
			flattenPack(d.Tree.Root, repl, &b)
			if err := scanPluginScripts("template "+d.Name(), b.String()); err != nil {
				return err
			}
			err := packBranches(d.Tree.Root, repl, func(m string) error {
				return scanPluginScripts("template "+d.Name(), m)
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// dotKind says what the dot is while collectCalls walks a template.
type dotKind int

const (
	dotRoot    dotKind = iota // the data the template was called with
	dotContent                // the content root, or a part of it narrowed by a with
	dotItem                   // the current item of a range over content (or a field of it)
	dotUnknown                // anything else (a with over a number, say)
)

// contentChain reports whether idents is a field chain a plugin may range over
// or narrow to. Content holds JSON-decoded values (maps, slices, strings,
// float64 or jsontext.Value, template.HTML after prepare); none has a method
// that returns a large int. Int64 and Float64 are refused in case a json.Number
// reaches it.
func contentChain(idents []string) bool {
	for _, id := range idents {
		if id == "Int64" || id == "Float64" {
			return false
		}
	}
	return true
}

// contentPipe reports whether p is one command with one argument that is data
// from the section's Content, given what the dot is.
func contentPipe(p *parse.PipeNode, dot dotKind) bool {
	if p == nil || len(p.Cmds) != 1 || len(p.Cmds[0].Args) != 1 {
		return false
	}
	switch a := p.Cmds[0].Args[0].(type) {
	case *parse.FieldNode:
		switch dot {
		case dotRoot:
			return a.Ident[0] == "Content" && contentChain(a.Ident)
		case dotContent, dotItem:
			return contentChain(a.Ident)
		}
	case *parse.VariableNode:
		return len(a.Ident) > 1 && a.Ident[0] == "$" && a.Ident[1] == "Content" && contentChain(a.Ident)
	case *parse.DotNode:
		return dot == dotContent || dot == dotItem
	}
	return false
}

// withDot is the dot inside a with over p, when the dot outside is dot. A with
// over a field of an item stays an item; one that starts at the content root
// ($.Content..., .Content... at a template root) is the content again.
func withDot(p *parse.PipeNode, dot dotKind) dotKind {
	if !contentPipe(p, dot) {
		return dotUnknown
	}
	if dot == dotItem {
		if _, ok := p.Cmds[0].Args[0].(*parse.VariableNode); !ok {
			return dotItem
		}
	}
	return dotContent
}

// errPluginOutput is errPackOutput worded for a plugin template.
var errPluginOutput = errors.New("plugin template output exceeds the size cap")

// maxStubString caps every string a stubbed builtin returns, so a chain of
// assignments that doubles a value cannot grow past it.
const maxStubString = 4 << 10

var errStubTooLarge = errors.New("sample render: builtin argument too large")

// wideFormat matches a width or precision that could allocate a lot.
var wideFormat = regexp.MustCompile(`\*|\d{4,}`)

func capStub(s string) string {
	if len(s) > maxStubString {
		return s[:maxStubString]
	}
	return s
}

// stubBuiltins are the builtins that build strings, with the real result cut to
// maxStubString: the sample render still sees what a literal expands to
// ("java{{print "script:"}}") but nothing can grow without limit.
func stubBuiltins() template.FuncMap {
	escaper := func(f func(...any) string) func(...any) string {
		return func(args ...any) string { return capStub(f(args...)) }
	}
	return template.FuncMap{
		"print":   func(args ...any) string { return capStub(fmt.Sprint(args...)) },
		"println": func(args ...any) string { return capStub(fmt.Sprintln(args...)) },
		"printf": func(format string, args ...any) (string, error) {
			if len(format) > maxStubString || wideFormat.MatchString(format) {
				return "", errStubTooLarge
			}
			return capStub(fmt.Sprintf(format, args...)), nil
		},
		"html":     escaper(template.HTMLEscaper),
		"js":       escaper(template.JSEscaper),
		"urlquery": escaper(template.URLQueryEscaper),
		"slice":    stubSlice,
	}
}

// stubSlice is the slice builtin for strings and slices (a result never
// outgrows its operand; a string result is capped).
func stubSlice(item any, idx ...int) (any, error) {
	v := reflect.ValueOf(item)
	if !v.IsValid() || len(idx) > 2 {
		return nil, errors.New("slice: bad arguments")
	}
	n := 0
	switch v.Kind() {
	case reflect.String, reflect.Slice:
		n = v.Len()
	default:
		return nil, errors.New("slice: cannot slice this value")
	}
	lo, hi := 0, n
	if len(idx) > 0 {
		lo = idx[0]
	}
	if len(idx) > 1 {
		hi = idx[1]
	}
	if lo < 0 || hi < lo || hi > n {
		return nil, errors.New("slice: index out of range")
	}
	if v.Kind() == reflect.String {
		return capStub(v.String()[lo:hi]), nil
	}
	return v.Slice(lo, hi).Interface(), nil
}

// stubFuncs has every plugin func answer with nothing (a sample render must not
// call into plugin code) and the string builtins answer capped: builtin output
// is represented, plugin-func output is not.
func stubFuncs(funcs template.FuncMap) template.FuncMap {
	out := stubBuiltins()
	for name := range funcs {
		out[name] = func(...any) (string, error) { return "", nil }
	}
	return out
}

// scanSampleRender executes each template of src on empty section data, with
// the plugin funcs stubbed, and scans what it prints, as a widget pack's sample
// render does. Content is schema-less, so a render cannot be made
// representative: a template that errors on empty data (a missing partial, a
// call on nothing) is skipped here and is covered by the source scan of every
// branch above.
func scanSampleRender(src string, funcs template.FuncMap) error {
	_, err := sampleRender(src, funcs)
	return err
}

// sampleRender is scanSampleRender that also returns the pool it drew on, so a
// test can count what was spent.
func sampleRender(src string, funcs template.FuncMap) (*rangeBudget, error) {
	// Without a load-time range rule a template could spin on a huge range
	// that prints nothing, so the sample render gets one silent budget, shared
	// by every template and every call in it: a template called from a range
	// must not get a fresh budget per call.
	fm := stubFuncs(funcs)
	shared := &rangeBudget{left: pluginRangeBudget}
	fm[rangeBudgetFunc] = func(plugin, tmpl string) *rangeHandle {
		return &rangeHandle{b: shared, plugin: plugin, tmpl: tmpl}
	}
	t, err := template.New("sample").Funcs(fm).Parse(src)
	if err != nil {
		return shared, nil
	}
	rewritePluginRanges(t, "sample", src, "")
	// Templates() walks a map; the pool is shared, so the order decides which
	// template meets an empty pool. Sort for one verdict per source.
	tmpls := t.Templates()
	sort.Slice(tmpls, func(i, j int) bool { return tmpls[i].Name() < tmpls[j].Name() })
	for _, d := range tmpls {
		var out bytes.Buffer
		data := preparedSection{Section: Section{Content: map[string]any{}}, Settings: map[string]any{}}
		err := d.Execute(&cappedWriter{buf: &out, max: maxPackOutput}, data)
		if errors.Is(err, errPackOutput) {
			return shared, fmt.Errorf("markup: sample render of %s: %w", d.Name(), errPluginOutput)
		}
		if err != nil {
			continue
		}
		if err := scanPluginScripts("sample render of "+d.Name(), out.String()); err != nil {
			return shared, err
		}
	}
	return shared, nil
}

// withoutScripts drops the sections and blanks the partials of every plugin the gate
// refuses that fail the script check, and returns why each was dropped
// ("section promo", "partial card": reason), keyed by plugin. Templates refused
// for the call graph are not about scripts: they are returned apart, in graph,
// in the same form.
func (s pluginSnapshot) withoutScripts(allowed func(string) bool) (out pluginSnapshot, why, graph map[string][]string) {
	why, graph = map[string][]string{}, map[string][]string{}
	out = s
	out.partials, out.sections = map[string]string{}, map[string]string{}
	graphFaults := s.callGraphFaults(s.partials, s.sections)
	keep := func(plugin, label, src string) bool {
		if allowed != nil && allowed(plugin) {
			return true
		}
		// A call-graph fault is refused here too: vet may see a different set
		// and find nothing, and a template of that plugin is never executed
		// unchecked.
		err := graphFaults[plugin]
		if err == nil {
			err = checkPluginScripts(src, s.funcs)
		}
		if err == nil {
			err = scanSampleRender(src, s.funcs)
		}
		if err != nil {
			msg := label + ": " + err.Error()
			if graphFaults[plugin] != nil {
				graph[plugin] = append(graph[plugin], msg)
			} else {
				if !errors.Is(err, errPluginOutput) {
					msg += " (not allowed without site.scripts)"
				}
				why[plugin] = append(why[plugin], msg)
			}
			return false
		}
		return true
	}
	for name, src := range s.partials {
		if keep(s.owner["p:"+name], "partial "+name, src) {
			out.partials[name] = src
		} else {
			// Not registered, but still defined (empty): a section calling it
			// renders nothing there. A missing template would fail the page.
			out.partials[name] = " "
		}
	}
	for typ, src := range s.sections {
		if keep(s.owner["s:"+typ], "section "+typ, src) {
			out.sections[typ] = src
		}
	}
	for _, l := range why {
		sort.Strings(l)
	}
	for _, l := range graph {
		sort.Strings(l)
	}
	return out, why, graph
}

// gateFingerprint changes whenever the gate's answer for a registered plugin
// does, so engines rebuild when the owner grants or revokes site.scripts.
func (r *pluginRegistry) gateFingerprint(allowed func(string) bool) uint64 {
	r.mu.RLock()
	seen := map[string]bool{}
	for _, plugin := range r.owner {
		seen[plugin] = true
	}
	r.mu.RUnlock()
	var sum uint64
	for plugin := range seen {
		h := fnv.New64a()
		h.Write([]byte(plugin))
		if allowed != nil && allowed(plugin) {
			h.Write([]byte{1})
		}
		sum += h.Sum64() // order-independent
	}
	return sum
}

// PluginMarkupStopped lists, for the admin, the partials and sections of the
// plugin that are not registered because they carry script constructs and the
// plugin does not hold site.scripts. It is empty once the owner grants it.
func PluginMarkupStopped(plugin string) []string {
	return slices.Clone(defaultPlugins.gateResult().why[plugin])
}
