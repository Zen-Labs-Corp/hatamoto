package admin

import (
	"context"
	"net/url"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/middleware"
)

// verificationReader is the optional read of how crawler verification went.
// agents.StatsRepository has it; stores without it show no banner.
type verificationReader interface {
	VerificationHealth(ctx context.Context, rg agents.Range, agentIDs []string) (agents.VerificationHealth, error)
}

// verifyBanner is the verification callout at the top of the Crawlers page:
// Lead, the link, then Tail.
type verifyBanner struct {
	// Kind is "danger" or "success".
	Kind     string
	Title    string
	Lead     string
	Href     string
	LinkText string
	Tail     string
}

// clientIPSentence says where client addresses come from.
func clientIPSentence(src middleware.ClientIPSource) string {
	if src.CDN {
		return "Client IPs are read from " + src.Header + " behind your trusted proxy."
	}
	if src.ProxiesConfigured {
		return "Client IPs are read from " + src.Header + " behind your trusted proxies."
	}
	return "Client IPs are read from the connection, or " + src.Header + " from a loopback proxy."
}

// ipsText counts addresses: "1 IP", "380 IPs", "3 cloud IPs".
func ipsText(n int64, cloud bool) string {
	noun := "IP"
	if cloud {
		noun = "cloud IP"
	}
	if n != 1 {
		noun += "s"
	}
	return fmtNum(n) + " " + noun
}

// verifyBannerFor builds the banner for the health numbers, or nil when there
// are too few decided fetches to say anything.
func verifyBannerFor(h agents.VerificationHealth, src middleware.ClientIPSource, href string) *verifyBanner {
	level := agents.ClassifyHealth(h)
	if level == agents.HealthUnknown {
		return nil
	}
	decided := h.Decided()
	b := &verifyBanner{Href: href, LinkText: "see them"}
	if level == agents.HealthOK {
		b.Kind, b.Title = "success", "Verification is healthy."
		b.Lead = clientIPSentence(src) + " " + fixed(ratio(h.Verified, decided), 1) + " of verifiable fetches matched"
		if h.Spoofed == 0 {
			b.Lead += "."
			b.Href, b.LinkText = "", ""
			return b
		}
		if h.SpoofedIPs == 0 {
			// The rollup outlives the raw hits: there is no IP list to link to.
			b.Lead += "; " + fmtNum(h.Spoofed) + " spoofed fetches."
			b.Href, b.LinkText = "", ""
			return b
		}
		b.Lead += "; " + fmtNum(h.Spoofed) + " spoofed fetches came from " + ipsText(h.SpoofedIPs, h.SpoofedIPsInCDN == h.SpoofedIPs) + " ("
		b.Tail = ")."
		return b
	}
	b.Kind, b.Title = "danger", "Verification looks misconfigured."
	b.Lead = fixed(ratio(h.Spoofed, decided), 0) + " of fetches from agents with published IP ranges failed. " +
		fmtNum(h.Spoofed) + " spoofed fetches"
	if h.SpoofedIPs == 0 {
		b.Href, b.LinkText = "", ""
		b.Tail = ". "
	} else {
		b.Lead += " from " + ipsText(h.SpoofedIPs, false) + " ("
		b.Tail = "). "
	}
	b.Tail += clientIPSentence(src)
	switch {
	case src.CDN && h.SpoofedIPsInCDN*2 > h.SpoofedIPs:
		b.Tail += " Fetches recorded before the setting stay counted until they leave the selected range."
	case h.SpoofedIPs > 0 && h.SpoofedIPsInCDN*2 > h.SpoofedIPs:
		b.Tail += " Most of these IPs belong to Cloudflare: set HATAMOTO_TRUSTED_CDN=cloudflare."
	default:
		b.Tail += " Set HATAMOTO_TRUSTED_PROXIES (or the CDN preset) to the proxies in front of this site."
	}
	return b
}

// verifyBanner reads the health of verification over rg; a failed read or a
// store without the reader shows no banner.
func (a *Admin) verifyBanner(ctx context.Context, q url.Values, rg agents.Range) *verifyBanner {
	stats, ok := a.agentStats()
	if !ok {
		return nil
	}
	r, ok := stats.(verificationReader)
	if !ok {
		return nil
	}
	h, err := r.VerificationHealth(ctx, rg, agents.RangePublishedIDs(a.registry()))
	if err != nil {
		a.logger().Error("verification health failed", "err", err)
		return nil
	}
	return verifyBannerFor(h, middleware.ClientIPInfo(), sectionHref(a.adminPath+"/analytics/health#w-h-ver", q))
}
