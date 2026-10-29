package seo

import (
	"reflect"
	"strings"
	"testing"
)

func TestDefaultsAreValid(t *testing.T) {
	if errs := Defaults().Validate(); len(errs) != 0 {
		t.Fatalf("defaults invalid: %v", errs)
	}
}

func TestDecodeFillsMissingKeysAndIgnoresUnknown(t *testing.T) {
	got, err := Decode([]byte(`{"separator":"-","future":{"a":1},"types":{"doc":{"title":"%title% %sep% Docs"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := Defaults()
	want.Separator = "-"
	want.Types["doc"] = TypeTemplates{Title: "%title% %sep% Docs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestDecodeEmptyAndNull(t *testing.T) {
	for _, in := range []string{"", "{}", `{"types":null,"taxonomy":null,"variables":null}`} {
		got, err := Decode([]byte(in))
		if err != nil || !reflect.DeepEqual(got, Defaults()) {
			t.Fatalf("%q: got %+v, %v", in, got, err)
		}
	}
}

func TestDecodeBadJSON(t *testing.T) {
	if _, err := Decode([]byte(`{`)); err == nil {
		t.Fatal("want error")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*Settings)
		field string
	}{
		{"separator", func(s *Settings) { s.Separator = "~" }, "separator"},
		{"unknown var", func(s *Settings) { s.Home = "%title% %nope%" }, "home"},
		{"undefined custom var", func(s *Settings) { s.Home = "%cv_brand%" }, "home"},
		{"too long", func(s *Settings) { s.Types["a"] = TypeTemplates{Description: strings.Repeat("x", MaxTemplateLen+1)} }, "type[a][description]"},
		{"taxonomy var", func(s *Settings) { s.Taxonomy["tag"] = "%bogus%" }, "taxonomy[tag]"},
		{"bad var name", func(s *Settings) { s.Variables["Bad Name"] = "x" }, "variable[Bad Name]"},
		{"long var value", func(s *Settings) { s.Variables["a"] = strings.Repeat("x", MaxVariableLen+1) }, "variable[a]"},
	}
	for _, tt := range tests {
		s := Defaults()
		tt.edit(&s)
		errs := s.Validate()
		if len(errs) == 0 || errs[0].Field != tt.field {
			t.Errorf("%s: got %v, want field %q", tt.name, errs, tt.field)
		}
	}
}

func TestValidateTooManyVariables(t *testing.T) {
	s := Defaults()
	for i := 0; i <= MaxVariables; i++ {
		s.Variables["v"+strings.Repeat("a", i%20)+string(rune('a'+i/20))] = "x"
	}
	found := false
	for _, e := range s.Validate() {
		found = found || e.Field == "variables"
	}
	if !found {
		t.Fatal("no variables error")
	}
}

func TestValidateAcceptsKnownAndCustomVars(t *testing.T) {
	s := Defaults()
	s.Variables["brand"] = "Acme"
	s.Home = "%title% %sep% %cv_brand% 100% real"
	for _, v := range KnownVars {
		s.Archives.Date += " %" + v + "%"
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Fatalf("got %v", errs)
	}
}

func TestVarNames(t *testing.T) {
	got := varNames("a %title% 50% off %sep%% %x y% %%")
	want := []string{"title", "sep"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestCloneIsIndependent(t *testing.T) {
	a := Defaults()
	b := a.Clone()
	b.Types["x"] = TypeTemplates{}
	if len(a.Types) != 0 {
		t.Fatal("clone shares the map")
	}
}
