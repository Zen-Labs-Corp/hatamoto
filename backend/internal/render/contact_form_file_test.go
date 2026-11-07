package render

import (
	"bytes"
	"strings"
	"testing"
)

func fileTestFields(withFile bool) []FormField {
	fields := []FormField{{Name: "name", Label: "Name", Type: "text"}}
	if withFile {
		fields = append(fields, FormField{Name: "cv", Label: "CV", Type: "file", Required: true, Accept: ".pdf,.docx", MaxMB: 10})
	}
	return fields
}

// A form with a file field posts multipart and shows a file input with its
// accept list; a form without one keeps the default encoding and no file input.
func TestFileFieldRendersInEveryServerRenderer(t *testing.T) {
	renderers := map[string]func(t *testing.T, fields []FormField) string{
		"section": func(t *testing.T, fields []FormField) string {
			e, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			v := minimalView()
			v.Forms = map[string]FormView{"apply": {Slug: "apply", Name: "Apply", Fields: fields}}
			v.Sections = []Section{{ID: "s", Type: "contact_form", Content: map[string]any{"form_slug": "apply"}}}
			var buf bytes.Buffer
			if err := e.RenderPage(&buf, v); err != nil {
				t.Fatal(err)
			}
			return mainInner(t, buf.String())
		},
		"widget": func(t *testing.T, fields []FormField) string {
			v := minimalView()
			v.Doc = loadWidgetDoc(t, "form")
			v.Forms = map[string]FormView{"contact": {Slug: "contact", Name: "Apply", Fields: fields}}
			return renderWithData(t, nil, v, false)
		},
	}
	for name, render := range renderers {
		t.Run(name, func(t *testing.T) {
			with := render(t, fileTestFields(true))
			for _, want := range []string{
				`enctype="multipart/form-data"`, `name="cv" type="file" accept=".pdf,.docx" required`, `.pdf,.docx · 10 MB`,
			} {
				if !strings.Contains(with, want) {
					t.Errorf("missing %q\n%s", want, with)
				}
			}
			if strings.Contains(with, `name="cv" type="text"`) {
				t.Error("file field rendered as a text input")
			}
			without := render(t, fileTestFields(false))
			for _, bad := range []string{"enctype", `type="file"`} {
				if strings.Contains(without, bad) {
					t.Errorf("form without a file field has %q", bad)
				}
			}
		})
	}
}

func TestFormViewHasFile(t *testing.T) {
	if (FormView{Fields: fileTestFields(false)}).HasFile() || !(FormView{Fields: fileTestFields(true)}).HasFile() {
		t.Error("HasFile")
	}
}
