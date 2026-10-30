package agents

import (
	"context"
	"fmt"
	"sort"
)

// The Crawlers banner is red when more than healthSpoofedShare of the decided
// fetches from range-published agents were spoofed, once there are at least
// healthMinDecided of them (fewer say nothing either way).
const (
	healthMinDecided   = 20
	healthSpoofedShare = 0.20
)

// VerificationHealth sums how verification went for the agents that publish IP
// ranges, over a date range. Verified, Spoofed and Unverifiable come from the
// daily rollup (kept forever). SpoofedIPs counts distinct addresses of spoofed
// raw hits: raw hits live about three months and addresses older than 30 days
// are truncated to their /24 (/48), so it counts networks there and
// undercounts for ranges longer than the raw retention.
type VerificationHealth struct {
	Verified     int64
	Spoofed      int64
	Unverifiable int64
	SpoofedIPs   int64
	// SpoofedIPsInCDN is the part of SpoofedIPs inside the built-in Cloudflare
	// list, whether or not the CDN preset is on.
	SpoofedIPsInCDN int64
}

// Decided is the number of fetches the verifier could judge. Unverifiable ones
// are left out: they say nothing about misconfiguration.
func (h VerificationHealth) Decided() int64 { return h.Verified + h.Spoofed }

// HealthLevel is the banner colour.
type HealthLevel int

const (
	// HealthUnknown: too few decided fetches to judge, show no banner.
	HealthUnknown HealthLevel = iota
	HealthOK
	HealthBad
)

// ClassifyHealth applies the banner thresholds.
func ClassifyHealth(h VerificationHealth) HealthLevel {
	decided := h.Decided()
	if decided < healthMinDecided {
		return HealthUnknown
	}
	if float64(h.Spoofed)/float64(decided) > healthSpoofedShare {
		return HealthBad
	}
	return HealthOK
}

// RangePublishedIDs returns the ids of the registry agents that publish IP
// ranges (every purpose, search engines included), sorted.
func RangePublishedIDs(reg *Registry) []string {
	var ids []string
	for _, a := range reg.Agents {
		if len(a.IPRanges) > 0 {
			ids = append(ids, a.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// VerificationHealth reads the health numbers for agentIDs (use
// RangePublishedIDs) over rg. The raw-hit counts mirror Spoofed.
func (r *StatsRepository) VerificationHealth(ctx context.Context, rg Range, agentIDs []string) (VerificationHealth, error) {
	var h VerificationHealth
	if len(agentIDs) == 0 {
		return h, nil
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return h, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	if err := conn.QueryRow(ctx, `
		SELECT COALESCE(SUM(verified_hits),0), COALESCE(SUM(spoofed_hits),0), COALESCE(SUM(unverifiable_hits),0)
		FROM hatamoto_agent_daily
		WHERE day BETWEEN $1 AND $2 AND agent_id = ANY($3)`,
		rg.From, rg.To, agentIDs).Scan(&h.Verified, &h.Spoofed, &h.Unverifiable); err != nil {
		return h, fmt.Errorf("verification health totals: %w", err)
	}
	cdn := CloudflareFallback()
	cidrs := make([]string, len(cdn))
	for i, p := range cdn {
		cidrs[i] = p.String()
	}
	if err := conn.QueryRow(ctx, `
		SELECT count(DISTINCT ip), count(DISTINCT ip) FILTER (WHERE ip <<= ANY($4::cidr[]))
		FROM hatamoto_agent_hits
		WHERE verification = 'spoofed' AND occurred_at >= $1 AND occurred_at < $2 AND agent_id = ANY($3)`,
		rg.From, rg.endExclusive(), agentIDs, cidrs).Scan(&h.SpoofedIPs, &h.SpoofedIPsInCDN); err != nil {
		return h, fmt.Errorf("verification health addresses: %w", err)
	}
	return h, nil
}
