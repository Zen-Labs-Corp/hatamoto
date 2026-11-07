package admin

import (
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/form"
)

func TestSubmissionsExportSkipsLayoutFields(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact",
		form.FormField{ID: "h", Type: form.FieldTypeHeading, Label: "Section", Text: "About you"},
		form.FormField{ID: "a", Type: form.FieldTypeText, Label: "Name", Name: "name"},
		form.FormField{ID: "d", Type: form.FieldTypeDivider},
		form.FormField{ID: "c", Type: form.FieldTypeConsent, Label: "I agree", Name: "agree"},
	)
	ff.addSub(fm, form.SubmissionStatusNew, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), map[string]any{"name": "Ada", "agree": "Yes"})
	rec := g.do("GET", "/admin/forms/submissions/export.csv?form="+fm.ID.String(), "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(rec.Body.String(), "\xef\xbb\xbf"))).ReadAll()
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %v, %v", rows, err)
	}
	if h := rows[0]; h[3] != "Name" || h[4] != "I agree" || h[5] != "Other answers" {
		t.Errorf("header: %v", h)
	}
	if r := rows[1]; r[3] != "Ada" || r[4] != "Yes" || r[5] != "" {
		t.Errorf("row: %v", r)
	}
	for _, c := range rows[0] {
		if c == "Section" {
			t.Error("layout field has a CSV column")
		}
	}
}

func TestFieldTypeLabelsCoverNewTypes(t *testing.T) {
	t.Parallel()

	for _, ft := range form.GetFieldTypes() {
		if fieldTypeLabels[ft] == "" {
			t.Errorf("no label for field type %q", ft)
		}
	}
	if fieldTypeIcon(form.FieldTypeConsent) == "pen" || fieldTypeIcon(form.FieldTypeHeading) == "pen" {
		t.Error("new types fall back to the default icon")
	}
}
