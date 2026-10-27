package server

import (
	"encoding/json"
	"testing"
)

func TestValidateThemePayloadBuilderKey(t *testing.T) {
	for body, ok := range map[string]bool{
		`{"builder":{"tokens":{"space":{"4":"2rem"},"color":{"accent":"#112233"}},"presets":{"card":{"label":"Card","s":{"base":{"pt":"space-4"}}}}}}`: true,
		`{"builder":null}`:                                                     true,
		`{"colors":{"accent":"#fff"}}`:                                         true,
		`{"builder":{"tokens":{"space":{"4":"1px;}"}}}}`:                       false,
		`{"builder":{"tokens":{"color":{"nope":"#fff"}}}}`:                     false,
		`{"builder":{"presets":{"Bad Name":{"s":{"base":{"pt":"space-1"}}}}}}`: false,
		`{"builder":{"presets":{"card":{"s":{"base":{"bg":"url(x)"}}}}}}`:      false,
		`{"builder":"x"}`: false,
	} {
		var sent map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &sent); err != nil {
			t.Fatal(err)
		}
		if err := validateThemePayload(sent); (err == nil) != ok {
			t.Errorf("%s: err=%v, want ok=%v", body, err, ok)
		}
	}
}
