package analytics

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/middleware"
)

// maxTrackEvents caps one /track batch. A page sends exactly one page_view, so
// a request can never count for more than one.
const maxTrackEvents = 1

// EventTypePageView is the pageview event type.
const EventTypePageView = "page_view"

// eventTypeEngage is the engagement event of a v2 body. The legacy body has no
// "v" and feeds the old pageview counter alone.
const eventTypeEngage = "engage"

// Field limits of a sanitised beacon event.
const (
	maxRefLen    = 100
	maxUTMLen    = 64
	maxPathLen   = 200
	trackBodyMax = 32 * 1024
)

// Handlers wires the public analytics route.
type Handlers struct {
	svc *Service
	geo Geo
}

// NewHandlers constructs the analytics handlers.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// SetGeo enables the country dimension. The IP is resolved to a country and
// dropped; it is never stored. Call before serving.
func (h *Handlers) SetGeo(g Geo) { h.geo = g }

type trackEvent struct {
	Type  string `json:"type"`
	Kind  string `json:"kind"`
	Entry bool   `json:"entry"`
	First bool   `json:"first"`
	Paid  bool   `json:"paid"`
	Ref   string `json:"ref"`
	US    string `json:"utm_source"`
	UM    string `json:"utm_medium"`
	UC    string `json:"utm_campaign"`
	Path  string `json:"path"`
}

type trackRequest struct {
	V      int          `json:"v"`
	Events []trackEvent `json:"events"`
}

// Track handles POST /api/v1/track. It counts a page_view (one per request)
// into the daily pageview counter and, for a v2 body, the traffic rollup
// (visit, channel, source, campaign, landing page, device, country) and
// engagement. Nothing about the visitor is stored: no IP, user agent or
// referrer URL. Known crawlers, generic bot hints and empty user agents are
// dropped (crawlers are recorded by the agent capture instead).
func (h *Handlers) Track(w http.ResponseWriter, r *http.Request) {
	var body trackRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, trackBodyMax)).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid_payload"}`, http.StatusBadRequest)
		return
	}
	// Decoding comes first so a malformed body is a 400 for every client, bots included.
	if ua := r.UserAgent(); ua == "" || agents.Default().LooksLikeBot(ua) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if len(body.Events) > maxTrackEvents {
		body.Events = body.Events[:maxTrackEvents]
	}
	for _, ev := range body.Events {
		switch ev.Type {
		case EventTypePageView:
			h.svc.RecordPageViews(1)
			if body.V >= 2 {
				h.recordTraffic(r, ev, "")
			}
		case eventTypeEngage:
			if body.V >= 2 && h.svc.EngagementEnabled() && isEngageKind(ev.Kind) {
				h.recordTraffic(r, ev, ev.Kind)
			}
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

// recordTraffic sanitises a v2 event and buffers it. A referrer equal to the
// site's own host is internal navigation: no visit and no traffic row.
func (h *Handlers) recordTraffic(r *http.Request, ev trackEvent, eng string) {
	e := Event{
		Entry:       ev.Entry && eng == "",
		Eng:         eng,
		First:       ev.First,
		Ref:         refHost(cleanToken(ev.Ref, maxRefLen)),
		UTMSource:   cleanToken(ev.US, maxUTMLen),
		UTMMedium:   cleanToken(ev.UM, maxUTMLen),
		UTMCampaign: cleanToken(ev.UC, maxUTMLen),
		Path:        cleanPath(ev.Path),
		Paid:        ev.Paid,
		Device:      DeviceClass(r.UserAgent()),
	}
	channel, source := Classify(e, agents.Default(), r.Host)
	if channel == "" {
		return
	}
	if h.geo != nil {
		e.Country = h.geo.Country(middleware.GetClientIP(r))
	}
	h.svc.RecordTraffic(e, channel, source)
}

// cleanToken lowercases s, keeps [a-z0-9._~:/_-] and truncates
// it to max bytes.
func cleanToken(s string, max int) string {
	s = strings.ToLower(s)
	b := make([]byte, 0, min(len(s), max))
	for i := 0; i < len(s) && len(b) < max; i++ {
		if c := s[i]; c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("._~:/-_", c) >= 0 {
			b = append(b, c)
		}
	}
	return string(b)
}

// cleanPath keeps the path of a landing page: it must start with "/", and any
// query or fragment is cut off.
func cleanPath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if !strings.HasPrefix(p, "/") {
		return ""
	}
	return cleanToken(p, maxPathLen)
}

// refHost reduces a referrer a client sent to its host: it drops a scheme
// ("https://") and anything from the first "/", "?" or "#", then normalises.
func refHost(ref string) string {
	// A scheme only counts when "://" comes before any "/", "?" or "#".
	if i := strings.Index(ref, "://"); i >= 0 && !strings.ContainsAny(ref[:i], "/?#") {
		ref = ref[i+3:]
	}
	if i := strings.IndexAny(ref, "/?#"); i >= 0 {
		ref = ref[:i]
	}
	return NormalizeHost(ref)
}

// isEngageKind reports whether k is an engagement event kind the rollup counts.
func isEngageKind(k string) bool {
	switch k {
	case "multi", "scroll", "form_seen", "form_start":
		return true
	}
	return false
}
