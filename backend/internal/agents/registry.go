// Package agents is the single source of truth for known AI crawlers and
// assistants: who they are, what they are for, and (in later phases) how to
// verify them. The registry is embedded JSON so the frontend generator and the
// Go side read the same file.
package agents

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

//go:embed registry.json
var registryJSON []byte

// Purpose classifies why an agent fetches pages.
type Purpose string

const (
	PurposeTraining    Purpose = "training"
	PurposeSearchIndex Purpose = "search_index"
	PurposeUserFetch   Purpose = "user_fetch"
	PurposeOtherAI     Purpose = "other_ai"
	PurposeSearch      Purpose = "search"
	PurposeSocial      Purpose = "social"
	PurposeSEO         Purpose = "seo"
	PurposeArchive     Purpose = "archive"
)

var validPurposes = map[Purpose]bool{
	PurposeTraining: true, PurposeSearchIndex: true, PurposeUserFetch: true, PurposeOtherAI: true,
	PurposeSearch: true, PurposeSocial: true, PurposeSEO: true, PurposeArchive: true,
}

// IsAI reports whether the purpose is one of the AI groups.
func (p Purpose) IsAI() bool {
	switch p {
	case PurposeTraining, PurposeSearchIndex, PurposeUserFetch, PurposeOtherAI:
		return true
	}
	return false
}

// Agent is one registry entry. Entries with an empty UA list are token-only
// (robots.txt tokens such as Google-Extended that never appear as a UA) and
// are never matched.
type Agent struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Operator      string   `json:"operator"`
	Purpose       Purpose  `json:"purpose"`
	UA            []string `json:"ua"`
	RobotsToken   string   `json:"robots_token"`
	IPRanges      []string `json:"ip_ranges"`
	RDNS          []string `json:"rdns"`
	IgnoresRobots bool     `json:"ignores_robots"`
}

// RangeSource is a published IP range list for one or more agents.
type RangeSource struct {
	URLs   []string `json:"urls"`
	Format string   `json:"format"`
}

// AIReferrer describes an AI assistant whose outbound links carry a referrer
// host or utm_source value.
type AIReferrer struct {
	Source string   `json:"source"`
	Hosts  []string `json:"hosts"`
	UTM    []string `json:"utm"`
}

// Registry is the parsed registry.json.
type Registry struct {
	Version        int                    `json:"version"`
	GenericUAHints []string               `json:"generic_ua_hints"`
	IPRangeSources map[string]RangeSource `json:"ip_range_sources"`
	AIReferrers    []AIReferrer           `json:"ai_referrers"`
	Agents         []Agent                `json:"agents"`

	byID map[string]*Agent
}

var idPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// Parse decodes and validates registry JSON.
func Parse(data []byte) (*Registry, error) {
	var r Registry
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("agents: parse registry: %w", err)
	}
	for _, ref := range r.AIReferrers {
		if ref.Source == "" {
			return nil, fmt.Errorf("agents: ai_referrer with empty source")
		}
		for _, v := range append(append([]string{}, ref.Hosts...), ref.UTM...) {
			if v == "" || v != strings.ToLower(v) {
				return nil, fmt.Errorf("agents: ai_referrer %s: %q must be non-empty lowercase", ref.Source, v)
			}
		}
	}
	r.byID = make(map[string]*Agent, len(r.Agents))
	for i := range r.Agents {
		a := &r.Agents[i]
		if !idPattern.MatchString(a.ID) {
			return nil, fmt.Errorf("agents: invalid id %q", a.ID)
		}
		if _, dup := r.byID[a.ID]; dup {
			return nil, fmt.Errorf("agents: duplicate id %q", a.ID)
		}
		if !validPurposes[a.Purpose] {
			return nil, fmt.Errorf("agents: %s: invalid purpose %q", a.ID, a.Purpose)
		}
		for _, p := range a.UA {
			if p == "" || p != strings.ToLower(p) {
				return nil, fmt.Errorf("agents: %s: ua pattern %q must be non-empty lowercase", a.ID, p)
			}
		}
		for _, s := range a.RDNS {
			if !strings.HasPrefix(s, ".") || s != strings.ToLower(s) {
				return nil, fmt.Errorf("agents: %s: rdns suffix %q must be lowercase and start with '.'", a.ID, s)
			}
		}
		for _, k := range a.IPRanges {
			if _, ok := r.IPRangeSources[k]; !ok {
				return nil, fmt.Errorf("agents: %s: unknown ip range key %q", a.ID, k)
			}
		}
		r.byID[a.ID] = a
	}
	return &r, nil
}

var (
	defaultOnce sync.Once
	defaultReg  *Registry
)

// Default returns the embedded registry, parsed once. It panics if the embedded
// file is invalid (a build-time bug caught by the tests).
func Default() *Registry {
	defaultOnce.Do(func() {
		r, err := Parse(registryJSON)
		if err != nil {
			panic(err)
		}
		defaultReg = r
	})
	return defaultReg
}

// Match returns the first agent (file order) with a UA pattern contained in ua,
// or nil. Token-only entries are never matched.
func (r *Registry) Match(ua string) *Agent {
	if ua == "" {
		return nil
	}
	l := strings.ToLower(ua)
	for i := range r.Agents {
		for _, p := range r.Agents[i].UA {
			if strings.Contains(l, p) {
				return &r.Agents[i]
			}
		}
	}
	return nil
}

// ByID returns the agent with the given id, or nil.
func (r *Registry) ByID(id string) *Agent { return r.byID[id] }

// LooksLikeBot is the broad "worth a closer look" test: a known agent or any
// generic hint substring.
func (r *Registry) LooksLikeBot(ua string) bool {
	if r.Match(ua) != nil {
		return true
	}
	l := strings.ToLower(ua)
	for _, h := range r.GenericUAHints {
		if strings.Contains(l, h) {
			return true
		}
	}
	return false
}

// UAHintRegexp is the case-insensitive union of every UA pattern and generic
// hint. The frontend BOT_UA_HINT is generated from the same data.
func (r *Registry) UAHintRegexp() *regexp.Regexp {
	seen := map[string]bool{}
	var parts []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			parts = append(parts, regexp.QuoteMeta(s))
		}
	}
	for _, h := range r.GenericUAHints {
		add(h)
	}
	for i := range r.Agents {
		for _, p := range r.Agents[i].UA {
			add(p)
		}
	}
	return regexp.MustCompile(`(?i)` + strings.Join(parts, "|"))
}
