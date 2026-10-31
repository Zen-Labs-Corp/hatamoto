package analytics

import (
	"encoding/json"

	"hatamoto.dev/core/internal/agents"
)

// maxAttributionRaw caps the hm_attr form field a browser sends.
const maxAttributionRaw = 1024

// Touch is one classified arrival: the channel and source the P3 classifier
// gives it, plus the campaign, medium and landing path the visitor came with.
type Touch struct {
	Channel, Source, Medium, Campaign, Landing string
}

// Attribution is the first and last touch of a tab at the time a form was
// submitted. A nil touch is unknown.
type Attribution struct {
	First, Last *Touch
}

// rawTouch is the wire shape of one touch in hm_attr: the raw inputs the
// browser keeps (referrer host only, never the URL), classified server side.
type rawTouch struct {
	Host     string `json:"h"`
	Source   string `json:"s"`
	Medium   string `json:"m"`
	Campaign string `json:"c"`
	Path     string `json:"p"`
	Paid     bool   `json:"g"`
}

type rawAttribution struct {
	First *rawTouch `json:"f"`
	Last  *rawTouch `json:"l"`
}

// ParseAttribution reads the hm_attr value of a form post and classifies each
// touch with Classify, so a lead's channel matches the Traffic tab. The client
// never supplies a channel. Empty, oversize or malformed input returns the zero
// Attribution and false; it is never an error, a submit must not fail for it.
// The raw value is not logged.
func ParseAttribution(raw string, reg *agents.Registry, selfHost string) (Attribution, bool) {
	if raw == "" || len(raw) > maxAttributionRaw {
		return Attribution{}, false
	}
	var in rawAttribution
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return Attribution{}, false
	}
	out := Attribution{First: classifyTouch(in.First, reg, selfHost), Last: classifyTouch(in.Last, reg, selfHost)}
	return out, out.First != nil || out.Last != nil
}

// classifyTouch sanitises and classifies one touch. A referrer equal to the
// site's own host is not an arrival from elsewhere: it counts as direct.
func classifyTouch(t *rawTouch, reg *agents.Registry, selfHost string) *Touch {
	if t == nil {
		return nil
	}
	ev := Event{
		Ref:         refHost(cleanToken(t.Host, maxRefLen)),
		UTMSource:   cleanToken(t.Source, maxUTMLen),
		UTMMedium:   cleanToken(t.Medium, maxUTMLen),
		UTMCampaign: cleanToken(t.Campaign, maxUTMLen),
		Path:        cleanPath(t.Path),
		Paid:        t.Paid,
	}
	channel, source := Classify(ev, reg, selfHost)
	if channel == "" {
		channel, source = ChannelDirect, SourceNone
	}
	return &Touch{Channel: channel, Source: source, Medium: ev.UTMMedium, Campaign: ev.UTMCampaign, Landing: ev.Path}
}
