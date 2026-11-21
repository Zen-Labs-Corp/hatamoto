package sdk

import (
	jsonv2 "encoding/json/v2"
	"strings"
	"testing"
)

func TestLicenseValidate(t *testing.T) {
	for _, tc := range []struct {
		license string
		ok      bool
	}{
		{"", true}, {"commercial", true},
		{"Commercial", false}, {"gpl", false}, {" commercial", false},
	} {
		m := Manifest{Name: "pro", Version: "0.1.0", SDK: "^0.26", License: tc.license}
		err := m.Validate()
		if tc.ok && err != nil {
			t.Errorf("license %q refused: %v", tc.license, err)
		}
		if !tc.ok && (err == nil || !strings.Contains(err.Error(), "license")) {
			t.Errorf("license %q: err = %v, want a license refusal", tc.license, err)
		}
	}
}

func TestLicenseJSONKey(t *testing.T) {
	b, err := jsonv2.Marshal(Manifest{Name: "pro", Version: "0.1.0", SDK: "^0.26", License: LicenseCommercial})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"license":"commercial"`) {
		t.Errorf("marshalled manifest = %s, want the license key", b)
	}
	var m Manifest
	if err := jsonv2.Unmarshal([]byte(`{"name":"p","version":"0.1.0","sdk":"^0.26","license":"commercial"}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.License != LicenseCommercial {
		t.Errorf("License = %q", m.License)
	}
}
