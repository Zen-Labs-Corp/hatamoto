package schemaform

import (
	"encoding/json/jsontext"
	"net/url"
	"strings"
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

func renderStart(t *testing.T, stored string, opt Options) string {
	t.Helper()
	out, err := Render(jsontext.Value(dateTimeSchema), map[string]any{"start": stored}, opt)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestRenderDateTimeInSiteZone(t *testing.T) {
	opt := newYork(t)
	cases := []struct {
		stored, value string
		step          bool
	}{
		{"2026-06-14T23:00:00Z", "2026-06-14T19:00", false},
		{"2026-06-14T23:00:30Z", "2026-06-14T19:00:30", true},
		{"2026-06-14T19:00", "2026-06-14T19:00", false},
	}
	for _, c := range cases {
		out := renderStart(t, c.stored, opt)
		if !strings.Contains(out, `type="datetime-local"`) || !strings.Contains(out, `value="`+c.value+`"`) {
			t.Errorf("%q: want value %q in %s", c.stored, c.value, out)
		}
		if strings.Contains(out, `step="1"`) != c.step {
			t.Errorf("%q: step present = %v, want %v", c.stored, !c.step, c.step)
		}
	}
}

func TestRenderDateTimeWithoutLocationIsRaw(t *testing.T) {
	out := renderStart(t, "2026-06-14T23:00:00Z", Options{})
	if !strings.Contains(out, `value="2026-06-14T23:00:00Z"`) || strings.Contains(out, "step=") {
		t.Errorf("not raw: %s", out)
	}
}

func TestDateTimeRoundTrip(t *testing.T) {
	opt := newYork(t)
	got, errs := Decode(jsontext.Value(dateTimeSchema), url.Values{"start": {"2026-06-14T19:00:30"}}, nil, opt)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if out := renderStart(t, got["start"].(string), opt); !strings.Contains(out, `value="2026-06-14T19:00:30"`) {
		t.Errorf("round trip lost the value: %s", out)
	}
}

func TestDecodeDateTimeUntouchedKeepsStored(t *testing.T) {
	opt := newYork(t)
	for _, stored := range []string{
		"2026-11-01T06:30:00Z",      // second 01:30 of the fall-back night
		"2026-06-14T21:00:00+02:00", // source offset
		"2026-06-14T23:00:00.250Z",  // fraction
	} {
		shown, _ := pickerValue(stored, opt.Location)
		got, errs := Decode(jsontext.Value(dateTimeSchema), url.Values{"start": {shown}}, map[string]any{"start": stored}, opt)
		if len(errs) != 0 || got["start"] != stored {
			t.Errorf("stored %q posted %q: got %v, %v", stored, shown, got["start"], errs)
		}
	}
}

func TestDecodeDateTimeChangedIsNormalised(t *testing.T) {
	got, errs := Decode(jsontext.Value(dateTimeSchema), url.Values{"start": {"2026-06-14T20:00"}}, map[string]any{"start": "2026-06-14T21:00:00+02:00"}, newYork(t))
	if len(errs) != 0 || got["start"] != "2026-06-14T20:00:00-04:00" {
		t.Errorf("got %v, %v", got["start"], errs)
	}
}

func TestDecodeDateTimeRepeaterItemUntouched(t *testing.T) {
	opt := newYork(t)
	prev := map[string]any{"rows": []any{
		map[string]any{"slot": "2026-11-01T06:30:00Z"},
		map[string]any{"slot": "2026-06-14T21:00:00+02:00"},
	}}
	form := url.Values{
		"_present.rows": {"1"},
		"rows.0.slot":   {"2026-11-01T01:30"},
		"rows.1.slot":   {"2026-06-14T16:00"}, // changed from 15:00
	}
	got, errs := Decode(jsontext.Value(dateTimeSchema), form, prev, opt)
	rows, _ := got["rows"].([]any)
	if len(errs) != 0 || len(rows) != 2 {
		t.Fatalf("got %v, %v", got, errs)
	}
	if r := rows[0].(map[string]any)["slot"]; r != "2026-11-01T06:30:00Z" {
		t.Errorf("untouched item: %v", r)
	}
	if r := rows[1].(map[string]any)["slot"]; r != "2026-06-14T16:00:00-04:00" {
		t.Errorf("changed item: %v", r)
	}
}

func TestDecodeRepeaterCompactedDoesNotLeakOtherItem(t *testing.T) {
	const schema = `{"type":"object","properties":{"rows":{"type":"array","items":{"type":"object","properties":{"title":{"type":"string"},"slot":{"type":"string","format":"date-time"}}}}}}`
	prev := map[string]any{"rows": []any{
		map[string]any{"title": map[string]any{"url": "a", "alt": "A"}, "slot": "2026-11-01T06:30:00Z"},
		map[string]any{"title": "b", "slot": "2026-06-14T21:00:00+02:00"},
	}}
	// Item 0 was removed; the survivor was re-indexed to 0.
	form := url.Values{
		"_present.rows": {"1"},
		"rows.0.title":  {"b"},
		"rows.0.slot":   {"2026-06-14T16:00"},
	}
	got, errs := Decode(jsontext.Value(schema), form, prev, newYork(t))
	rows, _ := got["rows"].([]any)
	if len(errs) != 0 || len(rows) != 1 {
		t.Fatalf("got %v, %v", got, errs)
	}
	row := rows[0].(map[string]any)
	if row["title"] != "b" {
		t.Errorf("title took another item's value: %v", row["title"])
	}
	if row["slot"] != "2026-06-14T16:00:00-04:00" {
		t.Errorf("slot: %v", row["slot"])
	}
}
