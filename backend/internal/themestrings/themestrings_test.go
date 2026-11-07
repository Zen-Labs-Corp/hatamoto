package themestrings

import (
	"encoding/json/jsontext"
	"maps"
	"strings"
	"testing"
)

func TestValidKeyAndText(t *testing.T) {
	for k, want := range map[string]bool{"search": true, "pager_page_2": true, "a": true, "": false, "Search": false, "1a": false, "a-b": false, strings.Repeat("a", 65): false, strings.Repeat("a", 64): true} {
		if ValidKey(k) != want {
			t.Errorf("ValidKey(%q) = %v", k, !want)
		}
	}
	for s, ok := range map[string]bool{"Hello": true, "": false, "<b>": false, "a & b": false, "a\nb": false, "a\x00": false, strings.Repeat("é", 300): true, strings.Repeat("é", 301): false, "“quoted” …": true} {
		if (ValidText(s) == nil) != ok {
			t.Errorf("ValidText(%q) ok=%v", s, !ok)
		}
	}
}

func TestParseDeclAndLenient(t *testing.T) {
	good := jsontext.Value(`{"a":"One","b_c":"Two {n}"}`)
	d, err := ParseDecl(good)
	if err != nil || len(d) != 2 {
		t.Fatalf("good: %v %v", d, err)
	}
	if d, err := ParseDecl(nil); d != nil || err != nil {
		t.Fatalf("absent: %v %v", d, err)
	}
	for _, bad := range []string{`[]`, `"x"`, `{"A":"x"}`, `{"a":"<b>"}`, `{"a":""}`, `{"a":1}`, `{"a":"` + strings.Repeat("x", 301) + `"}`} {
		if _, err := ParseDecl(jsontext.Value(bad)); err == nil {
			t.Errorf("ParseDecl(%s) accepted", bad)
		}
	}
	l := Lenient(jsontext.Value(`{"ok":"fine","Bad":"x","html":"<i>","long":"` + strings.Repeat("x", 301) + `"}`))
	if len(l) != 1 || l["ok"] != "fine" {
		t.Fatalf("lenient = %v", l)
	}
	if Lenient(jsontext.Value(`[1]`)) != nil || Lenient(nil) != nil {
		t.Fatal("malformed lenient not nil")
	}
}

func TestChain(t *testing.T) {
	extra := jsontext.Value(`{"other":1,"themeStrings":{"th":{"de1":{"a":"DefA","b":"DefB","gone":"x"},"fr":{"a":"FrA","c":"bad <","b":""}},"zz":{"fr":{"a":"Other"}}}}`)
	o := ParseOverrides(extra)
	decl := Decl{"a": "A", "b": "B", "c": "C"}
	cases := []struct {
		name, locale string
		want         map[string]string
	}{
		{"locale over default", "fr", map[string]string{"a": "FrA", "b": "DefB"}},
		{"empty locale is default", "", map[string]string{"a": "DefA", "b": "DefB"}},
		{"unknown locale falls to default", "es", map[string]string{"a": "DefA", "b": "DefB"}},
	}
	for _, c := range cases {
		if got := Chain(o, "th", c.locale, "de1", decl); !maps.Equal(got, c.want) {
			t.Errorf("%s: %v", c.name, got)
		}
	}
	if got := Chain(o, "none", "fr", "de1", decl); len(got) != 0 {
		t.Errorf("unknown theme: %v", got)
	}
	if ParseOverrides(jsontext.Value(`{"themeStrings":[1]}`)) != nil || ParseOverrides(nil) != nil {
		t.Error("malformed overrides not zero")
	}
}

func TestInterpolate(t *testing.T) {
	for _, c := range []struct {
		in   string
		args []any
		want string
	}{
		{"{n} min read", []any{5}, "5 min read"},
		{"{n} of {n}", []any{2}, "2 of 2"},
		{"{n} min", nil, "{n} min"},
		{"{x} {0} {{n}}", []any{3}, "{x} {0} {3}"},
	} {
		if got := Interpolate(c.in, c.args...); got != c.want {
			t.Errorf("%q = %q want %q", c.in, got, c.want)
		}
	}
}
