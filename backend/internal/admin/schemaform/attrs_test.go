package schemaform

import (
	"encoding/json/jsontext"
	"reflect"
	"testing"
)

func TestAttrs(t *testing.T) {
	t.Parallel()
	raw := jsontext.Value(`{"type":"object","properties":{
		"start_date":{"type":"string","format":"date","title":"Starts"},
		"capacity":{"type":"integer"},
		"price":{"type":"number"},
		"status":{"type":"string","enum":["scheduled","cancelled"]},
		"venue":{"type":"string"},
		"body":{"type":"string","x-hm-field":"richtext"},
		"cover":{"type":"string","x-hm-field":"image"},
		"site":{"type":"string","format":"uri"},
		"tags":{"type":"array","items":{"type":"string"}},
		"meta":{"type":"object"},
		"starts_at":{"type":"string","format":"date-time"},
		"secret":{"type":"string","writeOnly":true},
		"a.b":{"type":"string"},
		"flag":{"type":"boolean"}}}`)
	want := []Attr{
		{Key: "start_date", Label: "Starts", Type: "date"},
		{Key: "capacity", Label: "Capacity", Type: "number"},
		{Key: "price", Label: "Price", Type: "number"},
		{Key: "status", Label: "Status", Type: "enum", Enum: []string{"scheduled", "cancelled"}},
		{Key: "venue", Label: "Venue", Type: "text"},
	}
	if got := Attrs(raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("Attrs = %+v, want %+v", got, want)
	}
	if got := Attrs(jsontext.Value(`not json`)); got != nil {
		t.Fatalf("bad schema = %+v, want nil", got)
	}
}
