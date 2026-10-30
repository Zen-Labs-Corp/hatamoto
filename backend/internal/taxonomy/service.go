package taxonomy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/seo"
)

// Service is the application-layer facade for the taxonomy package. The
// HTTP handler layer talks to Service, not Repository, so we have one
// place to enforce validation and (in the future) emit listener events
// or invalidate caches.
type Service struct {
	repo   Repository
	logger *slog.Logger
	moved  MoveFunc
	// changed runs after any successful write (terms are part of rendered
	// pages: names, archive links, widgets).
	changed func(ctx context.Context)
}

// SetChangeObserver installs the callback run after a term was created,
// updated or deleted, or an entity's terms were replaced; nil disables it.
func (s *Service) SetChangeObserver(f func(ctx context.Context)) { s.changed = f }

func (s *Service) notifyChanged(ctx context.Context) {
	if s.changed != nil {
		s.changed(ctx)
	}
}

// PathMove is an archive path a term left and the one it has now.
type PathMove struct{ From, To string }

// MoveFunc is told which archive paths changed when an update renamed a term
// or gave it another parent (its descendants move with it). The server wires
// it to write redirects, like it does for entity moves.
type MoveFunc func(ctx context.Context, moves []PathMove)

// SetMoveObserver installs the callback run after a term moved; nil disables it.
func (s *Service) SetMoveObserver(f MoveFunc) { s.moved = f }

// treeStore is implemented by repositories that can list a term's subtree.
type treeStore interface {
	Descendants(ctx context.Context, id uuid.UUID) ([]*Term, error)
}

// subtree returns the term's descendants (nil without tree support).
func (s *Service) subtree(ctx context.Context, id uuid.UUID) ([]*Term, error) {
	ts, ok := s.repo.(treeStore)
	if !ok {
		return nil, nil
	}
	return ts.Descendants(ctx, id)
}

// archivePaths maps each term to its public archive path.
func (s *Service) archivePaths(ctx context.Context, terms ...*Term) (map[uuid.UUID]string, error) {
	if err := FillPaths(ctx, s.repo, terms...); err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]string, len(terms))
	for _, t := range terms {
		out[t.ID] = ArchivePrefix(t.Kind) + t.Path
	}
	return out, nil
}

// height is how many levels lie below id within desc.
func height(id uuid.UUID, desc []*Term) int {
	h := 0
	for _, d := range desc {
		depth, cur := 1, d
		for cur.ParentID != nil && *cur.ParentID != id && depth <= MaxDepth+1 {
			var next *Term
			for _, p := range desc {
				if p.ID == *cur.ParentID {
					next = p
					break
				}
			}
			if next == nil {
				break
			}
			cur, depth = next, depth+1
		}
		h = max(h, depth)
	}
	return h
}

// NewService wires a service around the supplied repository.
func NewService(repo Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, logger: logger}
}

// CreateTermInput is the create payload. Slug is optional — when empty,
// the service derives it from name via Slugify.
type CreateTermInput struct {
	Kind        string
	Name        string
	Slug        string
	Description string
	ParentID    *uuid.UUID
}

// MaxDescriptionLen caps a term description.
const MaxDescriptionLen = 2000

// Caps of the per-term SEO values, the same as the entry form's.
const (
	maxSEOTitleLen       = 255
	maxSEODescriptionLen = 500
	maxSEOURLLen         = 2048
)

// The SEOFieldError.Field values.
const (
	SEOFieldTitle       = "title"
	SEOFieldDescription = "description"
	SEOFieldCanonical   = "canonical"
	SEOFieldOGImage     = "og_image"
)

// SEOFieldError says which SEO value was refused. It matches ErrInvalidSEO.
type SEOFieldError struct{ Field string }

func (e *SEOFieldError) Error() string { return "invalid_seo: " + e.Field }
func (e *SEOFieldError) Unwrap() error { return ErrInvalidSEO }

// validateTermSEO trims and checks the values. A canonical must be an
// absolute http(s) URL or a root-relative path, unless it equals the stored
// value. All empty returns nil.
func validateTermSEO(in TermSEO, stored string) (*TermSEO, error) {
	in.MetaTitle = strings.TrimSpace(in.MetaTitle)
	in.MetaDescription = strings.TrimSpace(in.MetaDescription)
	in.CanonicalURL = strings.TrimSpace(in.CanonicalURL)
	in.OGImage = strings.TrimSpace(in.OGImage)
	for _, f := range []struct {
		field string
		value string
		max   int
	}{{SEOFieldTitle, in.MetaTitle, maxSEOTitleLen}, {SEOFieldDescription, in.MetaDescription, maxSEODescriptionLen},
		{SEOFieldCanonical, in.CanonicalURL, maxSEOURLLen}, {SEOFieldOGImage, in.OGImage, maxSEOURLLen}} {
		if utf8.RuneCountInString(f.value) > f.max {
			return nil, &SEOFieldError{Field: f.field}
		}
	}
	if c := in.CanonicalURL; !seo.CanonicalOK(c, stored) {
		return nil, &SEOFieldError{Field: SEOFieldCanonical}
	}
	if in.IsZero() {
		return nil, nil
	}
	return &in, nil
}

// checkParent verifies a parent term: it exists, has the same kind, and
// neither makes a cycle (self is the term being updated, uuid.Nil for a new
// one) nor nests deeper than MaxDepth.
func (s *Service) checkParent(ctx context.Context, kind string, self, parent uuid.UUID) error {
	if self != uuid.Nil && self == parent {
		return ErrTermParentInvalid
	}
	chain := s.chainOf(ctx, parent)
	if len(chain) == 0 {
		return ErrTermParentNotFound
	}
	if chain[0].Kind != kind {
		return fmt.Errorf("%w: parent is a %s, not a %s", ErrTermParentInvalid, chain[0].Kind, kind)
	}
	for _, t := range chain {
		if self != uuid.Nil && t.ID == self {
			return fmt.Errorf("%w: cycle", ErrTermParentInvalid)
		}
	}
	if len(chain)+1 > MaxDepth {
		return fmt.Errorf("%w: nesting deeper than %d", ErrTermParentInvalid, MaxDepth)
	}
	return nil
}

// chainOf returns parent's chain (root first), nil when it does not exist.
func (s *Service) chainOf(ctx context.Context, id uuid.UUID) []*Term {
	m, err := s.repo.Chains(ctx, []uuid.UUID{id})
	if err != nil {
		return nil
	}
	return m[id]
}

// CreateTerm validates input and persists a new term. Returns
// ErrTermSlugConflict on (kind, slug) collision.
func (s *Service) CreateTerm(ctx context.Context, in CreateTermInput) (*Term, error) {
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	in.Name = strings.TrimSpace(in.Name)
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	if !ValidKind(in.Kind) {
		return nil, ErrInvalidKind
	}
	if in.Name == "" || len(in.Name) > MaxNameLen {
		return nil, ErrInvalidName
	}
	if in.Slug == "" {
		in.Slug = Slugify(in.Name)
	}
	if !ValidSlug(in.Slug) {
		return nil, ErrInvalidSlug
	}
	in.Description = strings.TrimSpace(in.Description)
	if len(in.Description) > MaxDescriptionLen {
		return nil, ErrInvalidDescription
	}
	if in.ParentID != nil {
		if err := s.checkParent(ctx, in.Kind, uuid.Nil, *in.ParentID); err != nil {
			return nil, err
		}
	}
	t := &Term{Kind: in.Kind, Name: in.Name, Slug: in.Slug, Description: in.Description, ParentID: in.ParentID}
	if err := s.repo.CreateTerm(ctx, t); err != nil {
		return nil, err
	}
	s.notifyChanged(ctx)
	return t, nil
}

// UpdateTermInput is the update payload. Kind is immutable — see
// PostgresRepository.UpdateTerm rationale.
type UpdateTermInput struct {
	ID   uuid.UUID
	Name string
	Slug string
	// Description replaces the stored one when non-nil.
	Description *string
	// ParentID moves the term under a parent; ClearParent makes it a root.
	// Both unset leaves the parent alone.
	ParentID    *uuid.UUID
	ClearParent bool
	// SEO replaces the stored SEO values when non-nil; a pointer to the
	// zero value clears them. Nil keeps what is stored.
	SEO *TermSEO
}

// UpdateTerm patches name + slug. Empty slug auto-slugifies from name.
func (s *Service) UpdateTerm(ctx context.Context, in UpdateTermInput) (*Term, error) {
	if in.ID == uuid.Nil {
		return nil, fmt.Errorf("missing_id")
	}
	existing, err := s.repo.GetTerm(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	if in.Name == "" {
		in.Name = existing.Name
	}
	if in.Slug == "" {
		in.Slug = Slugify(in.Name)
	}
	if !ValidSlug(in.Slug) {
		return nil, ErrInvalidSlug
	}
	if len(in.Name) > MaxNameLen {
		return nil, ErrInvalidName
	}
	patched := &Term{ID: in.ID, Kind: existing.Kind, Name: in.Name, Slug: in.Slug,
		Description: existing.Description, ParentID: existing.ParentID, SEO: existing.SEO}
	if in.SEO != nil {
		var stored string
		if existing.SEO != nil {
			stored = existing.SEO.CanonicalURL
		}
		validated, err := validateTermSEO(*in.SEO, stored)
		if err != nil {
			return nil, err
		}
		patched.SEO = validated
	}
	if in.Description != nil {
		patched.Description = strings.TrimSpace(*in.Description)
		if len(patched.Description) > MaxDescriptionLen {
			return nil, ErrInvalidDescription
		}
	}
	desc, err := s.subtree(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	switch {
	case in.ClearParent:
		patched.ParentID = nil
	case in.ParentID != nil:
		if err := s.checkParent(ctx, existing.Kind, in.ID, *in.ParentID); err != nil {
			return nil, err
		}
		// The whole subtree moves with the term: it must still fit.
		if len(s.chainOf(ctx, *in.ParentID))+1+height(in.ID, desc) > MaxDepth {
			return nil, fmt.Errorf("%w: nesting deeper than %d", ErrTermParentInvalid, MaxDepth)
		}
		patched.ParentID = in.ParentID
	}
	moves := s.moved != nil && (existing.Slug != patched.Slug || !sameParent(existing.ParentID, patched.ParentID))
	var before map[uuid.UUID]string
	if moves {
		before, err = s.archivePaths(ctx, append([]*Term{existing}, desc...)...)
		if err != nil {
			s.logger.Warn("term move: paths before the update", "term", in.ID.String(), "error", err)
			moves = false
		}
	}
	if err := s.repo.UpdateTerm(ctx, patched); err != nil {
		return nil, err
	}
	if moves {
		s.reportMoves(ctx, in.ID, before)
	}
	s.notifyChanged(ctx)
	// Re-read so caller gets the canonical updated_at the DB stamped.
	return s.repo.GetTerm(ctx, in.ID)
}

func sameParent(a, b *uuid.UUID) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// reportMoves tells the observer which archive paths the update changed: the
// term and everything below it, compared with before.
func (s *Service) reportMoves(ctx context.Context, id uuid.UUID, before map[uuid.UUID]string) {
	self, err := s.repo.GetTerm(ctx, id)
	if err != nil {
		return
	}
	desc, err := s.subtree(ctx, id)
	if err != nil {
		return
	}
	after, err := s.archivePaths(ctx, append([]*Term{self}, desc...)...)
	if err != nil {
		s.logger.Warn("term move: paths after the update", "term", id.String(), "error", err)
		return
	}
	var moves []PathMove
	for tid, old := range before {
		if now, ok := after[tid]; ok && now != old {
			moves = append(moves, PathMove{From: old, To: now})
		}
	}
	if len(moves) > 0 {
		s.moved(ctx, moves)
	}
}

// DeleteTerm hard-deletes. Idempotent.
func (s *Service) DeleteTerm(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return errors.New("missing_id")
	}
	if err := s.repo.DeleteTerm(ctx, id); err != nil {
		return err
	}
	s.notifyChanged(ctx)
	return nil
}

// GetTerm pass-through.
func (s *Service) GetTerm(ctx context.Context, id uuid.UUID) (*Term, error) {
	return s.repo.GetTerm(ctx, id)
}

// GetTermBySlug pass-through.
func (s *Service) GetTermBySlug(ctx context.Context, kind, slug string) (*Term, error) {
	if !ValidKind(kind) {
		return nil, ErrInvalidKind
	}
	return s.repo.GetTermBySlug(ctx, kind, slug)
}

// ListTerms pass-through with input normalisation.
func (s *Service) ListTerms(ctx context.Context, kind, search string, page, perPage int) (*TermListResult, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "" && !ValidKind(kind) {
		return nil, ErrInvalidKind
	}
	return s.repo.ListTerms(ctx, kind, strings.TrimSpace(search), page, perPage)
}

// ListTermsForEntity pass-through.
func (s *Service) ListTermsForEntity(ctx context.Context, entityID uuid.UUID) ([]*Term, error) {
	return s.repo.ListTermsForEntity(ctx, entityID)
}

// ReplaceEntityTerms validates the term IDs (all must exist) then calls
// the repository. We pre-fetch to give the admin a clear error rather
// than a generic FK-violation surface.
func (s *Service) ReplaceEntityTerms(ctx context.Context, entityID uuid.UUID, termIDs []uuid.UUID) error {
	if entityID == uuid.Nil {
		return errors.New("missing_entity_id")
	}
	if len(termIDs) > MaxTermsPerEntity {
		return fmt.Errorf("too_many_terms")
	}
	if len(termIDs) > 0 {
		found, err := s.repo.ListTermsByIDs(ctx, termIDs)
		if err != nil {
			return err
		}
		// Build a set of supplied IDs and check every one resolved.
		want := make(map[uuid.UUID]struct{}, len(termIDs))
		for _, id := range termIDs {
			if id != uuid.Nil {
				want[id] = struct{}{}
			}
		}
		got := make(map[uuid.UUID]struct{}, len(found))
		for _, t := range found {
			got[t.ID] = struct{}{}
		}
		for id := range want {
			if _, ok := got[id]; !ok {
				return fmt.Errorf("unknown_term:%s", id)
			}
		}
	}
	if err := s.repo.ReplaceEntityTerms(ctx, entityID, termIDs); err != nil {
		return err
	}
	s.notifyChanged(ctx)
	return nil
}

// ListEntitiesForTerm proxies to the repository, paginating and assembling
// the wrapped TermArchiveResult for the public endpoint.
func (s *Service) ListEntitiesForTerm(ctx context.Context, kind, slug string, page, perPage int) (*TermArchiveResult, error) {
	term, err := s.GetTermBySlug(ctx, kind, slug)
	if err != nil {
		return nil, err
	}
	items, total, err := s.repo.ListEntitiesForTerm(ctx, term.ID, page, perPage)
	if err != nil {
		return nil, err
	}
	if perPage <= 0 || perPage > 100 {
		perPage = 20
	}
	if page < 1 {
		page = 1
	}
	totalPages := (total + perPage - 1) / perPage
	return &TermArchiveResult{
		Term:    term,
		Items:   items,
		Total:   total,
		Page:    page,
		PerPage: perPage,
		HasNext: page < totalPages,
		HasPrev: page > 1,
	}, nil
}
