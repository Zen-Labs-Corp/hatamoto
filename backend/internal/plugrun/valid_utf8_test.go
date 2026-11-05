package plugrun

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// Decision: a plugin document with invalid UTF-8 is refused up front as
// ErrInvalid. The database (UTF8 jsonb) would refuse it anyway, so the plugin
// gets the clear error instead of a failed query.
func TestValidJSONRefusesInvalidUTF8(t *testing.T) {
	for doc, want := range map[string]bool{
		"{\"a\":\"ok é\"}":     true,
		"{\"a\":1,\"a\":2}":    true,
		"{\"a\":\"bad \xff\"}": false,
		"[\"\xc3\x28\"]":       false,
		"{\"a\":":              false,
		"{\"a\":1} {\"b\":2}":  false,
	} {
		if got := validJSON([]byte(doc)); got != want {
			t.Errorf("validJSON(%q) = %v, want %v", doc, got, want)
		}
	}
}

func TestProgressRefusesInvalidUTF8(t *testing.T) {
	if accepted(t, func(s *Store) error { return s.Progress(context.Background(), uuid.UUID{}, []byte("{\"a\":\"\xff\"}")) }) {
		t.Fatal("invalid UTF-8 progress was accepted")
	}
}
