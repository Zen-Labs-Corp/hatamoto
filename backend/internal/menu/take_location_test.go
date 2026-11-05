package menu

import (
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

// REST and plugin callers decode menu input from JSON; they must not be able
// to ask for the location take-over that only the admin and the import use.
func TestTakeLocationNotSettableFromJSON(t *testing.T) {
	var c CreateMenuInput
	if err := jsonx.Unmarshal([]byte(`{"name":"x","location":"header","TakeLocation":true,"take_location":true}`), &c); err != nil {
		t.Fatal(err)
	}
	var u UpdateMenuInput
	if err := jsonx.Unmarshal([]byte(`{"location":"header","TakeLocation":true,"take_location":true}`), &u); err != nil {
		t.Fatal(err)
	}
	if c.TakeLocation || u.TakeLocation {
		t.Fatal("TakeLocation must not come from JSON")
	}
}
