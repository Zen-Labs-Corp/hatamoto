package entity

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/builder"
)

// MaxRefUsers caps the usage list of a block.
const MaxRefUsers = 200

// refUsageTimeout bounds the scan: it reads the attributes text, which is
// fine for a rare admin action and must never run on the render path.
const refUsageTimeout = 3 * time.Second

// RefUser is one entity that embeds a block: a page, another block or a part.
type RefUser struct {
	ID         uuid.UUID
	Name, Slug string
	SchemaSlug string
	Status     EntityStatus
	UpdatedAt  time.Time
}

// refScanner is implemented by repositories that can walk the entities whose
// attributes mention an id as text (candidates, verified by the caller).
type refScanner interface {
	ScanMentioning(ctx context.Context, id uuid.UUID, fn func(*Entity) bool) error
}

// RefUsers lists the entities whose page_doc holds a ref node to blockID,
// newest first, at most limit (MaxRefUsers when limit <= 0). capped is true
// when more exist. The block itself is never listed, and neither is an entity
// that only carries the uuid as text.
func (s *Service) RefUsers(ctx context.Context, blockID uuid.UUID, limit int) (users []RefUser, capped bool, err error) {
	sc, ok := s.entityRepo.(refScanner)
	if !ok {
		return nil, false, fmt.Errorf("usage lookup is not supported by this store")
	}
	if limit <= 0 || limit > MaxRefUsers {
		limit = MaxRefUsers
	}
	ctx, cancel := context.WithTimeout(ctx, refUsageTimeout)
	defer cancel()
	want := blockID.String()
	err = sc.ScanMentioning(ctx, blockID, func(e *Entity) bool {
		if !docRefsBlock(e.Attributes, want) {
			return true
		}
		if len(users) == limit {
			capped = true
			return false
		}
		u := RefUser{ID: e.ID, Name: e.Name, Slug: e.Slug, Status: e.Status, UpdatedAt: e.UpdatedAt}
		if e.Schema != nil {
			u.SchemaSlug = e.Schema.Slug
		}
		users = append(users, u)
		return true
	})
	if err != nil {
		return nil, false, err
	}
	return users, capped, nil
}

// docRefsBlock reports whether the page_doc in attributes has a ref to block.
func docRefsBlock(attrs json.RawMessage, block string) bool {
	var a struct {
		Doc json.RawMessage `json:"page_doc"`
	}
	if json.Unmarshal(attrs, &a) != nil || len(a.Doc) == 0 {
		return false
	}
	d, err := builder.Parse(a.Doc)
	if err != nil {
		return false
	}
	for _, id := range builder.RefIDs(d) {
		if id == block {
			return true
		}
	}
	return false
}
