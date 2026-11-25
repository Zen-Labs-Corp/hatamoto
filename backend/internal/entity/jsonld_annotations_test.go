package entity

import (
	"context"
	"testing"
	"time"
)

func TestSchemaOrgTarget(t *testing.T) {
	t.Parallel()
	good := map[string]string{
		"EventCancelled":                    "https://schema.org/EventCancelled",
		"https://schema.org/EventPostponed": "https://schema.org/EventPostponed",
		"  EventScheduled ":                 "https://schema.org/EventScheduled",
	}
	for in, want := range good {
		if got, ok := schemaOrgTarget(in); !ok || got != want {
			t.Errorf("%q = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"http://schema.org/X", "https://example.org/EventCancelled", "EventGone/..", "", "  ", "9Lives"} {
		if got, ok := schemaOrgTarget(in); ok {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
}

func TestDatetimeValue(t *testing.T) {
	t.Parallel()
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tzdata")
	}
	tests := []struct {
		name, date string
		clock      any
		loc        *time.Location
		want       string
		ok         bool
	}{
		{"paris winter", "2026-12-05", "18:30", paris, "2026-12-05T18:30:00+01:00", true},
		{"utc", "2026-12-05", "18:30", time.UTC, "2026-12-05T18:30:00+00:00", true},
		{"nil zone is utc", "2026-12-05", "18:30", nil, "2026-12-05T18:30:00+00:00", true},
		{"paris summer", "2026-07-05", "18:30:15", paris, "2026-07-05T18:30:15+02:00", true},
		{"empty time", "2026-12-05", "", paris, "2026-12-05", true},
		{"nil time", "2026-12-05", nil, paris, "2026-12-05", true},
		{"invalid time", "2026-12-05", "25:99", paris, "2026-12-05", true},
		{"non-string time", "2026-12-05", 42.0, paris, "2026-12-05", true},
		{"invalid date", "05/12/2026", "18:30", paris, "", false},
		{"rfc3339 date", "2026-12-05T10:00:00Z", "18:30", paris, "2026-12-05T10:00:00Z", true},
		{"empty date", "", "18:30", paris, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := datetimeValue(tc.date, tc.clock, tc.loc)
			if got != tc.want || ok != tc.ok {
				t.Errorf("got %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

const eventAnnotatedSchema = `{
  "type": "object",
  "x-schema-org-type": "Event",
  "properties": {
    "name": {"type": "string", "x-schema-org-property": "name"},
    "location": {"type": "string", "x-schema-org-property": "location"},
    "status": {"type": "string", "enum": ["scheduled", "cancelled", "other"],
      "x-schema-org-property": "eventStatus",
      "x-schema-org-enum": {"scheduled": "https://schema.org/EventScheduled", "cancelled": "https://schema.org/EventCancelled", "postponed": "EventPostponed", "gone": "EventGone/.."}},
    "date_start": {"type": "string", "format": "date", "x-schema-org-property": "startDate", "x-schema-org-datetime": {"time": "time_start"}},
    "time_start": {"type": "string"},
    "date_end": {"type": "string", "format": "date", "x-schema-org-property": "endDate", "x-schema-org-datetime": {"time": "time_end"}},
    "time_end": {"type": "string"}
  }
}`

func eventDoc(t *testing.T, schema, attrs string) map[string]any {
	t.Helper()
	doc := GenerateSchemaDrivernJSONLD(overrideEntity("event", `{}`, schema, attrs), "https://example.org")
	if doc == nil {
		t.Fatal("generator returned nil")
	}
	return doc
}

func TestAnnotatedEventJSONLD(t *testing.T) {
	// Not parallel: installs the global URL registry.
	if _, err := time.LoadLocation("Europe/Paris"); err != nil {
		t.Skip("no tzdata")
	}
	reg := NewURLRegistry(stubLister{}, nil)
	reg.SetTimezone(func(context.Context) string { return "Europe/Paris" })
	SetURLRegistry(reg)
	t.Cleanup(func() { SetURLRegistry(nil) })

	base := `"date_start":"2026-12-05","time_start":"18:30","date_end":"2026-12-05","time_end":"20:00"`
	doc := eventDoc(t, eventAnnotatedSchema, `{"status":"cancelled",`+base+`}`)
	if doc["eventStatus"] != "https://schema.org/EventCancelled" {
		t.Errorf("eventStatus = %v", doc["eventStatus"])
	}
	if doc["startDate"] != "2026-12-05T18:30:00+01:00" || doc["endDate"] != "2026-12-05T20:00:00+01:00" {
		t.Errorf("dates = %v / %v", doc["startDate"], doc["endDate"])
	}
	for k, v := range doc {
		if k == "time_start" || k == "time_end" || v == "18:30" || v == "20:00" {
			t.Errorf("time attribute printed on its own: %s=%v", k, v)
		}
	}

	for name, attrs := range map[string]string{
		"unmapped":    `{"status":"other",` + base + `}`,
		"non-string":  `{"status":42,` + base + `}`,
		"bad target":  `{"status":"gone",` + base + `}`,
		"missing key": `{` + base + `}`,
	} {
		if v, has := eventDoc(t, eventAnnotatedSchema, attrs)["eventStatus"]; has {
			t.Errorf("%s: eventStatus = %v", name, v)
		}
	}
	if got := eventDoc(t, eventAnnotatedSchema, `{"status":"postponed",`+base+`}`)["eventStatus"]; got != "https://schema.org/EventPostponed" {
		t.Errorf("bare term = %v", got)
	}
	empty := eventDoc(t, eventAnnotatedSchema, `{"date_start":"2026-12-05","time_start":""}`)
	if empty["startDate"] != "2026-12-05" {
		t.Errorf("empty time startDate = %v", empty["startDate"])
	}

	SetURLRegistry(nil)
	if got := eventDoc(t, eventAnnotatedSchema, `{`+base+`}`)["startDate"]; got != "2026-12-05T18:30:00+00:00" {
		t.Errorf("utc startDate = %v", got)
	}
}

func TestAnnotationsNestedAndFlattened(t *testing.T) {
	schema := `{
  "type": "object", "x-schema-org-type": "Event",
  "properties": {
    "details": {"type": "object", "x-schema-org-property": "subjectOf", "x-schema-org-type": "CreativeWork",
      "properties": {"state": {"type": "string", "x-schema-org-property": "creativeWorkStatus", "x-schema-org-enum": {"a": "EventScheduled"}}}},
    "flat": {"type": "object",
      "properties": {"status": {"type": "string", "x-schema-org-property": "eventStatus", "x-schema-org-enum": {"c": "EventCancelled"}}}}
  }
}`
	doc := eventDoc(t, schema, `{"details":{"state":"a"},"flat":{"status":"c"}}`)
	sub, _ := doc["subjectOf"].(map[string]any)
	if sub["creativeWorkStatus"] != "https://schema.org/EventScheduled" {
		t.Errorf("nested = %v", doc["subjectOf"])
	}
	if doc["eventStatus"] != "https://schema.org/EventCancelled" {
		t.Errorf("flattened = %v", doc["eventStatus"])
	}
}

func TestAnnotationsMalformedIgnored(t *testing.T) {
	for name, frag := range map[string]string{
		"enum string":   `"x-schema-org-enum": "oops"`,
		"enum non-str":  `"x-schema-org-enum": {"cancelled": 3}`,
		"enum keyword":  `"enum": "x", "x-schema-org-enum": {"cancelled": 7}`,
		"datetime str":  `"x-schema-org-datetime": "time_start"`,
		"datetime type": `"x-schema-org-datetime": {"time": 5}`,
	} {
		t.Run(name, func(t *testing.T) {
			schema := `{"type":"object","x-schema-org-type":"Event","properties":{"status":{"type":"string","x-schema-org-property":"eventStatus",` + frag + `}}}`
			doc := eventDoc(t, schema, `{"status":"cancelled"}`)
			if doc["@type"] != "Event" || doc["name"] == nil {
				t.Errorf("document broken: %v", doc)
			}
		})
	}
	schema := `{"type":"object","x-schema-org-type":"Event","properties":{"status":{"type":"string","x-schema-org-property":"eventStatus","x-schema-org-enum":"oops"}}}`
	if v, has := eventDoc(t, schema, `{"status":"cancelled"}`)["eventStatus"]; has {
		t.Errorf("malformed enum printed %v", v)
	}
}

func TestAnnotationsAbsentUnchanged(t *testing.T) {
	schema := `{"type":"object","x-schema-org-type":"Event","properties":{
"status":{"type":"string","x-schema-org-property":"eventStatus"},
"date_start":{"type":"string","x-schema-org-property":"startDate"},
"time_start":{"type":"string"}}}`
	doc := eventDoc(t, schema, `{"status":"cancelled","date_start":"2026-12-05","time_start":"18:30"}`)
	if doc["eventStatus"] != "cancelled" || doc["startDate"] != "2026-12-05" {
		t.Errorf("output changed: %v", doc)
	}
	if _, has := doc["time_start"]; has {
		t.Error("time_start printed")
	}
}
