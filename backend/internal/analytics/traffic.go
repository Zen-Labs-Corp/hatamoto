package analytics

import "time"

// Dim is one dimension of the traffic rollup (hatamoto_traffic_daily.dim).
type Dim string

// Traffic rollup dimensions.
const (
	DimChannel  Dim = "channel"
	DimSource   Dim = "source"
	DimCampaign Dim = "campaign"
	DimLanding  Dim = "landing"
	DimDevice   Dim = "device"
	DimCountry  Dim = "country"
)

// Marketing channels a visit is attributed to.
const (
	ChannelAI       = "ai_assistants"
	ChannelOrganic  = "organic"
	ChannelPaid     = "paid"
	ChannelEmail    = "email"
	ChannelSocial   = "social"
	ChannelReferral = "referral"
	ChannelDirect   = "direct"
)

// Placeholder keys: a source-less visit, a capped-out key, an unknown country.
const (
	SourceNone     = "(none)"
	KeyOther       = "(other)"
	CountryUnknown = "(unknown)"
)

// Event is one sanitised beacon event, already stripped of anything that could
// identify a visitor: referrer host only, no URL, no IP, no user agent.
type Event struct {
	Entry bool   // the first pageview of a visit
	Eng   string // "", "multi", "scroll", "form_seen" or "form_start": an engagement event, not a pageview
	First bool   // the visit's first engagement event
	Ref   string // referrer host
	UTMSource,
	UTMMedium,
	UTMCampaign string
	Path    string
	Paid    bool // gclid/msclkid seen on the landing URL
	Device  string
	Country string
}

// Counts are the counters of one rollup row.
type Counts struct {
	Visits, Pageviews, Engaged, Multi, Scrolled int64
	// FormSeen and FormStarted are the funnel steps "saw a form" and "started it".
	FormSeen, FormStarted int64
}

func (c *Counts) add(o Counts) {
	c.Visits += o.Visits
	c.Pageviews += o.Pageviews
	c.Engaged += o.Engaged
	c.Multi += o.Multi
	c.Scrolled += o.Scrolled
	c.FormSeen += o.FormSeen
	c.FormStarted += o.FormStarted
}

// TrafficRow is one (day, dim, k1, k2) row of the traffic rollup.
type TrafficRow struct {
	Day    time.Time
	Dim    Dim
	K1, K2 string
	Counts
}

// TrafficDay is the counters of one key on one day (charts and sparks).
type TrafficDay struct {
	Day string
	K1  string
	Counts
}

// TrafficTop is the counters of one key (K2 is the channel of a source or the
// medium of a campaign) summed over a range.
type TrafficTop struct {
	K1, K2 string
	Counts
}

// Geo resolves an IP address to an ISO 3166-1 alpha-2 country, "" if unknown.
// *GeoDB implements it.
type Geo interface{ Country(ip string) string }
