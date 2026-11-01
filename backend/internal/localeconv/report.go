package localeconv

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"slices"
	"strings"
)

// maxRedirectSample caps the listed redirect sources.
const maxRedirectSample = 10

func newReport() *Report {
	return &Report{
		Errors: []Issue{}, Warnings: []Issue{},
		Locales:           []LocaleReport{},
		Shared:            SharedReport{CountsBySchema: map[string]int{}},
		PathChanges:       PathChanges{Items: []PathChange{}},
		RenamedCollisions: []RenamedRow{},
		Groups:            GroupsReport{Conflicts: []GroupConflict{}},
		Overrides:         OverridesReport{SitemapGroupRemoved: []OverrideChange{}, KeptKeys: []string{}},
		Redirects:         RedirectsReport{ItemsSample: []string{}},
		UnmatchedValues:   []UnmatchedValue{},
	}
}

// assemble fills the parts of the report that need the planned rows: the
// schema override edit, the path check, redirects, the settings delta and the
// data fingerprint.
func (a *analysis) assemble() {
	after, changed, kept, err := StripSitemapGroup(a.snap.Schemas, a.plan.LocaleFrom.Attribute)
	if err != nil {
		a.errorf(CodePathChanges, "", "%v", err)
		return
	}
	a.rep.Overrides.SitemapGroupRemoved, a.rep.Overrides.KeptKeys = changed, kept
	if changed == nil {
		a.rep.Overrides.SitemapGroupRemoved = []OverrideChange{}
	}
	for i := range after {
		if !bytes.Equal(after[i].Settings, a.snap.Schemas[i].Settings) {
			a.ch.Schemas = append(a.ch.Schemas, SchemaChange{
				ID: after[i].ID, Slug: after[i].Slug, Before: a.snap.Schemas[i].Settings, After: after[i].Settings,
			})
		}
	}
	if a.plan.StripsLocaleAttribute() && stillMatchesAttribute(after, a.plan.LocaleFrom.Attribute) {
		a.errorf(CodeStripNeededAttribute, "", "strip_attributes removes %q, but a schema override still matches on it", a.plan.LocaleFrom.Attribute)
	}

	a.checkPaths(after)
	a.countRedirects()
	a.settingsDelta()
	a.rep.DataFingerprint = a.fingerprint()
}

// countRedirects lists redirect rules whose source sits at or under a locale segment.
func (a *analysis) countRedirects() {
	var hits []string
	for _, src := range a.snap.RedirectSources {
		for _, l := range a.plan.Locales {
			if src == "/"+l.Segment || strings.HasPrefix(src, "/"+l.Segment+"/") {
				hits = append(hits, src)
				break
			}
		}
	}
	slices.Sort(hits)
	hits = slices.Compact(hits)
	a.rep.Redirects.Count = len(hits)
	a.rep.Redirects.ItemsSample = append([]string{}, hits[:min(len(hits), maxRedirectSample)]...)
}

func (a *analysis) settingsDelta() {
	site := a.snap.Site
	a.rep.SiteSettings = SiteSettingsDelta{
		Language:      [2]string{site.Language, normalized(a.plan.Default)},
		LocalesCount:  [2]int{len(site.Locales), len(a.plan.Locales)},
		PrefixAll:     [2]bool{site.PrefixAll, a.plan.PrefixAllValue()},
		XDefault:      [2]string{site.XDefault, normalized(a.plan.XDefault)},
		SitemapNaming: [2]string{site.SitemapNaming, resolveNaming(a.plan, a.rep)},
	}
}

// fingerprint hashes what the conversion would touch: the rows it writes (id and
// updated_at), the schema settings it edits and the locale settings. It changes
// when an editor saves a touched row or one appears that the plan would convert.
func (a *analysis) fingerprint() string {
	h := sha256.New()
	rows := a.ch.Rows()
	fmt.Fprintf(h, "rows %d\n", len(rows))
	for _, p := range rows {
		fmt.Fprintf(h, "%s %d\n", p.Ent.ID, p.Ent.UpdatedAt.UnixMicro())
	}
	for _, s := range a.ch.Schemas {
		fmt.Fprintf(h, "schema %s %s\n", s.ID, s.Before)
	}
	site, _ := json.Marshal(a.snap.Site)
	fmt.Fprintf(h, "site %s\n", site)
	return sum(h)
}

// PlanSHA256 is the hash a report carries for its plan: the SHA-256 of the
// plan's JSON form, "" when the plan cannot be encoded.
func PlanSHA256(p *Plan) string {
	raw, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	h := sha256.New()
	h.Write(raw)
	return sum(h)
}

func sum(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }

// finish sorts the issues, so reports are stable, and sets OK.
func (a *analysis) finish() (*Changes, *Report) {
	sortIssues(a.rep.Errors)
	sortIssues(a.rep.Warnings)
	a.rep.PlanSHA256 = PlanSHA256(a.plan)
	a.rep.OK = len(a.rep.Errors) == 0
	return a.ch, a.rep
}

func sortIssues(list []Issue) {
	slices.SortStableFunc(list, func(x, y Issue) int {
		if c := strings.Compare(x.Code, y.Code); c != 0 {
			return c
		}
		if c := strings.Compare(x.EntityID, y.EntityID); c != 0 {
			return c
		}
		return strings.Compare(x.Message, y.Message)
	})
}
