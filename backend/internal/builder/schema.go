package builder

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// kindSpec describes one node kind. Adding a widget is one registry entry.
type kindSpec struct {
	children bool   // may hold child nodes
	schema   string // JSON Schema (draft 2020-12) for the node's props

	// Set for widget kinds loaded from widgets/<kind>/schema.json only.
	meta    *widgetMeta       // x-hm palette metadata
	order   []string          // prop keys in schema order
	widgets map[string]string // prop key -> x-hm-widget hint
	bind    map[string]string // bindable prop key -> class (x-hm-bind: text|url|image)
	own     map[string]bool   // plugin kinds: the element settings (anchor, aria, tip) the schema declares itself; the core skips them for the kind
}

const maxText = 20000

// relPattern is the link rel tokens an author may choose (noopener and
// noreferrer are added by the renderer with target=_blank).
const anchorPattern = `^[a-z][a-z0-9-]{0,63}$`

const relPattern = `^(nofollow|sponsored|ugc)( (nofollow|sponsored|ugc)){0,2}$`

// nativeKinds are the node kinds implemented by the renderer itself.
var nativeKinds = map[string]kindSpec{
	"container": {children: true, schema: `{
		"type":"object","additionalProperties":false,
		"properties":{
			"tag":{"enum":["section","div","header","footer","main","article","aside","nav"]},
			"layout":{"enum":["flex","grid"]},
			"v1":{"type":"boolean"},
			"href":{"type":"string","maxLength":2048},
			"target":{"enum":["_blank"]},
			"rel":{"type":"string","maxLength":32,"pattern":"` + relPattern + `"},
			"hidden":{"type":"boolean"}}}`},
	"component": {schema: `{
		"type":"object","additionalProperties":false,"required":["type"],
		"properties":{
			"type":{"type":"string","minLength":1,"maxLength":64},
			"title":{"type":"string","maxLength":2000},
			"content":{"type":"object"},
			"extra":{"type":"object","propertyNames":{"not":{"enum":["id","type","title","content","isVisible","sort_order"]}}},
			"hidden":{"type":"boolean"}}}`},
	"heading": {schema: `{
		"type":"object","additionalProperties":false,
		"properties":{
			"text":{"type":"string","maxLength":20000,"x-hm-bind":"text"},
			"html":{"type":"string","maxLength":20000},
			"level":{"type":"integer","minimum":1,"maximum":6},
			"hidden":{"type":"boolean"}}}`},
	"text": {schema: `{
		"type":"object","additionalProperties":false,
		"properties":{
			"text":{"type":"string","maxLength":20000,"x-hm-bind":"text"},
			"html":{"type":"string","maxLength":20000},
			"hidden":{"type":"boolean"}}}`},
	"button": {schema: `{
		"type":"object","additionalProperties":false,
		"properties":{
			"text":{"type":"string","maxLength":20000,"x-hm-bind":"text"},
			"href":{"type":"string","maxLength":2048,"x-hm-bind":"url"},
			"target":{"enum":["_blank"]},
			"rel":{"type":"string","maxLength":32,"pattern":"` + relPattern + `"},
			"hidden":{"type":"boolean"}}}`},
	"image": {schema: `{
		"type":"object","additionalProperties":false,
		"properties":{
			"src":{"type":"string","maxLength":2048,"x-hm-bind":"image"},
			"alt":{"type":"string","maxLength":2000,"x-hm-bind":"text"},
			"decorative":{"type":"boolean"},
			"href":{"type":"string","maxLength":2048,"x-hm-bind":"url"},
			"target":{"enum":["_blank"]},
			"rel":{"type":"string","maxLength":32,"pattern":"` + relPattern + `"},
			"caption":{"type":"string","maxLength":300,"x-hm-bind":"text"},
			"lightbox":{"type":"boolean"},
			"hidden":{"type":"boolean"}}}`},
	"ref": {schema: `{
		"type":"object","additionalProperties":false,"required":["block"],
		"properties":{
			"block":{"type":"string","pattern":"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"},
			"hidden":{"type":"boolean"}}}`},
	"slot": {schema: `{
		"type":"object","additionalProperties":false,
		"properties":{"hidden":{"type":"boolean"},"replace":{"type":"boolean"}}}`},
	"spacer": {schema: `{
		"type":"object","additionalProperties":false,
		"properties":{"hidden":{"type":"boolean"}}}`},
	"divider": {schema: `{
		"type":"object","additionalProperties":false,
		"properties":{"hidden":{"type":"boolean"}}}`},
}

// compileKinds compiles the prop schema of every kind in kinds.
func compileKinds(kinds map[string]kindSpec) map[string]*jsonschema.Schema {
	out := make(map[string]*jsonschema.Schema, len(kinds))
	for name, k := range kinds {
		s, err := compileKind(jsonschema.NewCompiler(), name, k.schema)
		if err != nil {
			panic(fmt.Sprintf("builder: schema for kind %q: %v", name, err))
		}
		out[name] = s
	}
	return out
}

// compileKind compiles one kind schema (plus the universal preset prop) with c.
func compileKind(c *jsonschema.Compiler, name, schema string) (*jsonschema.Schema, error) {
	c.Draft = jsonschema.Draft2020
	url := "kind/" + name + ".json"
	if err := c.AddResource(url, strings.NewReader(withUniversal(name, schema))); err != nil {
		return nil, err
	}
	return c.Compile(url)
}

// UniversalKeys are the props every kind schema gets from withUniversal rather
// than declaring them, so no kind lists them as a Content field: preset (a
// style preset name), navlabel (the author's name for the node in the
// navigator; never rendered), and the element settings anchor (public id),
// aria (aria-label) and tip (title attribute).
var UniversalKeys = []string{"preset", "navlabel", "anchor", "aria", "tip"}

// reservedBefore are the universal keys reserved before the element settings
// existed (preset, navlabel): a plugin schema declaring one is refused. The
// element settings keys may be declared by a plugin kind that already used the
// name; the core then skips that universal for the kind (see PackOwns).
var reservedBefore = UniversalKeys[:2]

// noElementProps are the kinds that take no anchor/aria/tip: they render no
// element of their own to carry the attributes (wrap a component in a container).
var noElementProps = map[string]bool{"component": true, "ref": true, "slot": true, "spacer": true}

// noAriaKinds take no accessible name: aria-label is prohibited on the element
// they render (a paragraph) or their template already writes the label itself
// (icon, menu, breadcrumbs), the tabs island names the panel (tabpanel), or the
// root is a role-less <div> (a test renders every widget and checks this list).
var noAriaKinds = map[string]bool{
	"text": true, "icon": true, "menu": true, "breadcrumbs": true, "tabpanel": true,
	"gallery": true, "columns": true, "buttons": true, "stat": true, "accordion": true, "tabs": true,
	"loop": true, "loopempty": true, "html": true, "richtext": true, "form": true, "video": true,
}

// universalFor lists the universal keys a kind schema gets.
// own are the element settings a plugin kind declares itself (none for core kinds).
func universalFor(kind string, own map[string]bool) []string {
	if noElementProps[kind] {
		return UniversalKeys[:2]
	}
	out := make([]string, 0, len(UniversalKeys))
	for _, k := range UniversalKeys {
		if own[k] || (k == "aria" && noAriaKinds[kind]) {
			continue
		}
		out = append(out, k)
	}
	return out
}

// withUniversal adds the universal props to a kind schema, so no kind schema
// declares them and widget packs inherit them. A kind schema that already
// declares one is a bug and panics.
func withUniversal(kind, schema string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(schema), &m); err != nil {
		panic(fmt.Sprintf("builder: schema for kind %q: %v", kind, err))
	}
	props, _ := m["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		m["properties"] = props
	}
	declared := map[string]bool{}
	for i, k := range UniversalKeys {
		if _, dup := props[k]; dup {
			if i >= len(reservedBefore) && IsPackKind(kind) {
				declared[k] = true // a plugin that used the name before the core did keeps it
				continue
			}
			panic(fmt.Sprintf("builder: schema for kind %q declares universal prop %q", kind, k))
		}
	}
	props["preset"] = map[string]any{"type": "string", "pattern": presetRe.String()}
	props["navlabel"] = map[string]any{"type": "string", "maxLength": 80, "pattern": `^[^\x00-\x1f\x7f]*$`}
	if !noElementProps[kind] {
		if !declared["anchor"] {
			props["anchor"] = map[string]any{"type": "string", "pattern": anchorPattern}
		}
		if !noAriaKinds[kind] && !declared["aria"] {
			props["aria"] = map[string]any{"type": "string", "maxLength": 200, "pattern": `^[^\x00-\x1f\x7f]*$`}
		}
		if !declared["tip"] {
			props["tip"] = map[string]any{"type": "string", "maxLength": 200, "pattern": `^[^\x00-\x1f\x7f]*$`}
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		panic(fmt.Sprintf("builder: schema for kind %q: %v", kind, err))
	}
	return string(out)
}
