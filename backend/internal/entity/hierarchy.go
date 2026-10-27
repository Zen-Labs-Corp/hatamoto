package entity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/urlpattern"
)

// Byline is who a post is credited to: a person entity, or the site
// organisation. Name is the display name (the company name for the organisation).
type Byline struct {
	PersonID *uuid.UUID
	Org      bool
	Name     string
}

// BylineResolver is the slice of the people store the entity service needs.
// People are entities of the `person` type, but this package never imports
// internal/person.
type BylineResolver interface {
	// Resolve maps byline text to a credit: the site organisation when it is
	// the company name, else the person with that slug or name, creating a
	// non-public person without a login when there is none.
	Resolve(ctx context.Context, name string) (Byline, error)
	// Name is the display name of the credit: the person's name, or the
	// organisation's when personID is nil.
	Name(ctx context.Context, personID *uuid.UUID) (string, error)
	// Check returns ErrAuthorNotFound when id is not a person.
	Check(ctx context.Context, id uuid.UUID) error
	// Mirror returns the hatamoto_authors row that mirrors the person (made on
	// first use). author_id is dual-written with it so a rollback still shows
	// bylines.
	Mirror(ctx context.Context, personID uuid.UUID) (*uuid.UUID, error)
	// PersonChanged is called after a person entity was created or saved: it
	// refreshes the mirror row and the plain-text byline on their posts.
	PersonChanged(ctx context.Context, personID uuid.UUID) error
	// PersonDeleting is called before a person entity is deleted: their posts
	// lose the reference and keep the plain-text byline.
	PersonDeleting(ctx context.Context, personID uuid.UUID) error
}

// personSchemaSlug is the slug of the builtin person type (person.SchemaSlug).
const personSchemaSlug = "person"

// errPersonStatus rejects a status change on a person: their status follows
// the Public setting (see personStatus).
var errPersonStatus = fmt.Errorf("%w: a person's visibility follows their Public setting; change that instead of the status", ErrValidationFailed)

// personStatus is the status a person entity must have: published when
// attributes.public is true, else draft. It is nil for every other schema.
//
// Visibility is also enforced by HiddenPersonSQL, but the previous release
// knows only the status, so a hidden person must not be published or a
// rollback would expose it. The service is the one write point (create,
// update, restore, import); every other path gets here or is rejected.
func personStatus(schemaSlug string, attrs json.RawMessage) *EntityStatus {
	if schemaSlug != personSchemaSlug {
		return nil
	}
	var a struct {
		Public bool `json:"public"`
	}
	_ = json.Unmarshal(attrs, &a)
	st := StatusDraft
	if a.Public {
		st = StatusPublished
	}
	return &st
}

// errReuseStatus rejects a status change or schedule on a block or theme part.
var errReuseStatus = fmt.Errorf("%w: blocks and layout parts are never published; they are used by reference", ErrValidationFailed)

// lockedStatus is the status an entity of sc must have, or nil when
// it is free: a person follows their Public setting, a block or theme part is
// always a draft (every public query needs published, so neither can be
// reached publicly). Every service write point applies it.
func lockedStatus(sc *EntitySchema, attrs json.RawMessage) *EntityStatus {
	if IsReuseSchema(sc) {
		st := StatusDraft
		return &st
	}
	if sc == nil {
		return nil
	}
	return personStatus(sc.Slug, attrs)
}

// PathChangeFunc is told about an update that may have moved an entity (and,
// with it, its descendants) to another URL: a new slug or a new parent. The
// server wires it to create redirects and refresh internal links.
type PathChangeFunc func(ctx context.Context, before, after *Entity, actor *uuid.UUID)

// SchemaPatternFunc is told after a schema update changed where its entities
// live (its URL pattern or overrides). before and after are the schema as it
// was and as it is. The server wires it to leave a 301 at every old path.
type SchemaPatternFunc func(ctx context.Context, before, after *EntitySchema)

// SetSchemaPatternObserver installs the callback run after a schema update
// changed its URL pattern or overrides; nil disables it.
func (s *Service) SetSchemaPatternObserver(f SchemaPatternFunc) { s.patternChanged = f }

// urlRouting is the part of a schema's settings that decides its entities'
// paths: the pattern and the per-attribute overrides.
func urlRouting(settings json.RawMessage) string {
	var st SchemaURLSettings
	if len(settings) > 0 && json.Unmarshal(settings, &st) != nil {
		st = SchemaURLSettings{}
	}
	overrides, _ := json.Marshal(st.Overrides)
	return st.URLPattern + "\x00" + string(overrides)
}

// LiveFunc is told about entities that may just have started to answer at
// their URL: created, published, or imported. The server wires it to retire
// the automatic redirects of their path.
type LiveFunc func(ctx context.Context, ids []uuid.UUID)

// SetLiveObserver installs the callback run after an entity was created,
// published or imported; nil disables it.
func (s *Service) SetLiveObserver(f LiveFunc) { s.live = f }

func (s *Service) fireLive(ctx context.Context, ids ...uuid.UUID) {
	if s.live != nil && len(ids) > 0 {
		s.live(ctx, ids)
	}
}

// SetBylines installs the people resolver; without one bylines stay plain text.
func (s *Service) SetBylines(b BylineResolver) { s.bylines = b }

// SetPathObserver installs the callback run after an update changed an
// entity's slug or parent; nil disables it.
func (s *Service) SetPathObserver(f PathChangeFunc) { s.pathChanged = f }

// checkParent verifies that parent exists, that making it the parent of self
// (nil for a row not created yet) creates no cycle and keeps the nesting
// within urlpattern.MaxPathDepth.
func (s *Service) checkParent(ctx context.Context, self *uuid.UUID, parent uuid.UUID) error {
	if self != nil && *self == parent {
		return ErrParentCycle
	}
	if _, err := s.entityRepo.GetByID(ctx, parent); err != nil {
		if err == ErrEntityNotFound {
			return ErrParentNotFound
		}
		return err
	}
	hs, ok := s.entityRepo.(HierarchyStore)
	if !ok {
		return nil
	}
	anc, err := hs.Ancestors(ctx, parent)
	if err != nil {
		return err
	}
	for _, a := range anc {
		if self != nil && a.ID == *self {
			return ErrParentCycle
		}
	}
	// ancestors + parent + the entity, plus whatever hangs below the entity: a
	// moved subtree must not end up deeper than a URL can express.
	below := 0
	if self != nil {
		if below, err = hs.SubtreeHeight(ctx, *self); err != nil {
			return err
		}
	}
	if len(anc)+2+below > urlpattern.MaxPathDepth {
		return ErrParentTooDeep
	}
	return nil
}

// checkPerson verifies a person id, when a resolver is installed.
func (s *Service) checkPerson(ctx context.Context, id uuid.UUID) error {
	if s.bylines == nil {
		return nil
	}
	return s.bylines.Check(ctx, id)
}

// mirrorOf returns the author_id that mirrors a person (nil without a resolver).
func (s *Service) mirrorOf(ctx context.Context, id uuid.UUID) (*uuid.UUID, error) {
	if s.bylines == nil {
		return nil, nil
	}
	return s.bylines.Mirror(ctx, id)
}

// bylineOf is the plain-text byline in attributes: author, else author_name.
func bylineOf(attrs json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(attrs, &m) != nil {
		return ""
	}
	for _, k := range []string{"author", "author_name"} {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// maxBylineLen is the longest author name (author.MaxNameLen).
const maxBylineLen = 255

// bylineFromText resolves the plain-text byline in attrs. ok is false when
// there is nothing to resolve (no byline, or one longer than a name).
func (s *Service) bylineFromText(ctx context.Context, attrs json.RawMessage) (b Byline, ok bool, err error) {
	if s.bylines == nil {
		return b, false, nil
	}
	name := bylineOf(attrs)
	// A byline longer than a name stays plain text.
	if name == "" || utf8.RuneCountInString(name) > maxBylineLen {
		return b, false, nil
	}
	b, err = s.bylines.Resolve(ctx, name)
	if err != nil {
		return b, false, fmt.Errorf("failed to resolve byline %q: %w", name, err)
	}
	return b, true, nil
}

// creditCreate sets the byline columns of a new entity from the input: an
// explicit person or organisation wins, else the byline text decides.
// author_id follows the person (dual-write).
func (s *Service) creditCreate(ctx context.Context, input *CreateEntityInput) error {
	switch {
	case input.BylinePersonID != nil:
		if err := s.checkPerson(ctx, *input.BylinePersonID); err != nil {
			return err
		}
		input.BylineOrg = false
	case input.BylineOrg:
		// The organisation has no author row: a rollback must not show a person.
		input.AuthorID = nil
	case input.AuthorID != nil:
		// A legacy author id is stored as given; the foreign key verifies it.
		return nil
	default:
		b, ok, err := s.bylineFromText(ctx, input.Attributes)
		if err != nil || !ok {
			return err
		}
		input.BylinePersonID, input.BylineOrg = b.PersonID, b.Org
	}
	if input.BylinePersonID == nil {
		return nil
	}
	mirror, err := s.mirrorOf(ctx, *input.BylinePersonID)
	if err != nil {
		return err
	}
	if mirror != nil {
		input.AuthorID = mirror
	}
	return nil
}

// syncBylineText rewrites the plain-text byline keys (author, author_name) the
// attributes already carry to the credit just set, so the old text can never
// win a later update. Attributes without those keys are left alone.
func (s *Service) syncBylineText(ctx context.Context, existing *Entity, input *UpdateEntityInput, person *uuid.UUID) error {
	if s.bylines == nil {
		return nil
	}
	attrs := existing.Attributes
	if input.Attributes != nil {
		attrs = *input.Attributes
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(attrs, &m) != nil {
		return nil
	}
	var keys []string
	for _, k := range []string{"author", "author_name"} {
		var cur string
		if raw, ok := m[k]; ok && json.Unmarshal(raw, &cur) == nil {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	name, err := s.bylines.Name(ctx, person)
	if err != nil || name == "" {
		return err
	}
	enc, err := json.Marshal(name)
	if err != nil {
		return err
	}
	for _, k := range keys {
		m[k] = enc
	}
	out, err := json.Marshal(m)
	if err != nil {
		return err
	}
	raw := json.RawMessage(out)
	input.Attributes = &raw
	return nil
}

// creditUpdate is creditCreate for an update; existing is the stored entity.
func (s *Service) creditUpdate(ctx context.Context, existing *Entity, input *UpdateEntityInput) error {
	setPerson := func(id uuid.UUID) error {
		input.BylinePersonID = &id
		off := false
		input.BylineOrg = &off
		mirror, err := s.mirrorOf(ctx, id)
		if err != nil {
			return err
		}
		if mirror != nil {
			input.AuthorID = mirror
		}
		return nil
	}
	// The organisation has no author row: author_id (the mirror a rollback
	// reads) is cleared with the person, or the old person would show again.
	setOrg := func() {
		on := true
		input.BylineOrg = &on
		input.ClearBylinePerson = true
		input.ClearAuthor, input.AuthorID = true, nil
	}
	switch {
	case input.ClearAuthor:
		off := false
		input.ClearBylinePerson, input.BylineOrg = true, &off
	case input.BylinePersonID != nil:
		if err := s.checkPerson(ctx, *input.BylinePersonID); err != nil {
			return err
		}
		if err := setPerson(*input.BylinePersonID); err != nil {
			return err
		}
		return s.syncBylineText(ctx, existing, input, input.BylinePersonID)
	case input.BylineOrg != nil && *input.BylineOrg:
		setOrg()
		return s.syncBylineText(ctx, existing, input, nil)
	case input.ClearBylinePerson, input.BylineOrg != nil, input.AuthorID != nil:
	case input.Attributes != nil && (existing.BylinePersonID != nil || existing.BylineOrg):
		// Credited: the text is output only. Whatever author text arrives
		// (an older copy, an edit) is overwritten with the credited name; the
		// credit changes through the explicit fields only.
		return s.syncBylineText(ctx, existing, input, existing.BylinePersonID)
	case input.Attributes != nil:
		// Uncredited: a changed plain-text byline adopts the credit it names.
		if bylineOf(*input.Attributes) == bylineOf(existing.Attributes) {
			return nil
		}
		b, ok, err := s.bylineFromText(ctx, *input.Attributes)
		if err != nil || !ok {
			return err
		}
		switch {
		case b.Org && !existing.BylineOrg:
			setOrg()
		case b.PersonID != nil && (existing.BylinePersonID == nil || *existing.BylinePersonID != *b.PersonID):
			return setPerson(*b.PersonID)
		}
	}
	return nil
}

// firePathChange tells the observer when an update changed where the entity lives.
func (s *Service) firePathChange(ctx context.Context, before, after *Entity, actor *uuid.UUID) {
	if s.pathChanged == nil || before == nil || after == nil {
		return
	}
	sameParent := (before.ParentID == nil && after.ParentID == nil) ||
		(before.ParentID != nil && after.ParentID != nil && *before.ParentID == *after.ParentID)
	if before.Slug == after.Slug && sameParent {
		return
	}
	s.pathChanged(ctx, before, after, actor)
}
