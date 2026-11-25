package server

import (
	"testing"

	"encoding/json/jsontext"

	"hatamoto.dev/core/internal/plugrun"
)

// The host stores progress with the lowercase keys of sdk.RunProgressArgs;
// runView must read them back (the decoder is case-sensitive).
func TestRunViewDecodesStoredProgress(t *testing.T) {
	v := runView(plugrun.Run{Progress: jsontext.Value(`{"done":37,"total":60,"message":"Imported 87 events"}`)})
	if v.Done != 37 || v.Total != 60 || v.Message != "Imported 87 events" {
		t.Fatalf("progress = %d/%d %q, want 37/60 \"Imported 87 events\"", v.Done, v.Total, v.Message)
	}
}
