package changeset

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// Risk is how closely a person must look at an item before it publishes.
type Risk string

// The risk classes (ADR 0004 section 5).
const (
	RiskLow      Risk = "low"
	RiskHigh     Risk = "high"
	RiskCritical Risk = "critical"
)

// RiskReason says why an item is high risk; empty for a low one.
type RiskReason string

// The reasons an item is high risk.
const (
	ReasonLinkHost    RiskReason = "link_host"
	ReasonLinkQuery   RiskReason = "link_query"
	ReasonEmbed       RiskReason = "embed"
	ReasonHiddenChar  RiskReason = "hidden_char"
	ReasonRedirect    RiskReason = "redirect"
	ReasonSlug        RiskReason = "slug"
	ReasonDelete      RiskReason = "delete"
	ReasonMenuLink    RiskReason = "menu_link"
	ReasonUnparseable RiskReason = "unparseable"
)

// Live is what the engine knows of the object an item updates. A nil *Live
// passed to Classify means the item creates.
type Live struct {
	Slug       string
	ParentID   string
	Attributes json.RawMessage
}

// Env is the site context an item is judged in: the hosts the site already
// links to (lowercase), and the paths of the entity items staged in the same
// changeset (path to seq).
type Env struct {
	SiteHosts   map[string]bool
	StagedPaths map[string]int
}

// Verdict is the classification of one item. Ref is the seq of the staged page
// a redirect points to; non-zero only for a redirect that may be approved as a
// group with that page.
type Verdict struct {
	Risk   Risk
	Reason RiskReason
	Ref    int
}

// ClassifyProposal classes a singleton proposal (site settings, URL patterns,
// home page, menu location). All of them are critical.
func ClassifyProposal(kind string) Risk { return RiskCritical }

// Classify judges one staged item. It fails closed: a payload it cannot read
// is high risk.
func Classify(it Item, live *Live, env Env) Verdict {
	high := func(r RiskReason) Verdict { return Verdict{Risk: RiskHigh, Reason: r} }
	if it.Op == OpDelete {
		return high(ReasonDelete)
	}
	switch it.Kind {
	case KindRedirect:
		var p redirectPayload
		if json.Unmarshal(it.Payload, &p) != nil {
			return high(ReasonUnparseable)
		}
		return Verdict{Risk: RiskHigh, Reason: ReasonRedirect, Ref: env.StagedPaths[p.Target]}
	case KindEntity:
		return classifyEntity(it, live, env)
	case KindMenu:
		var p struct {
			Items []struct {
				URL *string `json:"url"`
			} `json:"items"`
		}
		if json.Unmarshal(it.Payload, &p) != nil {
			return high(ReasonUnparseable)
		}
		for _, mi := range p.Items {
			if mi.URL == nil {
				continue
			}
			scheme, host, _, _, ok := splitLink(*mi.URL)
			if !ok || runsCode(scheme) || (host != "" && !env.SiteHosts[host]) {
				return high(ReasonMenuLink)
			}
		}
	}
	return Verdict{Risk: RiskLow}
}

func classifyEntity(it Item, live *Live, env Env) Verdict {
	high := func(r RiskReason) Verdict { return Verdict{Risk: RiskHigh, Reason: r} }
	var p struct {
		Name       string          `json:"name"`
		Slug       string          `json:"slug"`
		ParentID   string          `json:"parent_id"`
		ParentKey  string          `json:"parent_key"`
		Attributes json.RawMessage `json:"attributes"`
	}
	if json.Unmarshal(it.Payload, &p) != nil {
		return high(ReasonUnparseable)
	}
	var attrs any
	if len(p.Attributes) > 0 && string(p.Attributes) != "null" {
		if !isJSONObject(p.Attributes) || json.Unmarshal(p.Attributes, &attrs) != nil {
			return high(ReasonUnparseable)
		}
	}
	var liveLinks linkSet
	if live != nil {
		if p.Slug != live.Slug || p.ParentID != live.ParentID && (p.ParentKey == "" || live.ParentID != "") {
			return high(ReasonSlug)
		}
		var la any
		if len(live.Attributes) > 0 && json.Unmarshal(live.Attributes, &la) == nil {
			liveLinks = scanStrings(la).links
		}
	}
	scan := scanStrings(attrs)
	if hasHidden(p.Name) {
		scan.hidden = true
	}
	if scan.embed {
		return high(ReasonEmbed)
	}
	if scan.hidden {
		return high(ReasonHiddenChar)
	}
	for _, l := range scan.links {
		if l.bad || (l.host != "" && !env.SiteHosts[l.host] && !liveLinks.hasHost(l.host)) {
			return high(ReasonLinkHost)
		}
	}
	for _, l := range scan.links {
		if known := liveLinks.queries(l); known != nil && !known[l.query] {
			return high(ReasonLinkQuery)
		}
	}
	return Verdict{Risk: RiskLow}
}

func isJSONObject(b []byte) bool {
	s := strings.TrimSpace(string(b))
	return s != "" && s[0] == '{'
}

// link is a URL found in content. bad marks a scheme that runs code or embeds
// data; host is lowercase, empty for a relative link.
type link struct {
	host, path, query string
	bad               bool
}

type linkSet []link

func (s linkSet) hasHost(h string) bool {
	for _, l := range s {
		if l.host == h {
			return true
		}
	}
	return false
}

// queries returns the queries live links with the same host and path carry,
// or nil when there is no such link.
func (s linkSet) queries(l link) map[string]bool {
	var out map[string]bool
	for _, o := range s {
		if o.host == l.host && o.path == l.path {
			if out == nil {
				out = map[string]bool{}
			}
			out[o.query] = true
		}
	}
	return out
}

type scanResult struct {
	links         linkSet
	embed, hidden bool
}

var (
	mdLink  = regexp.MustCompile(`\]\(\s*<?([^)\s>]+)`)
	embedRE = regexp.MustCompile(`(?i)<\s*(iframe|embed|object)[\s/>]`)
)

// scanStrings walks every string of a decoded JSON value.
func scanStrings(v any) scanResult {
	var r scanResult
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			r.scan(x)
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(v)
	return r
}

func (r *scanResult) scan(s string) {
	if hasHidden(s) {
		r.hidden = true
	}
	if t := strings.TrimSpace(s); strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") || strings.HasPrefix(t, "//") {
		r.addLink(t)
	}
	for _, m := range mdLink.FindAllStringSubmatch(s, -1) {
		r.addLink(m[1])
	}
	if !strings.Contains(s, "<") {
		return
	}
	if embedRE.MatchString(s) {
		r.embed = true
	}
	z := html.NewTokenizer(strings.NewReader(s))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return
		case html.StartTagToken, html.SelfClosingTagToken:
			name, more := z.TagName()
			switch string(name) {
			case "iframe", "embed", "object":
				r.embed = true
			}
			for more {
				var k, v []byte
				k, v, more = z.TagAttr()
				if key := string(k); key == "href" || key == "src" || key == "action" || key == "data" {
					r.addLink(string(v))
				}
			}
		}
	}
}

func (r *scanResult) addLink(raw string) {
	scheme, host, path, query, ok := splitLink(raw)
	switch {
	case !ok:
		r.links = append(r.links, link{bad: true})
	case runsCode(scheme):
		r.links = append(r.links, link{bad: true})
	case scheme == "" || scheme == "http" || scheme == "https":
		r.links = append(r.links, link{host: host, path: path, query: query})
	}
}

// splitLink parses a link. scheme is set (lowercase) only for a scheme other
// than http and https; host is lowercase, empty for a relative link.
func splitLink(raw string) (scheme, host, path, query string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", "", "", false
	}
	scheme = strings.ToLower(u.Scheme)
	if scheme == "http" || scheme == "https" {
		scheme = ""
	}
	return scheme, strings.ToLower(u.Hostname()), u.Path, u.RawQuery, true
}

// runsCode reports a link scheme that runs code or embeds data.
func runsCode(scheme string) bool {
	return scheme == "javascript" || scheme == "data" || scheme == "vbscript"
}

// hasHidden reports zero-width and bidirectional control characters.
func hasHidden(s string) bool {
	for _, r := range s {
		switch {
		case r >= 0x200B && r <= 0x200D, r == 0x2060, r == 0xFEFF,
			r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
			return true
		}
	}
	return false
}
