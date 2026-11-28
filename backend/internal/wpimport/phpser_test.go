package wpimport

import (
	"reflect"
	"strings"
	"testing"
)

func TestPHPUnserializeScalarsAndNesting(t *testing.T) {
	v, err := phpUnserialize(`a:4:{s:5:"title";s:11:"Zoë 日本";s:1:"n";i:-3;s:1:"f";d:1.5;s:3:"sub";a:2:{i:0;b:1;i:1;N;}}`)
	if err != nil {
		t.Fatal(err)
	}
	a := v.(*phpArray)
	if !reflect.DeepEqual(a.Keys, []string{"title", "n", "f", "sub"}) {
		t.Fatalf("keys = %v", a.Keys)
	}
	if a.Vals["title"] != "Zoë 日本" || a.Vals["n"] != int64(-3) || a.Vals["f"] != 1.5 {
		t.Errorf("values = %#v", a.Vals)
	}
	sub := a.Vals["sub"].(*phpArray)
	if sub.Vals["0"] != true || sub.Vals["1"] != nil {
		t.Errorf("sub = %#v", sub.Vals)
	}
}

func TestPHPUnserializeRejectsBadInput(t *testing.T) {
	deep := strings.Repeat(`a:1:{i:0;`, phpMaxDepth+2) + `i:1;` + strings.Repeat(`}`, phpMaxDepth+2)
	for name, in := range map[string]string{
		"empty": "", "truncated": `a:2:{i:0;s:3:"abc";`, "short string": `s:10:"abc";`, "huge count": `a:99999999999:{}`,
		"negative length": `s:-1:"";`, "object": `O:3:"Foo":0:{}`, "bad key": `a:1:{d:1.5;i:1;}`, "deep": deep,
		"oversized": `s:1:"` + strings.Repeat("x", phpMaxLen) + `";`, "no semicolon": `i:5`, "lone type": `a`,
	} {
		if _, err := phpUnserialize(in); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestPHPStrings(t *testing.T) {
	for in, want := range map[string][]string{
		"": nil, "12": {"12"}, `a:2:{i:0;s:2:"12";i:1;i:7;}`: {"12", "7"}, "a:broken": {"a:broken"},
	} {
		if got := phpStrings(in); !reflect.DeepEqual(got, want) {
			t.Errorf("phpStrings(%q) = %v, want %v", in, got, want)
		}
	}
}

func FuzzPHPUnserialize(f *testing.F) {
	f.Add(`a:1:{s:1:"k";a:1:{i:0;s:1:"v";}}`)
	f.Fuzz(func(t *testing.T, s string) { _, _ = phpUnserialize(s) })
}
