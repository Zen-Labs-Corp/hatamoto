package entity

import (
	"encoding/json/jsontext"
	"reflect"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

func TestMergePatch(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, target, patch, want string }{
		{"replace member", `{"a":"b"}`, `{"a":"c"}`, `{"a":"c"}`},
		{"add member", `{"a":"b"}`, `{"b":"c"}`, `{"a":"b","b":"c"}`},
		{"null removes", `{"a":"b"}`, `{"a":null}`, `{}`},
		{"null removes one of two", `{"a":"b","b":"c"}`, `{"a":null}`, `{"b":"c"}`},
		{"scalar replaces array", `{"a":["b"]}`, `{"a":"c"}`, `{"a":"c"}`},
		{"array replaces scalar", `{"a":"c"}`, `{"a":["b"]}`, `{"a":["b"]}`},
		{"nested merge", `{"a":{"b":"c"}}`, `{"a":{"b":"d","c":null}}`, `{"a":{"b":"d"}}`},
		{"array replaces array", `{"a":[{"b":"c"}]}`, `{"a":[1]}`, `{"a":[1]}`},
		{"array patch", `["a","b"]`, `["c","d"]`, `["c","d"]`},
		{"array patch on object", `{"a":"b"}`, `["c"]`, `["c"]`},
		{"null patch", `{"a":"foo"}`, `null`, `null`},
		{"string patch", `{"a":"foo"}`, `"bar"`, `"bar"`},
		{"null under absent key", `{"e":null}`, `{"a":1}`, `{"e":null,"a":1}`},
		{"array target", `[1,2]`, `{"a":"b","c":null}`, `{"a":"b"}`},
		{"null leaves no key", `{}`, `{"a":{"bb":{"ccc":null}}}`, `{"a":{"bb":{}}}`},
		{"empty target", ``, `{"a":1}`, `{"a":1}`},
		{"unparsable target", `{nope`, `{"a":1}`, `{"a":1}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := MergePatch(jsontext.Value(tc.target), jsontext.Value(tc.patch))
			if err != nil {
				t.Fatal(err)
			}
			var g, w any
			if err := jsonx.Unmarshal(got, &g); err != nil {
				t.Fatalf("result %s: %v", got, err)
			}
			if err := jsonx.Unmarshal([]byte(tc.want), &w); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(g, w) {
				t.Errorf("MergePatch(%s, %s) = %s, want %s", tc.target, tc.patch, got, tc.want)
			}
		})
	}
}

func TestMergePatchKeepsExactNumbers(t *testing.T) {
	t.Parallel()
	got, err := MergePatch(jsontext.Value(`{"n":1}`), jsontext.Value(`{"big":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "9007199254740993") {
		t.Errorf("digits lost: %s", got)
	}
}

func TestMergePatchInvalidPatch(t *testing.T) {
	t.Parallel()
	if _, err := MergePatch(jsontext.Value(`{"a":1}`), jsontext.Value(`{"a":`)); err == nil {
		t.Error("want an error for an invalid patch")
	}
}
