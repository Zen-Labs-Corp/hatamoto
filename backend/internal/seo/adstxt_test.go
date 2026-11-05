package seo

import (
	"strings"
	"testing"
)

func TestValidateAdsTxt(t *testing.T) {
	ok := []string{
		"",
		"# comment\n\ngoogle.com, pub-1234, DIRECT, f08c47fec0942fa0\n",
		"google.com, pub-1, reseller\r\nad-sys.example.co.uk, a.b-c, RESELLER, abc-1 # note\n",
		"CONTACT=ads@example.com\nsubdomain=news.example.com\nINVENTORYPARTNERDOMAIN=x.com\nOWNERDOMAIN=a.com\nMANAGERDOMAIN=b.com\n",
		"\tgoogle.com,\tpub-1,\tDIRECT\n",
		"google.com, pub-1, DIRECT, f08c47fec0942fa0;extension\n",
		"google.com, pub-1, DIRECT, f08c47fec0942fa0;key=value;x\n",
		"google.com, pub-1, RESELLER;a=b\n",
		"google.com, pub-1, DIRECT,\n",
		"google.com, pub-1, DIRECT, # note\n",
	}
	for _, s := range ok {
		if errs := ValidateAdsTxt("f", s); len(errs) != 0 {
			t.Errorf("%q refused: %v", s, errs)
		}
	}
	bad := map[string]string{
		"# x\ngoogle.com, pub-1, MAYBE\n":       "Line 2: the account type must be DIRECT or RESELLER.",
		"google, pub-1, DIRECT\n":               "Line 1: the first field",
		"google.com, pub 1, DIRECT\n":           "Line 1: the account id",
		"google.com, pub-1\n":                   "Line 1: a record needs",
		"google.com, pub-1, DIRECT, a b\n":      "Line 1: the certification id",
		"google.com, pub-1, DIRECT, a b;ext\n":  "Line 1: the certification id",
		"google.com, pub-1, DIRECT, a, b\n":     "Line 1: a record needs",
		"FOO=bar\n":                             "Line 1: use CONTACT",
		"CONTACT=\n":                            "Line 1: CONTACT needs a value.",
		"google.com, pub-1, DIRECT\x01\n":       "Line 1: remove the control character.",
		strings.Repeat("#\n", MaxAdsTxtLines+1): "At most 5000 lines.",
		strings.Repeat("#", MaxAdsTxtBytes+1):   "At most 256 KiB.",
	}
	for s, want := range bad {
		errs := ValidateAdsTxt("f", s)
		if len(errs) != 1 || !strings.HasPrefix(errs[0].Message, want) || errs[0].Field != "f" {
			t.Errorf("%.40q: got %v, want %q", s, errs, want)
		}
	}
	if len(ValidateAdsTxt("f", strings.Repeat("#\n", MaxAdsTxtLines))) != 0 {
		t.Error("exactly the line limit must pass")
	}
}

func TestSettingsValidateChecksAdsTxt(t *testing.T) {
	s := Defaults()
	s.AppAdsTxt = "nope\n"
	var found bool
	for _, e := range s.Validate() {
		found = found || e.Field == "app_ads_txt"
	}
	if !found {
		t.Error("Validate ignored app_ads_txt")
	}
}

func TestNormalizeAdsTxt(t *testing.T) {
	if got := NormalizeAdsTxt("  a, b, DIRECT \r\n\r\nc \r\n\r\n"); got != "a, b, DIRECT\n\nc\n" {
		t.Errorf("got %q", got)
	}
	if NormalizeAdsTxt(" \n\r\n") != "" {
		t.Error("blank must normalise to empty")
	}
}

func TestAdSenseLine(t *testing.T) {
	want := "google.com, pub-123, DIRECT, f08c47fec0942fa0"
	for _, in := range []string{"pub-123", "ca-pub-123", " ca-pub-123 "} {
		if got := AdSenseLine(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	for _, in := range []string{"", "pub-", "pub-12x", "ca-app-pub-1"} {
		if AdSenseLine(in) != "" || AdSensePubID(in) != "" {
			t.Errorf("%q accepted", in)
		}
	}
	if AdSensePubID("ca-pub-9") != "pub-9" {
		t.Error("pub id")
	}
}
