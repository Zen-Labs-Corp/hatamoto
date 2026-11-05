package jsonx

import (
	"bytes"
	"reflect"
	"testing"
)

func TestIndent(t *testing.T) {
	got, err := Indent(map[string]any{"b": []string(nil), "a": map[string]int(nil)})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a\": {},\n  \"b\": []\n}"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestIndentValue(t *testing.T) {
	got, err := IndentValue([]byte(`{"a":[1,2]}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"a\": [\n    1,\n    2\n  ]\n}"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if _, err := IndentValue([]byte(`{"a":`)); err == nil {
		t.Fatal("invalid input: want error")
	}
}

func TestValid(t *testing.T) {
	for in, want := range map[string]bool{
		`[]`: true, `{"a":1}`: true, `{"a":1,"a":2}`: false, `{`: false, ``: false, `1 2`: false,
	} {
		if got := Valid([]byte(in)); got != want {
			t.Errorf("Valid(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestEncode(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, map[string]any{"k": "<x>", "n": []int(nil)}); err != nil {
		t.Fatal(err)
	}
	if want := `{"k":"\u003cx\u003e","n":[]}` + "\n"; buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
	buf.Reset()
	if err := Encode(&buf, "<", Output); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "\"<\"\n" {
		t.Fatalf("explicit Output: got %q", buf.String())
	}
}

func TestObjectEntries(t *testing.T) {
	got, err := ObjectEntries([]byte(`{"z":1,"a":{"x":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	vals := map[string]string{}
	for _, e := range got {
		keys = append(keys, e.Key)
		vals[e.Key] = string(e.Value)
	}
	if !reflect.DeepEqual(keys, []string{"z", "a"}) || vals["z"] != "1" || vals["a"] != `{"x":2}` {
		t.Fatalf("keys %v vals %v", keys, vals)
	}
	if got, err := ObjectEntries([]byte(`{}`)); err != nil || len(got) != 0 {
		t.Fatalf("empty object: %v %v", got, err)
	}
	for _, bad := range []string{`[1]`, `{"a":`, ``, `{} {}`} {
		if _, err := ObjectEntries([]byte(bad)); err == nil {
			t.Errorf("ObjectEntries(%q): want error", bad)
		}
	}
}
