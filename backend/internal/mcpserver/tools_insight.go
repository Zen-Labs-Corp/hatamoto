package mcpserver

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/analytics"
	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/middleware"
)

const (
	reportScope = hmauth.ScopeReadReports
	dayLayout   = "2006-01-02"
	maxRange    = 366 * 24 * time.Hour
	// maxPreview caps the text a preview returns to the agent.
	maxPreview = 64 * 1024
)

var subSitemap = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// dateRange reads from/to (YYYY-MM-DD, inclusive, UTC); the default is the
// last 30 days.
func dateRange(from, to string) (agents.Range, error) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	rg := agents.Range{From: today.AddDate(0, 0, -29), To: today}
	parse := func(name, v string, dst *time.Time) error {
		if v == "" {
			return nil
		}
		t, err := time.Parse(dayLayout, v)
		if err != nil {
			return userErr("%s must be YYYY-MM-DD", name)
		}
		*dst = t
		return nil
	}
	if err := parse("from", from, &rg.From); err != nil {
		return rg, err
	}
	if err := parse("to", to, &rg.To); err != nil {
		return rg, err
	}
	if to != "" && from == "" {
		rg.From = rg.To.AddDate(0, 0, -29)
	}
	if rg.From.After(rg.To) {
		return rg, userErr("from must not be after to")
	}
	if rg.To.Sub(rg.From) > maxRange {
		return rg, userErr("range must not exceed 366 days")
	}
	return rg, nil
}

func (s *Server) registerInsight() {
	type rangeIn struct {
		From string `json:"from,omitempty" jsonschema:"YYYY-MM-DD, inclusive (default: 29 days before to)"`
		To   string `json:"to,omitempty" jsonschema:"YYYY-MM-DD, inclusive (default: today, UTC)"`
	}

	type hitsIn struct {
		rangeIn
		EntityID string `json:"entity_id,omitempty" jsonschema:"also report this one page: last fetch per AI agent, first crawl, user fetches"`
		Limit    int    `json:"limit,omitempty" jsonschema:"pages to return (default 20, max 100)"`
	}
	register(s, toolSpec{name: "analytics_crawler_hits", description: "AI-crawler fetches of the site: per page and per agent over a date range (served responses only for pages). With entity_id, also that page's per-agent last fetch and first crawl.", scope: reportScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in hitsIn) (any, error) {
			rg, err := dateRange(in.From, in.To)
			if err != nil {
				return nil, err
			}
			pages, totalPages, err := s.d.Crawlers.TopPages(ctx, rg, agents.Page{Limit: clampLimit(in.Limit, 20)})
			if err != nil {
				return nil, err
			}
			byAgent, err := s.d.Crawlers.Agents(ctx, rg)
			if err != nil {
				return nil, err
			}
			out := object{"from": rg.From.Format(dayLayout), "to": rg.To.Format(dayLayout), "pages": pages, "total_pages": totalPages, "agents": byAgent}
			if in.EntityID != "" {
				id, err := parseID("entity_id", in.EntityID)
				if err != nil {
					return nil, err
				}
				act, err := s.pageActivity(ctx, id)
				if err != nil {
					return nil, err
				}
				out["page"] = act
			}
			return out, nil
		})

	type firstIn struct {
		rangeIn
		Limit int `json:"limit,omitempty" jsonschema:"rows (default 20, max 100)"`
	}
	register(s, toolSpec{name: "analytics_first_crawls", description: "Pages whose first AI crawl happened in the range: which agent, and how long after publishing.", scope: reportScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in firstIn) (any, error) {
			rg, err := dateRange(in.From, in.To)
			if err != nil {
				return nil, err
			}
			rows, total, err := s.d.Crawlers.FirstCrawls(ctx, rg, agents.Page{Limit: clampLimit(in.Limit, 20)})
			if err != nil {
				return nil, err
			}
			return object{"items": rows, "total": total}, nil
		})

	type googleIn struct {
		rangeIn
		Filter string `json:"filter,omitempty" jsonschema:"page rows to return: all (default), not-crawled, errors or stale"`
		Limit  int    `json:"limit,omitempty" jsonschema:"page rows (default 20, max 100)"`
	}
	register(s, toolSpec{name: "analytics_google", description: "Googlebot crawling over a date range: crawls, verified share, pages crawled of published, median publish to first crawl, and per-page rows (crawls, last crawl day, last status, stale). Spoofed fetches are left out of the page numbers.", scope: reportScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in googleIn) (any, error) {
			rg, err := dateRange(in.From, in.To)
			if err != nil {
				return nil, err
			}
			return googleSummary(ctx, s.d.Crawlers, rg, in.Filter, clampLimit(in.Limit, 20))
		})

	register(s, toolSpec{name: "analytics_ai_referrals", description: "Visits sent by AI assistants (ChatGPT, Perplexity, ...) per day. Counted per assistant and day only: the site keeps no per-page or per-visitor referral data.", scope: reportScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in rangeIn) (any, error) {
			rg, err := dateRange(in.From, in.To)
			if err != nil {
				return nil, err
			}
			days, err := s.d.Referrals.AIReferrals(ctx, rg.From, rg.To)
			if err != nil {
				return nil, err
			}
			var total int64
			for _, d := range days {
				total += d.Visits
			}
			return object{"days": days, "total_visits": total}, nil
		})

	type trafficIn struct {
		rangeIn
		By    string `json:"by,omitempty" jsonschema:"grouping: channel (default), source, campaign, page, device or country"`
		Limit int    `json:"limit,omitempty" jsonschema:"rows (default 20, max 100)"`
	}
	register(s, toolSpec{name: "analytics_traffic", description: "Human visits to the public site over a date range, grouped by channel, source, campaign, landing page, device or country: visits, pageviews and engaged share. Counts only; the site keeps no per-visitor, IP or user agent data. Bots are not counted here (see analytics_crawler_hits).", scope: reportScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in trafficIn) (any, error) {
			rg, err := dateRange(in.From, in.To)
			if err != nil {
				return nil, err
			}
			return trafficSummary(ctx, s.d.Traffic, rg, in.By, clampLimit(in.Limit, 20))
		})

	type conversionsIn struct {
		rangeIn
		Touch string `json:"touch,omitempty" jsonschema:"first (default) or last touch of the visitor's tab"`
		By    string `json:"by,omitempty" jsonschema:"grouping: channel (default), source, campaign, page or form"`
		Limit int    `json:"limit,omitempty" jsonschema:"rows (default 20, max 100)"`
	}
	register(s, toolSpec{name: "analytics_conversions", description: "Form submissions (leads) over a date range, grouped by channel, source, campaign, landing page or form, by the first or last touch of the submitter's tab. Counts only: no submitter emails, field values, IP or user agent. Spam is not counted; submissions stored before attribution was recorded count as channel unknown.", scope: reportScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in conversionsIn) (any, error) {
			rg, err := dateRange(in.From, in.To)
			if err != nil {
				return nil, err
			}
			return conversionsSummary(ctx, s.d.Conversions, rg, in.Touch, in.By, clampLimit(in.Limit, 20))
		})

	register(s, toolSpec{name: "analytics_crawler_verification", description: "How well AI-crawler IP verification works over a date range: verified, spoofed and unverifiable fetches from agents that publish IP ranges, the spoofed share, distinct spoofed addresses, and the header client IPs are read from. level is ok, bad (misconfigured proxy or CDN) or unknown (too few fetches to judge).", scope: reportScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in rangeIn) (any, error) {
			rg, err := dateRange(in.From, in.To)
			if err != nil {
				return nil, err
			}
			return crawlerVerification(ctx, s.d.Crawlers, rg, middleware.ClientIPInfo())
		})

	type sitemapIn struct {
		Sub string `json:"sub,omitempty" jsonschema:"child sitemap name (e.g. posts); empty = the sitemap index"`
	}
	register(s, toolSpec{name: "sitemap_preview", description: "The sitemap XML as crawlers get it now (index, or one child sitemap).", scope: readScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in sitemapIn) (any, error) {
			path := "/sitemap.xml"
			if in.Sub != "" {
				if !subSitemap.MatchString(in.Sub) {
					return nil, userErr("sub may only hold lowercase letters, digits, - and _")
				}
				path = "/sitemap-" + in.Sub + ".xml"
			}
			return s.preview(ctx, path, func(body string) object {
				return object{"urls": strings.Count(body, "<url>"), "sitemaps": strings.Count(body, "<sitemap>")}
			})
		})
	register(s, toolSpec{name: "llms_txt_preview", description: "The /llms.txt manifest as AI crawlers get it now.", scope: readScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, _ struct{}) (any, error) {
			return s.preview(ctx, "/llms.txt", nil)
		})

	type ldIn struct {
		EntityID string `json:"entity_id"`
	}
	register(s, toolSpec{name: "jsonld_validate", description: "Render a page's JSON-LD (the live page when published, otherwise generated from the draft) and check it: valid JSON, @context, @type, and the fields Google expects per type.", scope: readScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in ldIn) (any, error) {
			id, err := parseID("entity_id", in.EntityID)
			if err != nil {
				return nil, err
			}
			e, err := s.d.Entities.GetEntity(ctx, id)
			if err != nil {
				return nil, err
			}
			blocks, source, err := s.pageJSONLD(ctx, e)
			if err != nil {
				return nil, err
			}
			rep := ValidateJSONLD(blocks)
			return object{"entity_id": id, "source": source, "url": s.d.PageURL(ctx, e), "report": rep}, nil
		})
}

// pageActivity is one page's AI activity (the editor's AI Agents card).
func (s *Server) pageActivity(ctx context.Context, id uuid.UUID) (any, error) {
	var ids []string
	for _, a := range agents.Default().Agents {
		if a.Purpose.IsAI() && len(a.UA) > 0 {
			ids = append(ids, a.ID)
		}
	}
	d, err := s.d.Crawlers.EntityActivity(ctx, id, ids, time.Now().UTC().Truncate(24*time.Hour))
	if err != nil {
		return nil, err
	}
	if !d.Exists {
		return nil, userErr("entity not found")
	}
	type agentRow struct {
		AgentID     string     `json:"agent_id"`
		LastFetchAt *time.Time `json:"last_fetch_at"`
		LastStatus  *int       `json:"last_status"`
	}
	rows := []agentRow{}
	for _, aid := range ids {
		if lf, ok := d.Last[aid]; ok {
			at := lf.At
			rows = append(rows, agentRow{aid, &at, lf.Status})
		}
	}
	searchFirst, err := s.d.Crawlers.SearchFirstCrawl(ctx, id)
	if err != nil {
		return nil, err
	}
	return object{"first_crawl_at": d.FirstAt, "search_first_crawl_at": searchFirst, "published_at": d.PublishedAt, "user_fetch_7d": d.UserFetch7d, "user_fetch_30d": d.UserFetch30d, "agents_seen": rows}, nil
}

// preview fetches a public path in process and returns its (capped) text.
func (s *Server) preview(ctx context.Context, path string, stats func(body string) object) (any, error) {
	resp, err := s.d.Fetch(ctx, path)
	if err != nil {
		return nil, err
	}
	body := string(resp.Body)
	out := object{"path": path, "status": resp.Status, "bytes": len(resp.Body)}
	if stats != nil {
		for k, v := range stats(body) {
			out[k] = v
		}
	}
	if len(body) > maxPreview {
		body, out["truncated"] = strings.ToValidUTF8(body[:maxPreview], ""), true
	}
	out["body"] = body
	return out, nil
}

var ldScript = regexp.MustCompile(`(?s)<script[^>]*type="application/ld\+json"[^>]*>(.*?)</script>`)

// ExtractJSONLD returns the contents of every JSON-LD script block in html.
func ExtractJSONLD(html []byte) [][]byte {
	var out [][]byte
	for _, m := range ldScript.FindAllSubmatch(html, -1) {
		out = append(out, m[1])
	}
	return out
}

// pageJSONLD returns the JSON-LD blocks of the entity's page. A published
// entity is read from its rendered page; any other is generated the same way
// the page loader does.
func (s *Server) pageJSONLD(ctx context.Context, e *entity.Entity) ([][]byte, string, error) {
	if e.Status == entity.StatusPublished {
		resp, err := s.d.Fetch(ctx, s.d.PageURL(ctx, e))
		if err != nil {
			return nil, "", err
		}
		if resp.Status == 200 {
			return ExtractJSONLD(resp.Body), "rendered page", nil
		}
	}
	doc, err := entity.GenerateJSONLDWithFAQ(e, "", nil)
	if err != nil {
		return nil, "", err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, "", err
	}
	return [][]byte{raw}, "generated from the entity (not published, or page unavailable)", nil
}

// verificationReader is the part of agents.StatsRepository the verification
// tool needs.
type verificationReader interface {
	VerificationHealth(ctx context.Context, rg agents.Range, agentIDs []string) (agents.VerificationHealth, error)
}

var healthLevelNames = map[agents.HealthLevel]string{
	agents.HealthUnknown: "unknown",
	agents.HealthOK:      "ok",
	agents.HealthBad:     "bad",
}

// crawlerVerification builds the analytics_crawler_verification result.
func crawlerVerification(ctx context.Context, r verificationReader, rg agents.Range, src middleware.ClientIPSource) (object, error) {
	h, err := r.VerificationHealth(ctx, rg, agents.RangePublishedIDs(agents.Default()))
	if err != nil {
		return nil, err
	}
	decided := h.Decided()
	var share float64
	if decided > 0 {
		share = float64(h.Spoofed) / float64(decided)
	}
	return object{
		"from": rg.From.Format(dayLayout), "to": rg.To.Format(dayLayout),
		"level":         healthLevelNames[agents.ClassifyHealth(h)],
		"verified":      h.Verified,
		"spoofed":       h.Spoofed,
		"unverifiable":  h.Unverifiable,
		"decided":       decided,
		"spoofed_share": share,
		"spoofed_ips":   h.SpoofedIPs,
		"header":        src.Header,
	}, nil
}

// googleReader is the part of the agents stats the analytics_google tool reads.
type googleReader interface {
	GoogleKPIs(ctx context.Context, rg agents.Range) (agents.GoogleKPIs, error)
	GooglePages(ctx context.Context, rg agents.Range, filter string, limit, offset int) ([]agents.GooglePageRow, int, error)
}

// googleSummary builds the analytics_google result.
func googleSummary(ctx context.Context, r googleReader, rg agents.Range, filter string, limit int) (object, error) {
	switch filter {
	case "":
		filter = agents.GoogleFilterAll
	case agents.GoogleFilterAll, agents.GoogleFilterNotCrawled, agents.GoogleFilterErrors, agents.GoogleFilterStale:
	default:
		return nil, userErr("filter must be all, not-crawled, errors or stale")
	}
	k, err := r.GoogleKPIs(ctx, rg)
	if err != nil {
		return nil, err
	}
	rows, total, err := r.GooglePages(ctx, rg, filter, limit, 0)
	if err != nil {
		return nil, err
	}
	var share float64
	if k.Hits > 0 {
		share = float64(k.Verified) / float64(k.Hits)
	}
	var median *float64
	if k.MedianFirstCrawl != nil {
		secs := k.MedianFirstCrawl.Seconds()
		median = &secs
	}
	type pageRow struct {
		EntityID   *uuid.UUID `json:"entity_id"`
		Path       string     `json:"path"`
		Name       string     `json:"name"`
		Crawls     int64      `json:"crawls"`
		LastDay    *string    `json:"last_crawl_day"`
		LastStatus int        `json:"last_status"`
		Stale      bool       `json:"stale"`
	}
	items := make([]pageRow, 0, len(rows))
	for _, p := range rows {
		it := pageRow{p.EntityID, p.Path, p.Name, p.Crawls, nil, p.LastStatus, p.Stale}
		if p.LastDay != nil {
			d := p.LastDay.Format(dayLayout)
			it.LastDay = &d
		}
		items = append(items, it)
	}
	return object{
		"from": rg.From.Format(dayLayout), "to": rg.To.Format(dayLayout), "filter": filter,
		"crawls": k.Hits, "verified": k.Verified, "verified_share": share,
		"pages_crawled": k.PagesCrawled, "pages_published": k.Published,
		"median_first_crawl_seconds": median,
		"items":                      items, "total": total,
	}, nil
}

// trafficReader is the part of analytics.Reader the analytics_traffic tool reads.
type trafficReader interface {
	TrafficTop(ctx context.Context, dim analytics.Dim, from, to time.Time, limit int) ([]analytics.TrafficTop, error)
}

// trafficDims maps the tool's "by" values to rollup dimensions.
var trafficDims = map[string]analytics.Dim{
	"channel":  analytics.DimChannel,
	"source":   analytics.DimSource,
	"campaign": analytics.DimCampaign,
	"page":     analytics.DimLanding,
	"device":   analytics.DimDevice,
	"country":  analytics.DimCountry,
}

// trafficSummary builds the analytics_traffic result.
func trafficSummary(ctx context.Context, r trafficReader, rg agents.Range, by string, limit int) (object, error) {
	if by == "" {
		by = "channel"
	}
	dim, ok := trafficDims[by]
	if !ok {
		return nil, userErr("by must be channel, source, campaign, page, device or country")
	}
	top, err := r.TrafficTop(ctx, dim, rg.From, rg.To, limit)
	if err != nil {
		return nil, err
	}
	type row struct {
		Key        string  `json:"key"`
		Detail     string  `json:"detail,omitempty"`
		Visits     int64   `json:"visits"`
		Pageviews  int64   `json:"pageviews"`
		EngagedPct float64 `json:"engaged_pct"`
	}
	rows := make([]row, 0, len(top))
	for _, t := range top {
		var pct float64
		if t.Visits > 0 {
			pct = math.Round(float64(t.Engaged)/float64(t.Visits)*1000) / 10
		}
		rows = append(rows, row{Key: t.K1, Detail: t.K2, Visits: t.Visits, Pageviews: t.Pageviews, EngagedPct: pct})
	}
	return object{"by": by, "from": rg.From.Format(dayLayout), "to": rg.To.Format(dayLayout), "rows": rows}, nil
}

// conversionsReader is the part of analytics.Reader the analytics_conversions
// tool reads. It has no method that returns a submitter's email.
type conversionsReader interface {
	LeadsTop(ctx context.Context, dim analytics.Dim, touch analytics.TouchKind, from, to time.Time, limit int) ([]analytics.LeadRow, error)
	LeadsTotals(ctx context.Context, touch analytics.TouchKind, from, to time.Time) (total, unknown int64, err error)
	LeadsByForm(ctx context.Context, from, to time.Time, limit int) ([]analytics.LeadForm, error)
}

// conversionDims maps the tool's "by" values to lead dimensions.
var conversionDims = map[string]analytics.Dim{
	"channel":  analytics.DimChannel,
	"source":   analytics.DimSource,
	"campaign": analytics.DimCampaign,
	"page":     analytics.DimLanding,
}

// conversionsSummary builds the analytics_conversions result.
func conversionsSummary(ctx context.Context, r conversionsReader, rg agents.Range, touch, by string, limit int) (object, error) {
	if touch == "" {
		touch = string(analytics.TouchFirst)
	}
	if touch != string(analytics.TouchFirst) && touch != string(analytics.TouchLast) {
		return nil, userErr("touch must be first or last")
	}
	if by == "" {
		by = "channel"
	}
	tk := analytics.ParseTouch(touch)
	type row struct {
		Key    string `json:"key"`
		Detail string `json:"detail,omitempty"`
		Leads  int64  `json:"leads"`
	}
	var rows []row
	if by == "form" {
		forms, err := r.LeadsByForm(ctx, rg.From, rg.To, limit)
		if err != nil {
			return nil, err
		}
		for _, f := range forms {
			rows = append(rows, row{Key: f.Name, Leads: f.Leads})
		}
	} else {
		dim, ok := conversionDims[by]
		if !ok {
			return nil, userErr("by must be channel, source, campaign, page or form")
		}
		top, err := r.LeadsTop(ctx, dim, tk, rg.From, rg.To, limit)
		if err != nil {
			return nil, err
		}
		for _, t := range top {
			rows = append(rows, row{Key: t.K1, Detail: t.K2, Leads: t.Leads})
		}
	}
	if rows == nil {
		rows = []row{}
	}
	total, unknown, err := r.LeadsTotals(ctx, tk, rg.From, rg.To)
	if err != nil {
		return nil, err
	}
	return object{"touch": touch, "by": by, "from": rg.From.Format(dayLayout), "to": rg.To.Format(dayLayout),
		"total_leads": total, "unattributed_leads": unknown, "rows": rows}, nil
}
