package consent

import (
	"strings"
	"testing"
)

func TestDefaultsAreOffAndValid(t *testing.T) {
	t.Parallel()
	d := Defaults()
	if d.Enabled || d.Region != RegionAll || len(d.Categories) != 3 {
		t.Fatalf("defaults = %+v", d)
	}
	if errs := d.Validate(); len(errs) != 0 {
		t.Fatalf("defaults invalid: %v", errs)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*Settings){
		"region":     func(s *Settings) { s.Region = "mars" },
		"title":      func(s *Settings) { s.Title = strings.Repeat("a", MaxTitleLen+1) },
		"text":       func(s *Settings) { s.Text = strings.Repeat("a", MaxTextLen+1) },
		"categories": func(s *Settings) { s.Categories = []string{"analytics", "ads"} },
	}
	for field, mut := range cases {
		s := Defaults()
		mut(&s)
		errs := s.Validate()
		if len(errs) != 1 || errs[0].Field != field {
			t.Errorf("%s: errs = %v", field, errs)
		}
	}
	dup := Defaults()
	dup.Categories = []string{"analytics", "analytics"}
	if len(dup.Validate()) != 1 {
		t.Error("duplicate category not rejected")
	}
}

func TestVersionIgnoresOrderAndTracksSet(t *testing.T) {
	t.Parallel()
	a := Settings{Categories: []string{"analytics", "marketing"}}
	b := Settings{Categories: []string{"marketing", "analytics"}}
	c := Settings{Categories: []string{"analytics"}}
	if a.Version() != b.Version() {
		t.Error("order changed the version")
	}
	if a.Version() == c.Version() || len(a.Version()) != 8 {
		t.Errorf("versions %q %q", a.Version(), c.Version())
	}
}

func TestRegionShow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode, country string
		want          bool
	}{
		{RegionAll, "US", true},
		{RegionAll, "", true},
		{RegionEU, "DE", true},
		{RegionEU, "gb", true},
		{RegionEU, "US", false},
		{RegionEU, "", true},
	}
	for _, c := range cases {
		if got := RegionShow(c.mode, c.country); got != c.want {
			t.Errorf("RegionShow(%q,%q) = %v", c.mode, c.country, got)
		}
	}
	if len(EUUK) != 31 {
		t.Errorf("EUUK has %d entries, want 31", len(EUUK))
	}
}

func TestDecodeKeepsDefaultsForMissingKeys(t *testing.T) {
	t.Parallel()
	s, err := Decode([]byte(`{"enabled":true}`))
	if err != nil || !s.Enabled || s.Region != RegionAll || len(s.Categories) != 3 {
		t.Fatalf("decode = %+v, %v", s, err)
	}
}
