package render

import (
	"reflect"
	"testing"
)

func TestQueryFuncRangeArgs(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	tests := []struct {
		name         string
		args         string
		attr         string
		wantMin, max *float64
	}{
		{"ints", `"range_attr" "sort" "range_min" 1000000 "range_max" 2000000`, "sort", f(1000000), f(2000000)},
		{"strings", `"range_attr" " sort " "range_min" "1.5" "range_max" "x"`, "sort", f(1.5), nil},
		{"min only", `"range_attr" "sort" "range_min" 3`, "sort", f(3), nil},
		{"floats", `"range_attr" "sort" "range_min" 0.5 "range_max" 2.5`, "sort", f(0.5), f(2.5)},
		{"no attr drops bounds", `"range_min" 1 "range_max" 2`, "", nil, nil},
		{"bool is not a number", `"range_attr" "sort" "range_min" true`, "sort", nil, nil},
		{"nan", `"range_attr" "sort" "range_min" "NaN" "range_max" "Inf"`, "sort", nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spy := &spyData{}
			execFuncs(t, spy, `{{$r := query (dict "schema" "item" `+tc.args+`)}}`, nil)
			if spy.q.RangeAttr != tc.attr || !reflect.DeepEqual(spy.q.RangeMin, tc.wantMin) || !reflect.DeepEqual(spy.q.RangeMax, tc.max) {
				t.Fatalf("spec = %q %v %v", spy.q.RangeAttr, spy.q.RangeMin, spy.q.RangeMax)
			}
		})
	}
}
