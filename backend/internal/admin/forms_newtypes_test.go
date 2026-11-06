package admin

import (
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/jsonx"
)

func TestLegacyEditorKeepsLogicAndNewFieldData(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	off := false
	fm := ff.addForm("Contact",
		form.FormField{ID: "a", Type: form.FieldTypeText, Label: "Name", Name: "name", Width: "full", Position: 0},
		form.FormField{ID: "b", Type: form.FieldTypeText, Label: "Company", Name: "company", Width: "half", Position: 1, NameLocked: true, InEmail: &off,
			Logic: &form.FieldLogic{FieldID: "a", Op: form.LogicFilled}},
		form.FormField{ID: "c", Type: form.FieldTypeHeading, Text: "About you", Width: "full", Position: 2},
		form.FormField{ID: "d", Type: form.FieldTypeHidden, Name: "source", Width: "full", Position: 3, Hidden: &form.HiddenSpec{Source: "param", Param: "utm_source"}},
		form.FormField{ID: "e", Type: form.FieldTypeEmail, Label: "Email", Name: "email", Width: "full", Position: 4, BlockFreeEmail: true},
		form.FormField{ID: "f", Type: form.FieldTypeConsent, Label: "I agree", Name: "agree", Width: "full", Position: 5, Link: "/privacy"},
	)
	want, err := jsonx.Marshal(fm.Fields)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]map[string]string, 0, len(fm.Fields))
	for _, f := range fm.Fields {
		rows = append(rows, map[string]string{"id": f.ID, "type": f.Type, "label": f.Label, "name": f.Name, "width": f.Width})
	}
	rec := g.do("POST", "/admin/forms/edit/"+fm.ID.String(), "owner-token", fieldForm(rows...), nil)
	if rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	got, err := jsonx.Marshal(ff.forms[fm.ID].Fields)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("fields changed through the legacy editor:\n got %s\nwant %s", got, want)
	}
}

func TestLegacyEditorRoundTripLeavesLegacyFormByteEqual(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact",
		form.FormField{ID: "a", Type: form.FieldTypeText, Label: "Name", Name: "name", Placeholder: "Ada", Required: true, Width: "full", Position: 0},
		form.FormField{ID: "b", Type: form.FieldTypeSelect, Label: "Topic", Name: "topic", Width: "half", Position: 1,
			Options: []form.FieldOption{{Label: "Sales", Value: "sales"}, {Label: "Support", Value: "help"}}},
	)
	fm.Settings = form.FormSettings{SubmitButtonText: "Send", SuccessMessage: "Thank you for your submission!", StoreSubmissions: true}
	wantFields, _ := jsonx.Marshal(fm.Fields)
	wantSettings, _ := jsonx.Marshal(fm.Settings)

	rows := []map[string]string{
		{"id": "a", "type": "text", "label": "Name", "name": "name", "placeholder": "Ada", "required": "1", "width": "full"},
		{"id": "b", "type": "select", "label": "Topic", "name": "topic", "width": "half", "options": "Sales | sales\nSupport | help"},
	}
	if rec := g.do("POST", "/admin/forms/edit/"+fm.ID.String(), "owner-token", fieldForm(rows...), nil); rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	gotFields, _ := jsonx.Marshal(ff.forms[fm.ID].Fields)
	gotSettings, _ := jsonx.Marshal(ff.forms[fm.ID].Settings)
	if string(gotFields) != string(wantFields) || string(gotSettings) != string(wantSettings) {
		t.Errorf("legacy form changed:\n fields   %s\n want     %s\n settings %s\n want     %s", gotFields, wantFields, gotSettings, wantSettings)
	}
}

func TestLegacyEditorAcceptsLayoutRowsWithoutLabel(t *testing.T) {
	t.Parallel()

	rows := []formFieldRow{
		{Type: form.FieldTypeDivider, Width: "full"},
		{Type: form.FieldTypeHeading, Width: "full"},
		{Type: form.FieldTypeHidden, Name: "src", Width: "full"},
		{Type: form.FieldTypeText, Width: "full"},
	}
	out := buildFormFields(rows)
	for i, r := range rows[:3] {
		if len(r.Err) > 0 {
			t.Errorf("row %d: %v", i, r.Err)
		}
	}
	if rows[3].Err["label"] == "" {
		t.Error("a text field without a label must still be rejected")
	}
	if out[0].Name != "" || out[1].Name != "" || out[2].Name != "src" {
		t.Errorf("names: %q %q %q", out[0].Name, out[1].Name, out[2].Name)
	}
}

func TestFieldTypeOptionsLabelNewTypes(t *testing.T) {
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

func TestCarryFieldDataKeepsNewKeys(t *testing.T) {
	t.Parallel()

	off := false
	stored := []form.FormField{{ID: "a", Logic: &form.FieldLogic{FieldID: "x", Op: form.LogicIs, Value: "1"}, InEmail: &off, NameLocked: true,
		BlockFreeEmail: true, Text: "t", Link: "/p", Hidden: &form.HiddenSpec{Source: "page"}}}
	got := carryFieldData([]form.FormField{{ID: "a"}, {ID: "new"}}, stored)[0]
	if got.Logic == nil || got.Logic.Value != "1" || got.InEmail == nil || *got.InEmail || !got.NameLocked || !got.BlockFreeEmail ||
		got.Text != "t" || got.Link != "/p" || got.Hidden == nil || got.Hidden.Source != "page" {
		t.Errorf("not carried: %+v", got)
	}
}

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
