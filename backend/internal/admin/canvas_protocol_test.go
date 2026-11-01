package admin

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// This file is the Go mirror of web/admin/js/canvas/protocol.js: a reference
// validator written from the rules in docs/CANVAS.md, run over the shared
// fixture. The same fixture runs against the real protocol.js under node (QA
// harness), so a drift in either implementation fails one of the two. JSON
// cannot carry NaN or Infinity; the finite-number rule is tested directly.

type protocolCase struct {
	Name   string `json:"name"`
	Dir    string `json:"dir"`
	Valid  bool   `json:"valid"`
	Msg    any    `json:"msg"`
	Expect any    `json:"expect"`
}

func loadProtocolCases(t *testing.T) []protocolCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/canvas_protocol_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []protocolCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	for i := range f.Cases {
		f.Cases[i].Msg = expandFixture(f.Cases[i].Msg)
		f.Cases[i].Expect = expandFixture(f.Cases[i].Expect)
	}
	return f.Cases
}

// expandFixture turns {"$str":[ch,n]} and {"$repeat":[el,n]} into the value.
func expandFixture(v any) any {
	switch x := v.(type) {
	case []any:
		for i := range x {
			x[i] = expandFixture(x[i])
		}
	case map[string]any:
		if a, ok := x["$str"].([]any); ok {
			return strings.Repeat(a[0].(string), int(a[1].(float64)))
		}
		if a, ok := x["$repeat"].([]any); ok {
			out := make([]any, int(a[1].(float64)))
			for i := range out {
				out[i] = expandFixture(refClone(a[0]))
			}
			return out
		}
		for k := range x {
			x[k] = expandFixture(x[k])
		}
	}
	return v
}

func refClone(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

var (
	refIDRe       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	refPropRe     = regexp.MustCompile(`^[a-z-]{1,40}$`)
	refTextPropRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
)

type refObj = map[string]any

func refIsObj(v any) (refObj, bool) { m, ok := v.(map[string]any); return m, ok }
func refIsStr(v any) (string, bool) { s, ok := v.(string); return s, ok }
func refIsNum(v any) bool           { _, ok := v.(float64); return ok } // JSON numbers are finite
func refIsID(v any) bool            { s, ok := v.(string); return ok && refIDRe.MatchString(s) }
func refIsKind(v any) bool          { s, _ := v.(string); return s == "s" || s == "n" }
func refIsInt(v any) bool           { f, ok := v.(float64); return ok && f == float64(int64(f)) }
func refPick(m refObj, keys ...string) refObj {
	out := refObj{}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}
func refNums(m refObj, keys ...string) bool {
	for _, k := range keys {
		if !refIsNum(m[k]) {
			return false
		}
	}
	return true
}

var (
	refMarks      = map[string]bool{"bold": true, "italic": true, "underline": true, "strike": true, "code": true, "link": true}
	refFormatCmds = map[string]bool{"bold": true, "italic": true, "underline": true, "strike": true, "code": true, "link": true, "unlink": true, "clear": true, "done": true}
)

// refParse mirrors parseFrameMessage: the typed message, or nil.
func refParse(data any) refObj {
	m, ok := refIsObj(data)
	if !ok {
		return nil
	}
	typ, ok := refIsStr(m["type"])
	if !ok {
		return nil
	}
	switch m["hm"] {
	case float64(2):
		return refParseV2(typ, m)
	case float64(1):
		return refParseV1(typ, m)
	}
	return nil
}

func refParseV2(typ string, m refObj) refObj {
	shift, _ := m["shift"].(bool)
	_, shiftOK := m["shift"].(bool)
	_, ctrlOK := m["ctrl"].(bool)
	out := func(keys ...string) refObj {
		o := refPick(m, keys...)
		o["hm"], o["type"] = float64(2), typ
		return o
	}
	switch typ {
	case "ready":
		if _, ok := refIsStr(m["contract"]); ok {
			return out("contract")
		}
	case "layout":
		nodes, ok := m["nodes"].([]any)
		mainBox, ok2 := refIsObj(m["main"])
		if !ok || !ok2 || !refIsNum(m["docH"]) || !refNums(mainBox, "x", "y", "w", "h") || len(nodes) > 5000 {
			return nil
		}
		typed := make([]any, 0, len(nodes))
		for _, n := range nodes {
			nm, ok := refIsObj(n)
			if !ok || !refIsID(nm["id"]) || !refIsKind(nm["k"]) || !refNums(nm, "x", "y", "w", "h") {
				return nil
			}
			if t, ok := refIsStr(nm["t"]); !ok || len(t) > 100 {
				return nil
			}
			if p, ok := refIsStr(nm["p"]); !ok || p != "" && !refIDRe.MatchString(p) {
				return nil
			}
			if c, has := nm["c"]; has && c != float64(1) {
				return nil
			}
			if d, has := nm["dir"]; has && d != "row" && d != "column" && d != "grid" {
				return nil
			}
			if pd, has := nm["pd"]; has {
				arr, ok := pd.([]any)
				if !ok || len(arr) != 4 {
					return nil
				}
				for _, e := range arr {
					if f, ok := e.(float64); !ok || f != math.Trunc(f) || f < 0 || f > 10000 {
						return nil
					}
				}
			}
			typed = append(typed, refPick(nm, "id", "k", "t", "p", "x", "y", "w", "h", "c", "dir", "pd"))
		}
		o := out("docH")
		o["nodes"], o["main"] = typed, refPick(mainBox, "x", "y", "w", "h")
		return o
	case "hover":
		if m["id"] == nil {
			if _, has := m["id"]; has {
				return refObj{"hm": float64(2), "type": typ, "id": nil}
			}
			return nil
		}
		if refIsID(m["id"]) && refIsKind(m["k"]) {
			return out("id", "k")
		}
	case "select":
		if refIsID(m["id"]) && refIsKind(m["k"]) && shiftOK && ctrlOK {
			return out("id", "k", "shift", "ctrl")
		}
	case "contextmenu":
		if refIsID(m["id"]) && refIsKind(m["k"]) && refIsNum(m["x"]) && refIsNum(m["y"]) {
			return out("id", "k", "x", "y")
		}
	case "text":
		// exactly one of value (prop other than html) or html (prop html), each at most 200,000 characters
		p, ok2 := refIsStr(m["prop"])
		if !refIsID(m["id"]) || !refIsKind(m["k"]) || !ok2 || !refTextPropRe.MatchString(p) {
			return nil
		}
		key := "value"
		if p == "html" {
			key = "html"
		}
		other := map[string]string{"value": "html", "html": "value"}[key]
		v, ok := refIsStr(m[key])
		if _, both := m[other]; ok && !both && len([]rune(v)) <= 20000 {
			return out("id", "k", "prop", key)
		}
	case "selection":
		p, ok := refIsStr(m["prop"])
		rect, ok2 := refIsObj(m["rect"])
		marks, ok3 := m["marks"].([]any)
		if !refIsID(m["id"]) || !refIsKind(m["k"]) || !ok || !refTextPropRe.MatchString(p) || !ok2 || !refNums(rect, "x", "y", "w", "h") || !ok3 || len(marks) > 6 {
			return nil
		}
		seen := map[string]bool{}
		for _, x := range marks {
			mk, ok := refIsStr(x)
			if !ok || !refMarks[mk] || seen[mk] {
				return nil
			}
			seen[mk] = true
		}
		o := out("id", "k", "prop")
		o["rect"], o["marks"] = refPick(rect, "x", "y", "w", "h"), marks
		if v, has := m["el"]; has {
			el, ok := refIsObj(v)
			if !ok || !refNums(el, "x", "y", "w", "h") {
				return nil
			}
			o["el"] = refPick(el, "x", "y", "w", "h")
		}
		if v, has := m["kind"]; has {
			k, ok := refIsStr(v)
			if !ok || (k != "heading" && k != "text") {
				return nil
			}
			o["kind"] = k
		}
		if v, has := m["link"]; has {
			l, ok := refIsObj(v)
			href, ok2 := refIsStr(l["href"])
			blank, ok3 := l["blank"].(bool)
			if !ok || !ok2 || !ok3 || len([]rune(href)) > 2000 {
				return nil
			}
			o["link"] = refObj{"href": href, "blank": blank}
		}
		return o
	case "textlimit", "textend":
		p, ok := refIsStr(m["prop"])
		if refIsID(m["id"]) && refIsKind(m["k"]) && ok && refTextPropRe.MatchString(p) {
			return out("id", "k", "prop")
		}
	case "painted":
		f, ok := m["seq"].(float64)
		okFlag, hasOK := m["ok"].(bool)
		if _, present := m["ok"]; ok && refIsInt(f) && f >= 0 && (!present || hasOK) {
			o := out("seq")
			o["ok"] = !hasOK || okFlag
			return o
		}
	case "key":
		if k, ok := refIsStr(m["key"]); ok && len(k) >= 1 && len(k) <= 20 && shiftOK {
			_ = shift
			for _, opt := range []string{"alt", "mod"} {
				if v, present := m[opt]; present {
					if _, isBool := v.(bool); !isBool {
						return nil
					}
				}
			}
			o := out("key", "shift")
			for _, opt := range []string{"alt", "mod"} {
				if v, present := m[opt]; present {
					o[opt] = v
				}
			}
			return o
		}
	}
	return nil
}

func refParseV1(typ string, m refObj) refObj {
	out := func(keys ...string) refObj {
		o := refPick(m, keys...)
		o["hm"], o["type"] = float64(1), typ
		return o
	}
	s := func(keys ...string) bool {
		for _, k := range keys {
			if _, ok := refIsStr(m[k]); !ok {
				return false
			}
		}
		return true
	}
	box := func(v any, keys ...string) bool { b, ok := refIsObj(v); return ok && refNums(b, keys...) }
	switch typ {
	case "hm:ready":
		if s("contract") {
			return out("contract")
		}
	case "hm:layout":
		secs, ok := m["sections"].([]any)
		if !ok || !refIsNum(m["docHeight"]) {
			return nil
		}
		typed := []any{}
		for _, x := range secs {
			sm, ok := refIsObj(x)
			if _, ok2 := refIsStr(sm["id"]); !ok || !ok2 || !box(sm, "top", "height") {
				return nil
			}
			if _, ok := refIsStr(sm["type"]); !ok {
				return nil
			}
			typed = append(typed, refPick(sm, "id", "type", "top", "height"))
		}
		for _, k := range []string{"header", "footer"} {
			if v, has := m[k]; has && !box(v, "top", "height") {
				return nil
			}
		}
		o := out("docHeight", "header", "footer")
		o["sections"] = typed
		return o
	case "hm:select":
		if s("sectionId") {
			if _, has := m["path"]; has {
				if s("path") {
					return out("sectionId", "path")
				}
				return out("sectionId")
			}
			return out("sectionId")
		}
	case "hm:edit":
		if s("sectionId", "path", "value") && m["kind"] == "plaintext" {
			return out("sectionId", "path", "value", "kind")
		}
	case "hm:edit-text":
		idx, _ := m["index"].(float64)
		v, _ := m["value"].(string)
		e, _ := m["expect"].(string)
		if s("sectionId", "path", "value", "expect") && refIsInt(m["index"]) && idx >= 0 && idx <= 1_000_000 && len(v) <= 200000 && len(e) <= 200000 {
			return out("sectionId", "path", "index", "value", "expect")
		}
	case "hm:rich-open":
		if s("sectionId", "path") && box(m["rect"], "top", "height", "left", "width") {
			o := out("sectionId", "path")
			o["rect"] = refPick(m["rect"].(refObj), "top", "left", "width", "height")
			return o
		}
	case "hm:chrome-click":
		if m["area"] == "header" || m["area"] == "footer" {
			return out("area")
		}
	case "hm:scroll":
		if refIsNum(m["y"]) {
			return out("y")
		}
	case "hm:save":
		return out()
	case "hm:details-open":
		fields, ok := m["fields"].([]any)
		if !ok {
			return nil
		}
		typed := []any{}
		for _, f := range fields {
			fm, ok := refIsObj(f)
			if _, ok1 := refIsStr(fm["sectionId"]); !ok || !ok1 {
				return nil
			}
			if _, ok2 := refIsStr(fm["path"]); !ok2 {
				return nil
			}
			typed = append(typed, refPick(fm, "sectionId", "path"))
		}
		o := out()
		o["fields"] = typed
		return o
	}
	return nil
}

// refValidOut mirrors validToBridge.
func refValidOut(data any) bool {
	m, ok := refIsObj(data)
	if !ok || m["hm"] != float64(2) {
		return false
	}
	typ, ok := refIsStr(m["type"])
	if !ok {
		return false
	}
	if seq, has := m["seq"]; has && !(refIsInt(seq) && seq.(float64) >= 0) {
		return false
	}
	nodeRef := func(sel, key any) bool { return (sel == "section" || sel == "node") && refIsID(key) }
	subject := func() bool { return nodeRef(m["sel"], m["key"]) }
	html := func() bool { s, ok := refIsStr(m["html"]); return ok && len(s) <= 1_000_000 }
	cssMap := func(v any) bool {
		c, ok := refIsObj(v)
		if !ok || len(c) > 200 {
			return false
		}
		for k, r := range c {
			if s, ok := refIsStr(r); !refIsID(k) || !ok || len(s) > 100_000 {
				return false
			}
		}
		return true
	}
	optCSS := func() bool { c, has := m["css"]; return !has || cssMap(c) }
	place := func() bool {
		n := 0
		var which any
		for _, k := range []string{"before", "after", "parent"} {
			if v, has := m[k]; has {
				n++
				which = v
			}
		}
		if n != 1 {
			return false
		}
		p, ok := refIsObj(which)
		return ok && (p["sel"] == "main" || nodeRef(p["sel"], p["key"]))
	}
	switch typ {
	case "init":
		_, ed := m["editable"].(bool)
		sk, has := m["selectedKey"]
		return ed && has && (sk == nil || refIsID(sk))
	case "patch":
		return subject() && html() && optCSS()
	case "insert":
		return html() && optCSS() && place()
	case "move":
		return subject() && place()
	case "remove", "edit":
		return subject()
	case "setVar":
		d, ok := m["decls"].([]any)
		if !subject() || !ok || len(d) > 50 {
			return false
		}
		for _, x := range d {
			pair, ok := x.([]any)
			if !ok || len(pair) != 2 {
				return false
			}
			prop, ok := refIsStr(pair[0])
			if !ok || !refPropRe.MatchString(prop) {
				return false
			}
			if pair[1] != nil {
				if v, ok := refIsStr(pair[1]); !ok || len(v) > 200 {
					return false
				}
			}
		}
		return true
	case "css":
		return subject() && cssMap(m["css"])
	case "select":
		_, ok := m["scrollIntoView"].(bool)
		return subject() && ok
	case "scrollTo":
		return refIsNum(m["y"])
	case "format":
		cmd, ok := refIsStr(m["cmd"])
		if !subject() || !ok || !refFormatCmds[cmd] {
			return false
		}
		href, hasHref := m["href"]
		if hasHref {
			h, ok := refIsStr(href)
			if n := len([]rune(h)); !ok || n < 1 || n > 2000 {
				return false
			}
		} else if cmd == "link" {
			return false
		}
		if nt, has := m["newTab"]; has {
			if _, ok := nt.(bool); !ok {
				return false
			}
		}
		return true
	case "force":
		st, has := m["state"]
		if st == nil {
			return subject() && has
		}
		s, ok := refIsStr(st)
		return subject() && ok && (s == "hover" || s == "focus" || s == "active" || s == "parent-hover")
	}
	return false
}

func TestProtocolFixtureAgreesWithReference(t *testing.T) {
	t.Parallel()

	cases := loadProtocolCases(t)
	if len(cases) < 40 {
		t.Fatalf("fixture has %d cases, want at least 40", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.Dir == "out" {
				if got := refValidOut(c.Msg); got != c.Valid {
					t.Errorf("validToBridge reference = %v, fixture says %v", got, c.Valid)
				}
				return
			}
			got := refParse(c.Msg)
			if !c.Valid {
				if got != nil {
					t.Errorf("accepted an invalid message: %v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("rejected a valid message")
			}
			if !reflect.DeepEqual(refClone(got), c.Expect) {
				t.Errorf("typed message\n got %v\nwant %v", refClone(got), c.Expect)
			}
		})
	}
}

func TestProtocolFixtureCoversEveryType(t *testing.T) {
	t.Parallel()

	cases := loadProtocolCases(t)
	in, out := map[string]bool{}, map[string]bool{}
	for _, c := range cases {
		if m, ok := c.Msg.(map[string]any); ok && c.Valid {
			typ, _ := m["type"].(string)
			if c.Dir == "out" {
				out[typ] = true
			} else {
				in[typ] = true
			}
		}
	}
	for _, typ := range []string{"hm:ready", "hm:layout", "hm:select", "hm:edit", "hm:edit-text", "hm:rich-open", "hm:chrome-click", "hm:scroll", "hm:save", "hm:details-open",
		"ready", "layout", "hover", "select", "contextmenu", "text", "textend", "textlimit", "selection", "painted", "key"} {
		if !in[typ] {
			t.Errorf("no valid inbound case for %q", typ)
		}
	}
	for _, typ := range []string{"init", "patch", "insert", "move", "remove", "setVar", "css", "edit", "select", "scrollTo", "force", "format"} {
		if !out[typ] {
			t.Errorf("no valid outbound case for %q", typ)
		}
	}
}

// protocol.js and docs/CANVAS.md must name every message type in the fixture.
func TestProtocolSourcesMentionEveryFixtureType(t *testing.T) {
	t.Parallel()

	js, err := os.ReadFile("../../web/admin/js/canvas/protocol.js")
	if err != nil {
		t.Fatal(err)
	}
	doc, docErr := os.ReadFile("../../../docs/CANVAS.md") // lands with the docs todo
	types := map[string]bool{}
	for _, c := range loadProtocolCases(t) {
		if m, ok := c.Msg.(map[string]any); ok && c.Valid {
			typ, _ := m["type"].(string)
			types[typ] = true
		}
	}
	for typ := range types {
		if !strings.Contains(string(js), `case "`+typ+`"`) {
			t.Errorf("protocol.js has no case %q", typ)
		}
		if docErr == nil && !strings.Contains(string(doc), "`"+typ+"`") {
			t.Errorf("docs/CANVAS.md does not mention `%s`", typ)
		}
	}
}
