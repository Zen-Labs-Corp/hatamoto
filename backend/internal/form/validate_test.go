package form

import "testing"

func ip(n int) *int       { return &n }
func sp(s string) *string { return &s }

func TestValidateSubmission(t *testing.T) {
	t.Parallel()
	fields := []FormField{
		{Name: "code", Label: "Code", Type: FieldTypeText, Required: true, Validation: &FieldValidation{MinLength: ip(3), MaxLength: ip(5), Pattern: sp(`[A-Z]+`), Message: sp("Use 3-5 capitals")}},
		{Name: "age", Label: "Age", Type: FieldTypeNumber, Validation: &FieldValidation{Min: ip(18), Max: ip(99)}},
		{Name: "agree", Label: "Agree", Type: FieldTypeCheckbox, Required: true},
		{Name: "site", Label: "Site", Type: FieldTypeURL},
		{Name: "day", Label: "Day", Type: FieldTypeDate},
	}
	ok := map[string]any{"code": "ABC", "age": "30", "agree": "on", "site": "https://x.co", "day": "2026-01-31", "extra": "dropped"}
	clean, errs := ValidateSubmission(fields, ok)
	if len(errs) > 0 || clean["extra"] != nil || clean["age"] != float64(30) || clean["agree"] != true {
		t.Fatalf("valid: %v %v", clean, errs)
	}
	for name, mut := range map[string]map[string]any{
		"code": {"code": "ab"}, "code long": {"code": "ABCDEF"}, "code pattern": {"code": "ABc"},
		"age": {"age": "17"}, "age text": {"age": "x"}, "agree": {"agree": false}, "site": {"site": "javascript:alert(1)"}, "day": {"day": "31/01/2026"},
	} {
		in := map[string]any{}
		for k, v := range ok {
			in[k] = v
		}
		for k, v := range mut {
			in[k] = v
		}
		if _, errs := ValidateSubmission(fields, in); len(errs) != 1 {
			t.Errorf("%s: errs = %v", name, errs)
		}
	}
	if _, errs := ValidateSubmission(fields, map[string]any{"code": "ab", "agree": true}); errs["code"] != "Use 3-5 capitals" {
		t.Errorf("custom message: %v", errs)
	}
	if _, errs := ValidateSubmission(fields, nil); errs["code"] == "" || errs["agree"] == "" || len(errs) != 2 {
		t.Errorf("required: %v", errs)
	}
}
