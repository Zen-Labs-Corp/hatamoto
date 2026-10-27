package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/google/uuid"

	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
)

// emptyDoc is the layout of a page that has none yet.
const emptyDoc = `{"v":2,"nodes":[]}`

// builderTarget is an entity the builder tools work on: loaded, checked to be
// something the page builder edits, with its stored layout.
type builderTarget struct {
	e    *entity.Entity
	role string
	raw  json.RawMessage // the stored page_doc, or emptyDoc
	doc  *builder.Doc
}

// loadTarget loads entity id and refuses what the admin builder would not open.
// The rules are those of the admin's pageEditorFor and builderDoc (page and
// reuse types, or a type that carries sections; never a post or a person).
// A page that has no layout yet starts empty; one that has only the legacy
// page_sections is refused, since converting it is the admin builder's job.
func (s *Server) loadTarget(ctx context.Context, idText string) (*builderTarget, error) {
	id, err := parseID("entity_id", idText)
	if err != nil {
		return nil, err
	}
	st := s.store()
	e, err := st.GetEntity(ctx, id)
	if err != nil {
		return nil, err
	}
	sc := e.Schema
	if sc == nil {
		if sc, err = st.GetSchema(ctx, e.SchemaID); err != nil {
			return nil, err
		}
		e.Schema = sc
	}
	if !editableByBuilder(sc, e) {
		return nil, codedErr(codeNotBuilder, "%s entities are not edited with the page builder", sc.Slug)
	}
	d, state := entity.BuilderDoc(e)
	t := &builderTarget{e: e, role: entity.ReuseRole(sc), doc: d}
	switch state {
	case entity.DocOK:
		var attrs map[string]json.RawMessage
		_ = json.Unmarshal(e.Attributes, &attrs)
		t.raw = attrs["page_doc"]
	case entity.DocBroken:
		return nil, codedErr(codeNotBuilder, "the stored page_doc does not parse; repair it in the admin builder")
	case entity.DocLegacy:
		return nil, codedErr(codeNotBuilder, "legacy layout (page_sections); open it once in the admin builder")
	default:
		if hasLegacySections(e) {
			return nil, codedErr(codeNotBuilder, "legacy layout (page_sections); open it once in the admin builder")
		}
		t.raw = json.RawMessage(emptyDoc)
		t.doc, _ = builder.Parse(t.raw)
	}
	return t, nil
}

// editableByBuilder mirrors the admin's pageEditorFor: the page type and the
// reuse types, or a type that carries sections; never a post or a person.
func editableByBuilder(sc *entity.EntitySchema, e *entity.Entity) bool {
	if entity.IsPostEditorSchema(sc) || sc.Slug == "person" {
		return false
	}
	if sc.Slug == "page" || entity.IsReuseSchema(sc) {
		return true
	}
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(sc.Schema, &s) == nil {
		if _, ok := s.Properties["page_sections"]; ok {
			return true
		}
	}
	return hasLegacySections(e)
}

func hasLegacySections(e *entity.Entity) bool {
	var a struct {
		Sections []json.RawMessage `json:"page_sections"`
	}
	return json.Unmarshal(e.Attributes, &a) == nil && len(a.Sections) > 0
}

// copyDoc is a fresh parse of the stored layout, safe to edit.
func (t *builderTarget) copyDoc() (*builder.Doc, error) {
	return builder.Parse(t.raw)
}

// docProblem is a refusal of a document as the agent reads it.
func docProblem(p *entity.DocProblem) error {
	if p.Code == "invalid_page_doc" {
		return codedErr(codeInvalidPageDoc, "%s", p.Msg)
	}
	return codedErr(codeInvalidPageDoc, "%s: %s", p.Code, p.Msg)
}

type docSummary struct {
	Nodes    int `json:"nodes"`
	TopLevel int `json:"top_level"`
}

func summarise(d *builder.Doc) docSummary {
	n := 0
	var walk func(ns []*builder.Node)
	walk = func(ns []*builder.Node) {
		for _, x := range ns {
			n++
			walk(x.C)
		}
	}
	walk(d.Nodes)
	return docSummary{Nodes: n, TopLevel: len(d.Nodes)}
}

// storedDiffers reports whether the page_doc stored on e is not the posted one:
// the sanitiser or the service changed it on the way in.
func storedDiffers(e *entity.Entity, posted json.RawMessage) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(e.Attributes, &m) != nil {
		return true
	}
	var a, b any
	if json.Unmarshal(m["page_doc"], &a) != nil || json.Unmarshal(posted, &b) != nil {
		return true
	}
	return !reflect.DeepEqual(a, b)
}

// missingBlocks lists the blocks d refers to that cannot be read.
func (s *Server) missingBlocks(ctx context.Context, d *builder.Doc) []string {
	out := []string{}
	for _, id := range builder.RefIDs(d) {
		if _, ok := entity.LoadDoc(ctx, s.store(), id, entity.BlockSchemaSlug); !ok {
			out = append(out, id)
		}
	}
	return out
}

func (s *Server) registerBuilderDocs() {
	type getIn struct {
		EntityID string `json:"entity_id" jsonschema:"id of a page, block or layout part"`
	}
	register(s, toolSpec{name: "builder_get", description: "Read a page's builder layout: {version, doc}. Pass version back to builder_apply_ops. A page without a layout yet returns an empty doc. Refuses posts, people and pages that still have the legacy page_sections layout (not_builder).", scope: readScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in getIn) (any, error) {
			t, err := s.loadTarget(ctx, in.EntityID)
			if err != nil {
				return nil, err
			}
			return object{"entity_id": t.e.ID, "schema": t.e.Schema.Slug, "role": t.role, "name": t.e.Name, "status": t.e.Status,
				"version": t.e.Version, "doc": t.raw, "doc_summary": summarise(t.doc)}, nil
		})

	type validateIn struct {
		EntityID string `json:"entity_id,omitempty" jsonschema:"the page the layout is for; needed for ops and for the slot and block rules"`
		Doc      object `json:"doc,omitempty" jsonschema:"a whole layout {v:2,nodes:[...]} to check instead of the stored one"`
		Ops      []opIn `json:"ops,omitempty" jsonschema:"ops to try on the stored layout (a dry run, as in builder_apply_ops)"`
	}
	type problem struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	register(s, toolSpec{name: "builder_validate", description: "Check a layout without storing anything: a whole doc, or ops tried on a page's stored layout, or (neither given) the stored layout. Returns {ok, errors:[{code,message}], ids} where ids names the node ids generated for ops (as in builder_apply_ops). Uses the same rules as saving.", scope: readScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in validateIn) (any, error) {
			if in.Doc != nil && len(in.Ops) > 0 {
				return nil, userErr("send either doc or ops, not both")
			}
			if in.EntityID == "" && len(in.Ops) > 0 {
				return nil, userErr("ops need an entity_id")
			}
			fail := func(p *entity.DocProblem) (any, error) {
				return object{"ok": false, "errors": []problem{{p.Code, p.Msg}}}, nil
			}
			var t *builderTarget
			if in.EntityID != "" {
				var err error
				if t, err = s.loadTarget(ctx, in.EntityID); err != nil {
					return nil, err
				}
			}
			ids := []genID{}
			var raw json.RawMessage
			switch {
			case in.Doc != nil:
				raw = rawObject(in.Doc)
			case t == nil:
				return nil, userErr("send entity_id, doc or both")
			default:
				raw = t.raw
			}
			if len(in.Ops) > 0 {
				if err := checkOpLimits(in.Ops); err != nil {
					return nil, err
				}
				d, err := t.copyDoc()
				if err != nil {
					return nil, codedErr(codeNotBuilder, "%v", err)
				}
				if ids, err = applyOps(d, in.Ops); err != nil {
					return fail(&entity.DocProblem{Code: "invalid_page_doc", Msg: err.Error()})
				}
				if ids == nil {
					ids = []genID{}
				}
				if raw, err = json.Marshal(d); err != nil {
					return nil, err
				}
			}
			var parsed *builder.Doc
			if t != nil {
				var p *entity.DocProblem
				if parsed, _, p = entity.CheckDocFor(ctx, s.store(), t.e, raw); p != nil {
					return fail(p)
				}
			} else {
				var err error
				if parsed, err = builder.Parse(raw); err == nil {
					err = parsed.CheckSettings()
				}
				if err != nil {
					return fail(&entity.DocProblem{Code: "invalid_page_doc", Msg: err.Error()})
				}
			}
			sanitised := entity.SanitizeDocComponents(parsed)
			if err := entity.CheckSanitisedLength(parsed); err != nil {
				return fail(&entity.DocProblem{Code: "invalid_page_doc", Msg: err.Error()})
			}
			return object{"ok": true, "errors": []problem{}, "ids": ids, "doc_summary": summarise(parsed), "sanitised_differs": sanitised, "missingBlocks": s.missingBlocks(ctx, parsed)}, nil
		})

	type applyIn struct {
		EntityID string `json:"entity_id" jsonschema:"id of the page, block or layout part to edit"`
		Version  int    `json:"version" jsonschema:"the version builder_get returned; a stale one is refused with conflict"`
		Ops      []opIn `json:"ops" jsonschema:"the edits, applied in order; one failing op stores nothing (at most 500)"`
		Publish  bool   `json:"publish,omitempty" jsonschema:"also publish a draft page after saving"`
	}
	register(s, toolSpec{name: "builder_apply_ops", description: "Edit a page's layout by ops (insert remove move setProp setStyle setBind, and wrap unwrap duplicate) and store one new version. version must be the one builder_get returned: a stale version is refused with 'conflict: ... current version is N'; re-read and retry. All ops apply or none. Omitted node ids are generated and returned in ids. The same rules as the admin builder apply (valid nodes, slot and block rules, sanitising). Returns {version, changed, ids, doc_summary, missingBlocks, stored_differs, published}.", scope: writeScope, write: true, destructive: true},
		func(ctx context.Context, key *hmauth.APIKey, in applyIn) (any, error) {
			if err := checkOpLimits(in.Ops); err != nil {
				return nil, err
			}
			t, err := s.loadTarget(ctx, in.EntityID)
			if err != nil {
				return nil, err
			}
			if in.Version != t.e.Version {
				return nil, codedErr(codeConflict, "stale version %d; current version is %d (re-read with builder_get)", in.Version, t.e.Version)
			}
			if in.Publish && t.role != entity.RolePage {
				return nil, userErr("a %s is never published", t.role)
			}
			d, err := t.copyDoc()
			if err != nil {
				return nil, codedErr(codeNotBuilder, "%v", err)
			}
			ids, err := applyOps(d, in.Ops)
			if err != nil {
				return nil, codedErr(codeInvalidPageDoc, "%v", err)
			}
			raw, err := json.Marshal(d)
			if err != nil {
				return nil, err
			}
			publish := in.Publish && t.e.Status != entity.StatusPublished
			res, prob, err := entity.SaveBuilderDoc(ctx, s.store(), key.CreatedBy, t.e, raw, entity.SaveOpts{
				ChangeSummary: "[mcp:" + key.Name + "] Edited by " + key.Name + " via MCP", Publish: publish, ExpectedVersion: &in.Version,
			})
			if prob != nil {
				return nil, docProblem(prob)
			}
			var vc *entity.VersionConflictError
			if errors.As(err, &vc) {
				return nil, codedErr(codeConflict, "stale version %d; current version is %d (re-read with builder_get)", in.Version, vc.Current)
			}
			if err != nil {
				return nil, err
			}
			saved := res.Entity
			if fresh, err := s.store().GetEntity(ctx, t.e.ID); err == nil {
				saved = fresh
			}
			if ids == nil {
				ids = []genID{}
			}
			out := object{
				"entity_id": saved.ID, "version": saved.Version, "changed": saved.Version != t.e.Version, "status": saved.Status,
				"ids": ids, "doc_summary": summarise(d), "missingBlocks": s.missingBlocks(ctx, d),
				"stored_differs": storedDiffers(saved, raw), "published": saved.Status == entity.StatusPublished,
			}
			if res.PublishErr != nil { // the edit is stored: say so, never a bare error that invites a blind retry
				out["note"] = fmt.Sprintf("Saved as version %d, but the page could not be published: %v. Publish it with entities_publish.", saved.Version, publicError(s.d.Logger, "builder_apply_ops", res.PublishErr))
			}
			return out, nil
		})
}

// checkPageDocRules applies the builder's slot and block-reference rules to a
// page_doc sent whole through entities_update or entities_create, as the admin
// builder save does (a slot belongs in a layout part; blocks must not
// reference themselves in a loop). The rules read the attributes about to be
// stored, so a part's incoming slot counts, not the stored one. stored is the
// entity being updated (nil on create) and role the editor role of its schema.
// An update that changes neither page_doc nor slot is not checked, so a
// document that broke a rule earlier does not block unrelated edits. A doc
// that does not parse is left to the entity service, which refuses it with its
// own message.
func (s *Server) checkPageDocRules(ctx context.Context, stored *entity.Entity, role string, attrs object) error {
	pd, ok := attrs["page_doc"]
	if !ok || pd == nil {
		return nil
	}
	incoming := rawObject(attrs)
	if stored != nil && !docOrSlotChanged(stored.Attributes, incoming) {
		return nil
	}
	raw, err := json.Marshal(pd)
	if err != nil {
		return nil
	}
	d, err := builder.Parse(raw)
	if err != nil {
		return nil
	}
	e := &entity.Entity{ID: uuid.New()}
	if stored != nil {
		c := *stored
		e = &c
	}
	e.Attributes = incoming
	st := s.store()
	if p := entity.CheckReuseDoc(role, e, d, func(bid string) (*builder.Doc, bool) {
		return entity.LoadDoc(ctx, st, bid, entity.BlockSchemaSlug)
	}); p != nil {
		return userErr("invalid page_doc: %s", p.Msg)
	}
	return nil
}

// docOrSlotChanged reports whether page_doc or slot differ between two
// attribute objects.
func docOrSlotChanged(stored, incoming json.RawMessage) bool {
	var a, b map[string]any
	if json.Unmarshal(stored, &a) != nil || json.Unmarshal(incoming, &b) != nil {
		return true
	}
	for _, k := range []string{"page_doc", "slot"} {
		if !reflect.DeepEqual(a[k], b[k]) {
			return true
		}
	}
	return false
}
