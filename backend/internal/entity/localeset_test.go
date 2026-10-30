package entity

import (
	"errors"
	"testing"
)

func TestNewLocaleSetValidation(t *testing.T) {
	fr := LocaleEntry{"fr", "fr"}
	en := LocaleEntry{"en", ""}
	tests := []struct {
		name      string
		def       string
		entries   []LocaleEntry
		prefixAll bool
		xdef      string
		bad       bool
	}{
		{"empty list", "en", nil, false, "", false},
		{"ok", "en", []LocaleEntry{en, fr}, false, "en", false},
		{"normalizes tags", "en_GB", []LocaleEntry{{"en-gb", ""}, fr}, false, "", false},
		{"bad tag", "en", []LocaleEntry{en, {"!!", "x"}}, false, "", true},
		{"und tag", "en", []LocaleEntry{en, {"und", "x"}}, false, "", true},
		{"duplicate tag", "en", []LocaleEntry{en, fr, {"fr", "fr2"}}, false, "", true},
		{"duplicate tag after normalize", "en", []LocaleEntry{{"en-GB", ""}, {"en_gb", "g"}}, false, "", true},
		{"duplicate segment", "en", []LocaleEntry{en, fr, {"nl", "fr"}}, false, "", true},
		{"two empty segments", "en", []LocaleEntry{en, {"nl", ""}}, false, "", true},
		{"non-default empty segment", "en", []LocaleEntry{en, {"fr", ""}}, false, "", true},
		{"default empty with prefix-all", "en", []LocaleEntry{en, fr}, true, "", true},
		{"default segment with prefix-all", "en", []LocaleEntry{{"en", "en"}, fr}, true, "", false},
		{"uppercase segment", "en", []LocaleEntry{en, {"fr", "FR"}}, false, "", true},
		{"slash segment", "en", []LocaleEntry{en, {"fr", "a/b"}}, false, "", true},
		{"double hyphen", "en", []LocaleEntry{en, {"fr", "a--b"}}, false, "", true},
		{"trailing hyphen", "en", []LocaleEntry{en, {"fr", "a-"}}, false, "", true},
		{"hyphenated ok", "en", []LocaleEntry{en, {"fr-ca", "fr-ca"}}, false, "", false},
		{"segment too long", "en", []LocaleEntry{en, {"fr", "abcdefghijklmnopqrstuvwxyz0123456"}}, false, "", true},
		{"default not in list", "nl", []LocaleEntry{en, fr}, false, "", true},
		{"bad default", "", []LocaleEntry{en, fr}, false, "", true},
		{"x-default not in list", "en", []LocaleEntry{en, fr}, false, "nl", true},
		{"x-default bad", "en", []LocaleEntry{en, fr}, false, "??", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewLocaleSet(tc.def, tc.entries, tc.prefixAll, tc.xdef)
			if tc.bad {
				if !errors.Is(err, ErrValidationFailed) {
					t.Fatalf("want ErrValidationFailed, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLocaleSetResolveAndPrefix(t *testing.T) {
	s, err := NewLocaleSet("en", []LocaleEntry{{"en", ""}, {"fr", "fr"}, {"es-MX", "mx"}}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Multi() || s.Default() != "en" || s.XDefault() != "" {
		t.Fatalf("accessors: multi=%v def=%q", s.Multi(), s.Default())
	}
	if got := s.Tags(); len(got) != 3 || got[0] != "en" || got[1] != "fr" || got[2] != "es-MX" {
		t.Fatalf("tags: %v", got)
	}
	res := []struct {
		path, tag, rest string
		ok              bool
	}{
		{"/fr", "fr", "/", true},
		{"/fr/", "fr", "/", true},
		{"/fr/x", "fr", "/x", true},
		{"/mx/a/b", "es-MX", "/a/b", true},
		{"/frx", "", "", false},
		{"/fr-x/y", "", "", false},
		{"/", "", "", false},
		{"", "", "", false},
		{"/x/fr", "", "", false},
	}
	for _, r := range res {
		tag, rest, ok := s.Resolve(r.path)
		if tag != r.tag || rest != r.rest || ok != r.ok {
			t.Errorf("Resolve(%q) = %q,%q,%v want %q,%q,%v", r.path, tag, rest, ok, r.tag, r.rest, r.ok)
		}
	}
	for tag, want := range map[string]string{"en": "", "fr": "/fr", "es-MX": "/mx", LocaleShared: "", "nl": ""} {
		if got := s.Prefix(tag); got != want {
			t.Errorf("Prefix(%q) = %q want %q", tag, got, want)
		}
	}

	pa, err := NewLocaleSet("en", []LocaleEntry{{"en", "en"}, {"fr", "fr"}}, true, "en")
	if err != nil {
		t.Fatal(err)
	}
	if pa.Prefix("en") != "/en" || pa.Prefix(LocaleShared) != "" || pa.XDefault() != "en" {
		t.Fatalf("prefix-all: %q %q", pa.Prefix("en"), pa.XDefault())
	}
}

func TestLocaleSetSingleAndNil(t *testing.T) {
	one, err := NewLocaleSet("en", []LocaleEntry{{"en", ""}}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	empty, _ := NewLocaleSet("en", nil, false, "")
	var nilSet *LocaleSet
	for name, s := range map[string]*LocaleSet{"one": one, "empty": empty, "nil": nilSet} {
		if s.Multi() {
			t.Errorf("%s: Multi", name)
		}
		if _, _, ok := s.Resolve("/fr/x"); ok {
			t.Errorf("%s: Resolve matched", name)
		}
		if s.Prefix("en") != "" || s.Prefix("fr") != "" {
			t.Errorf("%s: Prefix not empty", name)
		}
		if name == "nil" && (s.Tags() != nil || s.Default() != "" || s.XDefault() != "") {
			t.Errorf("nil accessors not zero")
		}
	}
}

func TestLocaleSetSegment(t *testing.T) {
	s, err := NewLocaleSet("en", []LocaleEntry{{"en", ""}, {"fr", "fr"}, {"es-MX", "mx"}}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	pa, err := NewLocaleSet("en", []LocaleEntry{{"en", "en"}, {"fr", "fr"}}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	var nilSet *LocaleSet
	tests := []struct {
		name string
		set  *LocaleSet
		tag  string
		seg  string
		ok   bool
	}{
		{"default without prefix", s, "en", "", true},
		{"non-default", s, "fr", "fr", true},
		{"segment differs from tag", s, "es-MX", "mx", true},
		{"unknown tag", s, "xx", "", false},
		{"shared", s, LocaleShared, "", false},
		{"default with prefix_all", pa, "en", "en", true},
		{"non-default with prefix_all", pa, "fr", "fr", true},
		{"nil set", nilSet, "en", "", false},
	}
	for _, tc := range tests {
		seg, ok := tc.set.Segment(tc.tag)
		if seg != tc.seg || ok != tc.ok {
			t.Errorf("%s: Segment(%q) = %q, %v; want %q, %v", tc.name, tc.tag, seg, ok, tc.seg, tc.ok)
		}
	}
}
