package sdk

import (
	jsonv2 "encoding/json/v2"
	"strings"
	"testing"
)

// Validate does not look at license: a manifest that is already installed has
// to keep loading whatever the member holds. ParseNew refuses new installs.
func TestLicenseValueIsNotValidated(t *testing.T) {
	for _, v := range []License{"", "commercial", "Commercial", "MIT", LicenseNotString} {
		m := Manifest{Name: "pro", Version: "0.1.0", SDK: "^0.26", License: v}
		if err := m.Validate(); err != nil {
			t.Errorf("license %q refused: %v", v, err)
		}
	}
}

func TestLicenseUnmarshalIsTolerant(t *testing.T) {
	for _, tc := range []struct {
		json string
		want License
		ok   bool
	}{
		{`"commercial"`, LicenseCommercial, true},
		{`"Commercial"`, "Commercial", false},
		{`"MIT"`, "MIT", false},
		{`""`, "", true},
		{`null`, "", true},
		{`{"type":"MIT"}`, LicenseNotString, false},
		{`["MIT"]`, LicenseNotString, false},
		{`7`, LicenseNotString, false},
	} {
		var m Manifest
		if err := jsonv2.Unmarshal([]byte(`{"name":"p","version":"0.1.0","sdk":"^0.5","license":`+tc.json+`}`), &m); err != nil {
			t.Errorf("license %s: %v", tc.json, err)
			continue
		}
		if m.License != tc.want || m.License.Valid() != tc.ok {
			t.Errorf("license %s: got %q valid=%v, want %q valid=%v", tc.json, m.License, m.License.Valid(), tc.want, tc.ok)
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
