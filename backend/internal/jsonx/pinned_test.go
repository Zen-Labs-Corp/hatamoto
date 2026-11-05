package jsonx

import (
	"encoding/json/jsontext"
	"reflect"
	"testing"
	"time"
)

type goldenInner struct {
	List []int          `json:"list"`
	Dict map[string]int `json:"dict"`
}

type goldenStruct struct {
	S      []string          `json:"s"`
	M      map[string]string `json:"m"`
	P      *goldenInner      `json:"p"`
	Inner  goldenInner       `json:"inner"`
	OmitS  []string          `json:"omit_s,omitempty"`
	OmitM  map[string]int    `json:"omit_m,omitempty"`
	OmitSt string            `json:"omit_st,omitempty"`
}

type rawHolder struct {
	Raw jsontext.Value `json:"raw"`
}

// TestPinnedGolden compares MarshalPinned with literal bytes frozen from v1
// (and checked against it by hand), not with a live encoder: under go1.27
// encoding/json is itself backed by v2, so it is no oracle. Invalid UTF-8
// becomes the raw U+FFFD bytes EF BF BD, which pre-go1.27 toolchains wrote as
// the escape \ufffd.
func TestPinnedGolden(t *testing.T) {
	ts := time.Date(2026, 11, 5, 10, 30, 15, 123456789, time.UTC)
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"nil fields", goldenStruct{}, `{"s":null,"m":null,"p":null,"inner":{"list":null,"dict":null}}`},
		{"empty fields", goldenStruct{S: []string{}, M: map[string]string{}, Inner: goldenInner{List: []int{}}}, `{"s":[],"m":{},"p":null,"inner":{"list":[],"dict":null}}`},
		{"nil map", map[string]int(nil), `null`},
		{"nil slice", []int(nil), `null`},
		{"map key order", map[string]int{"b": 1, "a": 2, "c": 3, "B": 4, "": 5, "_": 6}, `{"":5,"B":4,"_":6,"a":2,"b":1,"c":3}`},
		{"html", "<script>alert(1) & 2</script>", `"\u003cscript\u003ealert(1) \u0026 2\u003c/script\u003e"`},
		{"line separators", "a\u2028b\u2029c", `"a\u2028b\u2029c"`},
		{"bytes", []byte("hello\x00\xff"), `"aGVsbG8A/w=="`},
		{"nil bytes", []byte(nil), `null`},
		{"time", ts, `"2026-11-05T10:30:15.123456789Z"`},
		{"time zone", ts.In(time.FixedZone("x", 3600)), `"2026-11-05T11:30:15.123456789+01:00"`},
		{"floats", []float64{1e21, 1e-7, 100, 0.1, 1, 1e20, 123456789.125, -0.0000001}, `[1e+21,1e-7,100,0.1,1,100000000000000000000,123456789.125,-1e-7]`},
		{"float32", []float32{0.1, 1e21, 3.4028235e38}, `[0.1,1e+21,3.4028235e+38]`},
		{"invalid utf8", "a\xffb", "\"a\xef\xbf\xbdb\""},
		{"invalid utf8 key", map[string]int{"k\xff": 1}, "{\"k\xef\xbf\xbd\":1}"},
		{"raw message", rawHolder{Raw: jsontext.Value(`{ "a" : [1, 2] }`)}, `{"raw":{"a":[1,2]}}`},
		{"nested nil", map[string]any{"a": []string(nil), "b": map[string]int(nil), "c": (*int)(nil)}, `{"a":null,"b":null,"c":null}`},
		{"any values", []any{1, "x", nil, true, 2.5, map[string]any{"z": 1, "y": 2}}, `[1,"x",null,true,2.5,{"y":2,"z":1}]`},
		{"embedded pointers", &goldenStruct{P: &goldenInner{}}, `{"s":null,"m":null,"p":{"list":null,"dict":null},"inner":{"list":null,"dict":null}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MarshalPinned(tc.v)
			if err != nil {
				t.Fatalf("pinned: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("pinned bytes differ\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

type ptrMarshaler struct{ A int }

func (p *ptrMarshaler) MarshalJSON() ([]byte, error) { return []byte(`"custom"`), nil }

type holdsPtrMarshaler struct {
	V ptrMarshaler `json:"v"`
}

type omitScalars struct {
	N int  `json:"n,omitempty"`
	B bool `json:"b,omitempty"`
}

type omitStruct struct {
	Meta struct {
		A int `json:"a,omitzero"`
	} `json:"meta,omitempty"`
}

type omitPtr struct {
	Notify *string `json:"notify_email,omitempty"`
}

type emptyMarshaler string

func (e emptyMarshaler) MarshalJSON() ([]byte, error) { return []byte(e), nil }

type omitMarshaler struct {
	V emptyMarshaler `json:"v,omitempty"`
}

// dupNames has two fields with one JSON name. It is built with reflection
// because go vet rejects such a struct literal.
func dupNames() any {
	t := reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: reflect.TypeFor[int](), Tag: `json:"x"`},
		{Name: "B", Type: reflect.TypeFor[int](), Tag: `json:"x"`},
	})
	return reflect.New(t).Elem().Interface()
}

// TestPinnedKnownDivergences records where a v2-only Pinned set differs from
// v1, as known as of go1.27.3. Each case carries the literal v1 bytes and the
// fix a later part applies to the affected wire type; when one starts to
// match v1, the assertion fails and the note can go.
func TestPinnedKnownDivergences(t *testing.T) {
	cases := []struct {
		name string
		v    any
		fix  string
		v1   string
		v2   string
	}{
		{
			name: "omitempty int and bool",
			v:    omitScalars{},
			fix:  "tag the fields omitzero",
			v1:   `{}`,
			v2:   `{"n":0,"b":false}`,
		},
		{
			name: "pointer receiver MarshalJSON, not addressable",
			v:    holdsPtrMarshaler{},
			fix:  "pass a pointer to the holder",
			v1:   `{"v":{"A":0}}`,
			v2:   `{"v":"custom"}`,
		},
		{
			name: "time.Duration",
			v:    map[string]time.Duration{"d": time.Second},
			fix:  "use a typed marshaler writing nanoseconds",
			v1:   `{"d":1000000000}`,
			v2:   "", // v2 refuses to encode it
		},
		{
			name: "[N]byte",
			v:    [2]byte{1, 2},
			fix:  "use a typed marshaler writing an array",
			v1:   `[1,2]`,
			v2:   `"AQI="`,
		},
		{
			name: "omitempty struct field that encodes as {}",
			v:    omitStruct{},
			fix:  "tag the field omitzero, or drop omitempty to keep writing {}",
			v1:   `{"meta":{}}`,
			v2:   `{}`,
		},
		{
			name: "omitempty non-nil pointer to an empty string",
			v:    omitPtr{Notify: new(string)},
			fix:  "drop omitempty (a nil pointer then writes null) or tag omitzero (keeps the empty string)",
			v1:   `{"notify_email":""}`,
			v2:   `{}`,
		},
		{
			name: "omitempty MarshalJSON returning an empty string",
			v:    omitMarshaler{V: `""`},
			fix:  "tag the field omitzero, or drop omitempty",
			v1:   `{"v":""}`,
			v2:   `{}`,
		},
		{
			name: "omitempty MarshalJSON returning {}",
			v:    omitMarshaler{V: `{}`},
			fix:  "tag the field omitzero, or drop omitempty",
			v1:   `{"v":{}}`,
			v2:   `{}`,
		},
		{
			name: "omitempty MarshalJSON returning null",
			v:    omitMarshaler{V: `null`},
			fix:  "tag the field omitzero, or drop omitempty",
			v1:   `{"v":null}`,
			v2:   `{}`,
		},
		{
			name: "two fields with one JSON name",
			v:    dupNames(),
			fix:  "rename or drop one of the fields; v1 silently omitted both",
			v1:   `{}`,
			v2:   "", // v2 returns a duplicate-name error
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Log("later-part fix:", tc.fix)
			got, err := MarshalPinned(tc.v)
			if tc.v2 == "" {
				if err == nil {
					t.Fatalf("pinned encoded %s, expected an error (v1: %s)", got, tc.v1)
				}
				return
			}
			if err != nil {
				t.Fatalf("pinned: %v", err)
			}
			if string(got) == tc.v1 {
				t.Fatalf("pinned now matches v1 (%s): drop this divergence", got)
			}
			if string(got) != tc.v2 {
				t.Fatalf("pinned = %s, want %s (v1: %s)", got, tc.v2, tc.v1)
			}
		})
	}
}

// TestPinnedUnmarshalFoldsDelimiters pins a divergence of PinnedUnmarshal: v2
// case-insensitive matching also ignores "_" and "-" in names, v1 did not. So
// {"user_id":1,"userid":2} decodes to 2 here and to 1 under v1.
// Fix for a later part: do not give a pinned input type fields whose names
// differ only by case and delimiters, or rename the stray field.
func TestPinnedUnmarshalFoldsDelimiters(t *testing.T) {
	var got struct {
		UserID int `json:"user_id"`
	}
	if err := UnmarshalPinned([]byte(`{"user_id":1,"userid":2}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.UserID != 2 { // v1: 1
		t.Fatalf("UserID = %d, want 2 (v1: 1)", got.UserID)
	}
}

func TestPinnedAddressableMatchesV1(t *testing.T) {
	// The documented fix for the pointer-receiver divergence.
	v := &holdsPtrMarshaler{}
	got, err := MarshalPinned(v)
	if want := `{"v":"custom"}`; err != nil || string(got) != want {
		t.Fatalf("pinned %s %v, v1 %s", got, err, want)
	}
	// And omitzero keeps the omission of zero scalars.
	type fixed struct {
		N int  `json:"n,omitzero"`
		B bool `json:"b,omitzero"`
	}
	got, err = MarshalPinned(fixed{})
	if err != nil || string(got) != `{}` {
		t.Fatalf("omitzero: %s %v", got, err)
	}
}

func TestUnmarshalPinned(t *testing.T) {
	type in struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}
	// Each input decodes to {Name: "a", N: 1} under v1.
	for name, raw := range map[string]string{
		"exact":        `{"name":"a","n":1}`,
		"case":         `{"NAME":"a","N":1}`,
		"dup names":    `{"name":"x","name":"a","n":1}`,
		"invalid utf8": "{\"name\":\"a\",\"n\":1,\"extra\":\"\xff\"}",
	} {
		t.Run(name, func(t *testing.T) {
			var got in
			if err := UnmarshalPinned([]byte(raw), &got); err != nil {
				t.Fatalf("pinned: %v", err)
			}
			if got != (in{Name: "a", N: 1}) {
				t.Fatalf("pinned %+v", got)
			}
		})
	}
	var v in
	if err := UnmarshalPinned([]byte(`{"name":"a"} junk`), &v); err == nil {
		t.Fatal("trailing data accepted")
	}
	if err := UnmarshalPinned([]byte(`{"name":"a"}{"name":"b"}`), &v); err == nil {
		t.Fatal("second value accepted")
	}
}
