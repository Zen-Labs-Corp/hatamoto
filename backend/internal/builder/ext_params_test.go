package builder

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

var paramDecl = []ExtParam{
	{Name: "q", Label: "Search", Type: ParamText},
	{Name: "cat", Label: "Category", Type: ParamSelect, Options: []ExtOption{{Value: "news", Label: "News"}, {Value: "ads"}}},
	{Name: "hot", Label: "Hot", Type: ParamBool},
	{Name: "min", Label: "Minimum", Type: ParamNumber},
}

func TestSanitizeParams(t *testing.T) {
	long := strings.Repeat("x", MaxParamValue+1)
	for _, c := range []struct {
		name string
		in   map[string]any
		want map[string]any
	}{
		{"all kinds", map[string]any{"q": "go", "cat": "news", "hot": true, "min": 2.5}, map[string]any{"q": "go", "cat": "news", "hot": true, "min": 2.5}},
		{"nil", nil, nil},
		{"unknown name dropped", map[string]any{"zzz": "a"}, nil},
		{"200 bytes kept", map[string]any{"q": strings.Repeat("x", MaxParamValue)}, map[string]any{"q": strings.Repeat("x", MaxParamValue)}},
		{"201 bytes dropped", map[string]any{"q": long}, nil},
		{"multibyte counts bytes", map[string]any{"q": strings.Repeat("é", 101)}, nil},
		{"empty string omitted", map[string]any{"q": ""}, nil},
		{"select outside options", map[string]any{"cat": "other"}, nil},
		{"select wrong type", map[string]any{"cat": 1.0}, nil},
		{"bool true", map[string]any{"hot": true}, map[string]any{"hot": true}},
		{"bool false omitted", map[string]any{"hot": false}, nil},
		{"bool as string dropped", map[string]any{"hot": "true"}, nil},
		{"number as string dropped", map[string]any{"min": "3"}, nil},
		{"number int normalised", map[string]any{"min": 3}, map[string]any{"min": 3.0}},
		{"number json.Number", map[string]any{"min": json.Number("1.0")}, map[string]any{"min": 1.0}},
		{"number NaN dropped", map[string]any{"min": math.NaN()}, nil},
		{"number Inf dropped", map[string]any{"min": math.Inf(1)}, nil},
		{"text wrong type", map[string]any{"q": 5.0}, nil},
		{"only empties", map[string]any{"q": "", "cat": "", "hot": false}, nil},
	} {
		got := SanitizeParams(paramDecl, c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %#v, want %#v", c.name, got, c.want)
		}
	}
	if got := SanitizeParams(nil, map[string]any{"q": "a"}); got != nil {
		t.Errorf("no declarations: %#v", got)
	}
}

func TestCanonicalParams(t *testing.T) {
	a := SanitizeParams(paramDecl, map[string]any{"q": "go", "min": 1, "hot": true})
	b := SanitizeParams(paramDecl, map[string]any{"hot": true, "min": json.Number("1.0"), "q": "go"})
	if CanonicalParams(a) != CanonicalParams(b) {
		t.Errorf("%s != %s", CanonicalParams(a), CanonicalParams(b))
	}
	if got, want := CanonicalParams(a), `{"hot":true,"min":1,"q":"go"}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	for range 50 { // map order is random: the bytes must not follow it
		if CanonicalParams(a) != `{"hot":true,"min":1,"q":"go"}` {
			t.Fatal("unstable")
		}
	}
	if CanonicalParams(map[string]any{"min": 1.5}) == CanonicalParams(map[string]any{"min": 1.0}) {
		t.Error("different numbers collide")
	}
}

func TestExtSourcesParamsRegistry(t *testing.T) {
	decl := cloneExtParams(paramDecl)
	withExt(t,
		ExtSource{Plugin: "weather", Name: "forecast", Label: "F", Kind: ExtItems, Params: decl},
		ExtSource{Plugin: "weather", Name: "temp", Label: "T", Kind: ExtValue, Params: decl},
		ExtSource{Plugin: "weather", Name: "bad", Label: "B", Kind: ExtItems, Params: []ExtParam{
			{Name: "ok", Label: "O", Type: ParamText},
			{Name: "ok", Label: "dup", Type: ParamBool},
			{Name: "sel", Label: "S", Type: ParamSelect},
			{Name: "typ", Label: "T", Type: "date"},
		}},
	)
	got := map[string]ExtSource{}
	for _, s := range ExtSources() {
		got[s.Name] = s
	}
	if len(got["forecast"].Params) != 4 {
		t.Errorf("forecast params = %v", got["forecast"].Params)
	}
	if got["temp"].Params != nil {
		t.Errorf("value source kept params: %v", got["temp"].Params)
	}
	if p := got["bad"].Params; len(p) != 1 || p[0].Name != "ok" {
		t.Errorf("invalid params not dropped: %v", p)
	}
	// Mutating what the caller handed in or got back never reaches the registry.
	decl[1].Options[0].Value = "changed"
	got["forecast"].Params[1].Options[0].Value = "changed"
	for _, s := range ExtSources() {
		if s.Name == "forecast" && s.Params[1].Options[0].Value != "news" {
			t.Errorf("registry mutated: %q", s.Params[1].Options[0].Value)
		}
	}
}

func TestStructuralParamsErr(t *testing.T) {
	many := map[string]any{}
	for i := range maxStoredParams + 1 {
		many[strings.Repeat("k", i+1)] = "v"
	}
	for _, c := range []struct {
		name string
		in   any
		ok   bool
	}{
		{"scalars", map[string]any{"a": "x", "b": 1.5, "c": true}, true},
		{"empty", map[string]any{}, true},
		{"go string map", map[string]string{"a": "x"}, true},
		{"array", []any{"a"}, false},
		{"string", "a", false},
		{"nested", map[string]any{"a": map[string]any{}}, false},
		{"list value", map[string]any{"a": []any{}}, false},
		{"null value", map[string]any{"a": nil}, false},
		{"long string", map[string]any{"a": strings.Repeat("x", MaxParamValue+1)}, false},
		{"33 entries", many, false},
	} {
		if err := StructuralParamsErr(c.in); (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestLoopParamsProp(t *testing.T) {
	for _, c := range []struct {
		name string
		p    map[string]any
		ok   bool
	}{
		{"object", map[string]any{"from": "source", "source": "ext.weather.forecast", "params": map[string]any{"q": "go", "n": 2, "on": true}}, true},
		{"undeclared name kept", map[string]any{"params": map[string]any{"gone": "x"}}, true},
		{"on a query loop", map[string]any{"from": "query", "params": map[string]any{"q": "go"}}, true},
		{"array", map[string]any{"params": []any{"a"}}, false},
		{"oversize string", map[string]any{"params": map[string]any{"q": strings.Repeat("x", MaxParamValue+1)}}, false},
		{"nested", map[string]any{"params": map[string]any{"q": map[string]any{}}}, false},
	} {
		err := loopDoc(c.p).Validate()
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v does not wrap ErrInvalid", c.name, err)
		}
	}
}
