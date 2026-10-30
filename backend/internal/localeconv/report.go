package localeconv

import (
	"slices"
	"strings"
)

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

// finish sorts the issues, so reports are stable, and sets OK.
func (a *analysis) finish() (*Changes, *Report) {
	sortIssues(a.rep.Errors)
	sortIssues(a.rep.Warnings)
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
