package render

import (
	"hash/fnv"
	"html/template"
	"sort"
	"strings"
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

// stubFuncs has every plugin func answer with nothing: a sample render must
// not call into plugin code.
func stubFuncs(funcs template.FuncMap) template.FuncMap {
	out := template.FuncMap{}
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
	t, err := template.New("sample").Funcs(stubFuncs(funcs)).Parse(src)
	if err != nil {
		return nil
	}
	for _, d := range t.Templates() {
		var out strings.Builder
		data := preparedSection{Section: Section{Content: map[string]any{}}, Settings: map[string]any{}}
		if d.Execute(&out, data) != nil {
			continue
		}
		if err := scanPluginScripts("sample render of "+d.Name(), out.String()); err != nil {
			return err
		}
	}
	return nil
}

// withoutScripts drops the sections and blanks the partials of every plugin the gate
// refuses that fail the script check, and returns why each was dropped
// ("section promo", "partial card": reason), keyed by plugin.
func (s pluginSnapshot) withoutScripts(allowed func(string) bool) (pluginSnapshot, map[string][]string) {
	why := map[string][]string{}
	out := s
	out.partials, out.sections = map[string]string{}, map[string]string{}
	keep := func(plugin, label, src string) bool {
		if allowed != nil && allowed(plugin) {
			return true
		}
		err := checkPluginScripts(src, s.funcs)
		if err == nil {
			err = scanSampleRender(src, s.funcs)
		}
		if err != nil {
			why[plugin] = append(why[plugin], label+": "+err.Error()+" (not allowed without site.scripts)")
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
	return out, why
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
	snap := defaultPlugins.snapshot()
	_, why := snap.withoutScripts(pluginScriptsAllowed())
	return why[plugin]
}
