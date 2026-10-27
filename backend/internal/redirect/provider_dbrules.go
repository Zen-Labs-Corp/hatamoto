package redirect

import "context"

// LookupFunc finds a rule by exact source_path, or nil.
type LookupFunc func(ctx context.Context, path string) *Redirect

// DBRulesProvider serves the admin-managed rules in hatamoto_redirects.
//
// Matching is exact, then retried with the trailing slash toggled, then
// against wildcard rules (see pattern.go) when WithPatterns is set.
type DBRulesProvider struct {
	lookup  LookupFunc
	pattern LookupFunc
}

// NewDBRulesProvider wraps a lookup function as a chain provider.
func NewDBRulesProvider(lookup LookupFunc) *DBRulesProvider {
	return &DBRulesProvider{lookup: lookup}
}

// WithPatterns adds wildcard-rule matching, tried after the exact forms.
func (p *DBRulesProvider) WithPatterns(lookup LookupFunc) *DBRulesProvider {
	p.pattern = lookup
	return p
}

func (p *DBRulesProvider) Name() string  { return "DBRules" }
func (p *DBRulesProvider) Priority() int { return PriorityDBRules }

// Resolve returns the matching rule and which path form found it.
func (p *DBRulesProvider) Resolve(ctx context.Context, path string) (*Match, string) {
	if p.lookup == nil {
		return nil, ""
	}
	if rd := p.lookup(ctx, path); rd != nil {
		return p.toMatch(rd), "exact"
	}
	if alt := ToggleSlash(path); alt != path {
		if rd := p.lookup(ctx, alt); rd != nil {
			return p.toMatch(rd), "slash-toggled"
		}
	}
	if p.pattern != nil {
		if rd := p.pattern(ctx, path); rd != nil {
			return p.toMatch(rd), "pattern"
		}
	}
	return nil, ""
}

func (p *DBRulesProvider) toMatch(rd *Redirect) *Match {
	status := rd.StatusCode
	if !ValidStatusCode(status) {
		status = 301
	}
	return &Match{Target: rd.TargetURL, Status: status, Rule: rd.SourcePath}
}
