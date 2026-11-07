package form

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

func TestTemplatesValid(t *testing.T) {
	want := []string{"blank", "contact", "quote", "newsletter", "rsvp", "job"}
	got := Templates()
	if len(got) != len(want) {
		t.Fatalf("templates = %d, want %d", len(got), len(want))
	}
	svc, _ := newStubService()
	for i, tpl := range got {
		if tpl.Key != want[i] {
			t.Errorf("template %d key = %q, want %q", i, tpl.Key, want[i])
		}
		if _, ok := TemplateByKey(tpl.Key); !ok {
			t.Errorf("%s: TemplateByKey misses it", tpl.Key)
		}
		in := tpl.Build()
		in.Name = tpl.Name
		for _, f := range in.Fields {
			if f.Type == "file" {
				t.Errorf("%s: file field", tpl.Key)
			}
		}
		if _, err := svc.CreateForm(context.Background(), &in); err != nil {
			t.Errorf("%s: CreateForm: %v", tpl.Key, err)
		}
		in2 := tpl.Build()
		for i := range in2.Fields {
			if in2.Fields[i].ID == "" {
				in2.Fields[i].ID = "id" + string(rune('a'+i))
			}
		}
		if errs := ValidateDefinitionStrict(in2.Fields); len(errs) > 0 {
			t.Errorf("%s: strict: %v", tpl.Key, errs)
		}
	}
	if _, ok := TemplateByKey("nope"); ok {
		t.Error("unknown key found")
	}
}

func TestTemplatesRSVPLogicRefsRadio(t *testing.T) {
	tpl, _ := TemplateByKey("rsvp")
	a, b := tpl.Build(), tpl.Build()
	var radio string
	for _, f := range a.Fields {
		if f.Type == FieldTypeRadio {
			radio = f.ID
		}
	}
	if radio == "" || radio == func() string {
		for _, f := range b.Fields {
			if f.Type == FieldTypeRadio {
				return f.ID
			}
		}
		return ""
	}() {
		t.Fatal("radio id missing or reused between builds")
	}
	n := 0
	for _, f := range a.Fields {
		if f.Logic != nil {
			n++
			if f.Logic.FieldID != radio {
				t.Errorf("%s logic refs %q", f.Name, f.Logic.FieldID)
			}
		}
	}
	if n == 0 {
		t.Error("no conditional fields")
	}
}

func TestCreateFormInputStatusNotInJSON(t *testing.T) {
	var in CreateFormInput
	if err := jsonx.Unmarshal([]byte(`{"name":"x","status":"archived"}`), &in, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	if in.Status != "" {
		t.Errorf("Status = %q, want empty", in.Status)
	}
	in.Status = StatusInactive
	b, _ := jsonx.Marshal(in)
	var m map[string]any
	_ = jsonx.Unmarshal(b, &m)
	if _, ok := m["status"]; ok {
		t.Error("status serialised")
	}
}
