package jsonx

import (
	"strings"
	"testing"
)

type named struct {
	Name string `json:"name"`
}

func TestStrictRejects(t *testing.T) {
	cases := map[string]string{
		"duplicate name": `{"name":"a","name":"b"}`,
		"invalid utf8":   "{\"name\":\"a\xffb\"}",
		"trailing junk":  `{"name":"a"} junk`,
		"second value":   `{"name":"a"}{"name":"b"}`,
		"wrong type":     `{"name":1}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			var v named
			if err := Unmarshal([]byte(in), &v); err == nil {
				t.Fatalf("Unmarshal(%q) accepted", in)
			}
		})
	}
}

func TestStrictAccepts(t *testing.T) {
	var v named
	if err := Unmarshal([]byte("{\"name\":\"a\"} \n\t"), &v); err != nil {
		t.Fatalf("trailing whitespace: %v", err)
	}
	if v.Name != "a" {
		t.Fatalf("Name = %q", v.Name)
	}
	var r named
	if err := UnmarshalRead(strings.NewReader(`{"name":"b"}`), &r); err != nil || r.Name != "b" {
		t.Fatalf("UnmarshalRead = %v, %+v", err, r)
	}
	if err := UnmarshalRead(strings.NewReader(`{"name":"b"} x`), &r); err == nil {
		t.Fatal("UnmarshalRead accepted trailing data")
	}
}

func TestStrictNoUnknownRejectsWrongCase(t *testing.T) {
	var v named
	err := Unmarshal([]byte(`{"NAME":"a"}`), &v, StrictNoUnknown)
	if err == nil {
		t.Fatal("wrong-case name accepted")
	}
	if !IsUnknownMember(err) {
		t.Fatalf("IsUnknownMember(%v) = false", err)
	}
	if err := Unmarshal([]byte(`{"x":1}`), &v, StrictNoUnknown); !IsUnknownMember(err) {
		t.Fatalf("unknown member: %v", err)
	}
	// Strict alone ignores the wrong-case name.
	v = named{}
	if err := Unmarshal([]byte(`{"NAME":"a"}`), &v); err != nil || v.Name != "" {
		t.Fatalf("Strict: %v %+v", err, v)
	}
}

func TestErrorPointer(t *testing.T) {
	var v struct {
		In named `json:"in"`
	}
	err := Unmarshal([]byte(`{"in":{"name":1}}`), &v)
	if got := ErrorPointer(err); got != "/in/name" {
		t.Fatalf("ErrorPointer = %q (%v)", got, err)
	}
	if got := ErrorPointer(nil); got != "" {
		t.Fatalf("ErrorPointer(nil) = %q", got)
	}
}

func TestStoredIgnoresUnknownAndCase(t *testing.T) {
	var v named
	if err := Unmarshal([]byte(`{"NAME":"a","x":1}`), &v, Stored); err != nil || v.Name != "" {
		t.Fatalf("Stored: %v %+v", err, v)
	}
}

func TestOutputNilAndOrder(t *testing.T) {
	type out struct {
		S []string          `json:"s"`
		M map[string]string `json:"m"`
	}
	b, err := Marshal(out{})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"s":[],"m":{}}` {
		t.Fatalf("nil: %s", got)
	}
	b, err = Marshal(map[string]int{"b": 1, "a": 2, "B": 3, "c": 4})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"B":3,"a":2,"b":1,"c":4}` {
		t.Fatalf("order: %s", got)
	}
}

func TestHTMLSafeEscapesLineSeparators(t *testing.T) {
	got, err := Marshal("a\u2028b\u2029c", HTMLSafe)
	if err != nil || string(got) != `"a\u2028b\u2029c"` {
		t.Fatalf("HTMLSafe: %s %v", got, err)
	}
}

func TestHTMLSafe(t *testing.T) {
	plain, err := Marshal("<a>&")
	if err != nil || string(plain) != `"<a>&"` {
		t.Fatalf("Output: %s %v", plain, err)
	}
	safe, err := Marshal("<a>&", HTMLSafe)
	if err != nil || string(safe) != `"\u003ca\u003e\u0026"` {
		t.Fatalf("HTMLSafe: %s %v", safe, err)
	}
}

func TestStoredLooseMatchesNamesCaseInsensitively(t *testing.T) {
	type doc struct {
		PageSections []int `json:"page_sections"`
	}
	in := []byte(`{"Page_Sections":[1,2]}`)
	var strict, loose doc
	if err := Unmarshal(in, &strict, Stored); err != nil || len(strict.PageSections) != 0 {
		t.Fatalf("Stored should ignore a wrong-case name: %+v %v", strict, err)
	}
	if err := Unmarshal(in, &loose, StoredLoose); err != nil || len(loose.PageSections) != 2 {
		t.Fatalf("StoredLoose: %+v %v", loose, err)
	}
}
