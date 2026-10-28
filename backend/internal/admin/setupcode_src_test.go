package admin

import (
	"os"
	"strings"
	"testing"
)

// The prefill behaviour is tested in scripts/admin-qa/unit/setupcode.test.mjs; this checks the
// script still reads the fragment (never the query string) and the page still loads it.
func TestSetupCodeModuleSource(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../web/admin/js/setupcode.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	for _, want := range []string{"#code=", "replaceState"} {
		if !strings.Contains(js, want) {
			t.Errorf("setupcode.js must contain %q", want)
		}
	}
	for _, bad := range []string{"eval(", "innerHTML", "URLSearchParams"} {
		if strings.Contains(js, bad) {
			t.Errorf("setupcode.js must not use %q", bad)
		}
	}

	p, err := os.ReadFile("../../web/admin/pages/setup/account.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(p), `{{define "modules"}}@hm/setupcode{{end}}`) {
		t.Error("account.html must declare the setupcode module")
	}
}
