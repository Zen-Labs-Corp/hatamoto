package entity

import (
	"encoding/json/jsontext"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func eventSchemaWith(status, dateStart, extra string) jsontext.Value {
	return jsontext.Value(`{"type":"object","x-schema-org-type":"Event","properties":{` +
		`"status":` + status + `,` +
		`"date_start":` + dateStart + `,` +
		`"time_start":{"type":"string"}` + extra + `}}`)
}

const (
	okStatus = `{"type":"string","enum":["scheduled","cancelled"],"x-schema-org-property":"eventStatus","x-schema-org-enum":{"scheduled":"EventScheduled","cancelled":"https://schema.org/EventCancelled"}}`
	okDate   = `{"type":"string","format":"date","x-schema-org-property":"startDate","x-schema-org-datetime":{"time":"time_start"}}`
)

func TestCheckSchemaAnnotations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		schema   jsontext.Value
		wantErr  []string
		wantWarn []string
	}{
		{name: "valid", schema: eventSchemaWith(okStatus, okDate, "")},
		{name: "key outside enum",
			schema:  eventSchemaWith(strings.Replace(okStatus, `"cancelled":"https`, `"canceled":"https`, 1), okDate, ""),
			wantErr: []string{`x-schema-org-enum on "status": "canceled" is not one of the enum values`}},
		{name: "bad target",
			schema:  eventSchemaWith(strings.Replace(okStatus, `"EventScheduled"`, `"EventGone/.."`, 1), okDate, ""),
			wantErr: []string{`x-schema-org-enum on "status": "EventGone/.." is not a schema.org URL or term`}},
		{name: "missing time property",
			schema:  eventSchemaWith(okStatus, strings.Replace(okDate, "time_start", "time_begin", 1), ""),
			wantErr: []string{`x-schema-org-datetime on "date_start": "time_begin" is not a property`}},
		{name: "datetime on date-time format",
			schema:  eventSchemaWith(okStatus, strings.Replace(okDate, `"format":"date"`, `"format":"date-time"`, 1), ""),
			wantErr: []string{`x-schema-org-datetime on "date_start": the property is not a string with format date`}},
		{name: "enum without property",
			schema:  eventSchemaWith(strings.Replace(okStatus, `"x-schema-org-property":"eventStatus",`, "", 1), okDate, ""),
			wantErr: []string{`x-schema-org-enum on "status": the property has no x-schema-org-property`}},
		{name: "datetime without property",
			schema:  eventSchemaWith(okStatus, strings.Replace(okDate, `"x-schema-org-property":"startDate",`, "", 1), ""),
			wantErr: []string{`x-schema-org-datetime on "date_start": the property has no x-schema-org-property`}},
		{name: "no enum",
			schema:  eventSchemaWith(strings.Replace(okStatus, `"enum":["scheduled","cancelled"],`, "", 1), okDate, ""),
			wantErr: []string{`x-schema-org-enum on "status": the property has no enum`}},
		{name: "both",
			schema: eventSchemaWith(okStatus, strings.Replace(okDate, `"x-schema-org-datetime"`, `"x-schema-org-enum":{"2026-01-01":"EventScheduled"},"x-schema-org-datetime"`, 1), ""),
			wantErr: []string{
				`x-schema-org-enum on "date_start": the property has no enum`,
				`"date_start": x-schema-org-enum and x-schema-org-datetime cannot be combined`,
			}},
		{name: "eventStatus outside the five",
			schema:   eventSchemaWith(strings.Replace(okStatus, `"EventScheduled"`, `"https://schema.org/EventGone"`, 1), okDate, ""),
			wantWarn: []string{`x-schema-org-enum on "status": "https://schema.org/EventGone" is not an eventStatus value`}},
		{name: "time property prints on its own",
			schema:   jsontext.Value(`{"x-schema-org-type":"Event","properties":{"date_start":` + okDate + `,"time_start":{"type":"string","x-schema-org-property":"doorTime"}}}`),
			wantWarn: []string{`x-schema-org-datetime on "date_start": "time_start" also prints on its own as "doorTime"`}},
		{name: "time property not a string",
			schema:  jsontext.Value(`{"x-schema-org-type":"Event","properties":{"date_start":` + okDate + `,"time_start":{"type":"number"}}}`),
			wantErr: []string{`x-schema-org-datetime on "date_start": "time_start" is not a string`}},
		{name: "nested path",
			schema:  jsontext.Value(`{"x-schema-org-type":"Event","properties":{"details":{"type":"object","properties":{"status":{"type":"string","x-schema-org-property":"eventStatus","x-schema-org-enum":{"x":"EventScheduled"}}}}}}`),
			wantErr: []string{`x-schema-org-enum on "details.status": the property has no enum`}},
		{name: "array path",
			schema:  jsontext.Value(`{"x-schema-org-type":"Event","properties":{"sessions":{"type":"array","items":{"type":"object","properties":{"status":{"type":"string","enum":["a"],"x-schema-org-property":"eventStatus","x-schema-org-enum":{"a":"Nope/"}}}}}}}`),
			wantErr: []string{`x-schema-org-enum on "sessions[].status": "Nope/" is not a schema.org URL or term`}},
		{name: "no root type",
			schema: jsontext.Value(`{"properties":{"status":{"x-schema-org-enum":{"a":"b/"}}}}`)},
		{name: "invalid json", schema: jsontext.Value(`{nope`)},
		{name: "malformed enum shape",
			schema:  jsontext.Value(`{"x-schema-org-type":"Event","properties":{"status":{"type":"string","enum":["a"],"x-schema-org-property":"eventStatus","x-schema-org-enum":"x"}}}`),
			wantErr: []string{`x-schema-org-enum on "status": must map enum values to schema.org URLs or terms`}},
		{name: "malformed datetime shape",
			schema:  jsontext.Value(`{"x-schema-org-type":"Event","properties":{"d":{"type":"string","format":"date","x-schema-org-property":"startDate","x-schema-org-datetime":"time_start"}}}`),
			wantErr: []string{`x-schema-org-datetime on "d": must be {"time": "<property>"}`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CheckSchemaAnnotations(tc.schema)
			if !slices.Equal(got.Errors, tc.wantErr) {
				t.Errorf("errors:\n got %q\nwant %q", got.Errors, tc.wantErr)
			}
			if !slices.Equal(got.Warnings, tc.wantWarn) {
				t.Errorf("warnings:\n got %q\nwant %q", got.Warnings, tc.wantWarn)
			}
		})
	}
	if got := CheckSchemaAnnotations(eventSchemaWith(okStatus, okDate, "")); got.RootType != "Event" {
		t.Errorf("RootType = %q", got.RootType)
	}
}

func TestCheckSchemaAnnotationsDeterministic(t *testing.T) {
	t.Parallel()
	schema := jsontext.Value(`{"x-schema-org-type":"Event","properties":{
"a":{"type":"string","enum":["x"],"x-schema-org-property":"p","x-schema-org-enum":{"b1":"1/","b2":"2/","b3":"3/","b4":"4/"}},
"b":{"type":"string","x-schema-org-enum":{"c":"d/","e":"f/"}},
"c":{"type":"string","x-schema-org-datetime":{"time":"zz"}}}}`)
	first := CheckSchemaAnnotations(schema)
	if len(first.Errors) < 8 {
		t.Fatalf("expected many findings, got %q", first.Errors)
	}
	for range 20 {
		if got := CheckSchemaAnnotations(schema); !reflect.DeepEqual(got, first) {
			t.Fatalf("output changed:\n%v\n%v", got, first)
		}
	}
}
