package entity

import (
	"fmt"
	"strings"
	"testing"
)

func TestRangeSQL(t *testing.T) {
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	lo, hi := 1.0, 2.0
	for _, tc := range []struct {
		name  string
		f     ListFilter
		terms int
		args  int
		want  string
	}{
		{"no attr", ListFilter{RangeMin: &lo}, 0, 0, ""},
		{"no bounds", ListFilter{RangeAttr: "sort"}, 0, 0, ""},
		{"both", ListFilter{RangeAttr: "sort", RangeMin: &lo, RangeMax: &hi}, 2, 3, ">= $2::numeric"},
		{"max only", ListFilter{RangeAttr: "sort", RangeMax: &hi}, 1, 2, "< $2::numeric"},
		{"invalid key", ListFilter{RangeAttr: "a'; DROP TABLE x; --", RangeMin: &lo}, 1, 0, "FALSE"},
	} {
		args = nil
		got := rangeSQL(tc.f, arg)
		if len(got) != tc.terms || len(args) != tc.args {
			t.Fatalf("%s: terms=%v args=%v", tc.name, got, args)
		}
		if tc.want != "" && !strings.Contains(strings.Join(got, " "), tc.want) {
			t.Errorf("%s: %v lacks %q", tc.name, got, tc.want)
		}
		for _, term := range got {
			if strings.Contains(term, "DROP") {
				t.Errorf("%s: key interpolated: %s", tc.name, term)
			}
		}
	}
	args = nil
	rangeSQL(ListFilter{RangeAttr: "sort", RangeMin: &lo}, arg)
	if args[0] != "sort" || args[1] != 1.0 {
		t.Errorf("args = %v", args)
	}
}
