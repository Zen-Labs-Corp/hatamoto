package form

import (
	"strings"
	"testing"
)

func fileField(required bool) FormField {
	mb := 5
	return FormField{ID: "f1", Name: "cv", Label: "CV", Type: FieldTypeFile, Required: required,
		Validation: &FieldValidation{Accept: []string{"pdf"}, MaxMB: &mb}}
}

func TestValidateFileAnswer(t *testing.T) {
	t.Parallel()
	ref := FileRef{ID: "0123456789abcdef0123456789abcdef", Name: "cv.pdf", Size: 10, Type: "application/pdf"}
	msg := "Please attach a PDF"
	custom := fileField(true)
	custom.Validation.Message = &msg
	tests := []struct {
		name    string
		field   FormField
		raw     any
		wantErr string
		wantVal bool
	}{
		{"server ref", fileField(true), ref, "", true},
		{"required missing", fileField(true), nil, "CV is required", false},
		{"required blank string", fileField(true), "  ", "CV is required", false},
		{"optional missing", fileField(false), nil, "", false},
		{"forged map", fileField(false), map[string]any{"id": ref.ID, "name": "x"}, "CV must be a file upload", false},
		{"forged string", fileField(true), "cv.pdf", "CV must be a file upload", false},
		{"forged list", fileField(false), []any{"a"}, "CV must be a file upload", false},
		{"ref without id", fileField(false), FileRef{Name: "x"}, "CV must be a file upload", false},
		{"custom message", custom, map[string]any{"id": "x"}, msg, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clean, errs := Validate([]FormField{tc.field}, map[string]any{"cv": tc.raw}, SubmitContext{})
			if tc.wantErr != "" {
				if errs["cv"] != tc.wantErr {
					t.Fatalf("err = %q, want %q", errs["cv"], tc.wantErr)
				}
				return
			}
			if len(errs) > 0 {
				t.Fatalf("unexpected errors %v", errs)
			}
			if _, ok := clean["cv"]; ok != tc.wantVal {
				t.Fatalf("clean = %v, want present=%v", clean, tc.wantVal)
			}
		})
	}
}

func TestValidateFileHiddenByLogicDropped(t *testing.T) {
	t.Parallel()
	gate := FormField{ID: "g", Name: "g", Label: "G", Type: FieldTypeText}
	f := fileField(true)
	f.Logic = &FieldLogic{FieldID: "g", Op: LogicFilled}
	clean, errs := Validate([]FormField{gate, f}, map[string]any{"cv": FileRef{ID: "x", Name: "a"}}, SubmitContext{})
	if len(errs) > 0 || len(clean) != 0 {
		t.Fatalf("clean=%v errs=%v", clean, errs)
	}
}

func TestValidateDefinitionFile(t *testing.T) {
	t.Parallel()
	mb := func(n int) *int { return &n }
	ok := func() FormField {
		return FormField{ID: "f", Name: "cv", Label: "CV", Type: FieldTypeFile,
			Validation: &FieldValidation{Accept: []string{"pdf", "docx"}, MaxMB: mb(10)}}
	}
	mut := func(fn func(*FormField)) []FormField { f := ok(); fn(&f); return []FormField{f} }
	tests := []struct {
		name   string
		fields []FormField
		want   string
	}{
		{"valid", mut(func(*FormField) {}), ""},
		{"valid default size", mut(func(f *FormField) { f.Validation.MaxMB = nil }), ""},
		{"no validation", mut(func(f *FormField) { f.Validation = nil }), "at least one"},
		{"empty accept", mut(func(f *FormField) { f.Validation.Accept = nil }), "at least one"},
		{"svg", mut(func(f *FormField) { f.Validation.Accept = []string{"svg"} }), "not allowed"},
		{"html", mut(func(f *FormField) { f.Validation.Accept = []string{"pdf", "html"} }), "not allowed"},
		{"exe", mut(func(f *FormField) { f.Validation.Accept = []string{"exe"} }), "not allowed"},
		{"dotted", mut(func(f *FormField) { f.Validation.Accept = []string{".pdf"} }), "not allowed"},
		{"upper", mut(func(f *FormField) { f.Validation.Accept = []string{"PDF"} }), "not allowed"},
		{"zero mb", mut(func(f *FormField) { f.Validation.MaxMB = mb(0) }), "between 1 and 25"},
		{"26 mb", mut(func(f *FormField) { f.Validation.MaxMB = mb(26) }), "between 1 and 25"},
		{"25 mb", mut(func(f *FormField) { f.Validation.MaxMB = mb(25) }), ""},
		{"options", mut(func(f *FormField) { f.Options = []FieldOption{{Label: "a", Value: "a"}} }), "only allowed types"},
		{"pattern", mut(func(f *FormField) { p := "x"; f.Validation.Pattern = &p }), "only allowed types"},
		{"max length", mut(func(f *FormField) { f.Validation.MaxLength = mb(3) }), "only allowed types"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			errs := ValidateDefinitionStrict(tc.fields)
			if tc.want == "" {
				if errs != nil {
					t.Fatalf("errs = %v", errs)
				}
				return
			}
			if len(errs) == 0 || !strings.Contains(errs[0].Message, tc.want) {
				t.Fatalf("errs = %v, want %q", errs, tc.want)
			}
		})
	}
}

func TestFileFieldRoundTripAndLegacyStable(t *testing.T) {
	t.Parallel()
	// Accept/MaxMB are omitempty: a legacy field marshals without them.
	legacy := `{"id":"a","type":"text","label":"N","name":"n","required":false,"validation":{"min_length":2},"position":0}`
	roundTrip[FormField](t, []byte(legacy))
	roundTrip[FormField](t, []byte(`{"id":"f","type":"file","label":"CV","name":"cv","required":false,"validation":{"accept":["pdf"],"max_mb":5},"position":0}`))
}
