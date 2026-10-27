package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
)

// Reuse in the builder: blocks (entities of the block type, embedded by a ref
// node) and layout parts (entities of the theme_part type). Both open in the
// builder like a page and are saved explicitly; they are never published.

// reuseListMax bounds the block list the palette and the picker carry.
const reuseListMax = 100

// Editor roles: what the builder is editing.
const (
	rolePage  = entity.RolePage
	roleBlock = entity.RoleBlock
	rolePart  = entity.RolePart
)

// reuseRole is what the builder edits for a schema of the entity.
func reuseRole(sc *entity.EntitySchema) string { return entity.ReuseRole(sc) }

// reuseAvailable reports whether blocks can be used on this site: the block
// type exists and is the built-in one (a site type that took the slug turns
// the feature off).
func (a *Admin) reuseAvailable(r *http.Request) bool {
	sc := a.schemaBySlug(r, entity.BlockSchemaSlug)
	return sc != nil && sc.IsBuiltin
}

func (a *Admin) mountBuilderReuse(authed *httpx.Group) {
	w := authed.Group("", a.RequireRole("owner", "editor"))
	w.HandleFunc("GET /theme/blocks", a.handleBlocksPage)
	w.HandleFunc("POST /entities/{id}/builder/block", a.handleMakeBlock)
	w.HandleFunc("GET /builder/blocks", a.handleBlocksList)
	w.HandleFunc("GET /builder/blocks/{id}", a.handleBlockNodes)
	w.HandleFunc("GET /builder/blocks/{id}/usage", a.handleBlockUsage)
	a.mountBuilderTemplates(w)
}

// partDraftAttrs is the first document of a new part: a single or archive part
// starts with its one Content slot.
func partDraftAttrs(slot string) json.RawMessage {
	nodes := `[]`
	if slot == builder.SlotSingle || slot == builder.SlotArchive {
		nodes = `[{"id":"slot0","t":"slot"}]`
	}
	b, _ := json.Marshal(map[string]any{
		"slot":     slot,
		"page_doc": json.RawMessage(`{"v":2,"nodes":` + nodes + `}`),
	})
	return b
}

// entityDoc reads the page_doc of the entity id when it is of schema slug.
func (a *Admin) entityDoc(ctx context.Context, id, slug string) (*builder.Doc, bool) {
	return entity.LoadDoc(ctx, a.deps.Content.Entities, id, slug)
}

// entityRole is the editor role of e, from its schema (loaded when e has none).
func (a *Admin) entityRole(ctx context.Context, e *entity.Entity) string {
	return entity.EntityRole(ctx, a.deps.Content.Entities, e)
}

type makeBlockRequest struct {
	Name   string          `json:"name"`
	NodeID string          `json:"nodeId"`
	Doc    json.RawMessage `json:"doc"`
}

// handleMakeBlock creates a block from a subtree (nodeId) or the whole page of
// the posted doc, with fresh ids. The page itself is not touched: the client
// replaces the subtree by a ref and saves like any edit.
func (a *Admin) handleMakeBlock(w http.ResponseWriter, r *http.Request) {
	e, _, ok := a.builderGate(w, r)
	if !ok {
		return
	}
	reply := func(status int, v any) { a.writeJSON(w, status, v) }
	problem := func(status int, code, msg string) { reply(status, map[string]string{"code": code, "error": msg}) }
	bs := a.schemaBySlug(r, entity.BlockSchemaSlug)
	if bs == nil || !bs.IsBuiltin {
		problem(http.StatusNotFound, "unavailable", "Blocks are not available on this site.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, builderSaveMaxBody)
	var req makeBlockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(http.StatusBadRequest, "bad_request", "Invalid request body.")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 255 {
		problem(http.StatusUnprocessableEntity, "invalid_name", "Name the block, using at most 255 characters.")
		return
	}
	var doc *builder.Doc
	if len(req.Doc) > 0 {
		var err error
		if doc, err = builder.Parse(req.Doc); err != nil {
			problem(http.StatusUnprocessableEntity, "invalid_page_doc", sentence(err.Error()))
			return
		}
	} else {
		doc, _ = builderDoc(e)
	}
	src := doc.Nodes
	if req.NodeID != "" {
		n, _ := doc.Find(req.NodeID)
		if n == nil {
			problem(http.StatusUnprocessableEntity, "unknown_node", "That element is not in the page.")
			return
		}
		src = []*builder.Node{n}
	}
	if len(src) == 0 {
		problem(http.StatusUnprocessableEntity, "empty", "There is nothing to put in a block yet.")
		return
	}
	copyDoc := &builder.Doc{V: 2, Nodes: builder.FreshIDs(src, newNodeID)}
	raw, err := json.Marshal(copyDoc)
	if err != nil {
		a.serverError(w, r, "The block could not be built", err)
		return
	}
	if _, err := builder.Parse(raw); err != nil {
		problem(http.StatusUnprocessableEntity, "invalid_page_doc", sentence(err.Error()))
		return
	}
	if builder.SlotCount(copyDoc) > 0 {
		problem(http.StatusUnprocessableEntity, "part_slot", "A block cannot hold the Content slot of a layout part.")
		return
	}
	if err := builder.CheckRefs(uuid.Nil.String(), copyDoc, func(id string) (*builder.Doc, bool) {
		return a.entityDoc(r.Context(), id, entity.BlockSchemaSlug)
	}); err != nil {
		problem(http.StatusUnprocessableEntity, "ref_cycle", sentence(err.Error()))
		return
	}
	attrs, _ := json.Marshal(map[string]json.RawMessage{"page_doc": raw})
	in := &entity.CreateEntityInput{SchemaID: bs.ID, Name: name, Slug: "block-" + uuid.NewString()[:8], Attributes: attrs}
	created, err := a.deps.Content.Entities.CreateEntity(r.Context(), actorID(r), in)
	if err != nil {
		if errors.Is(err, entity.ErrValidationFailed) {
			problem(http.StatusUnprocessableEntity, "invalid", sentence(entityErrorText(err)))
			return
		}
		a.logger().Error("admin: make block failed", "err", err)
		problem(http.StatusInternalServerError, "server_error", "The block could not be created.")
		return
	}
	reply(http.StatusCreated, map[string]string{"id": created.ID.String(), "name": created.Name})
}

type blockItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Updated string `json:"updated"`
}

// blockItems lists the blocks by name, matching q, except skip.
func (a *Admin) blockItems(r *http.Request, q string, skip uuid.UUID) []blockItem {
	out := []blockItem{}
	bs := a.schemaBySlug(r, entity.BlockSchemaSlug)
	if bs == nil || !bs.IsBuiltin {
		return out
	}
	res, err := a.deps.Content.Entities.ListEntitiesWithOffset(r.Context(),
		&entity.ListEntitiesFilter{SchemaID: &bs.ID, Search: q, Sort: "name", Asc: true}, &entity.OffsetPage{Page: 1, Limit: reuseListMax})
	if err != nil || res == nil {
		return out
	}
	for _, b := range res.Items {
		if b.ID != skip {
			out = append(out, blockItem{ID: b.ID.String(), Name: b.Name, Updated: b.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")})
		}
	}
	return out
}

func (a *Admin) handleBlocksList(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, a.blockItems(r, strings.TrimSpace(r.URL.Query().Get("q")), uuid.Nil))
}

// block loads the block named in the URL; it answers 404 for anything else.
func (a *Admin) block(w http.ResponseWriter, r *http.Request) (*entity.Entity, bool) {
	id, ok := uuidParam(r, "id")
	if !ok {
		a.writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "error": "No such block."})
		return nil, false
	}
	e, err := a.deps.Content.Entities.GetEntity(r.Context(), id)
	if err == nil {
		sc := e.Schema
		if sc == nil {
			sc, err = a.deps.Content.Entities.GetSchema(r.Context(), e.SchemaID)
		}
		if err == nil && entity.IsBlockSchema(sc) {
			return e, true
		}
	}
	a.writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "error": "No such block."})
	return nil, false
}

// handleBlockNodes returns the block's nodes with fresh ids: what Detach puts
// in place of a ref. The ids differ on every call.
func (a *Admin) handleBlockNodes(w http.ResponseWriter, r *http.Request) {
	e, ok := a.block(w, r)
	if !ok {
		return
	}
	d, state := builderDoc(e)
	if state != docOK {
		a.writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "invalid_page_doc", "error": "This block has no readable content."})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"id": e.ID.String(), "name": e.Name, "nodes": builder.FreshIDs(d.Nodes, newNodeID)})
}

type usageItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Href   string `json:"href"`
}

func (a *Admin) blockUsage(ctx context.Context, id uuid.UUID) ([]usageItem, bool, error) {
	users, capped, err := a.deps.Content.Entities.RefUsers(ctx, id, entity.MaxRefUsers)
	if err != nil {
		return nil, false, err
	}
	out := make([]usageItem, 0, len(users))
	for _, u := range users {
		out = append(out, usageItem{ID: u.ID.String(), Name: u.Name, Type: u.SchemaSlug, Status: string(u.Status), Href: a.adminPath + "/entities/" + url.PathEscape(u.ID.String()) + "/edit"})
	}
	return out, capped, nil
}

func (a *Admin) handleBlockUsage(w http.ResponseWriter, r *http.Request) {
	e, ok := a.block(w, r)
	if !ok {
		return
	}
	users, capped, err := a.blockUsage(r.Context(), e.ID)
	if err != nil {
		a.logger().Error("admin: block usage failed", "err", err)
		a.writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "server_error", "error": "The usage list could not be loaded."})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"users": users, "capped": capped})
}

// builderReuse is the boot data of the reuse features for e.
func (a *Admin) builderReuse(r *http.Request, e *entity.Entity, sc *entity.EntitySchema) map[string]any {
	role := reuseRole(sc)
	out := map[string]any{
		"available": a.reuseAvailable(r), "role": role, "slot": "",
		"blocks": []blockItem{}, "templates": a.templateItems(r),
		"urls": map[string]string{
			"blocks": a.adminPath + "/builder/blocks", "makeBlock": a.adminPath + "/entities/" + e.ID.String() + "/builder/block",
			"template": a.adminPath + "/builder/templates", "saveTemplate": a.adminPath + "/entities/" + e.ID.String() + "/builder/template",
		},
	}
	if role == rolePart {
		var attrs struct {
			Slot string `json:"slot"`
		}
		_ = json.Unmarshal(e.Attributes, &attrs)
		out["slot"] = attrs.Slot
	}
	if out["available"] == true {
		skip := uuid.Nil
		if role == roleBlock {
			skip = e.ID
		}
		out["blocks"] = a.blockItems(r, "", skip)
	}
	return out
}

// shellReuse fills the shell data of a block or part: the save label and, for a
// block, where it is used.
func (a *Admin) shellReuse(r *http.Request, sh *builderShell, e *entity.Entity, sc *entity.EntitySchema) {
	switch reuseRole(sc) {
	case roleBlock:
		sh.Role, sh.SaveLabel, sh.TitleLabel, sh.ListHref = roleBlock, "Save block", "Block name", a.adminPath+blocksPath
		sh.StatusBadge = nil
		users, capped, err := a.blockUsage(r.Context(), e.ID)
		if err != nil {
			a.logger().Warn("admin: block usage failed", "err", err)
			return
		}
		sh.Usage, sh.UsageLoaded = users, true
		sh.UsageLabel = usageLabel(len(users), capped)
	case rolePart:
		sh.Role, sh.SaveLabel, sh.TitleLabel, sh.ListHref = rolePart, "Save part", "Part name", a.adminPath+"/theme/parts"
		sh.StatusBadge = nil
	}
}

func usageLabel(n int, capped bool) string {
	c := strconv.Itoa(n)
	if capped {
		c += "+"
	}
	if n == 1 && !capped {
		return "Used on 1 page"
	}
	return "Used on " + c + " pages"
}
