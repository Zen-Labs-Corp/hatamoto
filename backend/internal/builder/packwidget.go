package builder

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"sort"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// PackGroup is the palette group of every plugin widget.
const PackGroup = "Plugin widgets"

// Plugin widget limits.
const (
	MaxPackWidgets      = 64    // registered plugin widgets in total
	MaxPackProps        = 24    // properties of one widget schema
	MaxPackSchemaBytes  = 16384 // size of one widget schema
	maxOrphanPropsBytes = 8192  // props of a node whose widget is not installed
	maxKindName         = 64    // ValidID limit, a pack kind must fit
)

// packKindRe matches <plugin>_<widget>: a plugin name (sdk.NamePattern), one
// underscore, a widget directory name. No core kind contains "_", so a plugin
// widget can never shadow a core kind, and plugin names are unique, so two
// plugins cannot collide.
var packKindRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}_[a-z][a-z0-9-]{0,31}$`)

var (
	pluginNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)
	widgetNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// PackKindName returns the builder kind of widget of plugin: "<plugin>_<widget>".
func PackKindName(plugin, widget string) (string, error) {
	if !pluginNameRe.MatchString(plugin) {
		return "", fmt.Errorf("bad plugin name %q", plugin)
	}
	if !widgetNameRe.MatchString(widget) {
		return "", fmt.Errorf("bad widget name %q (lowercase letters, digits and \"-\", at most 32)", widget)
	}
	kind := plugin + "_" + widget
	if len(kind) > maxKindName {
		return "", fmt.Errorf("kind %q is longer than %d characters", kind, maxKindName)
	}
	return kind, nil
}

// IsPackKind reports whether name has the shape of a plugin widget kind,
// whether or not it is registered.
func IsPackKind(name string) bool {
	return len(name) <= maxKindName && packKindRe.MatchString(name)
}

// PackWidgetSchema is one parsed plugin widget schema, ready for SetPackKinds.
type PackWidgetSchema struct {
	Name   string // the full kind, <plugin>_<widget>
	Schema []byte
	spec   kindSpec
}

// Owned lists the element settings (anchor, aria, tip) the widget declares as
// its own props, sorted. The core skips them for the kind. Installing a new
// package that does so is refused; an installed one keeps working.
func (w PackWidgetSchema) Owned() []string {
	out := make([]string, 0, len(w.spec.own))
	for k := range w.spec.own {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PackOwns reports whether the plugin kind declares the element setting key itself.
func PackOwns(kind, key string) bool {
	k, _ := kindOf(kind)
	return k.own[key]
}

// ParsePackWidget validates the schema.json of widget of plugin. own names the
// full kinds of every widget of the same package, which a seed or only_in may
// reference besides native and core kinds.
func ParsePackWidget(plugin, widget string, schema []byte, own map[string]bool) (PackWidgetSchema, error) {
	kind, err := PackKindName(plugin, widget)
	if err != nil {
		return PackWidgetSchema{}, err
	}
	if len(schema) > MaxPackSchemaBytes {
		return PackWidgetSchema{}, fmt.Errorf("schema is larger than %d bytes", MaxPackSchemaBytes)
	}
	var top struct {
		Additional *bool `json:"additionalProperties"`
	}
	if err := json.Unmarshal(schema, &top); err != nil {
		return PackWidgetSchema{}, fmt.Errorf("schema: %w", err)
	}
	if top.Additional == nil || *top.Additional {
		return PackWidgetSchema{}, fmt.Errorf(`schema needs "additionalProperties": false`)
	}
	spec, err := parseWidgetSpec(kind, schema, true)
	if err != nil {
		return PackWidgetSchema{}, err
	}
	var props struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &props); err != nil {
		return PackWidgetSchema{}, fmt.Errorf("schema: %w", err)
	}
	if len(props.Properties) > MaxPackProps {
		return PackWidgetSchema{}, fmt.Errorf("schema has more than %d props", MaxPackProps)
	}
	for _, k := range UniversalKeys {
		if _, dup := props.Properties[k]; dup {
			if !slices.Contains(reservedBefore, k) {
				if spec.own == nil {
					spec.own = map[string]bool{}
				}
				spec.own[k] = true
				continue
			}
			return PackWidgetSchema{}, fmt.Errorf("%w: schema property %q is reserved", ErrInvalid, k)
		}
	}
	known := func(k string) bool {
		if own[k] {
			return true
		}
		_, core := coreRegistry.kinds[k]
		return core
	}
	if u := seedUnknown(spec.meta.Seed, known); u != "" {
		return PackWidgetSchema{}, fmt.Errorf("x-hm.seed kind %q is not one of this package's widgets or a core kind", u)
	}
	if o := spec.meta.OnlyIn; o != "" && !known(o) {
		return PackWidgetSchema{}, fmt.Errorf("x-hm.only_in kind %q is not one of this package's widgets or a core kind", o)
	}
	if _, err := compilePackKind(kind, spec.schema); err != nil {
		return PackWidgetSchema{}, fmt.Errorf("schema: %w", err)
	}
	return PackWidgetSchema{Name: kind, Schema: append([]byte(nil), schema...), spec: spec}, nil
}

// compilePackKind compiles a plugin schema with a compiler that cannot load
// anything but the schema itself ($ref to a file or URL is refused).
func compilePackKind(kind, schema string) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.LoadURL = func(s string) (io.ReadCloser, error) {
		return nil, fmt.Errorf("external reference %q is not allowed", s)
	}
	return compileKind(c, kind, schema)
}

// SetPackKinds replaces the plugin kinds with ws (nil removes them all) in one
// atomic swap. Core kinds are always present and never replaced; on error the
// previous snapshot stays.
func SetPackKinds(ws []PackWidgetSchema) error {
	if len(ws) > MaxPackWidgets {
		return fmt.Errorf("builder: %d plugin widgets, at most %d", len(ws), MaxPackWidgets)
	}
	kinds := maps.Clone(coreRegistry.kinds)
	schemas := maps.Clone(coreRegistry.schemas)
	for _, w := range ws {
		if !IsPackKind(w.Name) {
			return fmt.Errorf("builder: %q is not a plugin widget kind", w.Name)
		}
		if _, dup := kinds[w.Name]; dup {
			return fmt.Errorf("builder: duplicate widget kind %q", w.Name)
		}
		if w.spec.meta == nil {
			return fmt.Errorf("builder: widget %q was not parsed by ParsePackWidget", w.Name)
		}
		s, err := compilePackKind(w.Name, w.spec.schema)
		if err != nil {
			return fmt.Errorf("builder: widget %q: %w", w.Name, err)
		}
		kinds[w.Name], schemas[w.Name] = w.spec, s
	}
	known := func(k string) bool { _, ok := kinds[k]; return ok }
	names := make([]string, 0, len(ws))
	for _, w := range ws {
		names = append(names, w.Name)
	}
	sort.Strings(names)
	for _, n := range names {
		m := kinds[n].meta
		if u := seedUnknown(m.Seed, known); u != "" {
			return fmt.Errorf("builder: widget %q: seed kind %q is not registered", n, u)
		}
		if m.OnlyIn != "" && !known(m.OnlyIn) {
			return fmt.Errorf("builder: widget %q: only_in kind %q is not registered", n, m.OnlyIn)
		}
	}
	reg.Store(&registry{kinds: kinds, schemas: schemas})
	return nil
}

// errOrphanEdit is returned by an op that would edit a node whose widget is
// not installed.
var errOrphanEdit = invalid("widget not installed")

// isOrphanIn is isOrphan against snapshot r.
func isOrphanIn(r *registry, n *Node) bool {
	if _, ok := r.kinds[n.T]; ok {
		return false
	}
	return IsPackKind(n.T)
}

// validateOrphan checks what can be checked of a node whose widget is not
// installed: bounded props, and binding keys and sources by syntax only.
func validateOrphan(x *Node) error {
	raw, err := json.Marshal(x.P)
	if err != nil {
		return fmt.Errorf("props: %w", err)
	}
	if len(raw) > maxOrphanPropsBytes {
		return fmt.Errorf("props are larger than %d bytes", maxOrphanPropsBytes)
	}
	for key, src := range x.B {
		if !bindSegRe.MatchString(key) {
			return fmt.Errorf("bad binding key %q", key)
		}
		if _, _, err := parseBind(src); err != nil {
			return err
		}
	}
	return nil
}
