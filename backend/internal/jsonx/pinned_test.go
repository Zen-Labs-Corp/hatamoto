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

// TestPinnedKnownDivergences records where a v2-only Pinned set differs from
// v1. Each case names the fix a later part applies to the affected wire type;
// when one starts to match v1, the assertion fails and the note can go.
func TestPinnedKnownDivergences(t *testing.T) {
	cases := []struct {
		name string
		v    any
		fix  string
		v2   string
	}{
		{
			name: "omitempty int and bool",
			v:    omitScalars{},
			fix:  "tag the fields omitzero",
			v2:   `{"n":0,"b":false}`,
		},
		{
			name: "pointer receiver MarshalJSON, not addressable",
			v:    holdsPtrMarshaler{},
			fix:  "pass a pointer to the holder",
			v2:   `{"v":"custom"}`,
		},
		{
			name: "time.Duration",
			v:    map[string]time.Duration{"d": time.Second},
			fix:  "use a typed marshaler writing nanoseconds",
			v2:   "", // v2 refuses to encode it
		},
		{
			name: "[N]byte",
			v:    [2]byte{1, 2},
			fix:  "use a typed marshaler writing an array",
			v2:   `"AQI="`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Log("later-part fix:", tc.fix)
			want, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatalf("v1: %v", err)
			}
			got, err := MarshalPinned(tc.v)
			if tc.v2 == "" {
				if err == nil {
					t.Fatalf("pinned encoded %s, expected an error (v1: %s)", got, want)
				}
				return
			}
			if err != nil {
				t.Fatalf("pinned: %v", err)
			}
			if string(got) == string(want) {
				t.Fatalf("pinned now matches v1 (%s): drop this divergence", got)
			}
			if string(got) != tc.v2 {
				t.Fatalf("pinned = %s, want %s (v1: %s)", got, tc.v2, want)
			}
		})
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
