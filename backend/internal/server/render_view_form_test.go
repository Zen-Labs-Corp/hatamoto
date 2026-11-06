package server

import (
	"testing"

	"hatamoto.dev/core/internal/form"
)

func TestFormViewNewFieldProps(t *testing.T) {
	off := false
	f := &form.Form{Slug: "c", Name: "C", Fields: []form.FormField{
		{ID: "b", Type: "text", Name: "b", Position: 2, Width: "half",
			Logic: &form.FieldLogic{FieldID: "a", Op: "is", Value: "x"}},
		{ID: "a", Type: "text", Name: "a", Position: 1, InEmail: &off, BlockFreeEmail: true},
		{ID: "c", Type: "text", Name: "c", Position: 3,
			Logic: &form.FieldLogic{FieldID: "gone", Op: "filled"}},
		{ID: "h", Type: "hidden", Name: "h", Position: 4, Hidden: &form.HiddenSpec{Source: "param", Param: "utm"}},
		{ID: "s", Type: "hidden", Name: "s", Position: 5, Hidden: &form.HiddenSpec{Source: "static", Value: "v"}},
		{ID: "t", Type: "heading", Position: 6, Text: "Hi"},
		{ID: "k", Type: "consent", Name: "k", Position: 7, Link: "/privacy"},
	}}
	v := formView(f)
	if len(v.Fields) != 7 || v.Fields[0].Name != "a" || v.Fields[1].Name != "b" {
		t.Fatalf("order/len: %+v", v.Fields)
	}
	if a := v.Fields[0]; a.Width != "full" {
		t.Errorf("default width = %q", a.Width)
	}
	b := v.Fields[1]
	if b.Width != "half" || b.LogicField != "a" || b.LogicOp != "is" || b.LogicValue != "x" {
		t.Errorf("b = %+v", b)
	}
	if c := v.Fields[2]; c.LogicField != "" || c.LogicOp != "" {
		t.Errorf("unknown source kept logic: %+v", c)
	}
	if h := v.Fields[3]; !h.Hidden || h.HiddenValue != "" {
		t.Errorf("param hidden leaked: %+v", h)
	}
	if s := v.Fields[4]; !s.Hidden || s.HiddenValue != "v" {
		t.Errorf("static hidden: %+v", s)
	}
	if v.Fields[5].Text != "Hi" || v.Fields[6].Link != "/privacy" {
		t.Errorf("text/link: %+v %+v", v.Fields[5], v.Fields[6])
	}
}
