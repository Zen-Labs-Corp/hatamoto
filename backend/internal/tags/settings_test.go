package tags

import "testing"

func TestDefaultsAreOffAndValid(t *testing.T) {
	t.Parallel()
	d := Defaults()
	if !d.ExcludeSignedIn || len(d.Enabled()) != 0 || len(d.Validate()) != 0 {
		t.Fatalf("defaults = %+v", d)
	}
}

func TestDecodeOlderRows(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "{}", `{"vendors":null}`} {
		s, err := Decode([]byte(raw))
		if err != nil || !s.ExcludeSignedIn || s.Vendors == nil {
			t.Errorf("Decode(%q) = %+v, %v", raw, s, err)
		}
	}
	s, _ := Decode([]byte(`{"exclude_signed_in":false,"vendors":{"ga4":{"enabled":true,"id":"G-ABC123XYZ9"}}}`))
	if s.ExcludeSignedIn || len(s.Enabled()) != 1 {
		t.Errorf("decoded = %+v", s)
	}
	if _, err := Decode([]byte("{")); err == nil {
		t.Error("bad json accepted")
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	cases := map[string]Settings{
		"ga4":      {Vendors: map[string]VendorState{"ga4": {Enabled: true, ID: "UA-1"}}},
		"gtm":      {Vendors: map[string]VendorState{"gtm": {Enabled: true}}},
		"gads.aux": {Vendors: map[string]VendorState{"gads": {Enabled: true, ID: "AW-123456789", Aux: "a b"}}},
		"nope":     {Vendors: map[string]VendorState{"nope": {Enabled: true, ID: "1"}}},
		"meta":     {Vendors: map[string]VendorState{"meta": {ID: "abc"}}}, // disabled but malformed
	}
	for field, s := range cases {
		errs := s.Validate()
		if len(errs) != 1 || errs[0].Field != field {
			t.Errorf("%s: errs = %v", field, errs)
		}
	}
	ok := Settings{Vendors: map[string]VendorState{
		"ga4":      {Enabled: true, ID: " g-abc123xyz9 "},
		"gtm":      {ID: ""},
		"linkedin": {Enabled: true, ID: "12345", Aux: "1234"},
	}}
	if errs := ok.Validate(); len(errs) != 0 {
		t.Errorf("valid settings rejected: %v", errs)
	}
}

func TestEnabledOrderAndNormalizedIDs(t *testing.T) {
	t.Parallel()
	s := Settings{Vendors: map[string]VendorState{
		"meta": {Enabled: true, ID: "123456789012345"},
		"ga4":  {Enabled: true, ID: " g-abc123xyz9"},
		"gtm":  {Enabled: false, ID: "GTM-ABC123"},
		"uet":  {Enabled: true},
	}}
	en := s.Enabled()
	if len(en) != 2 || en[0].ID != "ga4" || en[1].ID != "meta" || en[0].ValueID != "G-ABC123XYZ9" {
		t.Fatalf("enabled = %+v", en)
	}
}

func TestHostsMergeOnlyEnabled(t *testing.T) {
	t.Parallel()
	s := Settings{Vendors: map[string]VendorState{
		"ga4":  {Enabled: true, ID: "G-ABC123XYZ9"},
		"gads": {Enabled: true, ID: "AW-123456789"},
		"meta": {Enabled: false, ID: "123456789012345"},
	}}
	h := s.Hosts()
	n := 0
	for _, x := range h.Script {
		if x == "https://www.googletagmanager.com" {
			n++
		}
		if x == "https://connect.facebook.net" {
			t.Error("disabled vendor host present")
		}
	}
	if n != 1 {
		t.Errorf("gtm host listed %d times: %v", n, h.Script)
	}
	if got := (Defaults()).Hosts(); len(got.Script)+len(got.Connect) != 0 {
		t.Errorf("defaults hosts = %+v", got)
	}
}
