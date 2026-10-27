package render

import (
	"fmt"
	"html/template"
	"log/slog"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// pluginNameRe constrains plugin and entry names so the namespaced func name
// is a valid template identifier. Plugin names may also be kebab-case (the SDK
// manifest pattern); see pluginIdent.
var pluginNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// pluginIdent normalises a plugin name to a template identifier by turning
// "-" into "_": plugin "acme-seo", func "badge" is template func
// plugin_acme_seo_badge and partial "plugin/acme_seo/badge".
func pluginIdent(plugin string) string { return strings.ReplaceAll(plugin, "-", "_") }

// trustedTypes are the html/template content types a plugin func must never
// return: they would bypass contextual escaping.
var trustedTypes = []reflect.Type{
	reflect.TypeFor[template.HTML](),
	reflect.TypeFor[template.JS](),
	reflect.TypeFor[template.JSStr](),
	reflect.TypeFor[template.URL](),
	reflect.TypeFor[template.CSS](),
	reflect.TypeFor[template.HTMLAttr](),
	reflect.TypeFor[template.Srcset](),
}

var errorType = reflect.TypeFor[error]()

// pluginRegistry holds plugin-provided template funcs and partials. The engine
// snapshots it when it builds a template set. It is host-side only; the plugin
// SDK adapter calls RegisterPluginFunc / RegisterPluginPartial.
type pluginRegistry struct {
	mu       sync.RWMutex
	funcs    template.FuncMap
	partials map[string]string
	sections map[string]string // section type -> template source
	// owner maps "f:<func>", "p:<partial>" and "s:<type>" to the plugin that
	// registered it, so a plugin's registrations can be removed on reload.
	owner map[string]string
	gen   int // bumped on every change
}

func newPluginRegistry() *pluginRegistry {
	return &pluginRegistry{funcs: template.FuncMap{}, partials: map[string]string{}, sections: map[string]string{}, owner: map[string]string{}}
}

var defaultPlugins = newPluginRegistry()

// RegisterPluginFunc registers fn as template func plugin_<plugin>_<name>
// (a "-" in the plugin name becomes "_": acme-seo/badge is plugin_acme_seo_badge).
// fn must return one value or (value, error); value types are limited to
// string, bool, numbers and slices/maps of those. Trusted content types
// (template.HTML, JS, URL, CSS, HTMLAttr, ...) are rejected so plugin output is
// always contextually escaped.
func RegisterPluginFunc(plugin, name string, fn any) error {
	return defaultPlugins.registerFunc(plugin, name, fn)
}

// RegisterPluginPartial registers an html/template source as the partial
// "plugin/<plugin>/<name>".
func RegisterPluginPartial(plugin, name, src string) error {
	return defaultPlugins.registerPartial(plugin, name, src)
}

// RegisterPluginSection registers an html/template source as the section type
// typ. It defines "section-<typ>" like a theme's sections/<typ>.html and has
// lower precedence: a theme section of the same type wins.
func RegisterPluginSection(plugin, typ, src string) error {
	return defaultPlugins.registerSection(plugin, typ, src)
}

// PluginSectionOwner returns the plugin that registered the section type, if
// a plugin did.
func PluginSectionOwner(typ string) (string, bool) {
	defaultPlugins.mu.RLock()
	defer defaultPlugins.mu.RUnlock()
	plugin, ok := defaultPlugins.owner["s:"+typ]
	return plugin, ok
}

// RemovePlugin drops every func, partial, section and public asset link the
// plugin registered.
func RemovePlugin(plugin string) {
	defaultPlugins.removePlugin(plugin)
	SetPluginAssets(plugin, "", "")
}

// CheckPluginFunc reports whether RegisterPluginFunc would accept the func,
// without registering it, so a host can validate a plugin's registrations
// before applying any of them.
func CheckPluginFunc(plugin, name string, fn any) error {
	return defaultPlugins.checkFunc(plugin, name, fn)
}

// CheckPluginPartial is the RegisterPluginPartial dry run.
func CheckPluginPartial(plugin, name, src string) error {
	return defaultPlugins.checkPartial(plugin, name, src)
}

func checkNames(plugin, name string) error {
	if !pluginNameRe.MatchString(pluginIdent(plugin)) || !pluginNameRe.MatchString(name) {
		return fmt.Errorf("render: invalid plugin/name %q/%q (want [A-Za-z][A-Za-z0-9_]*; - is allowed in the plugin name)", plugin, name)
	}
	return nil
}

func (r *pluginRegistry) checkFunc(plugin, name string, fn any) error {
	if err := checkNames(plugin, name); err != nil {
		return err
	}
	if err := validatePluginFunc(fn); err != nil {
		return fmt.Errorf("render: plugin func %s/%s: %w", plugin, name, err)
	}
	full := "plugin_" + pluginIdent(plugin) + "_" + name
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, dup := r.funcs[full]; dup {
		return fmt.Errorf("render: plugin func %s already registered", full)
	}
	return nil
}

func (r *pluginRegistry) checkPartial(plugin, name, src string) error {
	if err := checkNames(plugin, name); err != nil {
		return err
	}
	if src == "" {
		return fmt.Errorf("render: plugin partial %s/%s: empty source", plugin, name)
	}
	full := "plugin/" + pluginIdent(plugin) + "/" + name
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, dup := r.partials[full]; dup {
		return fmt.Errorf("render: plugin partial %s already registered", full)
	}
	return nil
}

func (r *pluginRegistry) registerFunc(plugin, name string, fn any) error {
	if err := r.checkFunc(plugin, name, fn); err != nil {
		return err
	}
	full := "plugin_" + pluginIdent(plugin) + "_" + name
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.funcs[full]; dup {
		return fmt.Errorf("render: plugin func %s already registered", full)
	}
	r.funcs[full] = fn
	r.owner["f:"+full] = plugin
	r.gen++
	return nil
}

func (r *pluginRegistry) registerPartial(plugin, name, src string) error {
	if err := r.checkPartial(plugin, name, src); err != nil {
		return err
	}
	full := "plugin/" + pluginIdent(plugin) + "/" + name
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.partials[full]; dup {
		return fmt.Errorf("render: plugin partial %s already registered", full)
	}
	r.partials[full] = src
	r.owner["p:"+full] = plugin
	r.gen++
	return nil
}

func (r *pluginRegistry) registerSection(plugin, typ, src string) error {
	if !sectionTypeRe.MatchString(typ) || strings.TrimSpace(src) == "" {
		return fmt.Errorf("render: plugin section %s/%s: bad type (want %s) or empty source", plugin, typ, sectionTypeRe)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.sections[typ]; dup {
		return fmt.Errorf("render: section type %q already registered by plugin %s", typ, r.owner["s:"+typ])
	}
	r.sections[typ] = src
	r.owner["s:"+typ] = plugin
	r.gen++
	return nil
}

func (r *pluginRegistry) removePlugin(plugin string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, who := range r.owner {
		if who != plugin {
			continue
		}
		name := key[2:]
		switch key[0] {
		case 'f':
			delete(r.funcs, name)
		case 'p':
			delete(r.partials, name)
		case 's':
			delete(r.sections, name)
		}
		delete(r.owner, key)
		r.gen++
	}
}

// size is a change counter: it differs whenever the registered funcs,
// partials or sections changed, or a plugin's site.scripts grant did (cheap change detector).
func (r *pluginRegistry) size() int {
	fp := r.gateFingerprint(pluginScriptsAllowed())
	r.mu.RLock()
	defer r.mu.RUnlock()
	return int(uint64(r.gen)*0x9E3779B97F4A7C15 ^ fp)
}

// gated is snapshot without the partials and sections that carry script
// constructs while their plugin lacks site.scripts.
func (r *pluginRegistry) gated() pluginSnapshot {
	s, _ := r.snapshot().withoutScripts(pluginScriptsAllowed())
	return s
}

// pluginSnapshot is a consistent copy of the registry.
type pluginSnapshot struct {
	funcs    template.FuncMap
	partials map[string]string // "plugin/<ident>/<name>" -> source
	sections map[string]string // type -> source
	// owner maps "p:<partial>" and "s:<type>" to the registering plugin.
	owner map[string]string
}

func (r *pluginRegistry) snapshot() pluginSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return pluginSnapshot{
		funcs:    maps.Clone(r.funcs),
		partials: maps.Clone(r.partials),
		sections: maps.Clone(r.sections),
		owner:    maps.Clone(r.owner),
	}
}

// vet returns the plugin templates that may be added to a theme's template
// set. A plugin template may only define names of its own: partials define
// "plugin/<ident>/..." and a section defines "section-<type>" (the type must
// not exist in the theme) and "plugin/<ident>/...". Anything else (a
// {{define "head"}} that would replace the theme's template, a syntax error, an
// unknown func, a section that does not define its own entry) rejects ALL of
// that plugin's templates, with a log line. The theme is never touched.
func (s pluginSnapshot) vet(themeSections map[string]*SectionDef, log *slog.Logger) (partials, sections map[string]string) {
	rejected := map[string]bool{} // plugin name
	reject := func(plugin, what string, err error) {
		if !rejected[plugin] {
			log.Warn("render: plugin templates rejected", "plugin", plugin, "template", what, "error", err)
			NoteBootFault("plugin-templates-rejected:" + plugin)
		}
		rejected[plugin] = true
	}
	ok := func(src string, root string, allow func(string) bool) error {
		t, err := template.New(root).Funcs(s.funcs).Parse(src)
		if err != nil {
			return err
		}
		for _, d := range t.Templates() {
			if n := d.Name(); n != root && !allow(n) {
				return fmt.Errorf("defines %q, which is not a name of its own", n)
			}
		}
		return nil
	}
	partials, sections = map[string]string{}, map[string]string{}
	for name, src := range s.partials {
		plugin := s.owner["p:"+name]
		prefix := "plugin/" + pluginIdent(plugin) + "/"
		if !strings.HasPrefix(name, prefix) {
			reject(plugin, name, fmt.Errorf("partial name outside %s", prefix))
			continue
		}
		if err := ok(src, name, func(n string) bool { return strings.HasPrefix(n, prefix) }); err != nil {
			reject(plugin, name, err)
			continue
		}
		partials[name] = src
	}
	for typ, src := range s.sections {
		if themeSections[typ] != nil {
			continue // the theme's section of that type wins
		}
		plugin := s.owner["s:"+typ]
		prefix := "plugin/" + pluginIdent(plugin) + "/"
		root := "sections/" + typ + ".html"
		if err := ok(src, root, func(n string) bool { return n == "section-"+typ || strings.HasPrefix(n, prefix) }); err != nil {
			reject(plugin, root, err)
			continue
		}
		t, _ := template.New(root).Funcs(s.funcs).Parse(src)
		if t.Lookup("section-"+typ) == nil {
			reject(plugin, root, fmt.Errorf("must define \"section-%s\"", typ))
			continue
		}
		sections[typ] = src
	}
	for name := range s.partials {
		if rejected[s.owner["p:"+name]] {
			delete(partials, name)
		}
	}
	for typ := range sections {
		if rejected[s.owner["s:"+typ]] {
			delete(sections, typ)
		}
	}
	return partials, sections
}

func validatePluginFunc(fn any) error {
	if fn == nil {
		return fmt.Errorf("nil func")
	}
	t := reflect.TypeOf(fn)
	if t.Kind() != reflect.Func {
		return fmt.Errorf("not a func: %s", t)
	}
	switch t.NumOut() {
	case 1:
	case 2:
		if t.Out(1) != errorType {
			return fmt.Errorf("second return value must be error, got %s", t.Out(1))
		}
	default:
		return fmt.Errorf("must return 1 value or (value, error), returns %d", t.NumOut())
	}
	return validateReturnType(t.Out(0), 0)
}

func validateReturnType(t reflect.Type, depth int) error {
	if depth > 4 {
		return fmt.Errorf("return type too deeply nested")
	}
	if slices.Contains(trustedTypes, t) {
		return fmt.Errorf("return type %s bypasses escaping and is not allowed", t)
	}
	switch t.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return nil
	case reflect.Slice, reflect.Array:
		return validateReturnType(t.Elem(), depth+1)
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return fmt.Errorf("map key must be string, got %s", t.Key())
		}
		return validateReturnType(t.Elem(), depth+1)
	}
	return fmt.Errorf("return type %s not allowed (string, bool, numbers, slices and maps only)", t)
}
