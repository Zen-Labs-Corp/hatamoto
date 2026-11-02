package server

import (
	"context"
	"sort"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/seo"
	"hatamoto.dev/core/internal/taxonomy"
)

// crumbTerm is a term attached to an entry, as the breadcrumb trail sees it.
type crumbTerm struct {
	ID   string
	Kind string
	Name string
	Href string
}

// crumbInput is everything shapeCrumbs needs to build the trail of one entry.
type crumbInput struct {
	Home, Entry BreadcrumbItem
	Archive     *BreadcrumbItem
	// Title replaces the entry's crumb label when set (attributes.breadcrumb_title).
	Title string
	Term  *BreadcrumbItem
	Cfg   seo.Breadcrumbs
}

// shapeCrumbs returns Home > archive > term > entry with the saved label and
// title applied. With zero settings and no title it is the plain trail. The
// list is never empty.
func shapeCrumbs(in crumbInput) []BreadcrumbItem {
	home := in.Home
	home.Label = in.Cfg.Home()
	out := []BreadcrumbItem{home}
	if in.Archive != nil {
		out = append(out, *in.Archive)
	}
	if in.Term != nil {
		out = append(out, *in.Term)
	}
	entry := in.Entry
	if t := strings.TrimSpace(in.Title); t != "" {
		entry.Label = t
	}
	return append(out, entry)
}

// primaryTerm picks the term level of the trail: the entry's picked term when
// it is still attached, else the first attached category by name, else none.
func primaryTerm(pick string, terms []crumbTerm) *BreadcrumbItem {
	if pick != "" {
		for _, t := range terms {
			if t.ID == pick {
				return &BreadcrumbItem{Label: t.Name, Href: t.Href}
			}
		}
	}
	var best *crumbTerm
	for i := range terms {
		t := &terms[i]
		if t.Kind == "category" && (best == nil || t.Name < best.Name) {
			best = t
		}
	}
	if best == nil {
		return nil
	}
	return &BreadcrumbItem{Label: best.Name, Href: best.Href}
}

// crumbTerms loads the entry's terms with their archive URLs. Only called when
// the primary term is switched on.
func (s *Server) crumbTerms(ctx context.Context, id uuid.UUID) []crumbTerm {
	repo := taxonomy.NewPostgresRepository(s.db)
	terms, err := repo.ListTermsForEntity(ctx, id)
	if err != nil {
		s.logger.Warn("breadcrumbs: entity terms", "error", err, "entity", id)
		return nil
	}
	if err := taxonomy.FillPaths(ctx, repo, terms...); err != nil {
		s.logger.Warn("breadcrumbs: term paths", "error", err, "entity", id)
		return nil
	}
	out := make([]crumbTerm, 0, len(terms))
	for _, t := range terms {
		path := firstNonEmpty(t.Path, t.Slug)
		out = append(out, crumbTerm{ID: t.ID.String(), Kind: t.Kind, Name: t.Name, Href: taxonomy.ArchivePrefix(t.Kind) + path})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
