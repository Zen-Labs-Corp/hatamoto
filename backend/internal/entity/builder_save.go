package entity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/builder"
)

// The save rules of the page builder that do not depend on who saves: the
// admin builder and the MCP builder tools both go through them, so a document
// is accepted or refused the same way on either path.

// Editor roles: what the builder is editing.
const (
	RolePage  = "page"
	RoleBlock = "block"
	RolePart  = "part"
)

// ReuseRole is what the builder edits for a schema of the entity.
func ReuseRole(sc *EntitySchema) string {
	switch {
	case sc == nil:
		return RolePage
	case IsBlockSchema(sc):
		return RoleBlock
	case IsPartSchema(sc):
		return RolePart
	}
	return RolePage
}

// DocState is what an entity's stored page_doc is to the editor choice.
type DocState int

const (
	DocNone   DocState = iota // no page_doc (yet)
	DocOK                     // a layout the builder opens
	DocBroken                 // a page_doc that does not parse
	DocLegacy                 // no page_doc, and page_sections that do not convert
)

// BuilderDoc reads the page_doc stored on e.
func BuilderDoc(e *Entity) (*builder.Doc, DocState) {
	var m map[string]json.RawMessage
	if json.Unmarshal(e.Attributes, &m) != nil {
		return nil, DocNone
	}
	raw, ok := m["page_doc"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		if secs, ok := m["page_sections"]; ok && len(secs) > 2 { // more than "[]"
			if _, err := LegacyPageDocErr(e.Attributes); err != nil {
				return nil, DocLegacy
			}
		}
		return nil, DocNone
	}
	d, err := builder.Parse(raw)
	if err != nil {
		return nil, DocBroken
	}
	return d, DocOK
}

// DocProblem is a refusal of a document: an HTTP-style status, a stable code
// and the message. Msg is not yet phrased as a sentence for people.
type DocProblem struct {
	Status int
	Code   string
	Msg    string
}

func (p *DocProblem) Error() string { return p.Code + ": " + p.Msg }

// BuilderStore is what the shared save needs of the entity service.
type BuilderStore interface {
	GetEntity(ctx context.Context, id uuid.UUID) (*Entity, error)
	GetSchema(ctx context.Context, id uuid.UUID) (*EntitySchema, error)
	UpdateEntity(ctx context.Context, id uuid.UUID, userID *uuid.UUID, in *UpdateEntityInput) (*Entity, error)
	UpdateEntityStatus(ctx context.Context, id uuid.UUID, userID *uuid.UUID, status EntityStatus) (*Entity, error)
}

// EntityRole is the editor role of e, from its schema (loaded when e has none).
func EntityRole(ctx context.Context, st BuilderStore, e *Entity) string {
	sc := e.Schema
	if sc == nil {
		sc, _ = st.GetSchema(ctx, e.SchemaID)
	}
	return ReuseRole(sc)
}

// LoadDoc reads the page_doc of the entity id when it is of the built-in schema slug.
func LoadDoc(ctx context.Context, st BuilderStore, id, slug string) (*builder.Doc, bool) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, false
	}
	e, err := st.GetEntity(ctx, uid)
	if err != nil {
		return nil, false
	}
	sc := e.Schema
	if sc == nil {
		if sc, err = st.GetSchema(ctx, e.SchemaID); err != nil {
			return nil, false
		}
	}
	if sc.Slug != slug || !sc.IsBuiltin {
		return nil, false
	}
	d, state := BuilderDoc(e)
	return d, state == DocOK
}

// CheckReuseDoc applies the save rules of the reuse types to a doc about to be
// stored on e: a slot belongs to layout parts only (and a single or archive part
// has exactly one), and references must not loop. lookup reads a block's doc.
func CheckReuseDoc(role string, e *Entity, d *builder.Doc, lookup func(id string) (*builder.Doc, bool)) *DocProblem {
	slots := builder.SlotCount(d)
	if role != RolePart && slots > 0 {
		return &DocProblem{http.StatusUnprocessableEntity, "part_slot", "The Content slot belongs in a layout part. Remove it from this page."}
	}
	if role == RolePart {
		var attrs struct {
			Slot string `json:"slot"`
		}
		_ = json.Unmarshal(e.Attributes, &attrs)
		if attrs.Slot != builder.SlotArchive && builder.SlotReplaces(d) {
			return &DocProblem{http.StatusUnprocessableEntity, "part_slot", "Replace is only for archive parts."}
		}
		switch attrs.Slot {
		case builder.SlotSingle, builder.SlotArchive:
			if slots != 1 {
				return &DocProblem{http.StatusUnprocessableEntity, "part_slot", "A " + attrs.Slot + " part needs exactly one Content slot, where the page content goes."}
			}
		default:
			if slots > 0 {
				return &DocProblem{http.StatusUnprocessableEntity, "part_slot", "A " + attrs.Slot + " part has no Content slot. Remove it."}
			}
		}
	}
	if role == RolePage {
		return nil
	}
	if err := builder.CheckRefs(e.ID.String(), d, lookup); err != nil {
		return &DocProblem{http.StatusUnprocessableEntity, "ref_cycle", err.Error()}
	}
	return nil
}

// ParseDoc parses doc and checks its settings: the first rules every stored
// page_doc meets.
func ParseDoc(doc json.RawMessage) (*builder.Doc, *DocProblem) {
	parsed, err := builder.Parse(doc)
	if err == nil {
		err = parsed.CheckSettings()
	}
	if err != nil {
		return nil, &DocProblem{http.StatusUnprocessableEntity, "invalid_page_doc", err.Error()}
	}
	return parsed, nil
}

// CheckDocFor parses doc and runs every rule a stored page_doc of e must meet
// before the entity service sees it: structure, settings, slot and reference
// rules. It returns the parsed doc.
func CheckDocFor(ctx context.Context, st BuilderStore, e *Entity, doc json.RawMessage) (*builder.Doc, string, *DocProblem) {
	parsed, p := ParseDoc(doc)
	if p != nil {
		return nil, "", p
	}
	role := EntityRole(ctx, st, e)
	if p := checkReuse(ctx, st, role, e, parsed); p != nil {
		return nil, role, p
	}
	return parsed, role, nil
}

func checkReuse(ctx context.Context, st BuilderStore, role string, e *Entity, d *builder.Doc) *DocProblem {
	return CheckReuseDoc(role, e, d, func(id string) (*builder.Doc, bool) { return LoadDoc(ctx, st, id, BlockSchemaSlug) })
}

// SaveOpts tunes SaveBuilderDoc. Every field is optional.
type SaveOpts struct {
	ChangeSummary string // "" keeps "Updated in the builder"
	Autosave      bool   // a background save: recorded as AutosaveSummary, coalesced with the last one
	Publish       bool   // flip the page live after the save
	// ExpectedVersion makes the write atomic with the version check: the store
	// compares it under its row lock and returns a *VersionConflictError.
	ExpectedVersion *int

	// AfterParse runs once the doc parsed and the role is known, before the
	// reuse rules; BeforeWrite runs after every check, before the write. A
	// non-nil result refuses the save.
	AfterParse  func(role string) *DocProblem
	BeforeWrite func() *DocProblem
	// Tune changes the update before it is written (the admin sets the title).
	Tune func(in *UpdateEntityInput)
	// PublishGate returns why a requested publish is skipped ("" publishes).
	PublishGate func(saved *Entity) string
}

// SavedDoc is the outcome of a stored document.
type SavedDoc struct {
	Entity *Entity // as the service returned it, published when it was
	Parsed *builder.Doc
	// TakenSlug is the address Tune asked for that was in use; the entity kept its old one.
	TakenSlug string
	// PublishSkipped is the PublishGate's reason; PublishErr a failed status
	// change. Either way the doc is stored.
	PublishSkipped string
	PublishErr     error
}

// SaveBuilderDoc stores doc as e's page_doc: the shared checks, a merge into
// the stored attributes and UpdateEntity (which sanitises and normalises), then
// optionally the publish status change. The version is checked by the store
// when opts.ExpectedVersion is set. A refusal of the document is a
// *DocProblem; other errors come from the store.
func SaveBuilderDoc(ctx context.Context, st BuilderStore, actor *uuid.UUID, e *Entity, doc json.RawMessage, opts SaveOpts) (*SavedDoc, *DocProblem, error) {
	parsed, p := ParseDoc(doc)
	if p != nil {
		return nil, p, nil
	}
	role := EntityRole(ctx, st, e)
	if opts.AfterParse != nil {
		if p := opts.AfterParse(role); p != nil {
			return nil, p, nil
		}
	}
	attrs := map[string]json.RawMessage{}
	_ = json.Unmarshal(e.Attributes, &attrs)
	if attrs == nil {
		attrs = map[string]json.RawMessage{}
	}
	attrs["page_doc"] = doc
	raw, err := json.Marshal(attrs)
	if err != nil {
		return nil, nil, err
	}
	// The rules read the attributes that will be stored.
	next := *e
	next.Attributes = raw
	if p := checkReuse(ctx, st, role, &next, parsed); p != nil {
		return nil, p, nil
	}
	if opts.BeforeWrite != nil {
		if p := opts.BeforeWrite(); p != nil {
			return nil, p, nil
		}
	}
	summary := "Updated in the builder"
	if opts.ChangeSummary != "" {
		summary = opts.ChangeSummary
	}
	in := &UpdateEntityInput{Attributes: (*json.RawMessage)(&raw), ChangeSummary: &summary, ExpectedVersion: opts.ExpectedVersion}
	if opts.Autosave {
		summary = AutosaveSummary
		in.Autosave = true
	}
	if opts.Tune != nil {
		opts.Tune(in)
	}
	out := &SavedDoc{Parsed: parsed}
	updated, err := st.UpdateEntity(ctx, e.ID, actor, in)
	if errors.Is(err, ErrSlugAlreadyExists) && in.Slug != nil {
		out.TakenSlug = *in.Slug
		in.Slug = nil // the address is taken: keep the old one
		updated, err = st.UpdateEntity(ctx, e.ID, actor, in)
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidDoc), errors.Is(err, ErrPageSectionsRemoved):
			return nil, &DocProblem{http.StatusUnprocessableEntity, "invalid_page_doc", err.Error()}, nil
		case errors.Is(err, ErrValidationFailed), errors.Is(err, ErrRejectedByPlugin):
			return nil, &DocProblem{http.StatusUnprocessableEntity, "invalid", err.Error()}, nil
		}
		return nil, nil, err
	}
	out.Entity = updated
	if opts.Publish {
		if opts.PublishGate != nil {
			out.PublishSkipped = opts.PublishGate(updated)
		}
		if out.PublishSkipped == "" {
			published, err := st.UpdateEntityStatus(ctx, e.ID, actor, StatusPublished)
			if err != nil {
				out.PublishErr = err
			} else {
				out.Entity = published
			}
		}
	}
	return out, nil, nil
}
