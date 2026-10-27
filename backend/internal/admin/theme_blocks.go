package admin

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
)

// blocksPath is the Blocks page of the Theme area, relative to the admin prefix.
const blocksPath = "/theme/blocks"

// blocksPerPage keeps the usage lookups of one page small: each block's
// "used by" count scans the entities that mention it.
const blocksPerPage = 25

type blocksData struct {
	Table   ui.Table
	Primary *ui.Action // New block: a POST to the draft route
	Notice  string     // why blocks are unavailable
}

// usedBy is the "Used by" cell of block id: the number of entities that embed
// it ("200+" when the usage list is capped), or a dash when it cannot be read.
func (a *Admin) usedBy(r *http.Request, e *entity.Entity) string {
	users, capped, err := a.blockUsage(r.Context(), e.ID)
	if err != nil {
		return "-"
	}
	n := strconv.Itoa(len(users))
	if capped {
		n += "+"
	}
	return n
}

// handleBlocksPage lists the reusable blocks: name (opens the builder), where
// they are used and when they changed. New block creates a draft and opens it.
func (a *Admin) handleBlocksPage(w http.ResponseWriter, r *http.Request) {
	c := a.deps.Content
	if c.Entities == nil {
		a.unavailable(w, r, "Blocks")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	t := ui.Table{
		ID: "blocks", Caption: "Blocks", BaseURL: a.adminPath + blocksPath, CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "name", Label: "Name"}, {Key: "used", Label: "Used by", Align: "right"}, {Key: "updated", Label: "Updated"}},
		Query:   q, SearchLabel: "Search blocks",
		Empty: ui.Empty{Icon: "copy", Title: "No blocks yet", Body: "A block is a piece of a page you build once and place on many pages. Start one here, or choose \"Make a block\" on any element in the builder."},
	}
	d := blocksData{Table: t}
	v := a.NewView(w, r, "Blocks")
	if !a.reuseAvailable(r) {
		d.Notice = "Blocks are not available on this site: an entity type of its own already uses the slug block. Rename that type to turn them on."
		d.Table.Empty = ui.Empty{Icon: "copy", Title: "Blocks are off", Body: d.Notice}
		v.Data = d
		a.Render(w, r, http.StatusOK, "blocks/list", v)
		return
	}
	bs := a.schemaBySlug(r, entity.BlockSchemaSlug)
	res, err := c.Entities.ListEntitiesWithOffset(r.Context(),
		&entity.ListEntitiesFilter{SchemaID: &bs.ID, Search: q, Sort: "name", Asc: true}, &entity.OffsetPage{Page: page, Limit: blocksPerPage})
	if err != nil || res == nil {
		a.serverError(w, r, "The blocks could not be listed", err)
		return
	}
	query := url.Values{}
	if q != "" {
		query.Set("q", q)
	}
	d.Table.Page = ui.Pagination{Page: res.Page, PerPage: blocksPerPage, Total: res.Total, BaseURL: a.adminPath + blocksPath, Query: query}
	loc := a.siteLocation(r.Context())
	for _, e := range res.Items {
		id := e.ID.String()
		edit := a.entityEditHref(true, id)
		del := a.adminPath + "/entities/" + id + "/delete"
		d.Table.Rows = append(d.Table.Rows, ui.Row{ID: id, Label: e.Name, Cells: []ui.Cell{
			{Text: e.Name, Link: edit}, {Text: a.usedBy(r, e)}, {Text: fmtDate(e.UpdatedAt.In(loc)), Nowrap: true},
		}, Actions: []ui.Action{
			{Label: "Edit", Href: edit},
			{Label: "Delete", Href: del + "?back=" + url.QueryEscape(a.adminPath+blocksPath), Post: del, Fields: map[string]string{"back": a.adminPath + blocksPath}, Danger: true, ConfirmLabel: "Delete block",
				Confirm: "The block " + e.Name + " and its version history are deleted. Pages that place it show a missing block until you remove it."},
		}})
	}
	d.Primary = &ui.Action{Label: "New block", Icon: "plus", Post: a.adminPath + "/entities/drafts", Fields: map[string]string{"type": entity.BlockSchemaSlug}}
	v.Data = d
	a.Render(w, r, http.StatusOK, "blocks/list", v)
}
