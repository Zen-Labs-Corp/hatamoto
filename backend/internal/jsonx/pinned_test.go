package jsonx

import (
	"encoding/json"
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
	Raw json.RawMessage `json:"raw"`
}

func TestPinnedMatchesV1(t *testing.T) {
	ts := time.Date(2026, 11, 5, 10, 30, 15, 123456789, time.UTC)
	cases := map[string]any{
		"nil fields":        goldenStruct{},
		"empty fields":      goldenStruct{S: []string{}, M: map[string]string{}, Inner: goldenInner{List: []int{}}},
		"nil map":           map[string]int(nil),
		"nil slice":         []int(nil),
		"map key order":     map[string]int{"b": 1, "a": 2, "c": 3, "B": 4, "": 5, "_": 6},
		"html":              "<script>alert(1) & 2</script>",
		"line separators":   "a\u2028b\u2029c",
		"bytes":             []byte("hello\x00\xff"),
		"nil bytes":         []byte(nil),
		"time":              ts,
		"time zone":         ts.In(time.FixedZone("x", 3600)),
		"floats":            []float64{1e21, 1e-7, 100, 0.1, 1, 1e20, 123456789.125, -0.0000001},
		"float32":           []float32{0.1, 1e21, 3.4028235e38},
		"invalid utf8":      "a\xffb",
		"invalid utf8 key":  map[string]int{"k\xff": 1},
		"raw message":       rawHolder{Raw: json.RawMessage(`{ "a" : [1, 2] }`)},
		"nested nil":        map[string]any{"a": []string(nil), "b": map[string]int(nil), "c": (*int)(nil)},
		"any values":        []any{1, "x", nil, true, 2.5, map[string]any{"z": 1, "y": 2}},
		"embedded pointers": &goldenStruct{P: &goldenInner{}},
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			want, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("v1: %v", err)
			}
			got, err := MarshalPinned(v)
			if err != nil {
				t.Fatalf("pinned: %v", err)
			}
			if string(got) != string(want) {
				t.Fatalf("pinned bytes differ\n got: %s\nwant: %s", got, want)
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

type dupNames struct {
	A int `json:"x"`
	B int `json:"x"`
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
			v:    dupNames{A: 1, B: 2},
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
	want, _ := json.Marshal(v)
	got, err := MarshalPinned(v)
	if err != nil || string(got) != string(want) {
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
	for name, raw := range map[string]string{
		"exact":        `{"name":"a","n":1}`,
		"case":         `{"NAME":"a","N":1}`,
		"dup names":    `{"name":"x","name":"a","n":1}`,
		"invalid utf8": "{\"name\":\"a\",\"n\":1,\"extra\":\"\xff\"}",
	} {
		t.Run(name, func(t *testing.T) {
			var got, want in
			if err := UnmarshalPinned([]byte(raw), &got); err != nil {
				t.Fatalf("pinned: %v", err)
			}
			if err := json.Unmarshal([]byte(raw), &want); err != nil {
				t.Fatalf("v1: %v", err)
			}
			if got != want || got.Name != "a" {
				t.Fatalf("pinned %+v, v1 %+v", got, want)
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
