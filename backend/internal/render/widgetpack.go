package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html/template"
	"maps"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"text/template/parse"

	"golang.org/x/net/html"

	"hatamoto.dev/core/internal/builder"
)

// Plugin widget file limits.
const (
	maxPackHTML   = 16 << 10
	maxPackCSS    = 16 << 10
	maxPackIsland = 32 << 10
)

// PackWidget is the view side of one plugin widget: the files of
// widgets/<name>/. Kind is the full kind (<plugin>_<name>).
type PackWidget struct {
	Kind              string
	HTML, CSS, Island []byte
}

// PackFiles are the files of one plugin widget, as ValidatePackWidget reads them.
type PackFiles struct {
	HTML, CSS, Island []byte
}

// ValidatePackWidget checks the files of the plugin widget of kind. It never
// mutates global state. A template sees nothing but its node's props and
// children: no FuncMap, no template/define/block/call. The template's parse
// tree decides where actions may print, and its text is scanned for script,
// handlers and inline styles with the actions removed and replaced; a sample
// render is one more layer. Errors name the file and the rule.
// packValidateHook is a test-only seam that runs at the top of the template
// validator, to exercise the panic recovery.
var packValidateHook func()

func ValidatePackWidget(kind string, f PackFiles) (err error) {
	// The checks walk untrusted parse trees; a panic in one must refuse the
	// widget, not take down a background reconcile.
	defer func() {
		if r := recover(); r != nil {
			err = packErr("widget", "internal", "validator panic: %v", r)
		}
	}()
	if !builder.IsPackKind(kind) {
		return fmt.Errorf("%q is not a plugin widget kind", kind)
	}
	if err := validatePackHTML(kind, f.HTML); err != nil {
		return err
	}
	if err := validatePackCSS(kind, f.CSS); err != nil {
		return err
	}
	return validatePackIsland(f.Island)
}

func packErr(file, rule, format string, a ...any) error {
	return fmt.Errorf("%s: %s: %s", file, rule, fmt.Sprintf(format, a...))
}

// --- widget.html ---

// samplePackProps are typical prop values for the sample render; the keys
// cover what widget templates usually test or print.
func samplePackProps() map[string]any {
	p := map[string]any{
		"hidden": false, "level": float64(2), "count": float64(3), "columns": float64(3), "limit": float64(6),
		"checked": true, "open": false, "featured": true,
	}
	for _, k := range []string{"title", "heading", "text", "label", "body", "name", "description", "caption", "subtitle", "price", "alt", "tag", "value", "icon", "content", "cta", "button"} {
		p[k] = "Sample " + k
	}
	for _, k := range []string{"url", "link", "href", "cta_url", "button_url"} {
		p[k] = "https://example.com/"
	}
	for _, k := range []string{"image", "src", "photo", "logo"} {
		p[k] = "/media/sample.jpg"
	}
	return p
}

func validatePackHTML(kind string, src []byte) error {
	if packValidateHook != nil {
		packValidateHook()
	}
	const file = "widget.html"
	if len(bytes.TrimSpace(src)) == 0 {
		return packErr(file, "size", "template is empty")
	}
	if len(src) > maxPackHTML {
		return packErr(file, "size", "larger than %d bytes", maxPackHTML)
	}
	t, err := template.New("w-" + kind).Parse(strings.TrimSpace(string(src)))
	if err != nil {
		return packErr(file, "parse", "%v", err)
	}
	if err := checkPackTree(t); err != nil {
		return err
	}
	if err := checkPackActions(file, t.Tree); err != nil {
		return err
	}
	// The markup with every action removed and with every action replaced
	// by a letter: a banned token that only appears once the dynamic parts
	// are cut out (<scr{{if x}}{{end}}ipt>) shows up in one of the two.
	for _, repl := range []string{"", "x"} {
		var b strings.Builder
		flattenPack(t.Tree.Root, repl, &b)
		what := fmt.Sprintf("template with actions replaced by %q", repl)
		if err := scanPackMarkup(file, what, b.String()); err != nil {
			return err
		}
		if err := checkPackBalance(file, what, b.String()); err != nil {
			return err
		}
		err := packBranches(t.Tree.Root, repl, func(m string) error {
			return scanPackMarkup(file, fmt.Sprintf("a template branch with actions replaced by %q", repl), m)
		})
		if err != nil {
			return err
		}
	}
	for _, canvas := range []bool{false, true} {
		var attrs bytes.Buffer
		nodeAttrs(&attrs, nil, &builder.Node{ID: "sample", T: kind}, "hm-"+kind, canvas)
		d := widgetData{
			Attrs:    template.HTMLAttr(attrs.String()), // #nosec G203 -- built from the validated kind
			P:        samplePackProps(),
			Children: template.HTML("<span></span>"), // #nosec G203 -- constant
			Canvas:   canvas,
		}
		var out bytes.Buffer
		if err := t.Execute(&out, d); err != nil {
			return packErr(file, "sample-render", "canvas=%v: %v (guard prop use with {{if}})", canvas, err)
		}
		if err := scanPackMarkup(file, fmt.Sprintf("sample render (canvas=%v)", canvas), out.String()); err != nil {
			return err
		}
		if _, err := balancePackMarkup(out.Bytes()); err != nil {
			return packErr(file, "root", "canvas=%v: %v", canvas, err)
		}
	}
	return nil
}

// checkPackTree walks every template of the parse: only plain actions, ranges,
// ifs and withs are allowed, and the root element must carry .Attrs.
func checkPackTree(t *template.Template) error {
	const file = "widget.html"
	var failed error
	attrs := false
	nodes, rangeDepth := 0, 0
	fail := func(tr *parse.Tree, n parse.Node, rule, format string, a ...any) {
		if failed == nil {
			loc, _ := tr.ErrorContext(n)
			failed = packErr(file, rule, "%s: %s", loc, fmt.Sprintf(format, a...))
		}
	}
	var walk func(tr *parse.Tree, n parse.Node)
	walkAll := func(tr *parse.Tree, ns []parse.Node) {
		for _, n := range ns {
			walk(tr, n)
		}
	}
	walk = func(tr *parse.Tree, n parse.Node) {
		switch x := n.(type) {
		case nil:
			return
		case *parse.ListNode:
			if x == nil {
				return
			}
		case *parse.PipeNode:
			if x == nil {
				return
			}
		}
		if nodes++; nodes == maxPackTreeNodes+1 {
			fail(tr, tr.Root, "complexity", "more than %d template nodes", maxPackTreeNodes)
		}
		switch x := n.(type) {
		case *parse.ListNode:
			walkAll(tr, x.Nodes)
		case *parse.ActionNode:
			walk(tr, x.Pipe)
		case *parse.IfNode:
			walk(tr, x.Pipe)
			walk(tr, x.List)
			walk(tr, x.ElseList)
		case *parse.RangeNode:
			if !propRangePipe(x.Pipe) {
				fail(tr, x, "banned-range", "range may only iterate a prop (.P or .P.name), not a number or a function result")
			}
			if rangeDepth > 0 {
				fail(tr, x, "banned-range", "a range inside another range is not allowed")
			}
			walk(tr, x.Pipe)
			rangeDepth++
			walk(tr, x.List)
			rangeDepth--
			walk(tr, x.ElseList)
		case *parse.WithNode:
			if !withPipe(x.Pipe) {
				fail(tr, x, "banned-with", "with may only take a field chain such as .P.name or .")
			}
			walk(tr, x.Pipe)
			walk(tr, x.List)
			walk(tr, x.ElseList)
		case *parse.VariableNode:
			fail(tr, x, "banned-variable", "$ and variables are not allowed in plugin templates")
		case *parse.TemplateNode:
			fail(tr, x, "banned-action", "template, define and block are not allowed")
		case *parse.PipeNode:
			if len(x.Decl) > 0 {
				fail(tr, x, "banned-variable", "$ and variables are not allowed in plugin templates")
			}
			for _, c := range x.Cmds {
				walk(tr, c)
			}
		case *parse.CommandNode:
			walkAll(tr, x.Args)
		case *parse.ChainNode:
			walk(tr, x.Node)
		case *parse.IdentifierNode:
			if !packBuiltins[x.Ident] {
				fail(tr, x, "banned-function", "%s is not allowed; only %s", x.Ident, packBuiltinList)
			}
		case *parse.FieldNode:
			if len(x.Ident) > 0 && x.Ident[0] == "Attrs" {
				attrs = true
			}
		}
	}
	for _, tt := range t.Templates() {
		if tt.Tree == nil {
			continue
		}
		if tt.Name() != t.Name() {
			fail(tt.Tree, tt.Tree.Root, "banned-action", "template, define and block are not allowed (defines %q)", tt.Name())
			continue
		}
		walk(tt.Tree, tt.Tree.Root)
	}
	if failed != nil {
		return failed
	}
	if !attrs {
		return packErr(file, "attrs-required", "the root element must carry {{.Attrs}}")
	}
	return nil
}

// packBuiltins is the whole function set of a pack template: comparisons,
// logic and len/index. Anything that builds a string (print, printf, println,
// slice, html, js, urlquery) can allocate far more than it writes, and a
// template cannot be interrupted, so those are refused as a class; call too.
var packBuiltins = map[string]bool{
	"eq": true, "ne": true, "lt": true, "le": true, "gt": true, "ge": true,
	"and": true, "or": true, "not": true, "len": true, "index": true,
}

const packBuiltinList = "eq, ne, lt, le, gt, ge, and, or, not, len, index"

// maxPackTreeNodes bounds the size of a template's parse tree.
const maxPackTreeNodes = 2000

// withPipe reports whether pipe is one field chain or the dot: with can narrow
// the dot but never reach back to the root.
func withPipe(p *parse.PipeNode) bool {
	if p == nil || len(p.Cmds) != 1 || len(p.Cmds[0].Args) != 1 {
		return false
	}
	switch p.Cmds[0].Args[0].(type) {
	case *parse.FieldNode, *parse.DotNode:
		return true
	}
	return false
}

// propRangePipe reports whether pipe is a single field chain rooted at .P:
// the only thing a pack may iterate. A literal or a function result such as
// {{range 2000}} would let a template loop without bound.
func propRangePipe(p *parse.PipeNode) bool {
	if p == nil || len(p.Cmds) != 1 || len(p.Cmds[0].Args) != 1 {
		return false
	}
	f, ok := p.Cmds[0].Args[0].(*parse.FieldNode)
	return ok && len(f.Ident) >= 1 && f.Ident[0] == "P"
}

var bannedElements = map[string]bool{
	"script": true, "style": true, "iframe": true, "object": true, "embed": true,
	"link": true, "meta": true, "base": true, "form": true,
	"svg": true, "math": true, "use": true, "animate": true, "set": true, "foreignobject": true,
	"noscript": true, "template": true, "textarea": true, "title": true,
	"xmp": true, "plaintext": true, "noembed": true, "noframes": true, "frame": true, "frameset": true,
	"applet": true, "portal": true, "dialog": true,
}

// markupRules are the exceptions to the widget-pack baseline a caller may
// grant. Widget packs get none; plugin sections and partials get a short list.
type markupRules struct {
	allowForm  bool // <form> with an action (http(s) or relative) and a method
	allowStyle bool // <style> and the style attribute
}

// scanPackMarkup tokenizes markup (never regexp) and refuses banned elements
// and attributes. what says whether it is the template source or its output.
func scanPackMarkup(file, what, markup string) error {
	return scanMarkup(file, what, markup, markupRules{})
}

// scanMarkup is the one markup check, shared by widget packs and plugin
// sections: the pack baseline minus the exceptions rules grant.
func scanMarkup(file, what, markup string, rules markupRules) error {
	z := html.NewTokenizer(strings.NewReader(markup))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return nil // io.EOF; the tokenizer has no other failure
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			tok := z.Token()
			if bannedElements[tok.Data] && !(rules.allowForm && tok.Data == "form") && !(rules.allowStyle && tok.Data == "style") {
				return packErr(file, "banned-element", "%s: <%s> is not allowed", what, tok.Data)
			}
			for _, a := range tok.Attr {
				key := strings.ToLower(a.Key)
				switch {
				case strings.HasPrefix(key, "on"):
					return packErr(file, "banned-attribute", "%s: event handler attribute %q is not allowed", what, a.Key)
				case key == "style" && !rules.allowStyle:
					return packErr(file, "banned-attribute", "%s: style attribute is not allowed, use widget.css", what)
				case key == "action" && rules.allowForm && tok.Data == "form":
					if !plainFormTarget(a.Val) {
						return packErr(file, "banned-url", "%s: a form action must be an http(s) or relative URL", what)
					}
				case key == "srcdoc" || key == "formaction" || key == "http-equiv" || key == "xlink:href" || key == "action":
					return packErr(file, "banned-attribute", "%s: attribute %q is not allowed", what, a.Key)
				}
				if v := normalizeURLValue(a.Val); strings.HasPrefix(v, "javascript:") || strings.HasPrefix(v, "data:") || strings.HasPrefix(v, "vbscript:") {
					return packErr(file, "banned-url", "%s: %q has a javascript:, data: or vbscript: value", what, a.Key)
				}
			}
		}
	}
}

// plainFormTarget reports whether v is a relative URL or one with an http or
// https scheme, after the normalisation browsers apply to a scheme.
func plainFormTarget(v string) bool {
	n := normalizeURLValue(v)
	if i := strings.IndexAny(n, ":/?#"); i >= 0 && n[i] == ':' {
		return strings.HasPrefix(n, "http:") || strings.HasPrefix(n, "https:")
	}
	return true
}

// normalizeURLValue lowercases v and drops the whitespace and control
// characters browsers ignore inside a scheme.
func normalizeURLValue(v string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(v) {
		if r > ' ' && r != 0x7f {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// --- widget.css ---

var (
	packCSSAtRule = regexp.MustCompile(`(?i)@(import|font-face|charset|namespace|keyframes|property)\b|url\s*\(`)
	packCSSTag    = regexp.MustCompile(`<[/!?a-zA-Z]`)
	// Functions that load a resource without url(), and the properties
	// that would take the widget out of its box.
	packCSSFetch = regexp.MustCompile(`(?i)(image-set|image|cross-fade|element|src|paint)\s*\(|expression|-moz-binding`)
	// Only position:static and position:relative are allowed (an allowlist:
	// absolute, fixed and sticky lift a widget over the page, and keywords,
	// var(), env(), if() and attr() can all hide the value).
	packCSSPosition = regexp.MustCompile(`(?i)(?:^|[{;])\s*(?:-webkit-)?position\s*:\s*([^;}]*)`)
	packCSSPosOK    = regexp.MustCompile(`(?i)^(static|relative)\s*(!\s*important)?$`)
	// :has() is the one selector that reads outside the subject's own subtree.
	packCSSHas = regexp.MustCompile(`(?i):has\s*\(`)
)

func validatePackCSS(kind string, src []byte) error {
	const file = "widget.css"
	if len(src) > maxPackCSS {
		return packErr(file, "size", "larger than %d bytes", maxPackCSS)
	}
	// Validate what is served: minifyCSS is the exact text the page gets.
	if _, err := stripCSSComments(string(src)); err != nil {
		return err
	}
	css := minifyCSS(string(src))
	if unsafeCSS.MatchString(css) || packCSSAtRule.MatchString(css) || packCSSFetch.MatchString(css) {
		return packErr(file, "css-unsafe", "@import, @font-face, @charset, @namespace, @keyframes, @property, url(), image-set(), image(), cross-fade(), element(), src() and script constructs are not allowed")
	}
	for _, m := range packCSSPosition.FindAllStringSubmatch(css, -1) {
		if !packCSSPosOK.MatchString(strings.TrimSpace(m[1])) {
			return packErr(file, "css-position", "only position:static and position:relative are allowed")
		}
	}
	if packCSSHas.MatchString(css) {
		return packErr(file, "css-selector", ":has() is not allowed")
	}
	if strings.Contains(css, `\`) {
		return packErr(file, "css-unsafe", "backslash escapes are not allowed")
	}
	if packCSSTag.MatchString(css) {
		return packErr(file, "css-unsafe", "a tag start (<) is not allowed")
	}
	return scopeCheckCSS(kind, css)
}

// scopeCheckCSS requires that every selector of every rule starts with the
// widget's root class .hm-<kind>, so a plugin can never restyle the page. Only
// @media and @supports at-rules are allowed (their rules are checked too).
func scopeCheckCSS(kind, css string) error {
	css, err := stripCSSComments(css)
	if err != nil {
		return err
	}
	return scopeRules("widget.css", ".hm-"+kind, css, 0)
}

// stripCSSComments drops /* */ comments the way a browser reads them: a
// quoted string hides a "/*". minifyCSS reads strings the same way.
func stripCSSComments(css string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(css); i++ {
		switch c := css[i]; {
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(css) && css[j] != c {
				if css[j] == '\n' || css[j] == '\r' || css[j] == '\f' {
					return "", packErr("widget.css", "css-string", "a string may not contain a line break (browsers end it there)")
				}
				j++
			}
			if j == len(css) {
				return "", packErr("widget.css", "css-string", "unterminated string")
			}
			j++
			b.WriteString(css[i:j])
			i = j - 1
		case c == '/' && i+1 < len(css) && css[i+1] == '*':
			j := strings.Index(css[i+2:], "*/")
			if j < 0 {
				return "", packErr("widget.css", "css-braces", "unterminated comment")
			}
			b.WriteByte(' ')
			i += 2 + j + 1
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), nil
}

// scanTo returns the index of the first byte of s at brace/paren depth 0 and
// outside strings that is in stops, or -1.
func scanTo(s string, stops string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' || c == '\'':
			for i++; i < len(s) && s[i] != c; i++ {
			}
		case depth == 0 && strings.IndexByte(stops, c) >= 0:
			return i
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			if depth > 0 {
				depth--
			}
		}
	}
	return -1
}

// matchingBrace returns the index of the "}" closing the block whose body
// starts at s[0], or -1.
func matchingBrace(s string) int {
	depth := 1
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\'':
			for i++; i < len(s) && s[i] != c; i++ {
			}
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

func scopeRules(file, root, css string, depth int) error {
	for {
		css = strings.TrimSpace(css)
		if css == "" {
			return nil
		}
		open := scanTo(css, "{;}")
		if open < 0 || css[open] != '{' {
			return packErr(file, "css-braces", "unbalanced braces or a stray ; or } near %q", clip(css))
		}
		prelude := strings.TrimSpace(css[:open])
		end := matchingBrace(css[open+1:])
		if end < 0 {
			return packErr(file, "css-braces", "unbalanced braces near %q", clip(prelude))
		}
		body := css[open+1 : open+1+end]
		css = css[open+1+end+1:]
		if strings.HasPrefix(prelude, "@") {
			name := strings.ToLower(strings.TrimLeft(prelude, "@"))
			if i := strings.IndexFunc(name, func(r rune) bool { return r == ' ' || r == '(' || r == '\t' || r == '\n' }); i >= 0 {
				name = name[:i]
			}
			if name != "media" && name != "supports" {
				return packErr(file, "css-at-rule", "@%s is not allowed, only @media and @supports", name)
			}
			if depth > 0 {
				return packErr(file, "css-at-rule", "@%s cannot be nested", name)
			}
			if err := scopeRules(file, root, body, depth+1); err != nil {
				return err
			}
			continue
		}
		if strings.ContainsAny(body, "{") {
			return packErr(file, "css-braces", "nested rules are not allowed (rule %q)", clip(prelude))
		}
		if err := scopeSelectors(file, root, prelude); err != nil {
			return err
		}
	}
}

func scopeSelectors(file, root, list string) error {
	for list != "" {
		i := scanTo(list, ",")
		sel := list
		if i >= 0 {
			sel, list = list[:i], list[i+1:]
		} else {
			list = ""
		}
		sel = strings.TrimSpace(sel)
		rest, ok := strings.CutPrefix(sel, root)
		if !ok || (rest != "" && isIdentByte(rest[0])) {
			return packErr(file, "css-selector-scope", "selector %q must start with %s", clip(sel), root)
		}
		// A sibling combinator would reach elements outside the widget.
		if scanTo(sel, "+~") >= 0 {
			return packErr(file, "css-selector-scope", "selector %q uses a sibling combinator (+ or ~)", clip(sel))
		}
	}
	return nil
}

func isIdentByte(c byte) bool {
	return c == '-' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func clip(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

// --- island.js ---

var packIslandBanned = []struct {
	re   *regexp.Regexp
	name string
}{
	{regexp.MustCompile(`\beval\s*\(`), "eval("},
	{regexp.MustCompile(`\bnew\s+Function\b`), "new Function"},
	{regexp.MustCompile(`\bdocument\s*\.\s*write`), "document.write"},
	{regexp.MustCompile(`\bimport\s*\(`), "dynamic import()"},
	{regexp.MustCompile(`\bimportScripts\b`), "importScripts"},
	{regexp.MustCompile(`(?i)https?://`), "an absolute http(s):// URL"},
	{regexp.MustCompile("[\"'`]//[A-Za-z0-9]"), "a protocol-relative //host URL"},
}

// validatePackIsland is a best-effort lint, not a sandbox: an island is
// same-origin first-party script (the public CSP allows 'unsafe-inline' and
// does not contain it). The boundary is the owner's builder.island grant.
func validatePackIsland(js []byte) error {
	const file = "island.js"
	if len(js) > maxPackIsland {
		return packErr(file, "size", "larger than %d bytes", maxPackIsland)
	}
	for _, b := range packIslandBanned {
		if b.re.Match(js) {
			return packErr(file, "island-banned", "%s is not allowed", b.name)
		}
	}
	return nil
}

// --- registry ---

// packSet is the merged widget set (embedded plus plugin widgets); nil while
// no plugin widget is registered.
var packSet atomic.Pointer[widgetSet]

// SetPackWidgets replaces the plugin widgets with ws (nil removes them all) in
// one atomic swap. Every widget is validated again; on error the previous set
// stays. Kinds are ordered embedded first, then plugin widgets by name.
func SetPackWidgets(ws []PackWidget) error {
	if len(ws) == 0 {
		packSet.Store(nil)
		return nil
	}
	if len(ws) > builder.MaxPackWidgets {
		return fmt.Errorf("render: %d plugin widgets, at most %d", len(ws), builder.MaxPackWidgets)
	}
	base := embeddedWidgets()
	set := &widgetSet{
		kinds:  append([]string(nil), base.kinds...),
		tmpl:   maps.Clone(base.tmpl),
		prep:   maps.Clone(base.prep),
		css:    maps.Clone(base.css),
		island: maps.Clone(base.island),
	}
	sorted := append([]PackWidget(nil), ws...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Kind < sorted[j].Kind })
	for _, w := range sorted {
		if _, dup := set.tmpl[w.Kind]; dup {
			return fmt.Errorf("render: widget %q is already registered", w.Kind)
		}
		if err := ValidatePackWidget(w.Kind, PackFiles{HTML: w.HTML, CSS: w.CSS, Island: w.Island}); err != nil {
			return fmt.Errorf("render: widget %q: %w", w.Kind, err)
		}
		t, err := template.New("w-" + w.Kind).Parse(strings.TrimSpace(string(w.HTML)))
		if err != nil {
			return fmt.Errorf("render: widget %q: %w", w.Kind, err)
		}
		set.tmpl[w.Kind], set.css[w.Kind] = t, minifyCSS(string(w.CSS))
		if len(w.Island) > 0 {
			is := newWidgetIsland(w.Kind, w.Island)
			sum := sha256.Sum256(w.Island)
			is.integrity = "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
			set.island[w.Kind] = is
		}
		set.kinds = append(set.kinds, w.Kind)
	}
	packSet.Store(set)
	return nil
}

// missingCanvasCSS styles the canvas box of a plugin widget that is not installed.
const missingCanvasCSS = `.hm-missing{padding:var(--hm-space-4,1rem);border:1px dashed currentColor;opacity:.7;text-align:center}`

// writeMissingWidget renders a node whose plugin widget is not installed:
// a comment publicly, a selectable box in the canvas. Its children are dropped.
// The kind matches the pack-kind pattern, so it is safe in a comment.
func writeMissingWidget(b *bytes.Buffer, n *builder.Node, canvas bool) {
	if !canvas {
		b.WriteString("<!-- hm-widget: " + n.T + " not installed -->")
		return
	}
	b.WriteString("<div ")
	nodeAttrs(b, nil, n, "hm-missing", true)
	b.WriteString(">Widget &#34;" + n.T + "&#34; is not installed</div>")
}

// writeFailedWidget replaces a widget whose template failed or overran its
// output cap: a comment publicly, a selectable box in the canvas. The kind is
// a registry kind (core name or pack-shaped), safe in a comment.
func writeFailedWidget(b *bytes.Buffer, n *builder.Node, canvas bool) {
	if !canvas {
		b.WriteString("<!-- hm-widget: " + n.T + " failed -->")
		return
	}
	b.WriteString("<div ")
	nodeAttrs(b, nil, n, "hm-missing", true)
	b.WriteString(">Widget &#34;" + n.T + "&#34; failed to render</div>")
}
