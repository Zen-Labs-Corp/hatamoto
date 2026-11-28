package wpimport

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/form"
)

func cf7Fixture(t *testing.T) []CF7Form {
	t.Helper()
	return parseFixtureWXR(t, cf7FormWXR).CF7
}

func fieldByName(m CF7Mapped, name string) *form.FormField {
	for i := range m.Fields {
		if m.Fields[i].Name == name {
			return &m.Fields[i]
		}
	}
	return nil
}

func TestCF7MapContact(t *testing.T) {
	m := MapCF7(cf7Fixture(t)[0].Template)
	if len(m.Fields) != 5 || m.Submit != "Send" || len(m.Dropped) != 0 || m.Conditional {
		t.Fatalf("fields=%d submit=%q dropped=%v", len(m.Fields), m.Submit, m.Dropped)
	}
	for name, want := range map[string]struct {
		typ, label string
		required   bool
	}{
		"your_name":    {form.FieldTypeText, "Your name", true},
		"your_email":   {form.FieldTypeEmail, "Your email", true},
		"your_phone":   {form.FieldTypePhone, "Phone", false},
		"your_message": {form.FieldTypeTextarea, "Message", false},
		"terms":        {form.FieldTypeConsent, "I accept the terms", true},
	} {
		f := fieldByName(m, name)
		if f == nil || f.Type != want.typ || f.Label != want.label || f.Required != want.required {
			t.Errorf("%s = %+v, want %+v", name, f, want)
		}
	}
	if errs := form.ValidateDefinitionStrict(m.Fields); len(errs) != 0 {
		t.Errorf("definition errors: %v", errs)
	}
}

func TestCF7MapQuote(t *testing.T) {
	m := MapCF7(cf7Fixture(t)[1].Template)
	if m.Submit != "Ask" || len(m.Dropped) != 1 || m.Dropped[0] != "quiz q" || len(m.Fields) != 2 {
		t.Fatalf("submit=%q dropped=%v fields=%d", m.Submit, m.Dropped, len(m.Fields))
	}
	file := fieldByName(m, "upload")
	if file == nil || file.Type != form.FieldTypeFile || file.Validation == nil {
		t.Fatalf("upload = %+v", file)
	}
	if got := strings.Join(file.Validation.Accept, ","); got != "pdf,png" || file.Validation.MaxMB == nil || *file.Validation.MaxMB != 2 {
		t.Errorf("accept=%s maxmb=%v", got, file.Validation.MaxMB)
	}
	if errs := form.ValidateDefinitionStrict(m.Fields); len(errs) != 0 {
		t.Errorf("definition errors: %v", errs)
	}
}

func TestCF7MapChoicesAndNames(t *testing.T) {
	m := MapCF7(`[select* size "Small|s" "Large|l"] [radio plan "A" "B"] [checkbox opt "x" "y"] [checkbox one "only"]
[text 1st] [text 1st] [hidden src "web"] [file f filetypes:exe|svg limit:900kb] [captchac captcha-1] [group g1] [/group]`)
	if f := fieldByName(m, "size"); f == nil || !f.Required || len(f.Options) != 2 || f.Options[0].Label != "Small" || f.Options[0].Value != "s" {
		t.Errorf("size = %+v", f)
	}
	if f := fieldByName(m, "opt"); f == nil || f.Type != form.FieldTypeCheckbox || len(f.Options) != 2 {
		t.Errorf("opt = %+v", f)
	}
	if f := fieldByName(m, "one"); f == nil || len(f.Options) != 0 || f.Label != "only" {
		t.Errorf("one = %+v", f)
	}
	if fieldByName(m, "f_1st") == nil || fieldByName(m, "f_1st_2") == nil {
		t.Errorf("names = %+v", m.Fields)
	}
	if f := fieldByName(m, "src"); f == nil || f.Hidden == nil || f.Hidden.Value != "web" {
		t.Errorf("src = %+v", f)
	}
	f := fieldByName(m, "f")
	if f == nil || len(f.Validation.Accept) != 1 || f.Validation.Accept[0] != "pdf" || *f.Validation.MaxMB != 1 {
		t.Errorf("f = %+v", f)
	}
	if !m.Conditional || len(m.Dropped) != 2 {
		t.Errorf("conditional=%v dropped=%v", m.Conditional, m.Dropped)
	}
	if errs := form.ValidateDefinitionStrict(m.Fields); len(errs) != 0 {
		t.Errorf("definition errors: %v", errs)
	}
}

func TestCF7MapDropsInvalidField(t *testing.T) {
	m := MapCF7(`[select empty] [text ok]`)
	if len(m.Fields) != 1 || m.Fields[0].Name != "ok" || len(m.Dropped) != 1 {
		t.Errorf("fields=%+v dropped=%v", m.Fields, m.Dropped)
	}
}

func TestCF7MapGarbage(t *testing.T) {
	for _, in := range []string{"", "[[[ ]", "] [ [", "[", `[text "unterminated`, "[*]", "[1] [2 3]", "<p>just text</p>", strings.Repeat("[", 5000)} {
		if m := MapCF7(in); len(m.Fields) != 0 {
			t.Errorf("%q gave fields %+v", in, m.Fields)
		}
	}
	many := MapCF7(strings.Repeat("[text a] ", 80))
	if len(many.Fields) != cf7MaxFields {
		t.Errorf("fields = %d, want the cap %d", len(many.Fields), cf7MaxFields)
	}
}
