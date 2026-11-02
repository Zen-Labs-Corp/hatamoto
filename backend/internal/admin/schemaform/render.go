package schemaform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"hatamoto.dev/core/web"
)

// MaxItems caps the entries of one repeater/array a form may submit.
const MaxItems = 500

// Errors maps a dotted path ("faqs.2.question") to friendly messages.
type Errors map[string][]string

// UIHint is the per-field uiSchema: widget (textarea | richtext | tags),
// placeholder, help, autofocus, orderable. ui:options is parsed by the caller
// and ignored here.
type UIHint struct {
	Widget      string
	Placeholder string
	Help        string
	Autofocus   bool
	Unorderable bool // ui:orderable === false
}

// Options configure Render and Decode.
type Options struct {
	// Prefix namespaces field names ("content."), so section forms do not
	// clash with the entity's own fields.
	Prefix string
	// UI holds hints by dotted path without indices ("faqs.question").
	UI map[string]UIHint
	// Placeholders override the placeholder of text and textarea fields by
	// dotted path without indices. A form uses them to show the inherited
	// value of an empty field.
	Placeholders map[string]string
	Disabled     bool
	// OpenPath opens one repeater item and collapses the rest
	// ("faqs.2.question" opens item 2 of faqs).
	OpenPath string
	// Errors are rendered under their fields; repeater items holding an error
	// are expanded.
	Errors Errors
	// Secrets lists the top-level writeOnly fields that hold a stored value.
	// Such a field is never filled in: it is a password input that says a
	// value is stored, and offers to clear it.
	Secrets map[string]bool
	// Sections wraps each x-hm-group in a section of two-column pairs with a
	// hairline above its heading. Off, a group is only a heading.
	Sections bool
	// AddIcon is drawn before the label of a repeater's Add button. It is
	// trusted markup from the caller (an icon); empty draws none.
	AddIcon template.HTML
}

type opt struct {
	Value, Label string
	Selected     bool
}

type fld struct {
	Kind, ID, Name, Path, Label, Help, Placeholder string
	InputType, Value, Min, Max, Step, Pattern      string
	Rows                                           int
	MinLength, MaxLength, Len                      int
	Required, Disabled, Autofocus, Mono, Checked   bool
	Present                                        string // _present sentinel: "1" stored value, "default" absent (schema default shown)
	Keep                                           string // JSON of the keys the form cannot edit, round-tripped in a hidden input
	Multiple, Orderable                            bool
	Values                                         []string
	Options                                        []opt
	Err                                            string
	Described                                      string
	Children                                       []*fld
	Items                                          []*item
	ItemScalar                                     bool
	Count                                          int
	AddIcon                                        template.HTML
	ItemLabel                                      string // x-hm-item-label: what the Add button of a repeater adds
	EntitySchema                                   string
	Preview                                        string // image field: current URL
	Secret, SecretSet                              bool   // writeOnly: never shown; SecretSet when a value is stored
}

type item struct {
	Index   int
	Name    string
	Open    bool
	Keep    string
	Preview string
	Fields  []*fld
	Scalar  *fld
}

// richTextNames are field names that default to the rich text editor, like the
// Next admin (field-renderer.tsx RICH_TEXT_FIELD_NAMES).
var richTextNames = map[string]bool{
	"content": true, "body": true, "full_description": true, "bio": true, "description": true,
	"main_content": true, "page_content": true, "service_description": true, "summary": true,
	"overview": true, "details": true,
}

var (
	tplOnce sync.Once
	tpl     *template.Template
	tplErr  error
)

func templates() (*template.Template, error) {
	tplOnce.Do(func() {
		sub, err := fs.Sub(web.Admin(), "schemaform")
		if err != nil {
			tplErr = err
			return
		}
		tpl, tplErr = template.New("").Funcs(template.FuncMap{"add1": func(i int) int { return i + 1 }}).ParseFS(sub, "*.html")
	})
	return tpl, tplErr
}

// Render writes the form controls for schema, filled from values. The result is
// the trusted output of html/template (every dynamic string escaped); callers
// put it inside their own <form>. Submit buttons for repeaters (add, move,
// remove) are named _add./_move./_remove.; put a hidden default submit first in
// the form so Enter in a text field saves instead of moving an item.
func Render(schema json.RawMessage, values map[string]any, opt Options) (template.HTML, error) {
	root, err := ParseSchema(schema)
	if err != nil {
		return "", err
	}
	t, err := templates()
	if err != nil {
		return "", err
	}
	b := &builder{opt: opt}
	var out bytes.Buffer
	open, shut := "sf/group", ""
	if opt.Sections {
		open, shut = "sf/section", "sf/section-end"
	}
	heading := ""
	for _, p := range groupedProps(root.Props) {
		if g := strings.TrimSpace(p.Node.Group); g != heading {
			if heading != "" && shut != "" {
				if err := t.ExecuteTemplate(&out, shut, nil); err != nil {
					return "", err
				}
			}
			heading = g
			if err := t.ExecuteTemplate(&out, open, heading); err != nil {
				return "", err
			}
		}
		f := b.field(p.Name, p.Node, valueOf(values, p.Name, p.Node), root.isRequired(p.Name), p.Name, p.Name)
		if err := t.ExecuteTemplate(&out, "sf/field", f); err != nil {
			return "", err
		}
		out.WriteByte('\n')
	}
	if heading != "" && shut != "" {
		if err := t.ExecuteTemplate(&out, shut, nil); err != nil {
			return "", err
		}
	}
	return template.HTML(out.String()), nil //nolint:gosec // html/template output only
}

type builder struct{ opt Options }

// valueOf reads values[name], falling back to an x-hm-alias key.
func valueOf(values map[string]any, name string, n *Node) any {
	if v, ok := values[name]; ok {
		return v
	}
	for _, a := range n.Alias {
		if v, ok := values[a]; ok {
			return v
		}
	}
	return nil
}

func dashed(s string) string { return strings.ReplaceAll(s, ".", "-") }

// hintKey drops array indices: "faqs.2.question" -> "faqs.question". // gitleaks:allow (comment, not a key)
func hintKey(path string) string {
	var out []string
	for _, seg := range strings.Split(path, ".") {
		if _, err := strconv.Atoi(seg); err != nil {
			out = append(out, seg)
		}
	}
	return strings.Join(out, ".")
}

func asString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case bool:
		return strconv.FormatBool(x)
	}
	return fmt.Sprint(v)
}

func stringList(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			out = append(out, asString(e))
		}
		return out
	case string:
		if x != "" {
			return []string{x}
		}
	}
	return nil
}

func optionLabel(v string) string {
	words := strings.Fields(strings.ReplaceAll(v, "_", " "))
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// kind implements the dispatcher's precedence (field-renderer.tsx:96-403).
func (b *builder) kind(name string, n *Node, v any, hint UIHint) string {
	switch n.Type {
	case "string":
		if len(n.Enum) > 0 {
			return "select"
		}
		if n.WriteOnly {
			return "text"
		}
		if _, isObj := v.(map[string]any); isObj {
			return "object-synth"
		}
		if n.XHm == "html" {
			return "html"
		}
		if n.XMedia || (n.Format == "uri" && strings.Contains(strings.ToLower(name), "image")) {
			return "image"
		}
		if b.richText(name, n, hint) {
			return "richtext"
		}
		if hint.Widget == "textarea" || n.Format == "textarea" || (n.MaxLength != nil && *n.MaxLength > 200) {
			return "textarea"
		}
		return "text"
	case "number", "integer":
		return "number"
	case "boolean":
		return "checkbox"
	case "array":
		if n.XEntitySchema != "" {
			return "entityref"
		}
		if n.XHm == "lines" && plainStringItems(n.Items) {
			return "lines"
		}
		if hint.Widget == "tags" || plainStringItems(n.Items) {
			return "tags"
		}
		return "repeater"
	case "object":
		return "object"
	}
	return "text"
}

// plainStringItems reports whether array items are short single-line words,
// which edit as a comma separated tag list. Long text, rich text and
// addresses need a row each (a comma in a step must not split it).
func plainStringItems(it *Node) bool {
	if it == nil || it.Type != "string" || len(it.Props) > 0 || it.XMedia {
		return false
	}
	switch it.Format {
	case "textarea", "richtext", "uri":
		return false
	}
	return it.MaxLength == nil || *it.MaxLength <= 200
}

func (b *builder) richText(name string, n *Node, hint UIHint) bool {
	if hint.Widget == "richtext" || n.Format == "richtext" {
		return true
	}
	if hint.Widget == "textarea" || n.Format == "textarea" {
		return false
	}
	if richTextNames[strings.ToLower(name)] {
		return n.MaxLength == nil || *n.MaxLength >= 500
	}
	return false
}

// field builds the view model of one property. path is the dotted path without
// the Prefix; errPath is the key into Errors (same as path today, kept apart
// so a later layout can differ).
func (b *builder) field(name string, n *Node, v any, required bool, path, errPath string) *fld {
	hint := b.opt.UI[hintKey(path)]
	k := b.kind(name, n, v, hint)
	full := b.opt.Prefix + path
	f := &fld{
		Kind: k, ID: "sf-" + dashed(full), Name: full, Path: path, Label: n.label(name),
		Required: required, Disabled: b.opt.Disabled, Autofocus: hint.Autofocus,
		Help: firstNonEmpty(hint.Help, n.Description),
	}
	if msgs := b.opt.Errors[errPath]; len(msgs) > 0 {
		f.Err = strings.Join(msgs, " ")
	}
	var ids []string
	if f.Help != "" {
		ids = append(ids, f.ID+"-help")
	}
	if f.Err != "" {
		ids = append(ids, f.ID+"-error")
	}
	f.Described = strings.Join(ids, " ")

	switch k {
	case "select":
		f.Value = asString(v)
		for _, e := range n.Enum {
			f.Options = append(f.Options, opt{Value: e, Label: optionLabel(e), Selected: e == f.Value})
		}
	case "text":
		f.Value = asString(v)
		f.InputType = map[string]string{"email": "email", "uri": "url", "date": "date", "date-time": "datetime-local", "time": "time"}[n.Format]
		if f.InputType == "" {
			f.InputType = "text"
		}
		f.Placeholder = firstNonEmpty(hint.Placeholder, n.Description, "Enter "+f.Label)
		if n.WriteOnly {
			f.Secret, f.SecretSet = true, b.opt.Secrets[path]
			f.InputType, f.Value = "password", ""
			f.Placeholder = "Enter " + f.Label
			if f.SecretSet {
				f.Placeholder = "Stored. Leave blank to keep it."
			}
		}
		f.Pattern = n.Pattern
		if n.MinLength != nil {
			f.MinLength = *n.MinLength
		}
		if n.MaxLength != nil {
			f.MaxLength = *n.MaxLength
			f.Len = len([]rune(f.Value))
		}
	case "textarea", "richtext", "html":
		f.Value = asString(v)
		f.Mono = n.XHm == "code" || n.XHm == "css" || n.XHm == "html"
		f.Rows = 6
		switch {
		case k == "richtext":
			f.Rows = 10
		case k == "html" || n.XCode == "tall":
			f.Rows = 14
		}
		if n.XCode == "tall" {
			f.Rows = 22
		}
		f.Placeholder = firstNonEmpty(hint.Placeholder, n.Description)
		if n.MaxLength != nil {
			f.MaxLength = *n.MaxLength
			f.Len = len([]rune(f.Value))
		}
	case "image":
		f.Value = asString(v)
		f.Preview = f.Value
		f.Placeholder = firstNonEmpty(hint.Placeholder, "https://")
	case "number":
		f.Value = asString(v)
		f.InputType = "number"
		f.Step = "any"
		if n.Type == "integer" {
			f.Step = "1"
		}
		if n.Minimum != nil {
			f.Min = strconv.FormatFloat(*n.Minimum, 'f', -1, 64)
		}
		if n.Maximum != nil {
			f.Max = strconv.FormatFloat(*n.Maximum, 'f', -1, 64)
		}
	case "checkbox":
		f.Present = "1"
		if v == nil {
			// Absent in the document: show the schema default, and tell Decode
			// so that saving without touching it keeps the key absent.
			f.Present = "default"
			v = n.Default
		}
		f.Checked, _ = v.(bool)
	case "tags":
		f.Values = stringList(v)
		f.Value = strings.Join(f.Values, ", ")
	case "lines":
		f.Values = stringList(v)
		f.Value = strings.Join(f.Values, "\n")
		f.Rows = 4
		f.Placeholder = "One item per line"
	case "entityref":
		f.Values = stringList(v)
		f.Value = strings.Join(f.Values, ", ")
		f.EntitySchema = n.XEntitySchema
		f.Multiple = n.XMultiple == nil || *n.XMultiple
	case "object-synth":
		// A string-typed property holding an object (WordPress-imported "author"):
		// edit its sub-properties as strings instead of printing [object Object].
		f.Kind = "object"
		m := v.(map[string]any)
		keys := make([]string, 0, len(m))
		for key, val := range m {
			if _, isStr := val.(string); isStr {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			f.Children = append(f.Children, b.field(key, &Node{Type: "string", Title: Humanize(key)}, m[key], false, path+"."+key, errPath+"."+key))
		}
		// Numbers, nested objects and lists are not editable here; they ride along.
		f.Keep = b.keep(m, func(key string, val any) bool { _, isStr := val.(string); return !isStr })
	case "object":
		m, _ := v.(map[string]any)
		for _, p := range n.Props {
			f.Children = append(f.Children, b.field(p.Name, p.Node, valueOf(m, p.Name, p.Node), n.isRequired(p.Name), path+"."+p.Name, errPath+"."+p.Name))
		}
		f.Keep = b.keep(m, n.unknown)
	case "repeater":
		b.repeater(f, n, v, hint)
	}
	if ph := b.opt.Placeholders[hintKey(path)]; ph != "" && !f.Secret && (k == "text" || k == "textarea") {
		f.Placeholder = ph
	}
	return f
}

// keep serialises the entries of m that pick selects, for the hidden _keep
// input: Decode merges them back, so keys the form has no control for (legacy
// import fields, aliases of other schemas) survive a save even when repeater
// items were reordered in between. Nothing is emitted when the form is
// disabled (it cannot be saved).
func (b *builder) keep(m map[string]any, pick func(key string, val any) bool) string {
	if b.opt.Disabled {
		return ""
	}
	out := map[string]any{}
	for k, v := range m {
		if pick(k, v) {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return ""
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(raw)
}

// unknown reports a key that is neither a schema property nor one of its aliases.
func (n *Node) unknown(key string, _ any) bool {
	for _, p := range n.Props {
		if p.Name == key {
			return false
		}
		for _, a := range p.Node.Alias {
			if a == key {
				return false
			}
		}
	}
	return true
}

func (b *builder) repeater(f *fld, n *Node, v any, hint UIHint) {
	items, _ := v.([]any)
	f.Count = len(items)
	f.ItemLabel = n.ItemLabel
	f.AddIcon = b.opt.AddIcon
	f.Orderable = !hint.Unorderable && !b.opt.Disabled
	it := n.Items
	if it == nil {
		it = &Node{Type: "string"}
	}
	f.ItemScalar = !(it.Type == "object" && len(it.Props) > 0)
	openIdx := -1
	if rest, ok := strings.CutPrefix(b.opt.OpenPath, f.Path+"."); ok {
		seg, _, _ := strings.Cut(rest, ".")
		if i, err := strconv.Atoi(seg); err == nil {
			openIdx = i
		}
	}
	for i, raw := range items {
		ipath := f.Path + "." + strconv.Itoa(i)
		x := &item{Index: i, Name: b.opt.Prefix + ipath, Preview: fmt.Sprintf("Item %d", i+1)}
		if f.ItemScalar {
			x.Scalar = b.field(lastSeg(f.Path), it, raw, false, ipath, ipath)
			x.Scalar.Label = fmt.Sprintf("%s %d", f.Label, i+1)
			if s := asString(raw); s != "" {
				x.Preview = clip(s)
			}
		} else {
			m, _ := raw.(map[string]any)
			x.Keep = b.keep(m, it.unknown)
			for _, p := range it.Props {
				x.Fields = append(x.Fields, b.field(p.Name, p.Node, valueOf(m, p.Name, p.Node), it.isRequired(p.Name), ipath+"."+p.Name, ipath+"."+p.Name))
			}
			for _, key := range []string{"name", "title", "question", "metric", "label"} {
				if s := asString(m[key]); s != "" {
					x.Preview = clip(s)
					break
				}
			}
		}
		x.Open = i == openIdx || hasErrorUnder(b.opt.Errors, ipath)
		f.Items = append(f.Items, x)
	}
}

func clip(s string) string {
	r := []rune(s)
	if len(r) > 50 {
		return string(r[:50]) + "..."
	}
	return s
}

func hasErrorUnder(errs Errors, path string) bool {
	for k := range errs {
		if k == path || strings.HasPrefix(k, path+".") {
			return true
		}
	}
	return false
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// QueryKeys lists the form keys of the structural buttons present in form.
func structuralKeys(form url.Values) []string {
	var out []string
	for k := range form {
		if strings.HasPrefix(k, "_add.") || strings.HasPrefix(k, "_move.") || strings.HasPrefix(k, "_remove.") {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Structural reports whether the submission pressed an add/move/remove button:
// the handler then re-renders the (changed) form instead of saving it.
func Structural(form url.Values) bool { return len(structuralKeys(form)) > 0 }

func lastSeg(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[i+1:]
	}
	return path
}
