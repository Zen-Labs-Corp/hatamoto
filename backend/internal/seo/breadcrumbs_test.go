package seo

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

func TestBreadcrumbsZeroOmittedAndRoundTrip(t *testing.T) {
	raw, err := jsonx.Marshal(Settings{})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"breadcrumbs"`, `"indexnow"`} {
		if strings.Contains(string(raw), k) {
			t.Fatalf("zero settings emit %s: %s", k, raw)
		}
	}
	in := Settings{Breadcrumbs: Breadcrumbs{HomeLabel: "Start", Separator: "›", HideHome: true, HideCurrent: true, ShowTerm: true}}
	raw, _ = jsonx.Marshal(in)
	var out Settings
	if err := jsonx.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Breadcrumbs != in.Breadcrumbs || out.Clone().Breadcrumbs != in.Breadcrumbs {
		t.Fatalf("round trip: %+v", out.Breadcrumbs)
	}
}

func TestBreadcrumbsHomeAndIsZero(t *testing.T) {
	var b Breadcrumbs
	if !b.IsZero() || b.Home() != "Home" {
		t.Fatalf("zero: %v %q", b.IsZero(), b.Home())
	}
	b.HomeLabel = "Start"
	if b.IsZero() || b.Home() != "Start" {
		t.Fatalf("set: %v %q", b.IsZero(), b.Home())
	}
}

func TestValidateBreadcrumbs(t *testing.T) {
	cases := []struct {
		name string
		b    Breadcrumbs
		want string
	}{
		{"ok", Breadcrumbs{HomeLabel: "Start", Separator: "»"}, ""},
		{"long", Breadcrumbs{HomeLabel: strings.Repeat("a", 41)}, "breadcrumbs[home_label]"},
		{"control", Breadcrumbs{HomeLabel: "a\nb"}, "breadcrumbs[home_label]"},
		{"separator", Breadcrumbs{Separator: "|"}, "breadcrumbs[separator]"},
	}
	for _, c := range cases {
		errs := validateBreadcrumbs(c.b)
		if c.want == "" {
			if len(errs) != 0 {
				t.Errorf("%s: %v", c.name, errs)
			}
			continue
		}
		if len(errs) != 1 || errs[0].Field != c.want {
			t.Errorf("%s: %v", c.name, errs)
		}
	}
}
