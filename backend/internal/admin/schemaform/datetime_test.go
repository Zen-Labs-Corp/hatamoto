package schemaform

import (
	"encoding/json/jsontext"
	"net/url"
	"testing"
	"time"
)

const dateTimeSchema = `{"type":"object","properties":{
	"start":{"type":"string","format":"date-time"},
	"when":{"type":"object","properties":{"slot":{"type":"string","format":"date-time"}}},
	"rows":{"type":"array","items":{"type":"object","properties":{"slot":{"type":"string","format":"date-time"}}}}}}`

func newYork(t *testing.T) Options {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	return Options{Location: loc}
}

func TestDecodeDateTimeNormalises(t *testing.T) {
	schema := jsontext.Value(dateTimeSchema)
	opt := newYork(t)
	cases := map[string]string{
		"2026-06-14T19:00":       "2026-06-14T19:00:00-04:00",
		"2026-06-14T19:00:30":    "2026-06-14T19:00:30-04:00",
		"2026-06-14T23:00:00Z":   "2026-06-14T23:00:00Z",
		"2026-06-14T19:00+02:00": "2026-06-14T19:00+02:00",
		"garbage":                "garbage",
	}
	for in, want := range cases {
		got, errs := Decode(schema, url.Values{"start": {in}}, nil, opt)
		if len(errs) > 0 || got["start"] != want {
			t.Errorf("%q: got %v (%v), want %q", in, got["start"], errs, want)
		}
	}
}

func TestDecodeDateTimeEmptyUnsets(t *testing.T) {
	got, errs := Decode(jsontext.Value(dateTimeSchema), url.Values{"start": {" "}}, map[string]any{"start": "x"}, newYork(t))
	if _, ok := got["start"]; ok || len(errs) > 0 {
		t.Errorf("got %v (%v), want key removed", got, errs)
	}
}

func TestDecodeDateTimeNested(t *testing.T) {
	form := url.Values{"_present.rows": {"1"}, "when.slot": {"2026-06-14T19:00"}, "rows.0.slot": {"2026-12-01T08:15"}}
	got, errs := Decode(jsontext.Value(dateTimeSchema), form, nil, newYork(t))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if slot := got["when"].(map[string]any)["slot"]; slot != "2026-06-14T19:00:00-04:00" {
		t.Errorf("object field: %v", slot)
	}
	rows := got["rows"].([]any)
	if slot := rows[0].(map[string]any)["slot"]; slot != "2026-12-01T08:15:00-05:00" {
		t.Errorf("repeater field: %v", slot)
	}
}

func TestDecodeDateTimeNonexistentLocalTime(t *testing.T) {
	got, errs := Decode(jsontext.Value(dateTimeSchema), url.Values{"start": {"2026-03-08T02:30"}}, nil, newYork(t))
	if _, ok := got["start"]; ok {
		t.Errorf("value saved: %v", got["start"])
	}
	if len(errs["start"]) != 1 {
		t.Errorf("errors = %v, want one on start", errs)
	}
}

func TestDecodeDateTimeWithoutLocationIsUntouched(t *testing.T) {
	got, errs := Decode(jsontext.Value(dateTimeSchema), url.Values{"start": {"2026-06-14T19:00"}}, nil, Options{})
	if len(errs) > 0 || got["start"] != "2026-06-14T19:00" {
		t.Errorf("got %v (%v)", got["start"], errs)
	}
}

func TestValidateDateTimeStaysStrict(t *testing.T) {
	if errs := Validate(jsontext.Value(dateTimeSchema), jsontext.Value(`{"start":"2026-06-14T19:00"}`)); len(errs) == 0 {
		t.Error("naive date-time passed the validator")
	}
}
