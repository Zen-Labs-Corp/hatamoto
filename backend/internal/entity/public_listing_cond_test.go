package entity

import (
	"fmt"
	"strings"
	"testing"
)

func TestCondSQL(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    AttrCond
		want string // "" = exactly FALSE
		args []any
	}{
		{"invalid key", AttrCond{Key: "a'; DROP TABLE x; --", Op: CondEq, Text: "x"}, "", nil},
		{"unknown op", AttrCond{Key: "k", Op: 0}, "", nil},
		{"eq", AttrCond{Key: "status", Op: CondEq, Text: "open"}, "(e.attributes @> $1::jsonb)", []any{`{"status":"open"}`}},
		{"ne", AttrCond{Key: "status", Op: CondNe, Text: "open"}, "(e.attributes ? $1::text AND NOT (e.attributes @> $2::jsonb))", []any{"status", `{"status":"open"}`}},
		{"lt", AttrCond{Key: "n", Op: CondLt, Num: 5}, "END < $2::numeric", []any{"n", 5.0}},
		{"lte", AttrCond{Key: "n", Op: CondLe, Num: 5}, "END <= $2::numeric", []any{"n", 5.0}},
		{"gt", AttrCond{Key: "n", Op: CondGt, Num: 5}, "END > $2::numeric", []any{"n", 5.0}},
		{"gte", AttrCond{Key: "n", Op: CondGe, Num: 5}, "END >= $2::numeric", []any{"n", 5.0}},
		{"date ge", AttrCond{Key: "d", Op: CondDateGe, Text: "2026-01-02"}, "::text >= $2::text", []any{"d", "2026-01-02"}},
		{"date lt", AttrCond{Key: "d", Op: CondDateLt, Text: "2026-01-02"}, "::text < $2::text", []any{"d", "2026-01-02"}},
		{"date le", AttrCond{Key: "d", Op: CondDateLe, Text: "2026-01-02"}, "::text <= $2::text", []any{"d", "2026-01-02"}},
		{"bad date", AttrCond{Key: "d", Op: CondDateGe, Text: "tomorrow"}, "", nil},
	} {
		var args []any
		arg := func(v any) string {
			args = append(args, v)
			return fmt.Sprintf("$%d", len(args))
		}
		got := condSQL(ListFilter{Conds: []AttrCond{tc.c}}, arg)
		if len(got) != 1 {
			t.Fatalf("%s: terms = %v", tc.name, got)
		}
		if tc.want == "" {
			if got[0] != "FALSE" || len(args) != 0 {
				t.Errorf("%s: got %q args %v, want FALSE and no args", tc.name, got[0], args)
			}
			continue
		}
		if !strings.Contains(got[0], tc.want) {
			t.Errorf("%s: %q lacks %q", tc.name, got[0], tc.want)
		}
		if fmt.Sprint(args) != fmt.Sprint(tc.args) {
			t.Errorf("%s: args = %v, want %v", tc.name, args, tc.args)
		}
		if strings.Contains(got[0], "DROP") {
			t.Errorf("%s: key interpolated: %s", tc.name, got[0])
		}
	}
	if got := condSQL(ListFilter{}, func(any) string { return "$1" }); len(got) != 0 {
		t.Errorf("no conds gave %v", got)
	}
}
