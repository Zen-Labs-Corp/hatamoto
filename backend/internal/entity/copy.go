package entity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
)

// CopyFrom says what a new copy starts from.
type CopyFrom string

const (
	// CopyFromOriginal copies the original of the source's translation group.
	CopyFromOriginal CopyFrom = "original"
	// CopyFromThis copies the source itself.
	CopyFromThis CopyFrom = "this"
	// CopyFromEmpty creates a page with a name and slug only.
	CopyFromEmpty CopyFrom = "empty"
)

// ErrCopyUnsupported is returned for a source that is never copied: people,
// blocks and layout parts, and pages shared by every locale.
var ErrCopyUnsupported = fmt.Errorf("%w: this kind of entry cannot be copied to another locale", ErrValidationFailed)

// CopyOptions describes one copy.
type CopyOptions struct {
	// Locale is the target locale, one of the configured ones.
	Locale string
	// Name and Slug default to those of the entry the copy starts from.
	Name, Slug string
	// From defaults to CopyFromOriginal.
	From CopyFrom
	// DefaultLocale is the site's default locale; it picks the original of a
	// group that has no flagged one.
	DefaultLocale string
	// CreateParents creates drafts of the parent copies that are missing.
	CreateParents bool
	// AutoSlug retries once with "<slug>-<locale>" when the slug is taken.
	AutoSlug bool
	// Publish publishes the copy itself (never the parents created for it).
	Publish bool
	// Warn receives a problem that left the copy in place, such as terms that
	// could not be mapped.
	Warn func(msg string)
}

// CopyTerms maps and replaces an entity's terms. Taxonomy implements it in
// the server wiring; entity does not import it.
type CopyTerms interface {
	// MapTerms returns the ids of the entity's terms with each replaced by its
	// copy in locale when one exists.
	MapTerms(ctx context.Context, entityID uuid.UUID, locale string) ([]uuid.UUID, error)
	// ReplaceEntityTerms sets the entity's terms.
	ReplaceEntityTerms(ctx context.Context, entityID uuid.UUID, ids []uuid.UUID) error
}

// CopyService creates linked copies of entities in other locales.
type CopyService struct {
	svc    *Service
	groups *GroupStore
	terms  CopyTerms
}

// NewCopyService returns a CopyService. terms may be nil: copies then carry no
// terms.
func NewCopyService(svc *Service, groups *GroupStore, terms CopyTerms) *CopyService {
	return &CopyService{svc: svc, groups: groups, terms: terms}
}

// CreateCopy creates a copy of srcID in o.Locale and links it into srcID's
// translation group. A nil userID acts as the system. The copy keeps the source's attributes, metadata, byline
// and (mapped) terms, drops canonical_url, and is a draft unless o.Publish.
// Create and link cannot share a transaction (the group store has its own
// pool), so a failed link deletes the new row again.
func (c *CopyService) CreateCopy(ctx context.Context, userID uuid.UUID, srcID uuid.UUID, o CopyOptions) (*Entity, error) {
	locale, err := NormalizeLocale(o.Locale)
	if err != nil {
		return nil, err
	}
	src, err := c.svc.GetEntity(ctx, srcID)
	if err != nil {
		return nil, err
	}
	if err := copyable(src); err != nil {
		return nil, err
	}
	members, err := c.groups.Members(ctx, srcID)
	if err != nil {
		return nil, err
	}
	if src.Locale == locale || membersHave(members, locale) {
		return nil, ErrLocaleTaken
	}
	from := src
	if o.From == "" || o.From == CopyFromOriginal {
		if orig := OriginalOf(members, o.DefaultLocale); orig != nil && orig.ID != src.ID {
			if from, err = c.svc.GetEntity(ctx, orig.ID); err != nil {
				return nil, err
			}
		}
	}
	parentID, madeParents, err := c.resolveParent(ctx, userID, src, locale, o)
	if err != nil {
		return nil, err
	}
	cp, err := c.copyOne(ctx, userID, src, from, locale, o, parentID)
	if err != nil {
		c.deleteAll(ctx, madeParents)
		return nil, err
	}
	if o.Publish {
		if pub, err := c.svc.UpdateEntityStatus(ctx, cp.ID, actor(userID), StatusPublished); err != nil {
			c.warn(o, "The copy was created but could not be published: "+err.Error())
		} else {
			cp = pub
		}
	}
	return cp, nil
}

// copyOne creates one copy of content (the entry whose fields are copied) in
// locale, links it to link's group, and maps its terms. A failed link deletes
// the copy.
func (c *CopyService) copyOne(ctx context.Context, userID uuid.UUID, link, content *Entity, locale string, o CopyOptions, parentID *uuid.UUID) (*Entity, error) {
	in := &CreateEntityInput{
		SchemaID: content.SchemaID,
		Name:     content.Name,
		Slug:     content.Slug,
		Locale:   locale,
		ParentID: parentID,
	}
	if o.Name != "" {
		in.Name = o.Name
	}
	if o.Slug != "" {
		in.Slug = o.Slug
	}
	if o.From == CopyFromEmpty {
		in.Attributes = json.RawMessage(`{}`)
	} else {
		attrs, err := withoutCanonical(content.Attributes)
		if err != nil {
			return nil, err
		}
		in.Attributes = attrs
		in.Metadata = content.Metadata
		in.AuthorID = content.AuthorID
		in.BylinePersonID = content.BylinePersonID
		in.BylineOrg = content.BylineOrg
	}
	cp, err := c.svc.CreateEntity(ctx, actor(userID), in)
	if errors.Is(err, ErrSlugAlreadyExists) && o.AutoSlug {
		in.Slug = normalizeSlug(in.Slug + "-" + locale)
		cp, err = c.svc.CreateEntity(ctx, actor(userID), in)
	}
	if err != nil {
		return nil, err
	}
	if _, err := c.groups.Link(ctx, link.ID, cp.ID); err != nil {
		if delErr := c.svc.DeleteEntity(ctx, cp.ID); delErr != nil {
			c.svc.logger.Warn("delete copy after failed link", slog.String("entity_id", cp.ID.String()), slog.String("error", delErr.Error()))
		}
		return nil, err
	}
	cp.Locale = locale
	if c.terms != nil && o.From != CopyFromEmpty {
		if ids, err := c.terms.MapTerms(ctx, content.ID, locale); err != nil {
			c.warn(o, "Terms were not copied: "+err.Error())
		} else if len(ids) > 0 {
			if err := c.terms.ReplaceEntityTerms(ctx, cp.ID, ids); err != nil {
				c.warn(o, "Terms were not copied: "+err.Error())
			}
		}
	}
	return cp, nil
}

// MissingParent is an ancestor of a page that has no copy in a locale yet.
type MissingParent struct {
	ID   uuid.UUID
	Name string
	Slug string
}

// MissingParents lists the ancestors of srcID that need a copy before a copy
// of srcID in locale can sit under its parent's copy, outermost first. The
// walk goes up from the parent and stops at the first ancestor that already
// has a copy, so nothing is listed when the parent has one (or there is no
// parent).
func (c *CopyService) MissingParents(ctx context.Context, srcID uuid.UUID, locale string) ([]MissingParent, error) {
	locale, err := NormalizeLocale(locale)
	if err != nil {
		return nil, err
	}
	src, err := c.svc.GetEntity(ctx, srcID)
	if err != nil {
		return nil, err
	}
	missing, _, err := c.parentChain(ctx, src, locale)
	if err != nil {
		return nil, err
	}
	out := make([]MissingParent, len(missing))
	for i, m := range missing {
		out[i] = MissingParent{ID: m.ID, Name: m.Name, Slug: m.Slug}
	}
	return out, nil
}

// parentChain walks up from src's parent. It returns the ancestors without a
// copy in locale, outermost first, and the copy found above them (nil when no
// ancestor has one). Ancestors of another schema than src end the walk: a
// copy cannot be placed under them.
func (c *CopyService) parentChain(ctx context.Context, src *Entity, locale string) (missing []*Entity, found *uuid.UUID, err error) {
	if src.ParentID == nil {
		return nil, nil, nil
	}
	hs, ok := c.svc.entityRepo.(HierarchyStore)
	if !ok {
		return nil, nil, nil
	}
	anc, err := hs.Ancestors(ctx, src.ID)
	if err != nil {
		return nil, nil, err
	}
	for i := len(anc) - 1; i >= 0; i-- {
		a, err := c.svc.GetEntity(ctx, anc[i].ID)
		if err != nil {
			return nil, nil, err
		}
		if a.SchemaID != src.SchemaID {
			return nil, nil, nil
		}
		members, err := c.groups.Members(ctx, a.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, m := range members {
			if m.Locale == locale {
				id := m.ID
				return reversed(missing), &id, nil
			}
		}
		missing = append(missing, a)
	}
	return reversed(missing), nil, nil
}

func reversed(es []*Entity) []*Entity {
	out := make([]*Entity, len(es))
	for i, e := range es {
		out[len(es)-1-i] = e
	}
	return out
}

// resolveParent returns the parent for a copy of src in locale: the parent's
// copy, or with o.CreateParents a chain of new linked drafts made outermost
// first (returned in madeParents so a failure can delete them), else none.
func (c *CopyService) resolveParent(ctx context.Context, userID uuid.UUID, src *Entity, locale string, o CopyOptions) (parentID *uuid.UUID, madeParents []uuid.UUID, err error) {
	missing, found, err := c.parentChain(ctx, src, locale)
	if err != nil {
		return nil, nil, err
	}
	if len(missing) == 0 {
		return found, nil, nil
	}
	if !o.CreateParents {
		return nil, nil, nil
	}
	po := CopyOptions{AutoSlug: true, Warn: o.Warn, From: CopyFromThis}
	parent := found
	for _, m := range missing {
		cp, err := c.copyOne(ctx, userID, m, m, locale, po, parent)
		if err != nil {
			c.deleteAll(ctx, madeParents)
			return nil, nil, fmt.Errorf("create the %s copy of %q: %w", locale, m.Name, err)
		}
		madeParents = append(madeParents, cp.ID)
		id := cp.ID
		parent = &id
	}
	return parent, madeParents, nil
}

// deleteAll removes the given entities, newest first, logging failures.
func (c *CopyService) deleteAll(ctx context.Context, ids []uuid.UUID) {
	for i := len(ids) - 1; i >= 0; i-- {
		if err := c.svc.DeleteEntity(ctx, ids[i]); err != nil {
			c.svc.logger.Warn("delete parent copy after failed copy", slog.String("entity_id", ids[i].String()), slog.String("error", err.Error()))
		}
	}
}

func (c *CopyService) warn(o CopyOptions, msg string) {
	c.svc.logger.Warn("copy: " + msg)
	if o.Warn != nil {
		o.Warn(msg)
	}
}

// actor is the acting user, nil for uuid.Nil (a system action).
func actor(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// copyable reports whether e may be copied to another locale.
func copyable(e *Entity) error {
	if e.Locale == LocaleShared {
		return ErrCopyUnsupported
	}
	if e.Schema != nil && (e.Schema.Slug == personSchemaSlug || IsReuseSchema(e.Schema)) {
		return ErrCopyUnsupported
	}
	return nil
}

func membersHave(ms []Member, locale string) bool {
	for _, m := range ms {
		if m.Locale == locale {
			return true
		}
	}
	return false
}

// withoutCanonical returns attrs without the canonical_url key: a copy's
// canonical address would point at the page it translates.
func withoutCanonical(attrs json.RawMessage) (json.RawMessage, error) {
	if len(attrs) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(attrs, &m); err != nil {
		return nil, fmt.Errorf("read attributes: %w", err)
	}
	if _, ok := m["canonical_url"]; !ok {
		return attrs, nil
	}
	delete(m, "canonical_url")
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("write attributes: %w", err)
	}
	return out, nil
}
