package webhook

import (
	"encoding/json"
	"time"
)

// Envelope is the JSON body of every webhook request. The field order is part
// of the receiver contract: id, event, timestamp, data.
type Envelope struct {
	ID        string `json:"id"`
	Event     string `json:"event"`
	Timestamp string `json:"timestamp"` // RFC3339 UTC, enqueue time
	Data      any    `json:"data"`
}

// NewEnvelope returns the body stored for a delivery and re-sent verbatim on
// every attempt.
func NewEnvelope(id, event string, now time.Time, data any) ([]byte, error) {
	return json.Marshal(Envelope{ID: id, Event: event, Timestamp: now.UTC().Format(time.RFC3339), Data: data})
}
