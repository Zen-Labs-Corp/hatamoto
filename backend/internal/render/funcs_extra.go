package render

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// maxFromJSON bounds the input of fromJSON; larger documents decode to nil.
const maxFromJSON = 1 << 20

// maxSplit bounds the parts split returns.
const maxSplit = 1000

// extraFuncs are the generic arithmetic, string and JSON template funcs. All
// are pure, never fail the render (bad input gives a zero value) and none
// returns template.HTML/JS/URL, so html/template still escapes every result.
// String funcs take the subject first, like the strings package ("hasPrefix"):
// {{ replace .Attrs.title "-" " " }}, {{ contains $s "x" }}.
func extraFuncs() template.FuncMap {
	return template.FuncMap{
		// docBody .Body: the body with ids on h2/h3 plus its .TOC items.
		"docBody": docBody,
		"add":     func(a, b any) int { return toInt(a) + toInt(b) },
		"sub":     func(a, b any) int { return toInt(a) - toInt(b) },
		"mul":     func(a, b any) int { return toInt(a) * toInt(b) },
		// div and mod by zero give 0; integer division truncates toward zero.
		"div": func(a, b any) int {
			if d := toInt(b); d != 0 {
				return toInt(a) / d
			}
			return 0
		},
		"mod": func(a, b any) int {
			if d := toInt(b); d != 0 {
				return toInt(a) % d
			}
			return 0
		},
		"lower":      func(s any) string { return strings.ToLower(toStr(s)) },
		"upper":      func(s any) string { return strings.ToUpper(toStr(s)) },
		"contains":   func(s, sub any) bool { return strings.Contains(toStr(s), toStr(sub)) },
		"replace":    func(s, old, repl any) string { return strings.ReplaceAll(toStr(s), toStr(old), toStr(repl)) },
		"split":      split,
		"trim":       func(s any) string { return strings.TrimSpace(toStr(s)) },
		"trimPrefix": func(s, p any) string { return strings.TrimPrefix(toStr(s), toStr(p)) },
		"trimSuffix": func(s, p any) string { return strings.TrimSuffix(toStr(s), toStr(p)) },
		"hasSuffix":  func(s, p any) bool { return strings.HasSuffix(toStr(s), toStr(p)) },
		"atoi":       toInt,
		"fromJSON":   fromJSON,
	}
}

// split cuts s at every sep (an empty sep splits into characters), at most
// maxSplit parts.
func split(s, sep any) []string {
	return strings.SplitN(toStr(s), toStr(sep), maxSplit)
}

// toStr renders strings and numbers as text; anything else (nil, maps, bools)
// is "".
func toStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case template.HTML:
		return string(t)
	case json.Number:
		return t.String()
	}
	switch rv := reflect.ValueOf(v); rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(rv.Float(), 'f', -1, 64)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprint(v)
	}
	return ""
}

// toInt converts ints, floats (truncated) and numeric strings (surrounding
// space allowed); anything else, an overflow or NaN is 0.
func toInt(v any) int {
	switch t := v.(type) {
	case string:
		return atoiString(t)
	case template.HTML:
		return atoiString(string(t))
	case json.Number:
		return atoiString(t.String())
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return clampInt(rv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return clampInt(int64(min(rv.Uint(), math.MaxInt32)))
	case reflect.Float32, reflect.Float64:
		return floatInt(rv.Float())
	}
	return 0
}

func atoiString(s string) int {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return clampInt(n)
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return floatInt(f)
	}
	return 0
}

// maxArith bounds every converted number so add/sub/mul of two of them cannot
// overflow an int64.
const maxArith = math.MaxInt32

func clampInt(n int64) int { return int(min(max(n, -maxArith), maxArith)) }

func floatInt(f float64) int {
	if math.IsNaN(f) {
		return 0
	}
	return clampInt(int64(max(min(f, maxArith), -maxArith)))
}

// fromJSON decodes a JSON document (e.g. the text a plugin func returns) into
// maps, slices, strings, float64, bool and nil. Invalid or oversized (> 1 MiB)
// input gives nil.
func fromJSON(v any) any {
	s := toStr(v)
	if s == "" || len(s) > maxFromJSON {
		return nil
	}
	var out any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}
