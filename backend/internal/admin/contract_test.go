package admin

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"hatamoto.dev/core/web"
)

// Admin design contract rules DC-01 to DC-04, documented in docs/ADMIN_UI_RULES.md.
// DC-01: type, radius, spacing and z-index declarations use design tokens.

// contractDecl is one CSS declaration with the selector of the rule that holds it.
type contractDecl struct {
	File string
	Line int
	Sel  string // prelude of the innermost style rule
	Prop string // lower-case property name
	Val  string // value without !important
	Text string // the source line, matched against allowlist needles
	Rule int    // index of the rule holding the declaration, in file order
}

// contractDecls parses every admin stylesheet except tokens.css. It walks
// braces and semicolons (not lines), so one-line rules and nested @media
// blocks yield their declarations. Rules whose selector contains .prose are
// skipped: that scope styles user-authored rich text.
func contractDecls(t *testing.T, fsys fs.FS) []contractDecl {
	t.Helper()
	var out []contractDecl
	for _, f := range readUIFiles(t, fsys) {
		if !strings.HasSuffix(f.name, ".css") || f.name == "css/tokens.css" {
			continue
		}
		out = append(out, parseContractCSS(f.name, f.src)...)
	}
	return out
}

// blankCSSComments blanks comments like blankComments but leaves string
// literals alone, so "/*" inside content: "..." does not open a comment.
func blankCSSComments(src string) string {
	b := []byte(src)
	var quote byte
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			stop := len(b)
			if end >= 0 {
				stop = i + 2 + end + 2
			}
			for j := i; j < stop; j++ {
				if b[j] != '\n' {
					b[j] = ' '
				}
			}
			i = stop - 1
		}
	}
	return string(b)
}

// proseOnly reports whether every selector of the list is in the .prose scope.
// A mixed list (".prose h2, .card h2") is checked, since it styles non-prose too.
func proseOnly(prelude string) bool {
	parts := dcSplitTop(prelude, ',')
	for _, p := range parts {
		if !strings.Contains(p, ".prose") {
			return false
		}
	}
	return len(parts) > 0
}

// dcSplitTop splits s at sep outside parentheses and brackets.
func dcSplitTop(s string, sep byte) []string {
	var parts []string
	dep, from := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[':
			dep++
		case ')', ']':
			dep--
		case sep:
			if dep == 0 {
				parts = append(parts, s[from:i])
				from = i + 1
			}
		}
	}
	return append(parts, s[from:])
}

func parseContractCSS(file, src string) []contractDecl {
	blank := blankCSSComments(src)
	type frame struct {
		prelude string
		atRule  bool
		id      int
	}
	var (
		stack      []frame
		rules      int
		out        []contractDecl
		start, dep int
		quote      byte
	)
	// context returns the innermost style-rule selector and whether the
	// declarations are exempt (inside .prose or @font-face).
	context := func() (sel string, id int, skip bool) {
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].atRule && strings.HasPrefix(stack[i].prelude, "@font-face") {
				skip = true
			}
			if proseOnly(stack[i].prelude) {
				skip = true
			}
		}
		for i := len(stack) - 1; i >= 0; i-- {
			if !stack[i].atRule {
				return stack[i].prelude, stack[i].id, skip
			}
		}
		return "", 0, true
	}
	flush := func(end int) {
		text := blank[start:end]
		defer func() { start = end + 1 }()
		trimmed := strings.TrimSpace(text)
		colon := strings.Index(trimmed, ":")
		if trimmed == "" || colon < 0 || len(stack) == 0 {
			return
		}
		sel, id, skip := context()
		if skip {
			return
		}
		off := start + (len(text) - len(strings.TrimLeft(text, " \t\r\n")))
		val := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(trimmed[colon+1:]), "!important"))
		out = append(out, contractDecl{File: file, Line: lineOf(src, off), Sel: sel,
			Prop: strings.ToLower(strings.TrimSpace(trimmed[:colon])), Val: val, Text: lineText(src, off), Rule: id})
	}
	for i := 0; i < len(blank); i++ {
		c := blank[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(':
			dep++
		case c == ')':
			dep--
		case dep > 0:
		case c == '{':
			p := strings.Join(strings.Fields(blank[start:i]), " ")
			rules++
			stack = append(stack, frame{prelude: p, atRule: strings.HasPrefix(p, "@"), id: rules})
			start = i + 1
		case c == ';':
			flush(i)
		case c == '}':
			flush(i)
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return out
}

// dc01Prefix maps a checked property to the token families that may fill it.
func dc01Prefix(prop string) (prefixes []string, ok bool) {
	switch {
	case prop == "font-size":
		return []string{"--text-"}, true
	case prop == "font-weight":
		return []string{"--weight-"}, true
	case prop == "line-height":
		return []string{"--leading-"}, true
	case prop == "border-radius" || (strings.HasPrefix(prop, "border-") && strings.HasSuffix(prop, "-radius")):
		return []string{"--radius-"}, true
	case prop == "z-index":
		return []string{"--z-"}, true
	case prop == "padding" || strings.HasPrefix(prop, "padding-"):
		return []string{"--space-", "--page-pad"}, true
	case prop == "margin" || strings.HasPrefix(prop, "margin-"), prop == "gap", prop == "row-gap", prop == "column-gap":
		return []string{"--space-"}, true
	}
	return nil, false
}

var (
	dc01Var = regexp.MustCompile(`^var\((--[\w-]+)(?:\s*,\s*(.*))?\)$`)
	// dc01TextSize is the type scale; --text-body and --text-muted are colours.
	dc01TextSize = regexp.MustCompile(`^--text-(xs|sm|md|lg|xl|2xl|3xl|title|metric)$`)
	dc01Number   = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)$`)
	dc01Length   = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)(px|rem|em|%)?$`)
)

// dcSplit splits s at top-level whitespace (and at "/" when slash is set),
// keeping parenthesised groups whole.
func dcSplit(s string, slash bool) []string {
	var parts []string
	dep, from := 0, -1
	emit := func(end int) {
		if from >= 0 {
			parts = append(parts, s[from:end])
			from = -1
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '(':
			dep++
		case c == ')':
			dep--
		case dep == 0 && (c == ' ' || c == '\t' || c == '\n' || c == ',' || (slash && c == '/')):
			emit(i)
			continue
		}
		if from < 0 {
			from = i
		}
	}
	emit(len(s))
	return parts
}

var (
	// weight and size keywords that bypass the tokens in the font shorthand
	dc01FontWord = regexp.MustCompile(`^(bold|bolder|lighter|(xx?-)?(small|large)|xxx-large|smaller|larger|medium)$`)
	// the other token families a font shorthand may use
	dc01FontVar = regexp.MustCompile(`^--(weight|leading|font)-[\w-]+$`)
)

func dc01Keyword(v string) bool {
	switch v {
	case "inherit", "initial", "unset", "revert":
		return true
	}
	return false
}

// dc01Value reports whether one value token is allowed for a prop.
func dc01Value(prop, v string, prefixes []string) bool {
	if dc01Keyword(v) || v == "0" {
		return true
	}
	if m := dc01Var.FindStringSubmatch(v); m != nil {
		if strings.Contains(v, ",") && !dc01Value(prop, strings.TrimSpace(m[2]), prefixes) {
			return false // a fallback must be a token too, never a literal
		}
		if prefixes[0] == "--text-" {
			return dc01TextSize.MatchString(m[1])
		}
		for _, p := range prefixes {
			if strings.HasPrefix(m[1], p) {
				return true
			}
		}
		return false
	}
	if strings.HasPrefix(prop, "font-") || prop == "line-height" || prop == "z-index" {
		// no hairline allowances outside radius and spacing
	} else if v == "1px" || v == "-1px" {
		return true
	}
	if v == "auto" && !strings.Contains(prop, "radius") && prop != "z-index" && !strings.HasPrefix(prop, "font-") && prop != "line-height" && !strings.HasSuffix(prop, "gap") {
		return true
	}
	if v == "50%" && strings.Contains(prop, "radius") {
		return true
	}
	if strings.HasPrefix(v, "calc(") && strings.HasSuffix(v, ")") {
		return dc01Calc(prop, v[len("calc("):len(v)-1], prefixes)
	}
	return false
}

// dc01Calc accepts operators, plain numbers, nested groups and token vars.
func dc01Calc(prop, body string, prefixes []string) bool {
	for _, p := range dcSplit(strings.NewReplacer(",", " ").Replace(body), false) {
		switch {
		case p == "+" || p == "-" || p == "*" || p == "/":
		case dc01Number.MatchString(p):
		case strings.HasPrefix(p, "(") && strings.HasSuffix(p, ")"):
			if !dc01Calc(prop, p[1:len(p)-1], prefixes) {
				return false
			}
		case dc01Var.MatchString(p):
			if !dc01Value(prop, p, prefixes) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// dc01Font checks the font shorthand: size, weight and line-height parts must
// be tokens. Family names, styles and keywords are not checked.
func dc01Font(v string) bool {
	if dc01Keyword(v) {
		return true
	}
	for _, p := range dcSplit(v, true) {
		if dc01Length.MatchString(p) && p != "0" {
			return false
		}
		if strings.HasPrefix(p, "calc(") || dc01FontWord.MatchString(p) {
			return false
		}
		if m := dc01Var.FindStringSubmatch(p); m != nil {
			if m[2] != "" || !(dc01TextSize.MatchString(m[1]) || dc01FontVar.MatchString(m[1])) {
				return false
			}
		}
	}
	return true
}

func lintDC01(decls []contractDecl) []uiViolation {
	var out []uiViolation
	for _, d := range decls {
		var bad bool
		if d.Prop == "font" {
			bad = !dc01Font(d.Val)
		} else if prefixes, ok := dc01Prefix(d.Prop); ok {
			for _, tok := range dcSplit(d.Val, false) {
				if !dc01Value(d.Prop, tok, prefixes) {
					bad = true
				}
			}
		}
		if bad {
			out = append(out, uiViolation{File: d.File, Line: d.Line, Rule: "DC-01",
				Msg: d.Prop + ": " + d.Val + " is not a design token (see css/tokens.css)", Text: d.Text})
		}
	}
	return out
}

// contractAllow lists the deliberate exceptions. Each needs a reason.
var contractAllow = []uiAllowEntry{
	{Rule: "DC-02", File: "css/partials/listbox.css", Needle: "min-width: 30px; padding: 0", Reason: "the dark selection bubble sizes its icon buttons as 30px squares; it is a toolbar, not a form button"},
	{Rule: "DC-02", File: "css/pages/blogeditor.css", Needle: ".hm-be__side .hm-card {", Reason: "the editor side rail uses flat, borderless cards; follow-up: a card variant"},
	{Rule: "DC-02", File: "css/pages/content.css", Needle: `input[type="file"].hm-field__control`, Reason: "a native file input needs less padding than a text field"},
	{Rule: "DC-02", File: "css/partials/steps.css", Needle: `.hm-field__control[type="color"]`, Reason: "a colour swatch input is a block, not a text field; follow-up: move to field.css"},
}

// componentSpec is one entry of components.json, the single source of
// component values (see docs/ADMIN_UI_RULES.md, Components).
type componentSpec struct {
	Selector     string                       `json:"selector"`
	Owner        string                       `json:"owner"`
	Partial      string                       `json:"partial,omitempty"`
	Variants     map[string]string            `json:"variants,omitempty"`
	Props        map[string]string            `json:"props"`
	VariantProps map[string]map[string]string `json:"variantProps,omitempty"`
}

func loadComponents(fsys fs.FS) (map[string]componentSpec, error) {
	b, err := fs.ReadFile(fsys, "components.json")
	if err != nil {
		return nil, err
	}
	var spec map[string]componentSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		return nil, fmt.Errorf("components.json: %w", err)
	}
	for _, name := range sortedNames(spec) {
		c := spec[name]
		for _, v := range sortedKeys(c.VariantProps) {
			if _, ok := c.Variants[v]; !ok {
				return nil, fmt.Errorf("components.json: %s has variantProps %q without a matching variants key", name, v)
			}
		}
	}
	return spec, nil
}

func sortedNames(spec map[string]componentSpec) []string {
	names := make([]string, 0, len(spec))
	for n := range spec {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func normSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

var dcClass = regexp.MustCompile(`\.([\w-]+)`)

// sideValues expands a 1 to 4 value box shorthand to top, right, bottom, left.
func sideValues(vals []string) (top, right, bottom, left string) {
	switch len(vals) {
	case 1:
		return vals[0], vals[0], vals[0], vals[0]
	case 2:
		return vals[0], vals[1], vals[0], vals[1]
	case 3:
		return vals[0], vals[1], vals[2], vals[1]
	case 4:
		return vals[0], vals[1], vals[2], vals[3]
	}
	return
}

var dcBorderStyle = map[string]bool{"none": true, "solid": true, "dashed": true, "dotted": true, "double": true, "hidden": true}

// expandDecl turns shorthands that carry contract props into longhands.
// Values are kept as written; unknown properties map to themselves.
func expandDecl(prop, val string) map[string]string {
	vals := dcSplit(val, false)
	out := map[string]string{}
	box := func(t, r, b, l string) {
		if len(vals) < 1 || len(vals) > 4 {
			return
		}
		a, c, d, e := sideValues(vals)
		out[t], out[r], out[b], out[l] = a, c, d, e
	}
	switch prop {
	case "padding", "margin":
		box(prop+"-top", prop+"-right", prop+"-bottom", prop+"-left")
	case "border-color":
		box("border-top-color", "border-right-color", "border-bottom-color", "border-left-color")
	case "border-radius":
		box("border-top-left-radius", "border-top-right-radius", "border-bottom-right-radius", "border-bottom-left-radius")
	case "padding-inline", "margin-inline":
		p := strings.TrimSuffix(prop, "-inline")
		if len(vals) == 1 {
			vals = append(vals, vals[0])
		}
		if len(vals) == 2 {
			out[p+"-left"], out[p+"-right"] = vals[0], vals[1]
		}
	case "padding-block", "margin-block":
		p := strings.TrimSuffix(prop, "-block")
		if len(vals) == 1 {
			vals = append(vals, vals[0])
		}
		if len(vals) == 2 {
			out[p+"-top"], out[p+"-bottom"] = vals[0], vals[1]
		}
	case "padding-inline-start", "padding-inline-end":
		out["padding-"+map[bool]string{true: "left", false: "right"}[strings.HasSuffix(prop, "start")]] = val
	case "border", "border-top", "border-right", "border-bottom", "border-left":
		color := ""
		for _, v := range vals {
			if !dcBorderStyle[v] && !dc01Length.MatchString(v) && v != "thin" && v != "medium" && v != "thick" {
				color = v
			}
		}
		if color == "" {
			break
		}
		for _, side := range []string{"top", "right", "bottom", "left"} {
			if prop == "border" || prop == "border-"+side {
				out["border-"+side+"-color"] = color
			}
		}
	case "font":
		out["font-size"], out["font-weight"] = val, val
	default:
		out[prop] = val
	}
	return out
}

// ownerRule is one rule of an owner file whose selector equals the wanted one.
type ownerRule struct {
	props map[string]string
	line  int
}

// ruleProps returns the longhand values of every rule in file whose selector
// equals sel (including rules inside @media), in source order.
func ruleProps(decls []contractDecl, file, sel string) []ownerRule {
	want := normSpace(sel)
	var rules []ownerRule
	index := map[int]int{}
	for _, d := range decls {
		if d.File != file || normSpace(d.Sel) != want {
			continue
		}
		i, ok := index[d.Rule]
		if !ok {
			i = len(rules)
			index[d.Rule] = i
			rules = append(rules, ownerRule{props: map[string]string{}, line: d.Line})
		}
		for k, v := range expandDecl(d.Prop, d.Val) {
			rules[i].props[k] = normSpace(v)
		}
	}
	return rules
}

// lintDC03 checks that each owner file declares the contract in its base rule
// (and each variant rule its overrides).
func lintDC03(spec map[string]componentSpec, decls []contractDecl, fsys fs.FS) []uiViolation {
	var out []uiViolation
	check := func(name, file, sel string, want map[string]string) {
		rules := ruleProps(decls, file, sel)
		if len(rules) == 0 {
			out = append(out, uiViolation{File: file, Line: 1, Rule: "DC-03", Msg: fmt.Sprintf("%s: owner has no rule for %s", name, sel)})
			return
		}
		// The first rule must declare the whole contract; a later rule (an
		// @media override) may omit a prop but must not change one.
		for i, r := range rules {
			for _, p := range sortedKeys(want) {
				got, set := r.props[p]
				if (i == 0 || set) && got != normSpace(want[p]) {
					out = append(out, uiViolation{File: file, Line: r.line, Rule: "DC-03",
						Msg:  fmt.Sprintf("%s: %s declares %s: %q, components.json says %q", name, sel, p, got, want[p]),
						Text: sel})
				}
			}
		}
	}
	for _, name := range sortedNames(spec) {
		c := spec[name]
		if _, err := fs.Stat(fsys, c.Owner); err != nil {
			out = append(out, uiViolation{File: "components.json", Line: 1, Rule: "DC-03", Msg: fmt.Sprintf("%s: owner file %s does not exist", name, c.Owner)})
			continue
		}
		check(name, c.Owner, c.Selector, c.Props)
		for _, v := range sortedKeys(c.Variants) {
			check(name+"/"+v, c.Owner, c.Variants[v], c.VariantProps[v])
		}
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

// lintDC02 reports a contract property set outside the owner file, on a rule
// whose subject (last compound of any selector in the list) carries a class
// of the component. Sub-elements (.hm-card__title) are not the component.
func lintDC02(spec map[string]componentSpec, decls []contractDecl) []uiViolation {
	var out []uiViolation
	for _, name := range sortedNames(spec) {
		c := spec[name]
		classes := map[string]bool{}
		contract := map[string]bool{}
		for _, sel := range append([]string{c.Selector}, sortedValues(c.Variants)...) {
			// only the subject (last compound): ".hm-tabs[data-variant] .hm-tabs__tab"
			// is about the tab, and its .hm-tabs context is the container.
			for _, one := range dcSplitTop(sel, ',') {
				for _, m := range dcClass.FindAllStringSubmatch(lastCompound(one), -1) {
					classes[m[1]] = true
				}
			}
		}
		for p := range c.Props {
			contract[p] = true
		}
		for _, vp := range c.VariantProps {
			for p := range vp {
				contract[p] = true
			}
		}
		for _, d := range decls {
			if d.File == c.Owner || !subjectHasClass(d.Sel, classes) {
				continue
			}
			for p := range expandDecl(d.Prop, d.Val) {
				if contract[p] {
					out = append(out, uiViolation{File: d.File, Line: d.Line, Rule: "DC-02",
						Msg:  fmt.Sprintf("%s sets %s on %s: only %s may set %s's contract props", d.Sel, d.Prop, name, c.Owner, name),
						Text: d.Text})
					break
				}
			}
		}
	}
	return out
}

func sortedValues(m map[string]string) []string {
	var out []string
	for _, k := range sortedKeys(m) {
		out = append(out, m[k])
	}
	return out
}

var dcCombinator = regexp.MustCompile(`\s*[>+~]\s*|\s+`)

func lastCompound(selector string) string {
	parts := dcCombinator.Split(strings.TrimSpace(selector), -1)
	return parts[len(parts)-1]
}

func subjectHasClass(selector string, classes map[string]bool) bool {
	for _, one := range dcSplitTop(selector, ',') {
		for _, m := range dcClass.FindAllStringSubmatch(lastCompound(one), -1) {
			if classes[m[1]] {
				return true
			}
		}
	}
	return false
}

// lintContract runs DC-01 and, when the tree has a components.json, DC-02
// and DC-03.
func lintContract(t *testing.T, fsys fs.FS) []uiViolation {
	t.Helper()
	decls := contractDecls(t, fsys)
	out := lintDC01(decls)
	if _, err := fs.Stat(fsys, "components.json"); err != nil {
		return out
	}
	spec, err := loadComponents(fsys)
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, lintDC02(spec, decls)...)
	return append(out, lintDC03(spec, decls, fsys)...)
}

func TestDesignContract(t *testing.T) {
	t.Parallel()

	fsys := web.Admin()
	if _, err := loadComponents(fsys); err != nil {
		t.Fatal(err)
	}
	if n := len(contractDecls(t, fsys)); n < 1000 {
		t.Fatalf("parsed %d declarations: wrong root or broken parser?", n)
	}
	left, bad := applyAllow(contractAllow, lintContract(t, fsys))
	for _, v := range left {
		t.Error(v.String())
	}
	for _, b := range bad {
		t.Error(b)
	}
	t.Logf("%d contract violations, %d allowlist entries", len(left), len(contractAllow))
}

func TestDesignContractSelfTest(t *testing.T) {
	t.Parallel()

	planted := fstest.MapFS{
		"css/pages/a.css": {Data: []byte(".a { font-size: 13px; }\n" + // 1
			".b { font-weight: 600; }\n" + // 2
			".c { line-height: 1.4; }\n" + // 3
			".d { border-radius: 4px; }\n" + // 4
			".e { color: red; padding: 0 var(--space-1); margin: 3px; }\n" + // 5 one-liner, margin
			"@media (min-width: 40rem) {\n  .f { padding: 0 5px; }\n}\n" + // 7 nested
			".g { z-index: 2; }\n" + // 9
			".h { font: 400 14px/20px x; }\n" + // 10
			".i { gap: 6px }\n" + // 11 no semicolon
			".j { border-top-left-radius: 3px; }\n" + // 12
			".k { z-index: calc(var(--z-popup) + 1); padding-left: calc(var(--space-1) * 2) }\n" + // 13 ok
			".l { padding: var(--space-1) 7px; }\n" + // 14
			".m { font-size: var(--text-muted); }\n" + // 15 colour token, not a size
			".n { padding: var(--space-1, 5px); }\n" + // 16 literal fallback
			".o { font: bold var(--text-md) var(--font-ui); }\n" + // 17 keyword weight
			".p { font: var(--weight-medium) var(--text-body) x; }\n" + // 18 colour token in the shorthand
			".q { font: var(--weight-medium, 500) var(--text-md) x; }\n" + // 19 literal fallback in the shorthand
			".prose h2, .card h2 { font-size: 20px; }\n" + // 20 mixed list is checked
			".r { content: \"/*\"; font-size: 11px; }\n" + // 21 a string is not a comment
			".t { font: var(--weight-medium) 1.2em x; }\n")}, // 22 unit in the shorthand
		"css/pages/ok.css": {Data: []byte(".prose h2 { font-weight: 600; font-size: 1.1em; }\n" +
			".a { margin: -1px; padding: 0 1px; border-radius: 50%; font: inherit; }\n" +
			".b { border-radius: var(--radius-full) 1px 1px var(--radius-xs); }\n" +
			".c { padding: var(--page-pad); margin: 0 auto; }\n" +
			".d { font: var(--weight-medium) var(--text-md)/var(--leading-5) var(--font-ui); content: \"a;b{\"; }\n" +
			".e { font-size: var(--text-title); line-height: var(--leading-none); z-index: var(--z-behind); }\n" +
			".f { calc: 0; margin-inline: calc(-1 * var(--space-2)); gap: calc(var(--space-1) + var(--space-1)); }\n" +
			"@font-face { font-family: x; font-weight: 400 700; }\n" +
			".s { padding: var(--space-1, var(--space-2)); font-size: var(--text-3xl, var(--text-md)); }\n" +
			".prose h2, .prose h3 { font-size: 1.1em; }\n" +
			"/* .x { font-size: 9px; } */\n")},
		"css/tokens.css": {Data: []byte(":root { --a: 1px; font-size: 9px; }\n")},
	}
	var lines []int
	for _, v := range lintContract(t, planted) {
		if v.Rule != "DC-01" {
			t.Errorf("unexpected rule %s", v)
		}
		if v.File != "css/pages/a.css" {
			t.Errorf("a compliant or exempt declaration was flagged: %s", v)
			continue
		}
		lines = append(lines, v.Line)
	}
	want := []int{1, 2, 3, 4, 5, 7, 9, 10, 11, 12, 14, 15, 16, 17, 18, 19, 20, 21, 22}
	if len(lines) != len(want) {
		t.Fatalf("flagged lines %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("flagged lines %v, want %v", lines, want)
		}
	}

	// Allowlist machinery: stale and reasonless entries are reported.
	left, bad := applyAllow([]uiAllowEntry{
		{Rule: "DC-01", File: "css/pages/a.css", Needle: "font-size: 13px", Reason: "planted"},
		{Rule: "DC-01", File: "css/pages/a.css", Needle: "never-there", Reason: "planted"},
		{Rule: "DC-01", File: "css/pages/a.css", Needle: "z-index: 2", Reason: ""},
	}, lintContract(t, planted))
	if len(bad) != 2 {
		t.Errorf("want one stale and one reasonless entry, got %v", bad)
	}
	for _, v := range left {
		if v.Line == 1 {
			t.Errorf("allowlisted violation still reported: %s", v)
		}
	}
}

func TestDesignContractOwnerSelfTest(t *testing.T) {
	t.Parallel()

	spec := `{"button":{"selector":".hm-btn","owner":"css/partials/button.css","variants":{"sm":".hm-btn--sm"},
		"props":{"border-top-left-radius":"var(--radius-sm)","padding-left":"var(--space-3)"},
		"variantProps":{"sm":{"padding-left":"var(--space-2)"}}},
		"card":{"selector":".hm-card","owner":"css/partials/card.css","props":{"border-top-color":"var(--color-kinu)"}}}`
	owner := ".hm-btn { border-radius: var(--radius-sm); padding: 0 var(--space-3); }\n.hm-btn--sm { padding: 0 var(--space-2); }\n"
	card := ".hm-card { border: 1px solid var(--color-kinu); }\n"
	rules := func(fsys fstest.MapFS) map[string][]string {
		got := map[string][]string{}
		for _, v := range lintContract(t, fsys) {
			got[v.Rule] = append(got[v.Rule], v.String())
		}
		return got
	}
	base := func() fstest.MapFS {
		return fstest.MapFS{
			"components.json":         {Data: []byte(spec)},
			"css/partials/button.css": {Data: []byte(owner)},
			"css/partials/card.css":   {Data: []byte(card)},
		}
	}
	if got := rules(base()); len(got) != 0 {
		t.Fatalf("a consistent tree reported %v", got)
	}

	// DC-02: a non-owner rule sets a contract prop, in several selector shapes.
	for _, css := range []string{".hm-btn { border-radius: var(--radius-xs); }", ".x .hm-btn { padding: 0 var(--space-1); }", ".hm-btn.y:hover { border-radius: var(--radius-full); }", ".x, .hm-btn--sm { padding-inline: 1px; }"} {
		f := base()
		f["css/pages/x.css"] = &fstest.MapFile{Data: []byte(css + "\n")}
		if got := rules(f); len(got["DC-02"]) != 1 {
			t.Errorf("%q: want one DC-02, got %v", css, got)
		}
	}
	// Not violations: sub-element subject, descendant context only, non-contract prop.
	for _, css := range []string{".hm-btn-count { border-radius: var(--radius-xs); }", ".hm-btn .x { padding: 0 var(--space-1); }", ".hm-btn { width: 4px; }", ".hm-card__title { border-radius: var(--radius-xs); }"} {
		f := base()
		f["css/pages/x.css"] = &fstest.MapFile{Data: []byte(css + "\n")}
		if got := rules(f); len(got) != 0 {
			t.Errorf("%q: unexpected %v", css, got)
		}
	}

	// DC-03: drifted owner value, drifted variant, missing rule, missing owner file.
	f := base()
	f["css/partials/button.css"] = &fstest.MapFile{Data: []byte(strings.Replace(owner, "--radius-sm", "--radius-md", 1))}
	if got := rules(f); len(got["DC-03"]) != 1 {
		t.Errorf("drifted base rule: %v", got)
	}
	f = base()
	f["css/partials/button.css"] = &fstest.MapFile{Data: []byte(strings.Replace(owner, "0 var(--space-2)", "0 5px", 1))}
	if got := rules(f); len(got["DC-03"]) != 1 {
		t.Errorf("drifted variant: %v", got)
	}
	f = base()
	f["css/partials/button.css"] = &fstest.MapFile{Data: []byte(owner + "@media (max-width: 600px) { .hm-btn { padding-left: 1px; } }\n")}
	if got := rules(f); len(got["DC-03"]) != 1 {
		t.Errorf("drifted @media override: %v", got)
	}
	f = base()
	f["css/partials/card.css"] = &fstest.MapFile{Data: []byte(".other { color: red; }\n")}
	if got := rules(f); len(got["DC-03"]) != 1 {
		t.Errorf("missing owner rule: %v", got)
	}
	f = base()
	delete(f, "css/partials/card.css")
	if got := rules(f); len(got["DC-03"]) != 1 || !strings.Contains(got["DC-03"][0], "does not exist") {
		t.Errorf("missing owner file: %v", got)
	}
}

// The container in a variant's context is not the component: padding .hm-tabs
// from a page is not a DC-02 violation, padding .hm-tabs__tab still is.
func TestDesignContractVariantContextSelfTest(t *testing.T) {
	t.Parallel()

	spec := `{"tabs":{"selector":".hm-tabs__tab","owner":"css/partials/tabs.css",
		"variants":{"seg":".hm-tabs[data-variant=\"seg\"] .hm-tabs__tab"},
		"props":{"padding-left":"var(--space-3)"},"variantProps":{"seg":{"padding-left":"var(--space-2)"}}}}`
	owner := ".hm-tabs__tab { padding: 0 var(--space-3); }\n.hm-tabs[data-variant=\"seg\"] .hm-tabs__tab { padding: 0 var(--space-2); }\n"
	run := func(page string) []uiViolation {
		return lintContract(t, fstest.MapFS{
			"components.json":       {Data: []byte(spec)},
			"css/partials/tabs.css": {Data: []byte(owner)},
			"css/pages/x.css":       {Data: []byte(page + "\n")},
		})
	}
	if got := run(".hm-pageheader .hm-tabs{padding-left:0}"); len(got) != 0 {
		t.Errorf("the tabs container was treated as a tab: %v", got)
	}
	if got := run(".hm-pageheader .hm-tabs__tab{padding-left:0}"); len(got) != 1 || got[0].Rule != "DC-02" {
		t.Errorf("a tab padded from a page must be one DC-02: %v", got)
	}
}

// A comma-list selector keeps every part's subject class.
func TestDesignContractSelectorListSelfTest(t *testing.T) {
	t.Parallel()

	spec := `{"chip":{"selector":".hm-msel__chip, .hm-msel__more","owner":"css/partials/m.css","props":{"height":"var(--space-6)"}},
		"button":{"selector":".hm-btn","owner":"css/partials/b.css","variants":{"icon":".hm-btn--icon, .hm-btn[data-icon-only]"},
		"props":{"padding-left":"var(--space-3)"},"variantProps":{"icon":{"padding-left":"var(--space-2)"}}}}`
	run := func(page string) []uiViolation {
		return lintContract(t, fstest.MapFS{
			"components.json":    {Data: []byte(spec)},
			"css/partials/m.css": {Data: []byte(".hm-msel__chip, .hm-msel__more { height: var(--space-6); }\n")},
			"css/partials/b.css": {Data: []byte(".hm-btn { padding-left: var(--space-3); }\n.hm-btn--icon, .hm-btn[data-icon-only] { padding-left: var(--space-2); }\n")},
			"css/pages/x.css":    {Data: []byte(page + "\n")},
		})
	}
	for _, page := range []string{".x .hm-msel__chip{height:28px}", ".y .hm-btn--icon{padding-left:2px}"} {
		n := 0
		for _, v := range run(page) {
			if v.Rule == "DC-02" {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%q must be one DC-02, got %d", page, n)
		}
	}
}

func TestLoadComponentsRejectsOrphanVariantProps(t *testing.T) {
	t.Parallel()

	load := func(js string) error {
		_, err := loadComponents(fstest.MapFS{"components.json": {Data: []byte(js)}})
		return err
	}
	ok := `{"a":{"selector":".a","owner":"x.css","variants":{"v":".a--v"},"props":{},"variantProps":{"v":{"z-index":"var(--z-popup)"}}}}`
	if err := load(ok); err != nil {
		t.Fatalf("a consistent spec failed: %v", err)
	}
	for name, js := range map[string]string{
		"no variants key": `{"a":{"selector":".a","owner":"x.css","props":{},"variantProps":{"v":{"z-index":"var(--z-popup)"}}}}`,
		"unknown variant": `{"a":{"selector":".a","owner":"x.css","variants":{"w":".a--w"},"props":{},"variantProps":{"v":{"z-index":"var(--z-popup)"}}}}`,
	} {
		if err := load(js); err == nil || !strings.Contains(err.Error(), "variantProps") {
			t.Errorf("%s: want a variantProps error, got %v", name, err)
		}
	}
}

const (
	docsPath       = "../../../docs/ADMIN_UI_RULES.md"
	docsTableBegin = "<!-- components:begin -->"
	docsTableEnd   = "<!-- components:end -->"
)

// contractTable renders components.json as the table in ADMIN_UI_RULES.md.
// It is deterministic: components, props and variants are sorted.
func contractTable(spec map[string]componentSpec) string {
	props := func(m map[string]string) string {
		var parts []string
		for _, k := range sortedKeys(m) {
			parts = append(parts, "`"+k+": "+m[k]+"`")
		}
		return strings.Join(parts, ", ")
	}
	var b strings.Builder
	b.WriteString("| Component | Selector | Owner | Partial | Props |\n|---|---|---|---|---|\n")
	for _, name := range sortedNames(spec) {
		c := spec[name]
		partial := "none"
		if c.Partial != "" {
			partial = "`" + c.Partial + "`"
		}
		cell := props(c.Props)
		for _, v := range sortedKeys(c.VariantProps) {
			cell += "; `" + c.Variants[v] + "`: " + props(c.VariantProps[v])
		}
		fmt.Fprintf(&b, "| %s | `%s` | `%s` | %s | %s |\n", name, c.Selector, c.Owner, partial, cell)
	}
	return b.String()
}

// lintDC04 compares the table in the docs with the generated one.
func lintDC04(spec map[string]componentSpec, doc string) []uiViolation {
	i, j := strings.Index(doc, docsTableBegin), strings.Index(doc, docsTableEnd)
	if i < 0 || j < i {
		return []uiViolation{{File: "docs/ADMIN_UI_RULES.md", Line: 1, Rule: "DC-04", Msg: "components table markers are missing"}}
	}
	have := strings.TrimSpace(doc[i+len(docsTableBegin) : j])
	if have != strings.TrimSpace(contractTable(spec)) {
		return []uiViolation{{File: "docs/ADMIN_UI_RULES.md", Line: lineOf(doc, i), Rule: "DC-04",
			Msg: "components table is stale: run go test ./internal/admin -run TestDesignContractDocsTable -update"}}
	}
	return nil
}

func TestDesignContractDocsTable(t *testing.T) {
	t.Parallel()

	spec, err := loadComponents(web.Admin())
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(docsPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	if *update {
		i, j := strings.Index(doc, docsTableBegin), strings.Index(doc, docsTableEnd)
		if i < 0 || j < i {
			t.Fatal("components table markers are missing")
		}
		doc = doc[:i+len(docsTableBegin)] + "\n" + contractTable(spec) + doc[j:]
		if err := os.WriteFile(docsPath, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range lintDC04(spec, doc) {
		t.Error(v.String())
	}
	for _, rule := range []string{"DC-01", "DC-02", "DC-03", "DC-04"} {
		if !strings.Contains(doc, "| "+rule+" |") {
			t.Errorf("%s is missing from the static rules table in docs/ADMIN_UI_RULES.md", rule)
		}
	}
	for _, name := range sortedNames(spec) {
		if !strings.Contains(doc, "`"+spec[name].Selector+"`") {
			t.Errorf("%s: selector is missing from the docs", name)
		}
	}
}

func TestDesignContractDocsSelfTest(t *testing.T) {
	t.Parallel()

	spec := map[string]componentSpec{
		"b": {Selector: ".b", Owner: "css/b.css", Props: map[string]string{"font-size": "var(--text-sm)"}},
		"a": {Selector: ".a", Owner: "css/a.css", Partial: "hm/a", Props: map[string]string{"z-index": "var(--z-popup)"}},
	}
	table := contractTable(spec)
	if strings.Index(table, "| a |") > strings.Index(table, "| b |") || table != contractTable(spec) {
		t.Fatalf("table is not sorted and deterministic:\n%s", table)
	}
	fresh := "x\n" + docsTableBegin + "\n" + table + docsTableEnd + "\ny"
	if got := lintDC04(spec, fresh); len(got) != 0 {
		t.Errorf("fresh table reported %v", got)
	}
	stale := strings.Replace(fresh, "--z-popup", "--z-toast", 1)
	if got := lintDC04(spec, stale); len(got) != 1 || got[0].Rule != "DC-04" {
		t.Errorf("stale table not reported: %v", got)
	}
	if got := lintDC04(spec, "no markers"); len(got) != 1 {
		t.Errorf("missing markers not reported: %v", got)
	}
}

func TestDesignContractAllowlistNeedlesAreSpecific(t *testing.T) {
	t.Parallel()

	for _, a := range contractAllow {
		if bareTrigger(a.Needle) {
			t.Errorf("allowlist entry %s %s %q is a bare trigger", a.Rule, a.File, a.Needle)
		}
		if a.Max > uiAllowHardMax {
			t.Errorf("allowlist entry %s %s %q declares Max %d above %d", a.Rule, a.File, a.Needle, a.Max, uiAllowHardMax)
		}
	}
}
