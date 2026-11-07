package entity

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// formUsageTTL is the backstop for a stale index; invalidation hooks clear it
// sooner on every entity change.
const formUsageTTL = 5 * time.Minute

// Placement kinds and the way a form is placed.
const (
	PlacementPage = "page"
	PlacementArea = "area"

	ViaWidget    = "widget"
	ViaShortcode = "shortcode"
	ViaSection   = "section"
)

// FormPlacement is one place a form is used. For an area (a theme part or a
// block) Name is the slot or block name and Slug is empty: an area has no path.
type FormPlacement struct {
	EntityID   uuid.UUID
	Name, Slug string
	SchemaSlug string
	Status     string
	Area       string
	Kind       string // PlacementPage | PlacementArea
	Via        string // ViaWidget | ViaShortcode | ViaSection
}

// FormUsageIndex caches form slug -> placements, built by one bounded scan.
// It is for admin pages only, never the render path.
type FormUsageIndex struct {
	load func(ctx context.Context) (map[string][]FormPlacement, error)

	mu     sync.Mutex
	at     time.Time
	byslug map[string][]FormPlacement
}

// NewFormUsageIndex returns an index fed by svc's scan.
func NewFormUsageIndex(svc *Service) *FormUsageIndex {
	return &FormUsageIndex{load: svc.FormPlacements}
}

// Get returns the cached index, rebuilding it when empty, invalidated or older
// than five minutes. The map is shared: callers must not modify it.
func (x *FormUsageIndex) Get(ctx context.Context) (map[string][]FormPlacement, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.byslug != nil && time.Since(x.at) < formUsageTTL {
		return x.byslug, nil
	}
	m, err := x.load(ctx)
	if err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string][]FormPlacement{}
	}
	x.byslug, x.at = m, time.Now()
	return m, nil
}

// Invalidate drops the cached index.
func (x *FormUsageIndex) Invalidate() {
	x.mu.Lock()
	x.byslug = nil
	x.mu.Unlock()
}

// formScanner is implemented by repositories that can walk the entities whose
// attributes may place a form (candidates, verified by the caller).
type formScanner interface {
	ScanFormCandidates(ctx context.Context, fn func(*Entity) bool) error
}

// FormPlacements scans the entities that may use a form and returns the
// placements by form slug, at most MaxRefUsers entities.
func (s *Service) FormPlacements(ctx context.Context) (map[string][]FormPlacement, error) {
	sc, ok := s.entityRepo.(formScanner)
	if !ok {
		return nil, fmt.Errorf("form usage lookup is not supported by this store")
	}
	ctx, cancel := context.WithTimeout(ctx, refUsageTimeout)
	defer cancel()
	out := map[string][]FormPlacement{}
	n := 0
	err := sc.ScanFormCandidates(ctx, func(e *Entity) bool {
		for slug, ps := range entityFormPlacements(e) {
			out[slug] = append(out[slug], ps...)
		}
		n++
		return n < MaxRefUsers
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
