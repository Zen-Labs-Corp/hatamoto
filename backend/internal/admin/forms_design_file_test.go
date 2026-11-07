package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/form"
)

func TestFormDesignBootIncludesFileType(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	rec := g.do("GET", "/admin/forms/design/"+fm.ID.String(), "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("design: %d", rec.Code)
	}
	if want := `{"type":"file","label":"File upload","group":"advanced","icon":"upload"}`; !strings.Contains(rec.Body.String(), want) {
		t.Errorf("boot lacks %s", want)
	}
}

func TestFileFieldTypeMetadata(t *testing.T) {
	t.Parallel()

	if formDesignTypeGroup(form.FieldTypeFile) != "advanced" || fieldTypeIcon(form.FieldTypeFile) != "upload" || fieldTypeLabels[form.FieldTypeFile] != "File upload" {
		t.Error("file type metadata")
	}
}

func TestSettingsTokensSkipFileFields(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Apply",
		form.FormField{ID: "a", Type: form.FieldTypeText, Label: "Name", Name: "name"},
		form.FormField{ID: "f", Type: form.FieldTypeFile, Label: "CV", Name: "cvfile", Validation: &form.FieldValidation{Accept: []string{"pdf"}}},
	)
	rec := g.do("GET", "/admin/forms/design/"+fm.ID.String()+"/settings", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("settings: %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "{cvfile}") || !strings.Contains(rec.Body.String(), "{name}") {
		t.Error("subject tokens must list name but not the file field")
	}
}
