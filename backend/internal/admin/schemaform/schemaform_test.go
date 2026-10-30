package schemaform

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"hatamoto.dev/core/internal/admin/a11y"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (go test ./internal/admin/schemaform -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from its golden (-update to accept):\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

func themeSchemas(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "render", "themes", "default", "schemas", "*.json"))
	if err != nil || len(files) != 25 {
		t.Fatalf("expected the 25 default-theme section schemas, found %d (%v)", len(files), err)
	}
	out := map[string]json.RawMessage{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimSuffix(filepath.Base(f), ".json")] = b
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sample builds deterministic values exercising every property of a schema.
func sample(t *testing.T, root *Node) map[string]any {
	t.Helper()
	b := &builder{}
	var gen func(name string, n *Node) any
	gen = func(name string, n *Node) any {
		switch b.kind(name, n, nil, UIHint{}) {
		case "select":
			return n.Enum[len(n.Enum)-1]
		case "text":
			switch n.Format {
			case "email":
				return "a@example.com"
			case "uri":
				return "https://example.com/" + name
			case "date":
				return "2026-01-02"
			case "date-time":
				return "2026-01-02T10:30"
			case "time":
				return "10:30"
			}
			s := "Sample " + name
			if n.MaxLength != nil && len(s) > *n.MaxLength {
				s = s[:*n.MaxLength]
			}
			return s
		case "image":
			return "https://example.com/img/" + name + ".png"
		case "textarea":
			return "Line one of " + name + "\nLine two"
		case "richtext", "html":
			return "<p>Hello <strong>" + name + "</strong> &amp; \"friends\"</p>\n<p>Second</p>"
		case "number":
			if n.Type == "integer" {
				return int64(7)
			}
			return 3.5
		case "checkbox":
			return true
		case "tags":
			return []any{"alpha", "beta"}
		case "lines":
			return []any{"alpha", "beta, with comma"}
		case "entityref":
			return []any{"6f1c3f5e-1d6a-4a41-9a77-0a9bd1d3a001"}
		case "object":
			m := map[string]any{}
			for _, p := range n.Props {
				m[p.Name] = gen(p.Name, p.Node)
			}
			return m
		case "repeater":
			it := n.Items
			if it == nil {
				return []any{"x"}
			}
			return []any{gen(name, it), gen(name, it)}
		}
		t.Fatalf("no sample for %s", name)
		return nil
	}
	out := map[string]any{}
	for _, p := range root.Props {
		out[p.Name] = gen(p.Name, p.Node)
	}
	return out
}

// formFromHTML builds the url.Values a browser would submit: successful
// controls only (unchecked boxes and disabled controls omitted; buttons are not
// the submitter).
func formFromHTML(t *testing.T, doc string) url.Values {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{}
	attr := func(n *html.Node, k string) (string, bool) {
		for _, a := range n.Attr {
			if a.Key == k {
				return a.Val, true
			}
		}
		return "", false
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			name, hasName := attr(n, "name")
			_, disabled := attr(n, "disabled")
			if hasName && !disabled {
				switch n.Data {
				case "input":
					typ, _ := attr(n, "type")
					val, _ := attr(n, "value")
					switch typ {
					case "checkbox", "radio":
						if _, ok := attr(n, "checked"); ok {
							if val == "" {
								val = "on"
							}
							form.Add(name, val)
						}
					case "submit", "button", "image", "reset", "file":
					default:
						form.Add(name, val)
					}
				case "textarea":
					var sb strings.Builder
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						sb.WriteString(c.Data)
					}
					form.Add(name, sb.String())
				case "select":
					chosen, first := "", true
					found := false
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Data != "option" {
							continue
						}
						v, _ := attr(c, "value")
						if first {
							chosen, first = v, false
						}
						if _, sel := attr(c, "selected"); sel && !found {
							chosen, found = v, true
						}
					}
					form.Add(name, chosen)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return form
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func wrapForm(body string) string {
	return `<form method="post"><input type="hidden" name="_csrf" value="t">` + body + `<button type="submit">Save</button></form>`
}

// Every default-theme section schema renders empty and populated, the output
// passes the a11y checker, and Decode(form-of-Render(values)) gives the values
// back (the round trip).
func TestDefaultThemeSchemas(t *testing.T) {
	schemas := themeSchemas(t)
	for _, name := range sortedKeys(schemas) {
		raw := schemas[name]
		t.Run(name, func(t *testing.T) {
			root, err := ParseSchema(raw)
			if err != nil {
				t.Fatal(err)
			}
			empty, err := Render(raw, nil, Options{Prefix: "content."})
			if err != nil {
				t.Fatal(err)
			}
			vals := sample(t, root)
			full, err := Render(raw, vals, Options{Prefix: "content."})
			if err != nil {
				t.Fatal(err)
			}
			golden(t, "schemas/"+name+".empty.html", string(empty))
			golden(t, "schemas/"+name+".full.html", string(full))
			for _, doc := range []string{string(empty), string(full)} {
				if issues := a11y.Check(wrapForm(doc), a11y.Options{}); len(issues) > 0 {
					t.Errorf("a11y: %s", strings.Join(issues, "; "))
				}
			}

			got, errs := Decode(raw, formFromHTML(t, string(full)), nil, Options{Prefix: "content."})
			if len(errs) > 0 {
				t.Fatalf("decode errors: %v", errs)
			}
			if mustJSON(t, got) != mustJSON(t, vals) {
				t.Errorf("round trip changed the values\nwant %s\n got %s", mustJSON(t, vals), mustJSON(t, got))
			}

			// Empty form: nothing but explicit booleans survive.
			gotEmpty, errs := Decode(raw, formFromHTML(t, string(empty)), nil, Options{Prefix: "content."})
			if len(errs) > 0 {
				t.Fatalf("empty decode errors: %v", errs)
			}
			for k, v := range gotEmpty {
				if b, ok := v.(bool); !ok || b {
					t.Errorf("empty form decoded to %s=%v", k, v)
				}
			}
		})
	}
}

// Precedence of the field dispatcher, one case per rule of the Next renderer.
func TestFieldKindPrecedence(t *testing.T) {
	maxl := func(n int) *int { return &n }
	b := &builder{}
	str := func(mod func(n *Node)) *Node {
		n := &Node{Type: "string"}
		if mod != nil {
			mod(n)
		}
		n.mapHint() // as ParseSchema would
		return n
	}
	cases := []struct {
		name string
		node *Node
		val  any
		hint UIHint
		want string
	}{
		{"title", str(nil), nil, UIHint{}, "text"},
		{"enum wins over richtext name", str(func(n *Node) { n.Enum = []string{"a"} }), nil, UIHint{}, "select"},
		{"object value in a string property", str(nil), map[string]any{"name": "x"}, UIHint{}, "object-synth"},
		{"html", str(func(n *Node) { n.XHm = "html" }), nil, UIHint{}, "html"},
		{"html beats image", str(func(n *Node) { n.XHm = "html"; n.XMedia = true }), nil, UIHint{}, "html"},
		{"x-media", str(func(n *Node) { n.XMedia = true }), nil, UIHint{}, "image"},
		{"x-hm-field image", str(func(n *Node) { n.XHm = "image" }), nil, UIHint{}, "image"},
		{"uri with image in the name", str(func(n *Node) { n.Format = "uri" }), nil, UIHint{}, "image"},
		{"hero_image", str(func(n *Node) { n.Format = "uri" }), nil, UIHint{}, "image"},
		{"link", str(func(n *Node) { n.Format = "uri" }), nil, UIHint{}, "text"},
		{"content", str(nil), nil, UIHint{}, "richtext"},
		{"Content", str(nil), nil, UIHint{}, "richtext"},
		{"summary", str(func(n *Node) { n.MaxLength = maxl(499) }), nil, UIHint{}, "textarea"},
		{"summary", str(func(n *Node) { n.MaxLength = maxl(500) }), nil, UIHint{}, "richtext"},
		{"body", str(func(n *Node) { n.MaxLength = maxl(100) }), nil, UIHint{}, "text"},
		{"body", str(func(n *Node) { n.Format = "textarea" }), nil, UIHint{}, "textarea"},
		{"body", str(nil), nil, UIHint{Widget: "textarea"}, "textarea"},
		{"note", str(nil), nil, UIHint{Widget: "richtext"}, "richtext"},
		{"note", str(func(n *Node) { n.Format = "richtext" }), nil, UIHint{}, "richtext"},
		{"note", str(func(n *Node) { n.XHm = "richtext" }), nil, UIHint{}, "richtext"},
		{"note", str(func(n *Node) { n.MaxLength = maxl(200) }), nil, UIHint{}, "text"},
		{"note", str(func(n *Node) { n.MaxLength = maxl(201) }), nil, UIHint{}, "textarea"},
		{"snippet", str(func(n *Node) { n.XHm = "code" }), nil, UIHint{}, "textarea"},
		{"styles", str(func(n *Node) { n.XHm = "css" }), nil, UIHint{}, "textarea"},
		// Next's mapHints runs before dispatch: code/css turn the name-based rich
		// text rule off, url turns the "image" name rule on.
		{"body", str(func(n *Node) { n.XHm = "code" }), nil, UIHint{}, "textarea"},
		{"content", str(func(n *Node) { n.XHm = "css" }), nil, UIHint{}, "textarea"},
		{"description", str(func(n *Node) { n.XHm = "code" }), nil, UIHint{}, "textarea"},
		{"hero_image", str(func(n *Node) { n.XHm = "url" }), nil, UIHint{}, "image"},
		{"link", str(func(n *Node) { n.XHm = "url" }), nil, UIHint{}, "text"},
		{"n", &Node{Type: "integer"}, nil, UIHint{}, "number"},
		{"n", &Node{Type: "number"}, nil, UIHint{}, "number"},
		{"flag", &Node{Type: "boolean"}, nil, UIHint{}, "checkbox"},
		{"refs", &Node{Type: "array", XEntitySchema: "post", Items: &Node{Type: "string"}}, nil, UIHint{}, "entityref"},
		{"tags", &Node{Type: "array", Items: &Node{Type: "string"}}, nil, UIHint{}, "tags"},
		{"tags", &Node{Type: "array", Items: &Node{Type: "object", Props: []Prop{{"a", &Node{Type: "string"}}}}}, nil, UIHint{Widget: "tags"}, "tags"},
		{"rows", &Node{Type: "array", Items: &Node{Type: "object", Props: []Prop{{"a", &Node{Type: "string"}}}}}, nil, UIHint{}, "repeater"},
		{"nums", &Node{Type: "array", Items: &Node{Type: "number"}}, nil, UIHint{}, "repeater"},
		{"o", &Node{Type: "object"}, nil, UIHint{}, "object"},
		{"x", &Node{}, nil, UIHint{}, "text"},
	}
	for i, c := range cases {
		name := c.name
		if c.name == "title" || c.name == "hero_image" || c.name == "link" {
			name = c.name
		}
		if got := b.kind(name, c.node, c.val, c.hint); got != c.want {
			t.Errorf("case %d (%s %+v): %s, want %s", i, c.name, c.node.Type, got, c.want)
		}
	}
	// "image" in the field NAME is what triggers the uri rule: a plain uri named
	// link stays text, hero_image becomes the image field.
	if got := b.kind("hero_image", str(func(n *Node) { n.Format = "uri" }), nil, UIHint{}); got != "image" {
		t.Errorf("hero_image: %s", got)
	}
}

// Field-level HTML details of the port: attributes, counters, options, tags.
func TestRenderedControls(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["email"],"properties":{
		"email":{"type":"string","format":"email","title":"Email","minLength":3,"maxLength":80,"pattern":"^.+@.+$"},
		"kind":{"type":"string","enum":["blog_post","news"]},
		"age":{"type":"integer","minimum":1,"maximum":99},
		"price":{"type":"number"},
		"on":{"type":"boolean","title":"Enabled"},
		"labels":{"type":"array","items":{"type":"string"}},
		"code":{"type":"string","x-hm-field":"code","x-code-size":"tall"},
		"hero_image":{"type":"string","format":"uri"}}}`)
	out, err := Render(schema, map[string]any{"email": "a@b.co", "kind": "news", "labels": []any{"x", "y"}, "hero_image": "https://e.test/i.png"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`type="email"`, `minlength="3"`, `maxlength="80"`, `pattern="^.&#43;@.&#43;$"`, `required`, `6/80`,
		`<option value="">Select Kind</option>`, `<option value="blog_post">Blog Post</option>`, `<option value="news" selected>News</option>`,
		`type="number" id="sf-age" name="age" value="" step="1" min="1" max="99"`, `step="any"`,
		`name="_present.on"`, `value="x, y"`, `data-hm-tags`, `class="hm-field__control hm-mono"`, `rows="22"`,
		`<img class="hm-sf-image" src="https://e.test/i.png" alt="" loading="lazy">`, `data-hm-media-picker="sf-hero_image"`, `placeholder="Enter Email"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
}

func TestRichTextAndHTMLHooks(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"body":{"type":"string"},"markup":{"type":"string","x-hm-field":"html"}}}`)
	out, _ := Render(schema, map[string]any{"body": "<script>alert(1)</script>"}, Options{})
	s := string(out)
	if !strings.Contains(s, `data-hm-editor="tiptap"`) || !strings.Contains(s, `data-hm-editor="html"`) {
		t.Errorf("editor hooks missing:\n%s", s)
	}
	if strings.Contains(s, "<script>") || !strings.Contains(s, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("content was not escaped:\n%s", s)
	}
}

func TestRepeaterBehaviour(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"faqs":{"type":"array","title":"Questions","items":{"type":"object","properties":{"question":{"type":"string"},"answer":{"type":"string","x-hm-field":"richtext"}}}}}}`)
	vals := map[string]any{"faqs": []any{
		map[string]any{"question": "One?", "answer": "A"},
		map[string]any{"question": "Two?", "answer": "B"},
		map[string]any{"answer": "no title"},
	}}
	out, _ := Render(schema, vals, Options{OpenPath: "faqs.1.question", Errors: Errors{"faqs.2.question": {"Question is required"}}})
	s := string(out)
	for _, want := range []string{
		`<summary class="hm-repeater__summary">One?</summary>`, `<summary class="hm-repeater__summary">Item 3</summary>`,
		`name="faqs.1.question"`, `data-hm-sortable`, `name="_move.faqs" value="0:up"`, `name="_remove.faqs" value="2"`, `name="_add.faqs"`, `(3 items)`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Only item 1 (OpenPath) and item 2 (has an error) are expanded.
	if got := strings.Count(s, "<details open>"); got != 2 {
		t.Errorf("%d expanded items, want 2:\n%s", got, s)
	}
	// ui:orderable false removes reordering but keeps remove.
	out, _ = Render(schema, vals, Options{UI: map[string]UIHint{"faqs": {Unorderable: true}}})
	if strings.Contains(string(out), "_move.faqs") || strings.Contains(string(out), "data-hm-sortable") || !strings.Contains(string(out), "_remove.faqs") {
		t.Error("orderable=false not honoured")
	}
	// Disabled forms offer no mutation buttons.
	out, _ = Render(schema, vals, Options{Disabled: true})
	if strings.Contains(string(out), "_remove.") || strings.Contains(string(out), "_add.") {
		t.Error("disabled form still has add/remove buttons")
	}
}

func TestStructuralButtons(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"faqs":{"type":"array","items":{"type":"object","properties":{"question":{"type":"string"}}}}}}`)
	base := func() url.Values {
		return url.Values{"_present.faqs": {"1"}, "faqs.0.question": {"A"}, "faqs.1.question": {"B"}, "faqs.2.question": {"C"}}
	}
	question := func(v map[string]any) string {
		var qs []string
		for _, it := range v["faqs"].([]any) {
			q, _ := it.(map[string]any)["question"].(string)
			qs = append(qs, q)
		}
		return strings.Join(qs, ",")
	}
	cases := []struct {
		key, val, want string
	}{
		{"_move.faqs", "1:up", "B,A,C"}, {"_move.faqs", "1:down", "A,C,B"}, {"_move.faqs", "0:up", "A,B,C"}, {"_move.faqs", "2:down", "A,B,C"},
		{"_remove.faqs", "1", "A,C"}, {"_add.faqs", "1", "A,B,C,"},
	}
	for _, c := range cases {
		f := base()
		f.Set(c.key, c.val)
		if !Structural(f) {
			t.Fatal("not structural")
		}
		got, errs := Decode(schema, f, nil, Options{})
		if len(errs) > 0 || question(got) != c.want {
			t.Errorf("%s=%s: %q (%v), want %q", c.key, c.val, question(got), errs, c.want)
		}
	}
	if Structural(base()) {
		t.Error("plain submit reported as structural")
	}
}

func TestDecodeMalformedInput(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{
		"n":{"type":"integer"},"f":{"type":"number"},"k":{"type":"string","enum":["a","b"]},
		"refs":{"type":"array","x-entity-schema":"post","items":{"type":"string"}},
		"one":{"type":"array","x-entity-schema":"post","x-multiple":false,"items":{"type":"string"}},
		"rows":{"type":"array","items":{"type":"object","properties":{"t":{"type":"string"}}}}}}`)
	id := "6f1c3f5e-1d6a-4a41-9a77-0a9bd1d3a001"
	id2 := "6f1c3f5e-1d6a-4a41-9a77-0a9bd1d3a002"
	cases := map[string]struct {
		form url.Values
		path string
		msg  string
	}{
		"non-numeric int":   {url.Values{"n": {"abc"}}, "n", "Enter a whole number"},
		"decimal int":       {url.Values{"n": {"1.5"}}, "n", "Enter a whole number"},
		"non-numeric float": {url.Values{"f": {"1,5"}}, "f", "Enter a number"},
		"unknown enum":      {url.Values{"k": {"zzz"}}, "k", "Choose one of the options"},
		"bad uuid":          {url.Values{"_present.refs": {"1"}, "refs": {"nope"}}, "refs", "Enter valid entity IDs"},
		"too many for one":  {url.Values{"_present.one": {"1"}, "one": {id + "," + id2}}, "one", "Choose only one"},
		"item bomb":         {bomb(MaxItems + 1), "rows", "Add at most 500 items"},
		"absurd index":      {url.Values{"_present.rows": {"1"}, "rows.99999999.t": {"x"}}, "rows", "Add at most 500 items"},
		"too many tags":     {tagBomb(MaxItems + 1), "refs", "Add at most 500 items"},
	}
	for name, c := range cases {
		prev := map[string]any{"keep": "me", "n": int64(1)}
		got, errs := Decode(schema, c.form, prev, Options{})
		if len(errs[c.path]) == 0 || errs[c.path][0] != c.msg {
			t.Errorf("%s: errors %v, want %q at %s", name, errs, c.msg, c.path)
		}
		if got["keep"] != "me" {
			t.Errorf("%s: unknown key lost", name)
		}
		if name == "non-numeric int" && got["n"] != int64(1) {
			t.Errorf("%s: a rejected value must not overwrite the stored one: %v", name, got["n"])
		}
	}
	// Sparse indices are sorted numerically and compacted; blank items dropped.
	got, errs := Decode(schema, url.Values{"_present.rows": {"1"}, "rows.10.t": {"ten"}, "rows.2.t": {"two"}, "rows.5.t": {""}}, nil, Options{})
	if len(errs) > 0 || mustJSON(t, got["rows"]) != `[{"t":"two"},{"t":"ten"}]` {
		t.Errorf("sparse: %s %v", mustJSON(t, got["rows"]), errs)
	}
	// _present without items empties the list.
	got, _ = Decode(schema, url.Values{"_present.rows": {"1"}}, map[string]any{"rows": []any{map[string]any{"t": "old"}}}, Options{})
	if _, ok := got["rows"]; ok {
		t.Error("an emptied repeater must remove the key")
	}
	// Fields the form does not carry are left alone.
	got, _ = Decode(schema, url.Values{}, map[string]any{"n": int64(5), "rows": []any{1}}, Options{})
	if got["n"] != int64(5) || got["rows"] == nil {
		t.Errorf("untouched fields changed: %v", got)
	}
}

func bomb(n int) url.Values {
	v := url.Values{"_present.rows": {"1"}}
	for i := 0; i < n; i++ {
		v.Set(fmt.Sprintf("rows.%d.t", i), "x")
	}
	return v
}

func tagBomb(n int) url.Values {
	var ids []string
	for i := 0; i < n; i++ {
		ids = append(ids, fmt.Sprintf("t%d", i))
	}
	return url.Values{"_present.refs": {"1"}, "refs": {strings.Join(ids, ",")}}
}

// Saving must never destroy content the schema does not list, and editing a
// canonical property drops the legacy alias that would shadow it.
func TestUnknownKeysAndAliases(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"heading":{"type":"string","x-hm-alias":["headline"]},"faqs":{"type":"array","x-hm-alias":["items"],"items":{"type":"object","properties":{"q":{"type":"string"}}}}}}`)
	prev := map[string]any{"headline": "Old", "wp_author": map[string]any{"id": 4}, "items": []any{map[string]any{"q": "old?"}}}
	// Render reads the alias value.
	out, _ := Render(schema, prev, Options{})
	if !strings.Contains(string(out), `value="Old"`) || !strings.Contains(string(out), `value="old?"`) {
		t.Errorf("alias values not shown:\n%s", out)
	}
	got, errs := Decode(schema, url.Values{"heading": {"New"}, "_present.faqs": {"1"}, "faqs.0.q": {"new?"}}, prev, Options{})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	want := `{"faqs":[{"q":"new?"}],"heading":"New","wp_author":{"id":4}}`
	if mustJSON(t, got) != want {
		t.Errorf("got %s want %s", mustJSON(t, got), want)
	}
	if reflect.DeepEqual(prev, got) || prev["headline"] != "Old" {
		t.Error("Decode must not mutate prev")
	}
}

// A string property holding an object (WordPress import) is editable.
func TestObjectValueInStringProperty(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"author":{"type":"string"}}}`)
	out, _ := Render(schema, map[string]any{"author": map[string]any{"display_name": "Jo", "id": "4"}}, Options{})
	if !strings.Contains(string(out), `name="author.display_name"`) || !strings.Contains(string(out), "Display name") {
		t.Errorf("synthetic object not rendered:\n%s", out)
	}
}

func TestRenderEscapesEverything(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"t":{"type":"string","title":"<i>T</i>","description":"\"><script>x</script>"}}}`)
	out, _ := Render(schema, map[string]any{"t": `"><img src=x onerror=alert(1)>`}, Options{Errors: Errors{"t": {"<b>bad</b>"}}})
	for _, bad := range []string{"<script>", "<img", "<i>T", "<b>bad"} {
		if strings.Contains(string(out), bad) {
			t.Errorf("unescaped %q in\n%s", bad, out)
		}
	}
}

// html/template output is the only HTML this package may produce.
func TestOnlyOneTrustedHTMLConversion(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	n := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		n += strings.Count(string(b), "template.HTML(")
	}
	if n != 1 {
		t.Fatalf("%d template.HTML( conversions in schemaform, want exactly 1 (Render's return)", n)
	}
}

func TestValidateMessages(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["title","contact"],"properties":{
		"title":{"type":"string","title":"Title","minLength":3,"maxLength":5},
		"slug":{"type":"string","pattern":"^[a-z]+$"},
		"email":{"type":"string","format":"email"},
		"age":{"type":"integer","minimum":18,"maximum":99},
		"kind":{"type":"string","enum":["a","b"]},
		"n":{"type":"number"},
		"tags":{"type":"array","minItems":2,"maxItems":3,"items":{"type":"string"}},
		"contact":{"type":"object","required":["phone"],"properties":{"phone":{"type":"string","title":"Phone number"}}},
		"faqs":{"type":"array","items":{"type":"object","required":["q"],"properties":{"q":{"type":"string","title":"Question"}}}}}}`)
	attrs := json.RawMessage(`{"title":"ab","slug":"NOPE","email":"x","age":10,"kind":"z","n":"str","tags":["a"],"contact":{},"faqs":[{"q":"ok"},{}]}`)
	errs := Validate(schema, attrs)
	want := map[string]string{
		"title":         "Use at least 3 characters",
		"slug":          "Slug is not in the expected format",
		"email":         "Enter a valid email address",
		"age":           "Enter a number of at least 18",
		"kind":          "Choose one of the options",
		"n":             "Enter a number",
		"tags":          "Add at least 2 items",
		"contact.phone": "Phone number is required",
		"faqs.1.q":      "Question is required",
	}
	for path, msg := range want {
		if len(errs[path]) == 0 || errs[path][0] != msg {
			t.Errorf("%s: got %v, want %q", path, errs[path], msg)
		}
	}
	if errs2 := Validate(schema, json.RawMessage(`{"contact":{"phone":"1"},"title":"abcd"}`)); len(errs2) != 0 {
		t.Errorf("valid document reported errors: %v", errs2)
	}
	if e := Validate(schema, json.RawMessage(`{"title":"toolong!","contact":{"phone":"1"}}`)); len(e["title"]) == 0 || e["title"][0] != "Use at most 5 characters" {
		t.Errorf("maxLength: %v", e)
	}
	if e := Validate(schema, json.RawMessage(`{"title":"abcd","contact":{"phone":"1"},"tags":["a","b","c","d"]}`)); len(e["tags"]) == 0 || e["tags"][0] != "Add at most 3" {
		t.Errorf("maxItems: %v", e)
	}
}

// roundTrip renders values, "submits" the rendered form unchanged, and decodes it.
func roundTrip(t *testing.T, schema string, values map[string]any, opt Options) map[string]any {
	t.Helper()
	doc, err := Render(json.RawMessage(schema), values, opt)
	if err != nil {
		t.Fatal(err)
	}
	got, errs := Decode(json.RawMessage(schema), formFromHTML(t, string(doc)), values, opt)
	if len(errs) > 0 {
		t.Fatalf("decode errors: %v", errs)
	}
	return got
}

func TestObjectInStringPropertyIsEditable(t *testing.T) {
	schema := `{"type":"object","properties":{"author":{"type":"string"},"faqs":{"type":"array","items":{"type":"object","properties":{"q":{"type":"string"},"by":{"type":"string"}}}}}}`
	values := map[string]any{
		"author": map[string]any{"display_name": "Jo", "id": float64(4), "links": []any{"a"}},
		"faqs":   []any{map[string]any{"q": "Why?", "by": map[string]any{"display_name": "Al", "id": "9"}}},
	}
	if got := roundTrip(t, schema, values, Options{}); mustJSON(t, got) != mustJSON(t, values) {
		t.Fatalf("round trip changed values\nwant %s\n got %s", mustJSON(t, values), mustJSON(t, got))
	}
	// Edit the object's sub-fields (top level, and inside the repeater item).
	doc, _ := Render(json.RawMessage(schema), values, Options{})
	form := formFromHTML(t, string(doc))
	form.Set("author.display_name", "Joanna")
	form.Set("faqs.0.by.display_name", "Alan")
	got, errs := Decode(json.RawMessage(schema), form, values, Options{})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	want := `{"author":{"display_name":"Joanna","id":4,"links":["a"]},"faqs":[{"by":{"display_name":"Alan","id":"9"},"q":"Why?"}]}`
	if mustJSON(t, got) != want {
		t.Errorf("edit lost\nwant %s\n got %s", want, mustJSON(t, got))
	}
	// Emptying one sub-field removes just that key.
	form.Set("author.display_name", "")
	got, _ = Decode(json.RawMessage(schema), form, values, Options{})
	if a, _ := got["author"].(map[string]any); a["display_name"] != nil || a["id"] != float64(4) {
		t.Errorf("author after clearing: %v", got["author"])
	}
}

func TestUnknownKeysSurviveInRepeatersAndNestedObjects(t *testing.T) {
	schema := `{"type":"object","properties":{"faqs":{"type":"array","items":{"type":"object","properties":{"q":{"type":"string"}}}},"cta":{"type":"object","properties":{"label":{"type":"string"},"inner":{"type":"object","properties":{"x":{"type":"string"}}}}}}}`
	values := map[string]any{
		"faqs": []any{map[string]any{"q": "one", "id": "wp-1"}, map[string]any{"q": "two", "legacy": map[string]any{"k": true}}},
		"cta":  map[string]any{"label": "Go", "wp_id": float64(7), "inner": map[string]any{"x": "y", "wp_inner": "z"}},
	}
	if got := roundTrip(t, schema, values, Options{}); mustJSON(t, got) != mustJSON(t, values) {
		t.Fatalf("round trip dropped unknown keys\nwant %s\n got %s", mustJSON(t, values), mustJSON(t, got))
	}
	// Moving item 0 down keeps each item's own unknown keys with it, even
	// though the handler's prev would line up by the old index.
	doc, _ := Render(json.RawMessage(schema), values, Options{})
	form := formFromHTML(t, string(doc))
	form.Set("_move.faqs", "0:down")
	got, _ := Decode(json.RawMessage(schema), form, values, Options{})
	items := got["faqs"].([]any)
	if items[0].(map[string]any)["q"] != "two" || items[0].(map[string]any)["legacy"] == nil || items[1].(map[string]any)["id"] != "wp-1" {
		t.Errorf("unknown keys did not follow their items: %s", mustJSON(t, got))
	}
	// A structural re-render followed by a save keeps them too.
	re, _ := Render(json.RawMessage(schema), got, Options{})
	got2, _ := Decode(json.RawMessage(schema), formFromHTML(t, string(re)), values, Options{})
	if mustJSON(t, got2) != mustJSON(t, got) {
		t.Errorf("second round differs\n%s\n%s", mustJSON(t, got), mustJSON(t, got2))
	}
}

func TestBooleanDefaultsAndAbsentVersusUnchecked(t *testing.T) {
	schema := `{"type":"object","properties":{"show_map":{"type":"boolean","default":true},"plain":{"type":"boolean"},"rows":{"type":"array","items":{"type":"object","properties":{"included":{"type":"boolean","default":true},"text":{"type":"string"}}}}}}`
	// Absent + default true: the box shows checked, and saving untouched keeps the key absent.
	doc, _ := Render(json.RawMessage(schema), map[string]any{}, Options{})
	if !strings.Contains(string(doc), `id="sf-show_map" name="show_map" value="true" checked`) {
		t.Errorf("default true not shown checked:\n%s", doc)
	}
	got, _ := Decode(json.RawMessage(schema), formFromHTML(t, string(doc)), map[string]any{}, Options{})
	if _, ok := got["show_map"]; ok {
		t.Errorf("an untouched default was written: %v", got)
	}
	// Unchecking writes an explicit false (unchecked is not absent)...
	form := formFromHTML(t, string(doc))
	form.Del("show_map")
	got, _ = Decode(json.RawMessage(schema), form, map[string]any{}, Options{})
	if got["show_map"] != false {
		t.Errorf("unchecked default-true box: %v", got)
	}
	// ...and an explicit false stored in the document stays false when saved.
	stored := map[string]any{"show_map": false, "plain": true}
	if got := roundTrip(t, schema, stored, Options{}); mustJSON(t, got) != mustJSON(t, stored) {
		t.Errorf("explicit booleans changed: %s", mustJSON(t, got))
	}
	// Checking a default-false box writes true.
	form = formFromHTML(t, string(doc))
	form.Set("plain", "true")
	got, _ = Decode(json.RawMessage(schema), form, map[string]any{}, Options{})
	if got["plain"] != true {
		t.Errorf("checked: %v", got)
	}
}

func TestAddedRepeaterItemStartsWithSchemaDefaults(t *testing.T) {
	schema := `{"type":"object","properties":{"rows":{"type":"array","items":{"type":"object","properties":{"included":{"type":"boolean","default":true},"text":{"type":"string"},"tier":{"type":"string","default":"free"}}}}}}`
	form := url.Values{"_present.rows": {"1"}, "_add.rows": {"1"}}
	got, _ := Decode(json.RawMessage(schema), form, nil, Options{})
	if mustJSON(t, got) != `{"rows":[{"included":true,"tier":"free"}]}` {
		t.Errorf("new item: %s", mustJSON(t, got))
	}
	doc, _ := Render(json.RawMessage(schema), got, Options{})
	if !strings.Contains(string(doc), `name="rows.0.included" value="true" checked`) || !strings.Contains(string(doc), `value="free"`) {
		t.Errorf("defaults not shown in the new item:\n%s", doc)
	}
}

func TestDisabledFormPostsNothingThatCouldClearValues(t *testing.T) {
	schema := `{"type":"object","properties":{"flag":{"type":"boolean"},"tags":{"type":"array","items":{"type":"string"}},"refs":{"type":"array","x-entity-schema":"post","items":{"type":"string"}},"rows":{"type":"array","items":{"type":"object","properties":{"q":{"type":"string"},"extra":{"type":"string"}}}},"author":{"type":"string"}}}`
	values := map[string]any{"flag": true, "tags": []any{"a", "b"}, "refs": []any{"11111111-1111-1111-1111-111111111111"}, "rows": []any{map[string]any{"q": "x", "id": 1}}, "author": map[string]any{"display_name": "Jo"}}
	doc, _ := Render(json.RawMessage(schema), values, Options{Disabled: true})
	form := formFromHTML(t, string(doc)) // like a browser: disabled controls do not post
	for k := range form {
		if strings.HasPrefix(k, "_present.") || strings.HasPrefix(k, "_keep.") {
			t.Errorf("disabled form still posts %s", k)
		}
	}
	got, errs := Decode(json.RawMessage(schema), form, values, Options{Disabled: true})
	if len(errs) > 0 || mustJSON(t, got) != mustJSON(t, values) {
		t.Errorf("saving a disabled form changed values: %s %v", mustJSON(t, got), errs)
	}
}

// x-hm-field "lines" keeps commas inside an item; plain tag fields still split on them.
func TestLinesFieldKeepsCommas(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{
		"strip":{"type":"array","x-hm-field":"lines","items":{"type":"string"}},
		"tags":{"type":"array","items":{"type":"string"}}}}`)
	vals := map[string]any{"strip": []any{"One Go binary", "htmx admin, no Node"}, "tags": []any{"a", "b"}}
	out, err := Render(schema, vals, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"<textarea", `name="strip"`, "One Go binary\nhtmx admin, no Node</textarea>", `value="a, b"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if strings.Contains(s, `name="strip" value=`) {
		t.Errorf("lines field rendered as a text input:\n%s", s)
	}
	got, errs := Decode(schema, formFromHTML(t, s), nil, Options{})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if mustJSON(t, got) != mustJSON(t, vals) {
		t.Errorf("round trip changed values: %s", mustJSON(t, got))
	}
	form := url.Values{"_present.strip": {"1"}, "strip": {"a, b\r\n\r\n a, b \nc,d\na, b"}, "_present.tags": {"1"}, "tags": {"x, y\nz"}}
	got, errs = Decode(schema, form, nil, Options{})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if want := `{"strip":["a, b","c,d"],"tags":["x","y","z"]}`; mustJSON(t, got) != want {
		t.Errorf("got %s, want %s", mustJSON(t, got), want)
	}
}

func TestPlaceholdersOverrideTextAndTextareaOnly(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{
		"phone":{"type":"string"},
		"note":{"type":"string","format":"textarea"},
		"kind":{"type":"string","enum":["a","b"]},
		"rows":{"type":"array","items":{"type":"object","properties":{"label":{"type":"string"}}}}}}`)
	out, err := Render(schema, map[string]any{"rows": []any{map[string]any{"label": ""}}}, Options{
		Placeholders: map[string]string{
			"phone": "inherited phone", "note": "inherited note", "kind": "inherited kind", "rows.label": "inherited label",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`placeholder="inherited phone"`, `placeholder="inherited note"`, `placeholder="inherited label"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if strings.Contains(s, "inherited kind") {
		t.Errorf("a select must not carry a placeholder override:\n%s", s)
	}
	plain, err := Render(schema, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plain), `placeholder="Enter Phone"`) {
		t.Errorf("without an override the default placeholder stays:\n%s", plain)
	}
}
