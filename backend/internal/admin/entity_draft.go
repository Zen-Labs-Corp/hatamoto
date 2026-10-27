package admin

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
)

// draftType reports whether New for this type skips the form: posts and pages
// open their editor on an "Untitled" draft, which needs an entity id for
// autosave, preview and the SEO checks.
// A block starts the same way; a theme part needs its slot, so only the draft
// route (never the New form) creates one.
func draftType(sc *entity.EntitySchema) bool {
	return postEditorFor(sc) || (sc != nil && (sc.Slug == "page" || entity.IsBlockSchema(sc)))
}

// draftNoun is the word the UI uses for one entity of the type: "page" and
// "post" for those types and the built-in post types, otherwise the schema's
// own name in lower case ("guide").
func draftNoun(sc *entity.EntitySchema) string {
	switch {
	case sc.Slug == "page":
		return "page"
	case entity.IsBlockSchema(sc):
		return "block"
	case postEditors[sc.Slug]:
		return "post"
	}
	return strings.ToLower(strings.TrimSpace(sc.Name))
}

// handleEntityDraft is POST /admin/entities/drafts: it creates an "Untitled"
// draft of the posted type and redirects to its editor. A writer's newest
// untouched draft of that type is reused, so double clicks and refreshes do
// not pile up empty drafts. It is a POST so prefetch and prerender cannot
// create anything.
func (a *Admin) handleEntityDraft(w http.ResponseWriter, r *http.Request) {
	c := a.deps.Content
	if c.Entities == nil {
		a.unavailable(w, r, "Entities")
		return
	}
	sc := a.schemaBySlug(r, r.PostFormValue("type"))
	slot := r.PostFormValue("slot")
	isPart := entity.IsPartSchema(sc) && builder.ValidPartSlot(slot)
	if sc == nil || !(draftType(sc) || isPart) || (entity.IsReuseSlug(sc.Slug) && !sc.IsBuiltin) {
		a.Error(w, r, http.StatusBadRequest, "Unknown type", "Choose a post or a page to start.")
		return
	}
	name := "Untitled " + draftNoun(sc) // "Untitled page" is untitledPage
	if isPart {
		name = "Untitled " + slot + " part"
	}
	actor := actorID(r)
	if id, ok := a.untouchedDraft(r, sc, name, actor); ok {
		Redirect(w, r, a.entityEditHref(true, id.String()))
		return
	}
	// A new page starts as an empty builder layout; posts have no layout.
	attrs := json.RawMessage(`{}`)
	switch {
	case isPart:
		attrs = partDraftAttrs(slot)
	case sc.Slug == "page" || entity.IsBlockSchema(sc):
		attrs = json.RawMessage(`{"page_doc":{"v":2,"nodes":[]}}`)
	}
	if sc.Slug != "page" && !entity.IsBlockSchema(sc) && !isPart {
		attrs = seedRequired(sc, name)
	}
	in := &entity.CreateEntityInput{SchemaID: sc.ID, Name: name, Slug: "untitled-" + uuid.NewString()[:8], Attributes: attrs}
	created, err := c.Entities.CreateEntity(r.Context(), actor, in)
	if err != nil {
		a.logger().Error("new draft failed", "type", sc.Slug, "err", err)
		a.SetFlash(w, r, "error", "The draft could not be created: "+sentence(entityErrorText(err)))
		Redirect(w, r, a.adminPath+"/entities")
		return
	}
	// The builder opens on its own start panel; the other editors still say where to write.
	if !(isPart || sc.Slug == "page" || entity.IsBlockSchema(sc)) {
		a.SetFlash(w, r, "success", "Draft created. Start writing below.")
	}
	Redirect(w, r, a.entityEditHref(true, created.ID.String()))
}

// schemaDecl is the part of a schema JSON the post editor reads: the declared
// properties and which of them are required.
type schemaDecl struct {
	Properties map[string]struct {
		Type any `json:"type"`
	} `json:"properties"`
	Required []string `json:"required"`
}

func declOf(sc *entity.EntitySchema) schemaDecl {
	var d schemaDecl
	if sc != nil {
		_ = json.Unmarshal(sc.Schema, &d)
	}
	return d
}

// declaresString reports whether the schema declares prop as a string.
func (d schemaDecl) declaresString(prop string) bool {
	p, ok := d.Properties[prop]
	return ok && p.Type == "string"
}

// seedRequired is the attributes of a new draft: every required property gets
// a zero value of its type (the title is the draft name), so the draft passes
// the schema's validation before anything is typed.
func seedRequired(sc *entity.EntitySchema, name string) json.RawMessage {
	d := declOf(sc)
	attrs := map[string]any{}
	for _, k := range d.Required {
		switch t, _ := d.Properties[k].Type.(string); {
		case k == "title" && t == "string":
			attrs[k] = name
		case t == "string":
			attrs[k] = ""
		case t == "integer", t == "number":
			attrs[k] = 0
		case t == "boolean":
			attrs[k] = false
		case t == "array":
			attrs[k] = []any{}
		case t == "object":
			attrs[k] = map[string]any{}
		}
	}
	raw, _ := json.Marshal(attrs)
	return raw
}

// mirrorTitle keeps attributes.title in step with the entity name for schemas
// that declare a string title: listings, feeds and related cards prefer it.
func mirrorTitle(sc *entity.EntitySchema, attrs map[string]any, name string) {
	if name != "" && declOf(sc).declaresString("title") {
		attrs["title"] = name
	}
}

// untouchedDraft finds the actor's newest draft of sc that still has the
// generated name and was never saved again.
func (a *Admin) untouchedDraft(r *http.Request, sc *entity.EntitySchema, name string, actor *uuid.UUID) (uuid.UUID, bool) {
	if actor == nil {
		return uuid.Nil, false
	}
	draft := entity.StatusDraft
	res, err := a.deps.Content.Entities.ListEntitiesWithOffset(r.Context(), &entity.ListEntitiesFilter{SchemaID: &sc.ID, Status: &draft, Search: name, Sort: "created"}, &entity.OffsetPage{Page: 1, Limit: 20})
	if err != nil || res == nil {
		return uuid.Nil, false
	}
	for _, e := range res.Items {
		if e.Name == name && e.Version == 1 && e.CreatedBy != nil && *e.CreatedBy == *actor {
			return e.ID, true
		}
	}
	return uuid.Nil, false
}
