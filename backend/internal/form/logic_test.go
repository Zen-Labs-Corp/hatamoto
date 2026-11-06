package form

import "testing"

func TestVisible(t *testing.T) {
	t.Parallel()
	fields := []FormField{
		{ID: "a", Type: FieldTypeText, Name: "a"},
		{ID: "b", Type: FieldTypeText, Name: "b", Logic: &FieldLogic{FieldID: "a", Op: LogicIs, Value: "x"}},
		{ID: "c", Type: FieldTypeText, Name: "c", Logic: &FieldLogic{FieldID: "b", Op: LogicEmpty}},
		{ID: "d", Type: FieldTypeText, Name: "d", Logic: &FieldLogic{FieldID: "b", Op: LogicFilled}},
		{ID: "", Type: FieldTypeText, Name: "noid"},
	}
	tests := []struct {
		name string
		data map[string]any
		want map[string]bool
	}{
		{"source matches", map[string]any{"a": "x", "b": "hi"}, map[string]bool{"a": true, "b": true, "c": false, "d": true}},
		{"source differs: cascade empties b", map[string]any{"a": "y", "b": "hi"}, map[string]bool{"a": true, "b": false, "c": true, "d": false}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Visible(fields, tc.data)
			for id, w := range tc.want {
				if got[id] != w {
					t.Errorf("visible[%s] = %v, want %v", id, got[id], w)
				}
			}
			if len(got) != 4 {
				t.Errorf("want only fields with ids, got %v", got)
			}
		})
	}
}

func TestEvalLogicOps(t *testing.T) {
	t.Parallel()
	tests := []struct {
		op, want string
		src      any
		ok       bool
	}{
		{LogicIs, "a", "a", true},
		{LogicIs, "a", "b", false},
		{LogicIs, "a", []any{"b", "a"}, true},
		{LogicIsNot, "a", "b", true},
		{LogicIsNot, "a", nil, true},
		{LogicContains, "ell", "hello", true},
		{LogicContains, "z", "hello", false},
		{LogicContains, "b", []any{"a", "b"}, true},
		{LogicFilled, "", "x", true},
		{LogicFilled, "", "  ", false},
		{LogicFilled, "", false, false},
		{LogicFilled, "", true, true},
		{LogicEmpty, "", []any{}, true},
		{LogicIs, "true", true, true},
		{LogicIs, "3", float64(3), true},
		{"bogus", "", "x", false},
	}
	for _, tc := range tests {
		if got := evalLogic(&FieldLogic{Op: tc.op, Value: tc.want}, tc.src); got != tc.ok {
			t.Errorf("%s %q on %#v = %v, want %v", tc.op, tc.want, tc.src, got, tc.ok)
		}
	}
}

func TestVisibleLogicCycleDoesNotHang(t *testing.T) {
	t.Parallel()
	fields := []FormField{
		{ID: "a", Name: "a", Logic: &FieldLogic{FieldID: "b", Op: LogicEmpty}},
		{ID: "b", Name: "b", Logic: &FieldLogic{FieldID: "a", Op: LogicEmpty}},
	}
	_ = Visible(fields, nil)
}
