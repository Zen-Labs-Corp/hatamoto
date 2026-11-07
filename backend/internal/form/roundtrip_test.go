package form

import (
	"bytes"
	"encoding/json/jsontext"
	"os"
	"reflect"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

func TestLegacyFormsRoundTrip(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/legacy_forms.json")
	if err != nil {
		t.Fatal(err)
	}
	var forms []struct {
		Name     string         `json:"name"`
		Fields   jsontext.Value `json:"fields"`
		Settings jsontext.Value `json:"settings"`
	}
	if err := jsonx.Unmarshal(raw, &forms, jsonx.Stored); err != nil {
		t.Fatal(err)
	}
	if len(forms) < 3 {
		t.Fatalf("fixture has %d forms, want >= 3", len(forms))
	}
	for _, f := range forms {
		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()
			roundTrip[[]FormField](t, f.Fields)
			roundTrip[FormSettings](t, f.Settings)
		})
	}
}

// roundTrip decodes src into T, re-encodes it, and requires the result to be
// JSON-equal to src (no key added, none lost) and byte-stable on a second pass.
func roundTrip[T any](t *testing.T, src []byte) {
	t.Helper()
	var v T
	if err := jsonx.Unmarshal(src, &v, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	first, err := jsonx.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := jsonx.Unmarshal(src, &want, jsonx.Stored); err != nil {
		t.Fatal(err)
	}
	if err := jsonx.Unmarshal(first, &got, jsonx.Stored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round trip changed the document:\n in: %s\nout: %s", src, first)
	}
	var again T
	if err := jsonx.Unmarshal(first, &again, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	second, err := jsonx.Marshal(again)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("marshal not byte-stable:\n%s\n%s", first, second)
	}
}

func TestNewFieldTypes(t *testing.T) {
	t.Parallel()
	for _, ty := range []string{"heading", "paragraph", "divider", "hidden", "consent"} {
		if !ValidFieldType(ty) {
			t.Errorf("ValidFieldType(%q) = false", ty)
		}
		found := false
		for _, g := range GetFieldTypes() {
			found = found || g == ty
		}
		if !found {
			t.Errorf("GetFieldTypes lacks %q", ty)
		}
	}
	if !(FormField{Type: "divider"}).IsLayout() || (FormField{Type: "hidden"}).IsLayout() {
		t.Error("IsLayout wrong")
	}
	off := false
	if (FormField{InEmail: &off}).InEmailOn() || !(FormField{}).InEmailOn() {
		t.Error("InEmailOn wrong")
	}
}

func TestIsFreeEmail(t *testing.T) {
	t.Parallel()
	listed := FreeEmailDomains()[0]
	for addr, want := range map[string]bool{
		"a@" + listed: true, "a@" + strings.ToUpper(listed): true, "a@corp.example": false, "nope": false,
	} {
		if got := IsFreeEmail(addr); got != want {
			t.Errorf("IsFreeEmail(%q) = %v, want %v", addr, got, want)
		}
	}
	d := FreeEmailDomains()
	for i := 1; i < len(d); i++ {
		if d[i-1] >= d[i] {
			t.Fatalf("domains not sorted/unique at %q", d[i])
		}
	}
}
