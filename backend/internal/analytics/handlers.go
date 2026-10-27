package analytics

import (
	"encoding/json"
	"io"
	"net/http"

	"hatamoto.dev/core/internal/agents"
)

// maxTrackEvents caps one /track batch. A page sends exactly one page_view, so
// a request can never count for more than one.
const maxTrackEvents = 1

// EventTypePageView is the only client-reported event type /track counts.
const EventTypePageView = "page_view"

// Handlers wires the public analytics route.
type Handlers struct {
	svc *Service
}

// NewHandlers constructs the analytics handlers.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

type trackRequest struct {
	Events []struct {
		Type string `json:"type"`
	} `json:"events"`
}

// Track handles POST /api/v1/track. It counts a page_view (one per request)
// into the daily rollup's buffer and ignores every other type; nothing about the visitor is stored.
// Known crawlers are dropped (they are recorded by the agent capture instead).
func (h *Handlers) Track(w http.ResponseWriter, r *http.Request) {
	if agents.Default().Match(r.UserAgent()) != nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var body trackRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid_payload"}`, http.StatusBadRequest)
		return
	}
	if len(body.Events) > maxTrackEvents {
		body.Events = body.Events[:maxTrackEvents]
	}
	for _, ev := range body.Events {
		if ev.Type == EventTypePageView {
			h.svc.RecordPageViews(1)
		}
	}
	w.WriteHeader(http.StatusAccepted)
}
