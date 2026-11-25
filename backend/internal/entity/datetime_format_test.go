package entity

import (
	"encoding/json/jsontext"
	"testing"
)

// The API and plugin writes stay strict: only a full RFC 3339 date-time is
// accepted. The admin form converts the picker's value before it gets here.
func TestValidatorDateTimeStaysStrictRFC3339(t *testing.T) {
	v := NewValidator()
	schema := jsontext.Value(`{"type":"object","properties":{"start":{"type":"string","format":"date-time"}}}`)
	check := func(value string, wantValid bool) {
		t.Helper()
		attrs := jsontext.Value(`{"start":"` + value + `"}`)
		if got := v.ValidateWithDetails(schema, attrs); got.Valid != wantValid {
			t.Errorf("%q: valid = %v, want %v (%v)", value, got.Valid, wantValid, got.Errors)
		}
	}
	for _, bad := range []string{"2026-06-14T19:00", "2026-06-14T19:00:30"} {
		check(bad, false)
	}
	for _, ok := range []string{"2026-06-14T19:00:00-04:00", "2026-06-14T23:00:00Z"} {
		check(ok, true)
	}
}
