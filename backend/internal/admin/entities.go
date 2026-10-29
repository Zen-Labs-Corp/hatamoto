package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/person"
)

const entitiesPerPage = 20

// usesVisualEditor reports whether e has an editor beyond the generic form: the
// Go page editor (page_editor.go) for pages and every entity with Page Builder
// sections, and the Go blog editor (blog_editor.go) for post types.
func usesVisualEditor(sc *entity.EntitySchema, e *entity.Entity) bool {
	return pageEditorFor(sc, e) || postEditorFor(sc)
}

// reuseSchemaIDs are the built-in block and theme part types. They are managed
// under Theme (Blocks, Layout parts), so Entities leaves them out of its list,
// counts, type tabs, New menu and pickers; a site's own type named "block" is
// not one of them.
func reuseSchemaIDs(schemas []*entity.EntitySchema) []uuid.UUID {
	var ids []uuid.UUID
	for _, sc := range schemas {
		if entity.IsReuseSchema(sc) {
			ids = append(ids, sc.ID)
		}
	}
	return ids
}

func (a *Admin) mountEntities(authed *httpx.Group) {
	authed.HandleFunc("GET /entities", a.handleEntities)
	authed.HandleFunc("POST /entities/bulk", a.handleEntitiesBulk)
	authed.HandleFunc("GET /entities/new", a.handleEntityNew)
	authed.Group("", a.RequireRole("owner", "editor")).HandleFunc("POST /entities/drafts", a.handleEntityDraft)
	authed.HandleFunc("GET /entities/parent-options", a.handleParentOptions)
	authed.HandleFunc("POST /entities", a.handleEntityCreate)
	authed.HandleFunc("GET /entities/{id}", a.handleEntityEdit)
	authed.HandleFunc("POST /entities/{id}", a.handleEntitySave)
	authed.HandleFunc("POST /entities/{id}/analyze", a.handleEntityAnalyze)
	authed.HandleFunc("POST /entities/{id}/status", a.handleEntityStatus)
	authed.HandleFunc("GET /entities/{id}/delete", a.handleEntityDeleteConfirm)
	authed.HandleFunc("POST /entities/{id}/delete", a.handleEntityDelete)
	authed.HandleFunc("GET /entities/{id}/versions", a.handleVersions)
	authed.HandleFunc("GET /entities/{id}/versions/compare", a.handleVersionCompare)
	authed.HandleFunc("GET /entities/{id}/versions/{version}", a.handleVersionView)
	authed.HandleFunc("GET /entities/{id}/versions/{version}/restore", a.handleVersionRestoreConfirm)
	authed.HandleFunc("POST /entities/{id}/versions/{version}/restore", a.handleVersionRestore)
	authed.Group("", a.RequireRole("owner", "editor")).HandleFunc("POST /entities/{id}/versions/{version}/nodes/{node}/restore", a.handleVersionRestoreNode)
	authed.Group("", a.RequireRole("owner", "editor")).HandleFunc("GET /entities/{id}/builder/node-versions", a.handleBuilderNodeVersions)
}

// entitiesData feeds entities/list. The unfiltered list has the Create menu and
// a description; a type's list has Primary (its one "New <type>" button) instead
// and no description.
type entitiesData struct {
	Title       string
	Description string
	Menu        *ui.ButtonMenu
	Primary     *ui.Action
	Table       ui.Table
	StatusTabs  []ui.Tab
}

// maxParentOptions caps the Parent filter (the parents with the most children).
const maxParentOptions = 50

var statusLabels = []struct{ Value, Label string }{
	{"", "All"}, {"published", "Published"}, {"draft", "Draft"}, {"scheduled", "Scheduled"}, {"archived", "Archived"},
}

func statusBadge(s entity.EntityStatus) *ui.Badge {
	kind := "neutral"
	switch s {
	case entity.StatusPublished:
		kind = "success"
	case entity.StatusScheduled:
		kind = "info"
	case entity.StatusArchived:
		kind = "warning"
	}
	label := string(s)
	if label != "" {
		label = strings.ToUpper(label[:1]) + label[1:]
	}
	return &ui.Badge{Label: label, Kind: kind}
}

// listParams are the entity list's filters, read from the URL.
type listParams struct {
	q, status, typ, author, term, under, from, to, sortKey, dir string
	page                                                        int
}

func readListParams(r *http.Request) listParams {
	q := r.URL.Query()
	p := listParams{
		q: strings.TrimSpace(q.Get("q")), status: q.Get("status"), typ: q.Get("type"), author: q.Get("author"), term: q.Get("term"), under: q.Get("under"),
		from: q.Get("from"), to: q.Get("to"), sortKey: q.Get("sort"), dir: q.Get("dir"),
	}
	p.page, _ = strconv.Atoi(q.Get("page"))
	if p.page < 1 {
		p.page = 1
	}
	switch p.sortKey {
	case "name", "status", "updated":
	default:
		p.sortKey = "updated"
		if p.dir == "" {
			p.dir = "desc"
		}
	}
	if p.dir != "asc" {
		p.dir = "desc"
	}
	if p.sortKey == "name" && q.Get("dir") == "" {
		p.dir = "asc"
	}
	return p
}

// query is the URL state that links keep (everything but the page).
func (p listParams) query() url.Values {
	v := url.Values{}
	for k, s := range map[string]string{"q": p.q, "status": p.status, "type": p.typ, "author": p.author, "term": p.term, "under": p.under, "from": p.from, "to": p.to} {
		if s != "" {
			v.Set(k, s)
		}
	}
	if p.sortKey != "updated" || p.dir != "desc" {
		v.Set("sort", p.sortKey)
		v.Set("dir", p.dir)
	}
	return v
}

// hrefWithout is the list URL with the given params dropped (and paging reset).
func (p listParams) hrefWithout(prefix string, keys ...string) string {
	q := p.query()
	for _, k := range keys {
		q.Del(k)
	}
	if len(q) == 0 {
		return prefix + "/entities"
	}
	return prefix + "/entities?" + q.Encode()
}

// hrefWithStatus is the list URL with the status set ("" drops it), every other filter kept.
func (p listParams) hrefWithStatus(prefix, status string) string {
	q := p.query()
	q.Del("status")
	if status != "" {
		q.Set("status", status)
	}
	if len(q) == 0 {
		return prefix + "/entities"
	}
	return prefix + "/entities?" + q.Encode()
}

// chips are the removable chips of the popover filters in use.
func (p listParams) chips(prefix string, loc *time.Location, authors map[uuid.UUID]string, termOpts []ui.Option) []ui.Chip {
	var out []ui.Chip
	day := func(s string) string {
		if t, ok := parseDay(s, loc); ok {
			return fmtDate(t)
		}
		return s
	}
	if _, ok := parseDay(p.from, loc); ok {
		out = append(out, ui.Chip{Label: "Updated from " + day(p.from), RemoveHref: p.hrefWithout(prefix, "from")})
	}
	if _, ok := parseDay(p.to, loc); ok {
		out = append(out, ui.Chip{Label: "Updated to " + day(p.to), RemoveHref: p.hrefWithout(prefix, "to")})
	}
	if id, err := uuid.Parse(p.author); err == nil && authors[id] != "" {
		out = append(out, ui.Chip{Label: "Author: " + authors[id], RemoveHref: p.hrefWithout(prefix, "author")})
	}
	for _, o := range termOpts {
		if o.Value != "" && o.Selected {
			out = append(out, ui.Chip{Label: "Term: " + strings.TrimSpace(strings.ReplaceAll(o.Label, "— ", "")), RemoveHref: p.hrefWithout(prefix, "term")})
		}
	}
	return out
}

func parseDay(s string, loc *time.Location) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02", s, loc)
	return t, err == nil
}

func (a *Admin) handleEntities(w http.ResponseWriter, r *http.Request) {
	c := a.deps.Content
	if c.Entities == nil {
		a.unavailable(w, r, "Entities")
		return
	}
	ctx := r.Context()
	p := readListParams(r)
	schemas, err := c.Entities.ListSchemas(ctx)
	if err != nil {
		a.serverError(w, r, "The entity types could not be loaded", err)
		return
	}
	bySlug := map[string]*entity.EntitySchema{}
	byID := map[uuid.UUID]*entity.EntitySchema{}
	for _, s := range schemas {
		byID[s.ID] = s
		if !entity.IsReuseSchema(s) {
			bySlug[s.Slug] = s
		}
	}

	f := &entity.ListEntitiesFilter{Search: p.q, Asc: p.dir == "asc", ExcludeSchemaIDs: reuseSchemaIDs(schemas)}
	switch p.sortKey {
	case "name", "status":
		f.Sort = p.sortKey
	default:
		f.Sort = "updated"
	}
	if st, err := entity.ParseStatus(p.status); err == nil {
		f.Status = &st
	}
	var resolved *entity.EntitySchema // the type the list is filtered to; nil for All and an unknown ?type=
	if p.typ == person.SchemaSlug && a.deps.People != nil {
		// People has its own list; this keeps the subnav's no-JS select working.
		Redirect(w, r, a.adminPath+"/people")
		return
	}
	if s, ok := bySlug[p.typ]; ok {
		f.SchemaID, resolved = &s.ID, s
	}
	if id, err := uuid.Parse(p.author); err == nil {
		f.PersonID = &id
	}
	if id, err := uuid.Parse(p.term); err == nil {
		f.TermID = &id
	}
	if id, err := uuid.Parse(p.under); err == nil {
		f.Under = &id
	}
	loc := a.siteLocation(ctx)
	if t, ok := parseDay(p.from, loc); ok {
		f.UpdatedFrom = &t
	}
	if t, ok := parseDay(p.to, loc); ok {
		t = t.AddDate(0, 0, 1)
		f.UpdatedBefore = &t
	}

	authors := map[uuid.UUID]string{}
	var authorOpts []ui.Option
	if c.Authors != nil {
		if list, err := c.Authors.List(ctx); err == nil {
			authorOpts = append(authorOpts, ui.Option{Value: "", Label: "All authors", Selected: p.author == ""})
			for _, au := range list {
				authors[au.ID] = au.Name
				authorOpts = append(authorOpts, ui.Option{Value: au.ID.String(), Label: au.Name, Selected: p.author == au.ID.String()})
			}
		}
	}
	counts := a.entityCounts(ctx, schemas, f.SchemaID)
	// The status filter is a tab row with counts; each tab keeps the other filters in its link.
	statusTabs := make([]ui.Tab, 0, len(statusLabels))
	for _, sl := range statusLabels {
		n := counts.byStatus[sl.Value]
		if sl.Value == "" {
			n = counts.all
		}
		statusTabs = append(statusTabs, ui.Tab{Label: sl.Label, Href: p.hrefWithStatus(a.adminPath, sl.Value), Count: n, Active: p.status == sl.Value})
	}
	// Phones have no sidebar subnav: the type is picked here (hidden beside the subnav on wider screens).
	typeOpts := []ui.Option{{Value: "", Label: "All types", Selected: resolved == nil}}
	for _, ty := range counts.types {
		if ty.Slug == person.SchemaSlug {
			continue // People has its own list: a live swap cannot follow that redirect
		}
		typeOpts = append(typeOpts, ui.Option{Value: ty.Slug, Label: typePluralFor(ty.Slug, ty.Name), Selected: resolved != nil && resolved.Slug == ty.Slug})
	}
	// The filter form is one GET: what it has no field for rides along as hidden values.
	hidden := map[string]string{}
	if p.status != "" {
		hidden["status"] = p.status
	}
	var primary []ui.Filter
	if len(typeOpts) > 2 {
		primary = append(primary, ui.Filter{Name: "type", Label: "Type", Options: typeOpts})
	} else if p.typ != "" {
		hidden["type"] = p.typ
	}
	if parentOpts := a.parentFilterOptions(ctx, byID, p.under); len(parentOpts) > 1 {
		primary = append(primary, ui.Filter{Name: "under", Label: "Parent", Options: parentOpts})
	}
	popover := []ui.Filter{
		{Name: "from", Label: "Updated from", Input: "date", Value: p.from},
		{Name: "to", Label: "Updated to", Input: "date", Value: p.to},
	}
	if len(authorOpts) > 1 {
		popover = append(popover, ui.Filter{Name: "author", Label: "Author", Options: authorOpts})
	}
	termOpts := a.termFilterOptions(ctx, p.term)
	if len(termOpts) > 1 {
		popover = append(popover, ui.Filter{Name: "term", Label: "Term", Options: termOpts})
	}
	chips := p.chips(a.adminPath, loc, authors, termOpts)

	types := counts.types
	caption := "Entities"
	if resolved != nil {
		caption = typePluralFor(resolved.Slug, resolved.Name)
	}
	res, err := c.Entities.ListEntitiesWithOffset(ctx, f, &entity.OffsetPage{Page: p.page, Limit: entitiesPerPage})
	if err != nil {
		a.logger().Error("list entities failed", "err", err)
		a.renderEntities(w, r, p, ui.Table{ID: "entities", Caption: caption, BaseURL: a.adminPath + "/entities", Error: "The entities could not be loaded."}, nil, -1, types, resolved)
		return
	}
	if res.TotalPages > 0 && p.page > res.TotalPages {
		p.page = res.TotalPages
	}

	// The Type column (and each row's Type cell) is dropped on a type's list.
	cols := []ui.Col{{Key: "name", Label: "Title", Sortable: true}}
	if resolved == nil {
		cols = append(cols, ui.Col{Key: "type", Label: "Type", WideOnly: true})
	}
	// Children: shown when a row on this page has entities under it; the count links to the list filtered to them.
	children := a.childCounts(ctx, res.Items)
	if len(children) > 0 {
		cols = append(cols, ui.Col{Key: "children", Label: "Children", Role: "num", WideOnly: true})
	}
	// Author: only when some row on this page has one.
	hasAuthor := false
	for _, e := range res.Items {
		if (e.BylinePersonID != nil && authors[*e.BylinePersonID] != "") || e.BylineOrg {
			hasAuthor = true
			break
		}
	}
	cols = append(cols, ui.Col{Key: "status", Label: "Status", Sortable: true})
	if hasAuthor {
		cols = append(cols, ui.Col{Key: "author", Label: "Author", WideOnly: true})
	}
	cols = append(cols, ui.Col{Key: "updated", Label: "Updated", Sortable: true})

	back := r.URL.RequestURI()
	t := ui.Table{
		ID: "entities", Caption: caption, BaseURL: a.adminPath + "/entities", CSRF: csrfToken(ctx),
		Columns: cols,
		SortKey: p.sortKey, SortDir: p.dir, Query: p.q, SearchLabel: "Search titles and URLs", PrimaryFilters: primary, PopoverFilters: popover, Chips: chips, ClearHref: p.hrefWithout(a.adminPath, "from", "to", "author", "term"),
		Hidden: hidden,
		Bulk: []ui.BulkAction{
			{Label: "Publish", Action: a.adminPath + "/entities/bulk", Value: "publish"},
			{Label: "Move to draft", Action: a.adminPath + "/entities/bulk", Value: "draft"},
			{Label: "Archive", Action: a.adminPath + "/entities/bulk", Value: "archive"},
			{Label: "Delete", Action: a.adminPath + "/entities/bulk", Value: "delete", Danger: true, Confirm: "Delete {n} selected entities? This cannot be undone."},
		},
		BulkURL: a.adminPath + "/entities/bulk",
		Page:    ui.Pagination{Page: res.Page, PerPage: entitiesPerPage, Total: res.Total, BaseURL: a.adminPath + "/entities", Query: p.query()},
		Empty:   ui.Empty{Icon: "file-text", Title: "No entities match", Body: "Try other filters, or create the first one.", Actions: []ui.Button{{Label: "Create entity", Variant: "primary", Href: a.adminPath + "/entities/new"}}},
	}
	for _, e := range res.Items {
		sc := byID[e.SchemaID]
		typeName := "Unknown"
		if sc != nil {
			typeName = sc.Name
		}
		authorName := "-"
		switch {
		case e.BylinePersonID != nil && authors[*e.BylinePersonID] != "":
			authorName = authors[*e.BylinePersonID]
		case e.BylineOrg:
			authorName = "Site organisation"
		}
		id := e.ID.String()
		visual := usesVisualEditor(sc, e)
		edit := a.entityEditHref(visual, id)
		var path string
		if c.PublicPath != nil {
			e.Schema = sc
			path = c.PublicPath(ctx, e)
		}
		cells := []ui.Cell{{Text: e.Name, Link: edit, Sub: path}}
		if resolved == nil {
			cells = append(cells, ui.Cell{Text: typeName})
		}
		if len(children) > 0 {
			cell := ui.Cell{Text: "-"}
			if n := children[e.ID]; n > 0 {
				cell = ui.Cell{Text: strconv.Itoa(n), Link: a.adminPath + "/entities?under=" + id}
			}
			cells = append(cells, cell)
		}
		cells = append(cells, ui.Cell{Badge: statusBadge(e.Status)})
		if hasAuthor {
			cells = append(cells, ui.Cell{Text: authorName})
		}
		cells = append(cells, ui.Cell{Text: fmtDate(e.UpdatedAt.In(loc)), Nowrap: true})
		row := ui.Row{ID: id, Label: e.Name, Cells: cells, Actions: []ui.Action{{Label: "Edit", Href: edit}}}
		if visual {
			row.Actions = append(row.Actions, ui.Action{Label: "Settings", Href: a.adminPath + "/entities/" + id})
		}
		if e.Status == entity.StatusPublished {
			if path != "" {
				row.Actions = append(row.Actions, ui.Action{Label: "View on site", Href: path})
			}
			row.Actions = append(row.Actions, ui.Action{Label: "Unpublish", Post: a.adminPath + "/entities/" + id + "/status", Fields: map[string]string{"status": "draft", "back": back}})
		} else if e.Status != entity.StatusScheduled {
			row.Actions = append(row.Actions, ui.Action{Label: "Publish", Post: a.adminPath + "/entities/" + id + "/status", Fields: map[string]string{"status": "published", "back": back}})
		}
		row.Actions = append(row.Actions, ui.Action{Label: "Delete", Href: a.adminPath + "/entities/" + id + "/delete?back=" + url.QueryEscape(back), Post: a.adminPath + "/entities/" + id + "/delete", Danger: true,
			ConfirmLabel: "Delete entity", Confirm: "“" + e.Name + "” and its version history will be deleted. This cannot be undone."})
		t.Rows = append(t.Rows, row)
	}
	if resolved != nil {
		t.Empty.Actions = nil // the header's "New <type>" is the call to action; a generic one would create the wrong thing
	}
	a.renderEntities(w, r, p, t, statusTabs, res.Total, types, resolved)
}

func (a *Admin) entityEditHref(visual bool, id string) string {
	if visual {
		return a.adminPath + "/entities/" + id + "/edit"
	}
	return a.adminPath + "/entities/" + id
}

// newTypeAction is the "create one of this type" action: a draft POST for the
// types that have a visual editor (creating a draft is a write, never a link),
// else a link to the new-entity form.
func (a *Admin) newTypeAction(label, slug string, draft bool) ui.Action {
	act := ui.Action{Label: label, Href: a.adminPath + "/entities/new?type=" + url.QueryEscape(slug)}
	if draft {
		act.Href, act.Post, act.Fields = "", a.adminPath+"/entities/drafts", map[string]string{"type": slug}
	}
	return act
}

// renderEntities renders the list page: the unfiltered list (resolved nil) with
// the Create menu, or one type's list with a single "New <type>" button. Both
// carry the Entities subnav built from types.
func (a *Admin) renderEntities(w http.ResponseWriter, r *http.Request, p listParams, t ui.Table, statusTabs []ui.Tab, total int, types []typeTab, resolved *entity.EntitySchema) {
	d := entitiesData{Table: t, StatusTabs: statusTabs, Title: "Entities", Description: "Pages, posts and other content on your site."}
	if total >= 0 {
		d.Description = fmt.Sprintf("%d %s. %s", total, plural(total, "entity", "entities"), d.Description)
	}
	owner := false
	if u := userFrom(r.Context()); u != nil && u.Role == "owner" {
		owner = true
	}
	active := ""
	if resolved != nil {
		active = resolved.Slug
		d.Title, d.Description = typePluralFor(resolved.Slug, resolved.Name), ""
		if total >= 0 {
			d.Description = fmt.Sprintf("%d %s", total, strings.ToLower(plural(total, resolved.Name, typePluralFor(resolved.Slug, resolved.Name))))
		}
		act := a.newTypeAction("New "+strings.ToLower(resolved.Name), resolved.Slug, draftType(resolved))
		act.Icon = "plus"
		d.Primary = &act
	} else {
		create := ui.ButtonMenu{ID: "create-menu", Label: "Create", Icon: "plus"}
		for _, ty := range types {
			item := a.newTypeAction(ty.Name, ty.Slug, ty.Draft)
			item.Icon = "file-text"
			create.Items = append(create.Items, item)
		}
		if owner {
			create.Footer = []ui.Action{{Label: "Manage entity types", Href: a.adminPath + "/settings/types", Icon: "content-model"}}
		}
		if len(create.Items) == 0 {
			create.Items = []ui.Action{{Label: "Choose a type", Href: a.adminPath + "/entities/new"}}
		}
		d.Menu = &create
	}
	v := a.NewView(w, r, d.Title)
	v.entityNav, v.entityManage = entitiesNavIn(a.adminPath, types, active), owner
	v.Data = d
	a.Render(w, r, http.StatusOK, "entities/list", v)
}

// childCounts is the direct child count of each listed entity (absent = none).
// A failed count drops the Children column.
func (a *Admin) childCounts(ctx context.Context, items []*entity.Entity) map[uuid.UUID]int {
	ids := make([]uuid.UUID, 0, len(items))
	for _, e := range items {
		ids = append(ids, e.ID)
	}
	n, err := a.deps.Content.Entities.CountEntityChildren(ctx, ids)
	if err != nil {
		a.logger().Error("count child entities failed", "err", err)
		return nil
	}
	return n
}

// parentFilterOptions lists the entities that have children (the biggest
// maxParentOptions), labelled by their public path, else their name. Choosing one
// keeps everything under it. A failed count shows no filter.
func (a *Admin) parentFilterOptions(ctx context.Context, byID map[uuid.UUID]*entity.EntitySchema, selected string) []ui.Option {
	c := a.deps.Content
	rows, err := c.Entities.CountEntitiesByParent(ctx, maxParentOptions)
	if err != nil {
		a.logger().Error("count entities per parent failed", "err", err)
		return nil
	}
	type opt struct{ label, id string }
	var opts []opt
	for _, r := range rows {
		e := r.Parent
		if e == nil {
			continue
		}
		label := e.Name
		if c.PublicPath != nil {
			e.Schema = byID[e.SchemaID]
			if pth := c.PublicPath(ctx, e); pth != "" && pth != "/" {
				label = pth
			}
		}
		opts = append(opts, opt{fmt.Sprintf("%s (%d)", label, r.Count), e.ID.String()})
	}
	// A parent reached through a Children link may be outside the biggest maxParentOptions: keep it selectable.
	if id, err := uuid.Parse(selected); err == nil && !slices.ContainsFunc(opts, func(o opt) bool { return o.id == selected }) {
		if e, err := c.Entities.GetEntity(ctx, id); err == nil && e != nil {
			label := e.Name
			if c.PublicPath != nil {
				e.Schema = byID[e.SchemaID]
				if pth := c.PublicPath(ctx, e); pth != "" && pth != "/" {
					label = pth
				}
			}
			opts = append(opts, opt{label, selected})
		}
	}
	slices.SortStableFunc(opts, func(x, y opt) int { return strings.Compare(x.label, y.label) })
	out := []ui.Option{{Value: "", Label: "Any parent", Selected: selected == ""}}
	for _, o := range opts {
		out = append(out, ui.Option{Value: o.id, Label: o.label, Selected: selected == o.id})
	}
	return out
}

// typeTab is one entity type tab with its entity count.
type typeTab struct {
	Slug, Name string
	Count      int
	Draft      bool // New creates a draft and opens the visual editor
}

// entityCounts are the list's tab and select counts, from one grouped query.
type entityCounts struct {
	types    []typeTab      // every type, for the tabs and the Create menu
	byStatus map[string]int // status counts, for the selected type (or all)
	all      int            // total for the selected type (or all)
}

// entityCounts runs one count query for the type tabs and the status select.
// A failed query shows zeros: the list itself still renders.
func (a *Admin) entityCounts(ctx context.Context, schemas []*entity.EntitySchema, selected *uuid.UUID) entityCounts {
	out := entityCounts{byStatus: map[string]int{}}
	perType := map[uuid.UUID]int{}
	hidden := map[uuid.UUID]bool{}
	for _, id := range reuseSchemaIDs(schemas) {
		hidden[id] = true
	}
	if rows, err := a.deps.Content.Entities.CountEntitiesBySchemaStatus(ctx); err == nil {
		for _, r := range rows {
			if hidden[r.SchemaID] {
				continue
			}
			perType[r.SchemaID] += r.Count
			if selected == nil || *selected == r.SchemaID {
				out.byStatus[string(r.Status)] += r.Count
				out.all += r.Count
			}
		}
	} else {
		a.logger().Error("count entities failed", "err", err)
	}
	for _, sc := range schemas {
		if hidden[sc.ID] {
			continue
		}
		out.types = append(out.types, typeTab{Slug: sc.Slug, Name: sc.Name, Count: perType[sc.ID], Draft: draftType(sc)})
	}
	return out
}

// handleEntitiesBulk applies one action to the checked entities.
func (a *Admin) handleEntitiesBulk(w http.ResponseWriter, r *http.Request) {
	c := a.deps.Content
	if c.Entities == nil {
		a.unavailable(w, r, "Entities")
		return
	}
	_ = r.ParseForm()
	back := backTo(r, a.adminPath+"/entities", a.adminPath+"/entities")
	ids := r.PostForm["id"]
	if len(ids) == 0 {
		a.SetFlash(w, r, "error", "Select at least one entity first.")
		Redirect(w, r, back)
		return
	}
	if len(ids) > 100 {
		a.SetFlash(w, r, "error", "Select at most 100 entities at a time.")
		Redirect(w, r, back)
		return
	}
	action := r.PostFormValue("action")
	statuses := map[string]entity.EntityStatus{"publish": entity.StatusPublished, "draft": entity.StatusDraft, "archive": entity.StatusArchived}
	if action == "delete" && r.PostFormValue("confirmed") != "1" {
		hidden := []hiddenField{{"action", "delete"}, {"confirmed", "1"}, {"back", back}}
		for _, id := range ids {
			hidden = append(hidden, hiddenField{"id", id})
		}
		noun := "entity"
		if len(ids) != 1 {
			noun = "entities"
		}
		a.renderConfirm(w, r, confirmData{
			Title: fmt.Sprintf("Delete %d %s?", len(ids), noun), Message: "They and their version history will be deleted. This cannot be undone.",
			Post: a.adminPath + "/entities/bulk", Label: "Delete", Back: back, Danger: true, Hidden: hidden,
		})
		return
	}
	st, isStatus := statuses[action]
	if !isStatus && action != "delete" {
		a.SetFlash(w, r, "error", "Choose an action for the selected entities.")
		Redirect(w, r, back)
		return
	}
	done, failed := 0, 0
	var firstErr string
	for _, raw := range ids {
		id, err := uuid.Parse(raw)
		if err != nil {
			failed++
			continue
		}
		if isStatus {
			if err = a.refuseGeneratedSlug(r, id, st); err == nil {
				var e *entity.Entity
				if e, err = c.Entities.UpdateEntityStatus(r.Context(), id, actorID(r), st); err == nil {
					a.auditEntity(r, auditStatusAction(st), id, e, map[string]any{"bulk": true})
				}
			}
		} else {
			e := a.entityForAudit(r, id)
			if err = c.Entities.DeleteEntity(r.Context(), id); err == nil {
				a.auditEntity(r, auditEntityDelete, id, e, map[string]any{"bulk": true})
			}
		}
		if err != nil {
			failed++
			if firstErr == "" {
				firstErr = entityErrorText(err)
				if errors.Is(err, errGeneratedSlug) {
					firstErr = err.Error()
				}
			}
			continue
		}
		done++
	}
	msg := fmt.Sprintf("%d %s.", done, bulkVerb(action, done))
	if failed > 0 {
		msg += fmt.Sprintf(" %d could not be changed: %s", failed, firstErr)
		a.SetFlash(w, r, "error", msg)
	} else {
		a.SetFlash(w, r, "success", msg)
	}
	Redirect(w, r, back)
}

func bulkVerb(action string, n int) string {
	noun := "entities"
	if n == 1 {
		noun = "entity"
	}
	switch action {
	case "publish":
		return noun + " published"
	case "draft":
		return noun + " moved to draft"
	case "archive":
		return noun + " archived"
	}
	return noun + " deleted"
}

// entityErrorText turns a service error into a sentence for the editor.
func entityErrorText(err error) string {
	switch {
	case errors.Is(err, entity.ErrHasChildren):
		return "it has child entities: move or delete them first."
	case errors.Is(err, entity.ErrEntityNotFound):
		return "it no longer exists."
	case errors.Is(err, entity.ErrSlugAlreadyExists):
		return "that slug is already used."
	case errors.Is(err, entity.ErrParentCycle):
		return "the parent would make the entity its own ancestor."
	case errors.Is(err, entity.ErrParentTooDeep):
		return "nesting would be too deep."
	case errors.Is(err, entity.ErrParentNotFound):
		return "the parent no longer exists."
	case errors.Is(err, entity.ErrAuthorNotFound):
		return "the author no longer exists."
	case errors.Is(err, entity.ErrScheduleInPast):
		return "the scheduled time must be in the future."
	case errors.Is(err, entity.ErrRejectedByPlugin), errors.Is(err, entity.ErrInvalidDoc), errors.Is(err, entity.ErrPageSectionsRemoved):
		return sentence(err.Error())
	}
	return "something went wrong."
}

// handleEntityStatus is the one-click status change of a list row.
func (a *Admin) handleEntityStatus(w http.ResponseWriter, r *http.Request) {
	c := a.deps.Content
	id, ok := uuidParam(r, "id")
	if !ok || c.Entities == nil {
		a.notFound(w, r, "Entity", a.adminPath+"/entities", "Back to entities")
		return
	}
	st, err := entity.ParseStatus(r.PostFormValue("status"))
	if err != nil || st == entity.StatusScheduled {
		a.Error(w, r, http.StatusBadRequest, "Invalid status", "Choose published, draft or archived.")
		return
	}
	back := backTo(r, a.adminPath+"/entities", a.adminPath+"/entities/"+id.String())
	if err := a.refuseGeneratedSlug(r, id, st); err != nil {
		a.SetFlash(w, r, "error", err.Error())
		Redirect(w, r, back)
		return
	}
	e, err := c.Entities.UpdateEntityStatus(r.Context(), id, actorID(r), st)
	if err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			a.notFound(w, r, "Entity", a.adminPath+"/entities", "Back to entities")
			return
		}
		a.serverError(w, r, "The status could not be changed", err)
		return
	}
	a.auditEntity(r, auditStatusAction(st), id, e, nil)
	a.SetFlash(w, r, "success", "Status changed to "+string(st)+".")
	Redirect(w, r, back)
}

// entityBack is where a delete returns to: the entity list or, for a block
// deleted from the Blocks page, that page.
func (a *Admin) entityBack(r *http.Request, fallback string) string {
	if b := r.FormValue("back"); b == a.adminPath+blocksPath {
		return b
	}
	return backTo(r, a.adminPath+"/entities", fallback)
}

func (a *Admin) handleEntityDeleteConfirm(w http.ResponseWriter, r *http.Request) {
	c := a.deps.Content
	id, ok := uuidParam(r, "id")
	if !ok || c.Entities == nil {
		a.notFound(w, r, "Entity", a.adminPath+"/entities", "Back to entities")
		return
	}
	e, err := c.Entities.GetEntity(r.Context(), id)
	if err != nil {
		a.notFound(w, r, "Entity", a.adminPath+"/entities", "Back to entities")
		return
	}
	back := a.entityBack(r, a.adminPath+"/entities/"+id.String())
	a.renderConfirm(w, r, confirmData{
		Title: "Delete “" + e.Name + "”?", Message: "The entity and its version history will be deleted. This cannot be undone.",
		Post: a.adminPath + "/entities/" + id.String() + "/delete", Label: "Delete entity", Back: back, Danger: true,
		Hidden: []hiddenField{{"back", back}},
	})
}

func (a *Admin) handleEntityDelete(w http.ResponseWriter, r *http.Request) {
	c := a.deps.Content
	id, ok := uuidParam(r, "id")
	if !ok || c.Entities == nil {
		a.notFound(w, r, "Entity", a.adminPath+"/entities", "Back to entities")
		return
	}
	back := a.entityBack(r, a.adminPath+"/entities")
	gone := a.entityForAudit(r, id)
	if err := c.Entities.DeleteEntity(r.Context(), id); err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			a.notFound(w, r, "Entity", a.adminPath+"/entities", "Back to entities")
			return
		}
		if errors.Is(err, entity.ErrHasChildren) {
			a.SetFlash(w, r, "error", "That entity has child entities: move or delete them first.")
			Redirect(w, r, a.adminPath+"/entities/"+id.String())
			return
		}
		a.serverError(w, r, "The entity could not be deleted", err)
		return
	}
	a.auditEntity(r, auditEntityDelete, id, gone, nil)
	a.SetFlash(w, r, "success", "Entity deleted.")
	if strings.HasPrefix(back, a.adminPath+"/entities/"+id.String()) {
		back = a.adminPath + "/entities"
	}
	Redirect(w, r, back)
}

// termFilterOptions are the term filter's options, indented by depth.
func (a *Admin) termFilterOptions(ctx context.Context, selected string) []ui.Option {
	nodes, err := a.termNodes(ctx)
	if err != nil || len(nodes) == 0 {
		return nil
	}
	opts := []ui.Option{{Value: "", Label: "All terms", Selected: selected == ""}}
	for _, n := range nodes {
		opts = append(opts, ui.Option{Value: n.ID.String(), Label: n.Kind + ": " + strings.Repeat("— ", n.Depth) + n.Name, Selected: selected == n.ID.String()})
	}
	return opts
}

// errGeneratedSlug is why a draft with its generated address is not published.
//
//lint:ignore ST1005 shown to the editor verbatim as a sentence
var errGeneratedSlug = errors.New("Change the generated address (untitled-...) under Details before publishing.")

// generatedSlug reports whether slug is the placeholder a new draft is created with.
func generatedSlug(slug string) bool { return strings.HasPrefix(slug, "untitled-") }

// refuseGeneratedSlug stops a publish while the entity still has its generated
// address, so a placeholder never goes live. Any other status change passes.
func (a *Admin) refuseGeneratedSlug(r *http.Request, id uuid.UUID, st entity.EntityStatus) error {
	if st != entity.StatusPublished {
		return nil
	}
	if e, err := a.deps.Content.Entities.GetEntity(r.Context(), id); err == nil && generatedSlug(e.Slug) {
		return errGeneratedSlug
	}
	return nil
}
