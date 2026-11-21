package database

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

type scanCase struct {
	name string
	oid  uint32
	code int16
	src  []byte
}

func jsonScanCases() []scanCase {
	var out []scanCase
	fixtures := map[string][]byte{
		"object":     []byte(`{"a":1}`),
		"whitespace": []byte(" {\"a\": 1}\n "),
		"null":       []byte(`null`),
		"dup":        []byte(`{"a":1,"a":2}`),
		"escape":     []byte(`"\u00e9"`),
		"array":      []byte(`[1, 2]`),
		"sqlnull":    nil,
	}
	for n, src := range fixtures {
		out = append(out,
			scanCase{"json/text/" + n, pgtype.JSONOID, pgtype.TextFormatCode, src},
			scanCase{"jsonb/text/" + n, pgtype.JSONBOID, pgtype.TextFormatCode, src})
		bin := src
		if src != nil {
			bin = append([]byte{1}, src...)
		}
		out = append(out, scanCase{"jsonb/binary/" + n, pgtype.JSONBOID, pgtype.BinaryFormatCode, bin})
	}
	return out
}

func fastMap(fallback func([]byte, any) error) *pgtype.Map {
	m := pgtype.NewMap()
	for _, t := range jsonTypes(fallback) {
		m.RegisterType(t)
	}
	return m
}

func scanValue(t *testing.T, m *pgtype.Map, c scanCase) jsontext.Value {
	t.Helper()
	v := jsontext.Value("stale")
	plan := m.PlanScan(c.oid, c.code, &v)
	if plan == nil {
		t.Fatalf("%s: no scan plan", c.name)
	}
	if err := plan.Scan(bytes.Clone(c.src), &v); err != nil {
		t.Fatalf("%s: scan: %v", c.name, err)
	}
	return v
}

func TestJSONFastPathSkipsUnmarshal(t *testing.T) {
	calls := 0
	m := fastMap(func(b []byte, v any) error { calls++; return json.Unmarshal(b, v) })
	for _, c := range jsonScanCases() {
		scanValue(t, m, c)
	}
	if calls != 0 {
		t.Fatalf("fallback called %d times, want 0", calls)
	}
}

func TestJSONFastPathMatchesDefault(t *testing.T) {
	def, fast := pgtype.NewMap(), fastMap(json.Unmarshal)
	for _, c := range jsonScanCases() {
		want, got := scanValue(t, def, c), scanValue(t, fast, c)
		if !bytes.Equal(want, got) || (want == nil) != (got == nil) {
			t.Errorf("%s: fast %q (nil=%v), default %q (nil=%v)", c.name, got, got == nil, want, want == nil)
		}
	}
	for _, src := range [][]byte{nil, []byte(`{}`)} {
		var want, got *jsontext.Value
		for m, dst := range map[*pgtype.Map]**jsontext.Value{def: &want, fast: &got} {
			if err := m.PlanScan(pgtype.JSONBOID, pgtype.TextFormatCode, dst).Scan(src, dst); err != nil {
				t.Fatal(err)
			}
		}
		if (want == nil) != (got == nil) || (want != nil && !bytes.Equal(*want, *got)) {
			t.Errorf("**Value src %q: fast %v, default %v", src, got, want)
		}
	}
}

func TestJSONFastPathCopies(t *testing.T) {
	m := fastMap(json.Unmarshal)
	for _, oid := range []uint32{pgtype.JSONOID, pgtype.JSONBOID} {
		src := []byte(`{"a":1}`)
		var v jsontext.Value
		if err := m.PlanScan(oid, pgtype.TextFormatCode, &v).Scan(src, &v); err != nil {
			t.Fatal(err)
		}
		copy(src, "XXXXXXX")
		if string(v) != `{"a":1}` {
			t.Fatalf("oid %d: value aliases the source buffer: %q", oid, v)
		}
	}
}

func TestJSONOtherTargetsUseUnmarshal(t *testing.T) {
	calls := 0
	m := fastMap(func(b []byte, v any) error { calls++; return json.Unmarshal(b, v) })
	src := []byte(`{"A":1}`)
	var (
		mp map[string]any
		st struct{ A int }
		an any
	)
	for name, dst := range map[string]any{"map": &mp, "struct": &st, "any": &an} {
		calls = 0
		if err := m.PlanScan(pgtype.JSONBOID, pgtype.TextFormatCode, dst).Scan(src, dst); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if calls != 1 {
			t.Errorf("%s: fallback calls = %d, want 1", name, calls)
		}
	}
	if mp["A"] != float64(1) || st.A != 1 || an.(map[string]any)["A"] != float64(1) {
		t.Fatalf("decoded wrong: %v %v %v", mp, st, an)
	}
}
