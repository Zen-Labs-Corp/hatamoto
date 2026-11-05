package jsonx

import (
	"encoding/json/jsontext"
	"testing"
)

func TestExactNumbersKeepsLargeIntegers(t *testing.T) {
	in := `{"a":9007199254740993,"l":[1.50,{"n":18446744073709551615}],"s":"x","b":true,"z":null}`
	var v any
	if err := Unmarshal([]byte(in), &v, ExactNumbers); err != nil {
		t.Fatal(err)
	}
	if n, ok := v.(map[string]any)["a"].(jsontext.Value); !ok || string(n) != "9007199254740993" {
		t.Fatalf("a = %#v", v.(map[string]any)["a"])
	}
	out, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"a":9007199254740993,"b":true,"l":[1.50,{"n":18446744073709551615}],"s":"x","z":null}`; string(out) != want {
		t.Fatalf("got %s\nwant %s", out, want)
	}
}
