package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/render"
)

const (
	maxMenuItems = 200
	maxMenuDepth = 5
	// menuLimitNotice is the refusal for an edit that would pass maxMenuItems; the
	// editor script says the same (menutree.js).
	menuLimitNotice = "A menu holds at most 200 items."
	// The menu and menu_items columns: name and label varchar(100), url varchar(500).
	maxNameLen  = 100
	maxLabelLen = 100
	maxURLLen   = 500
)

// menuTypes are the kinds of target a menu item can have, as the form names
// them. They map to menu link types: entity -> entity; custom -> external for
// an absolute address and custom for a path; archive -> archive (a term's
// archive page, stored as its path). An item may have no target yet: it is
// stored as the type chosen and shown with a warning. The form also knows the
// type none (plain text).
var menuTypes = []ui.Option{
	{Value: "entity", Label: "A page or post"},
	{Value: "custom", Label: "A custom address"},
	{Value: "archive", Label: "A category or tag archive"},
}

// menuNoLinkWarning is shown on an item that will render as plain text.
const menuNoLinkWarning = "This item has no link, so it shows as plain text."

// menuItemErrKeys are the per-item field errors in the order the summary lists them.
var menuItemErrKeys = []string{"label", "entity", "url", "term", "class", "style", "color", "badge", "icon", "show", "id", "title", "aria", "rel"}

// menuRow is one item as the editor shows it: the tree flattened in reading
// order, with Depth giving the nesting.
type menuRow struct {
	N                int
	Label, Type      string
	Entity, EntityLb string
	EntityQ, TermQ   string // text typed in an unconfirmed search box (no JavaScript)
	URL              string
	Term, TermLb     string
	Depth            int
	NewTab           bool
	Subtree          bool // has sub-items (Remove takes them along)
	TypeIcon, Target string
	NoIndent         bool // already the deepest level allowed, or nothing above to nest under
	NoOutdent        bool
	Types            []ui.Option
	Err              map[string]string
	// Warn is set (not Err) when the item has no link: it saves and renders as text.
	Warn string
	// Class holds the item's own CSS classes. Style, Color and Icon are preset
	// ids of the active theme, Badge a badge preset id, Show is
	// all, desktop or mobile. HTMLID, Title and Aria are the id, title and
	// aria-label attributes, Rel the link relations.
	Class, Style, Color, Badge, Icon, Show string
	HTMLID, Title, Aria                    string
	Rel                                    []string
}

type menuForm struct {
	ID, Name, Location string
	New                bool
	Items              []menuRow
	// Blank is the row the editor script copies when it adds an item without a
	// round trip (the page keeps it in a template, never in the visible form).
	Blank menuRow
	// StyleItems and ColorItems are the theme's styles and colours as the items
	// of the inspector's tile and swatch pickers; empty when the theme has none.
	StyleItems, ColorItems []map[string]any
	Locations              []ui.Option
	Errors                 map[string]string
	Summary                []ui.FieldError
	AddType                string
	AddTypes               []ui.Option
	Notice                 string // structural edit applied but not saved yet
	// NoticeKind is "warning" when the edit was refused (the rows did not
	// change) and "info" otherwise.
	NoticeKind string
	// Decl is what the active theme declares for menu items; zero when it
	// declares nothing, so the editor offers only classes and advanced fields.
	Decl render.MenuDecl
	// MaxDepth and MaxItems are the limits the editor enforces; the page
	// hands them to the script so they are defined once.
	MaxDepth, MaxItems int
}

func (a *Admin) mountMenus(authed *httpx.Group) {
	authed.HandleFunc("GET /theme/menus", a.handleMenus)
	authed.HandleFunc("GET /theme/menus/new", a.handleMenuNew)
	authed.HandleFunc("POST /theme/menus", a.handleMenuCreate)
	authed.HandleFunc("GET /theme/menus/{id}", a.handleMenuEdit)
	authed.HandleFunc("POST /theme/menus/preview", a.handleMenuPreview)
	authed.HandleFunc("POST /theme/menus/{id}", a.handleMenuSave)
	authed.HandleFunc("GET /theme/menus/{id}/delete", a.handleMenuDeleteForm)
	authed.HandleFunc("POST /theme/menus/{id}/delete", a.handleMenuDelete)
	authed.HandleFunc("GET /pick/entities", a.handlePickEntities)
	authed.HandleFunc("GET /pick/terms", a.handlePickTerms)
}

func locationOptions(selected string) []ui.Option {
	return []ui.Option{
		{Value: "", Label: "No location", Selected: selected == ""},
		{Value: menu.LocationHeader, Label: "Header", Selected: selected == menu.LocationHeader},
		{Value: menu.LocationFooter, Label: "Footer", Selected: selected == menu.LocationFooter},
		{Value: menu.LocationSidebar, Label: "Sidebar", Selected: selected == menu.LocationSidebar},
	}
}

// typeOptions lists the types, "No link" last, with selected marked.
func typeOptions(selected string) []ui.Option {
	out := make([]ui.Option, len(menuTypes), len(menuTypes)+1)
	for i, o := range menuTypes {
		o.Selected = o.Value == selected
		out[i] = o
	}
	return append(out, ui.Option{Value: menu.LinkTypeNone, Label: "No link", Selected: selected == menu.LinkTypeNone})
}

func locationLabel(loc *string) string {
	if loc == nil || *loc == "" {
		return "None"
	}
	return strings.ToUpper((*loc)[:1]) + (*loc)[1:]
}

func (a *Admin) handleMenus(w http.ResponseWriter, r *http.Request) {
	if a.deps.Growth.Menus == nil {
		a.unavailable(w, r, "Menus")
		return
	}
	menus, err := a.deps.Growth.Menus.ListMenus(r.Context())
	if err != nil {
		a.logger().Error("list menus failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The menus could not be loaded.")
		return
	}
	created := createdID(r)
	t := ui.Table{
		ID: "menus", CreatedID: created, Caption: "Menus", BaseURL: a.adminPath + "/theme/menus", CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "name", Label: "Name"}, {Key: "location", Label: "Location"}, {Key: "slug", Label: "Identifier"}},
		Empty:   ui.Empty{Icon: "menu", Title: "No menus yet", Body: "Create a menu, then choose where it appears."},
	}
	for _, m := range menus {
		id := m.ID.String()
		kind := "neutral"
		if m.Location != nil && *m.Location != "" {
			kind = "info"
		}
		t.Rows = append(t.Rows, ui.Row{ID: id, Label: m.Name, Cells: []ui.Cell{
			{Text: m.Name, Link: a.adminPath + "/theme/menus/" + id},
			{Badge: &ui.Badge{Label: locationLabel(m.Location), Kind: kind}},
			{Text: m.Slug},
		}, Actions: []ui.Action{
			{Label: "Edit", Href: a.adminPath + "/theme/menus/" + id},
			{Label: "Delete", Href: a.adminPath + "/theme/menus/" + id + "/delete", Post: a.adminPath + "/theme/menus/" + id + "/delete", Danger: true, ConfirmLabel: "Delete menu",
				Confirm: "The menu " + m.Name + " and its items are removed. Pages that show it lose the navigation."},
		}})
	}
	t.Rows = pinFirst(t.Rows, created)
	v := a.NewView(w, r, "Menus")
	v.Data = struct {
		Table     ui.Table
		Locations []ui.Option
		Errors    map[string]string
		Name      string
	}{Table: t, Locations: locationOptions("")}
	a.Render(w, r, http.StatusOK, "menus/list", v)
}

func (a *Admin) handleMenuCreate(w http.ResponseWriter, r *http.Request) {
	if a.deps.Growth.Menus == nil {
		a.unavailable(w, r, "Menus")
		return
	}
	name, loc := strings.TrimSpace(r.PostFormValue("name")), r.PostFormValue("location")
	f := menuForm{Name: name, Location: loc, New: true, Errors: map[string]string{}}
	switch {
	case name == "":
		f.Errors["name"] = "Give the menu a name."
	case utf8.RuneCountInString(name) > maxNameLen:
		f.Errors["name"] = "Use up to 100 characters."
	}
	if !menu.ValidLocation(loc) {
		f.Errors["location"] = "Choose one of the listed locations."
	}
	if len(f.Errors) > 0 {
		a.renderMenuNew(w, r, f)
		return
	}
	in := &menu.CreateMenuInput{Name: name}
	if loc != "" {
		in.Location = &loc
	}
	m, err := a.deps.Growth.Menus.CreateMenu(r.Context(), in)
	switch {
	case errors.Is(err, menu.ErrSlugAlreadyExists):
		f.Errors["name"] = "A menu with that name already exists. Choose another name."
		a.renderMenuNew(w, r, f)
		return
	case err != nil:
		a.logger().Error("create menu failed", "err", err)
		f.Errors["name"] = "The menu could not be created. Try again."
		a.renderMenuNew(w, r, f)
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Menu created.")
	Redirect(w, r, createdRedirect(a.adminPath+"/theme/menus", m.ID.String()))
}

func (a *Admin) handleMenuNew(w http.ResponseWriter, r *http.Request) {
	if a.deps.Growth.Menus == nil {
		a.unavailable(w, r, "Menus")
		return
	}
	a.renderMenuNew(w, r, menuForm{New: true})
}

// renderMenuNew shows the no-JS create page; with errors it is a 422 that keeps what was typed.
func (a *Admin) renderMenuNew(w http.ResponseWriter, r *http.Request, f menuForm) {
	f.Locations = locationOptions(f.Location)
	status := http.StatusOK
	if len(f.Errors) > 0 {
		status = http.StatusUnprocessableEntity
		for _, k := range []string{"name", "location"} {
			if m := f.Errors[k]; m != "" {
				id := "menu-" + k
				f.Summary = append(f.Summary, ui.FieldError{ID: id, Label: strings.ToUpper(k[:1]) + k[1:], Message: m})
			}
		}
	}
	v := a.NewView(w, r, "New menu")
	v.Crumbs = a.areaCrumbs("menus", Crumb{Label: "New menu"}) // the same trail for /new and the 422 re-render
	v.Data = f
	a.Render(w, r, status, "menus/new", v)
}

// flattenMenu lists the item tree in reading order with depths.
func flattenMenu(items []*menu.MenuItem, depth int, out *[]menuRow) {
	for _, it := range items {
		row := menuRow{Label: it.Label, Depth: depth, NewTab: it.OpenInNewTab, Subtree: len(it.Children) > 0}
		a := it.Attrs
		row.Style, row.Color, row.Badge, row.Icon, row.Show = a.Style, a.Color, a.Badge, a.Icon, a.ShowOn
		row.HTMLID, row.Title, row.Aria, row.Rel = a.ID, a.Title, a.AriaLabel, a.Rel
		if it.CSSClasses != nil {
			row.Class = *it.CSSClasses
		}
		switch {
		case it.LinkType == menu.LinkTypeNone:
			row.Type = menu.LinkTypeNone
		case it.LinkType == menu.LinkTypeEntity:
			row.Type = "entity"
			if it.EntityID != nil {
				row.Entity = it.EntityID.String()
			}
			if it.EntityName != nil {
				row.EntityLb = *it.EntityName
			}
		case it.LinkType == menu.LinkTypeArchive:
			row.Type = "archive"
			if it.URL != nil {
				row.Term, row.TermLb = *it.URL, *it.URL
			}
		default:
			row.Type = "custom"
			if it.URL != nil {
				row.URL = *it.URL
			}
		}
		row.Warn = menuRowWarning(row)
		*out = append(*out, row)
		flattenMenu(it.Children, depth+1, out)
	}
}

func (a *Admin) loadMenu(w http.ResponseWriter, r *http.Request) (*menu.Menu, bool) {
	if a.deps.Growth.Menus == nil {
		a.unavailable(w, r, "Menus")
		return nil, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err == nil {
		var m *menu.Menu
		if m, err = a.deps.Growth.Menus.GetMenuWithItems(r.Context(), id); err == nil {
			return m, true
		}
		if !errors.Is(err, menu.ErrMenuNotFound) {
			a.logger().Error("load menu failed", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The menu could not be loaded.")
			return nil, false
		}
	}
	a.ErrorBack(w, r, http.StatusNotFound, "Menu not found", "That menu does not exist.", a.adminPath+"/theme/menus", "Back to menus")
	return nil, false
}

// menuRowWarning is the warning of a row that has no target to link to; none
// for a No-link row, which is plain text on purpose.
func menuRowWarning(r menuRow) string {
	switch {
	case r.Type == menu.LinkTypeNone:
		return ""
	case r.Type == "entity" && r.Entity == "", r.Type == "archive" && r.Term == "", r.Type == "custom" && r.URL == "":
		return menuNoLinkWarning
	}
	return ""
}

// menuTarget is the row's type icon and a short summary of where it links, as
// "Page or post · About us". The name is left out when it repeats the item's own title.
func menuTarget(r menuRow) (icon, target string) {
	icon, kind, name := "link", "Link", r.URL
	switch r.Type {
	case menu.LinkTypeNone:
		return "minus", "No link"
	case "entity":
		icon, kind, name = "file-text", "Page or post", r.EntityLb
	case "archive":
		icon, kind, name = "tag", "Archive", r.TermLb
	}
	if name == "" || name == r.Label {
		return icon, kind
	}
	return icon, kind + " · " + name
}

func (a *Admin) handleMenuEdit(w http.ResponseWriter, r *http.Request) {
	m, ok := a.loadMenu(w, r)
	if !ok {
		return
	}
	f := menuForm{ID: m.ID.String(), Name: m.Name}
	if m.Location != nil {
		f.Location = *m.Location
	}
	flattenMenu(m.Items, 0, &f.Items)
	a.renderMenuForm(w, r, http.StatusOK, f)
}

// pickerItems lists a theme's presets for a tile or swatch picker, led by the
// "Default" choice that stores nothing. A theme with no presets gets no picker.
func pickerItems(list []render.MenuPreset) []map[string]any {
	if len(list) == 0 {
		return nil
	}
	out := []map[string]any{{"Label": "Default", "Value": ""}}
	for _, p := range list {
		out = append(out, map[string]any{"Label": p.Label, "Value": p.ID, "Swatch": p.Swatch})
	}
	return out
}

func (a *Admin) renderMenuForm(w http.ResponseWriter, r *http.Request, status int, f menuForm) {
	f.Locations = locationOptions(f.Location)
	f.AddTypes = typeOptions(f.AddType)
	f.MaxDepth, f.MaxItems = maxMenuDepth, maxMenuItems
	if a.deps.Content.MenuTheme != nil {
		f.Decl = a.deps.Content.MenuTheme()
	}
	for i := range f.Items {
		f.Items[i].N = i
		if len(f.Items[i].Err) == 0 {
			f.Items[i].Warn = menuRowWarning(f.Items[i])
		}
		f.Items[i].Types = typeOptions(f.Items[i].Type)
		f.Items[i].Subtree = i+1 < len(f.Items) && f.Items[i+1].Depth > f.Items[i].Depth
		f.Items[i].TypeIcon, f.Items[i].Target = menuTarget(f.Items[i])
		f.Items[i].NoOutdent = f.Items[i].Depth == 0
		f.Items[i].NoIndent = i == 0 || f.Items[i].Depth > f.Items[i-1].Depth || f.Items[i].Depth >= maxMenuDepth
	}
	// N is past the last row a menu can hold, so the ids in the copy are unique on the page;
	// the script renumbers the copy when it is added.
	f.Blank = menuRow{N: maxMenuItems, Type: "entity", NoIndent: true, NoOutdent: true}
	f.Blank.Types = typeOptions(f.Blank.Type)
	f.Blank.TypeIcon, f.Blank.Target = menuTarget(f.Blank)
	f.StyleItems = pickerItems(f.Decl.Styles)
	f.ColorItems = pickerItems(f.Decl.Colors)
	f.Summary = nil
	if m := f.Errors["name"]; m != "" {
		f.Summary = append(f.Summary, ui.FieldError{ID: "name", Label: "Name", Message: m})
	}
	for _, it := range f.Items {
		if len(it.Err) == 0 {
			continue
		}
		for _, k := range menuItemErrKeys {
			if m := it.Err[k]; m != "" {
				f.Summary = append(f.Summary, ui.FieldError{ID: "i-" + strconv.Itoa(it.N) + "-" + k, Label: "Item " + strconv.Itoa(it.N+1), Message: m})
			}
		}
	}
	v := a.NewView(w, r, "Edit menu: "+f.Name)
	v.Crumbs = a.areaCrumbs("menus", Crumb{Label: f.Name})
	v.Data = f
	a.Render(w, r, status, "menus/edit", v)
}

// parseMenuItems reads the submitted rows (i.N.field), ordered by N.
func parseMenuItems(form url.Values) []menuRow {
	seen := map[int]bool{}
	for k := range form {
		if rest, ok := strings.CutPrefix(k, "i."); ok {
			if n, _, ok := strings.Cut(rest, "."); ok {
				if i, err := strconv.Atoi(n); err == nil && i >= 0 && i < 10000 {
					seen[i] = true
				}
			}
		}
	}
	ns := make([]int, 0, len(seen))
	for n := range seen {
		ns = append(ns, n)
	}
	sort.Ints(ns)
	rows := make([]menuRow, 0, len(ns))
	for _, n := range ns {
		p := "i." + strconv.Itoa(n) + "."
		g := func(k string) string { return strings.TrimSpace(form.Get(p + k)) }
		d, _ := strconv.Atoi(g("depth"))
		typ := g("type")
		if typ != "custom" && typ != "archive" && typ != menu.LinkTypeNone {
			typ = "entity"
		}
		rows = append(rows, menuRow{
			Label: g("label"), Type: typ, Entity: g("entity"), EntityLb: g("entity_prev"), URL: g("url"),
			Term: g("term"), TermLb: g("term_prev"), EntityQ: g("entity_q"), TermQ: g("term_q"), Depth: d, NewTab: g("newtab") != "",
			Class: g("class"), Style: g("style"), Color: g("color"), Badge: g("badge"), Icon: g("icon"), Show: g("show"),
			HTMLID: g("id"), Title: g("title"), Aria: g("aria"), Rel: splitRel(form[p+"rel"]),
		})
	}
	normaliseDepths(rows)
	return rows
}

// splitRel reads the rel values of a row: one field per relation (switches) or
// one comma or space separated field. Duplicates are dropped.
func splitRel(vals []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range vals {
		for _, r := range strings.FieldsFunc(strings.ToLower(v), func(c rune) bool { return c == ',' || c == ' ' }) {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	return out
}

// normaliseDepths keeps the tree valid: the first item is top level and an item
// is at most one level below the one before it.
func normaliseDepths(rows []menuRow) {
	prev := -1
	for i := range rows {
		d := rows[i].Depth
		if d < 0 {
			d = 0
		}
		if d > prev+1 {
			d = prev + 1
		}
		if d > maxMenuDepth {
			d = maxMenuDepth
		}
		rows[i].Depth, prev = d, d
	}
}

// blockEnd is the index after row i and its sub-items.
func blockEnd(rows []menuRow, i int) int {
	j := i + 1
	for j < len(rows) && rows[j].Depth > rows[i].Depth {
		j++
	}
	return j
}

// applyMenuOp performs one structural edit named "<op>.<index>" (or "add").
// target is the chosen destination of "moveto": the index of a top-level row, or "top".
func applyMenuOp(rows []menuRow, op, addType, target string) ([]menuRow, string) {
	if op == "add" {
		if len(rows) >= maxMenuItems {
			return rows, menuLimitNotice
		}
		if addType != "custom" && addType != "archive" && addType != menu.LinkTypeNone {
			addType = "entity"
		}
		row := menuRow{Type: addType}
		row.Warn = menuRowWarning(row)
		return append(rows, row), "Item added. It is not saved yet."
	}
	if op == "refresh" {
		return rows, "Link fields updated. It is not saved yet."
	}
	name, idx, ok := strings.Cut(op, ".")
	i, err := strconv.Atoi(idx)
	if !ok || err != nil || i < 0 || i >= len(rows) {
		return rows, ""
	}
	end := blockEnd(rows, i)
	d := rows[i].Depth
	switch name {
	case "remove":
		// The item goes with everything under it.
		return append(rows[:i:i], rows[end:]...), "Item removed with its sub-items. It is not saved yet."
	case "dup":
		if len(rows)+end-i > maxMenuItems {
			return rows, menuLimitNotice
		}
		out := make([]menuRow, 0, len(rows)+end-i)
		out = append(out, rows[:end]...)
		for _, r := range rows[i:end] {
			r.Err, r.HTMLID = nil, "" // an id is unique on the page
			out = append(out, r)
		}
		return append(out, rows[end:]...), "Item duplicated. It is not saved yet."
	case "moveto":
		return moveMenuBlock(rows, i, end, target)
	case "indent":
		if i == 0 || d > rows[i-1].Depth || d >= maxMenuDepth {
			return rows, "This item cannot be indented: it needs an item above it to go under."
		}
		for k := i; k < end; k++ {
			rows[k].Depth++
		}
		return rows, "Item indented. It is not saved yet."
	case "outdent":
		if d == 0 {
			return rows, "This item is already at the top level."
		}
		// The item and its sub-items go right after the parent's group; the
		// siblings that followed it stay under the parent.
		p := i - 1
		for rows[p].Depth >= d {
			p--
		}
		pend := blockEnd(rows, p)
		out := make([]menuRow, 0, len(rows))
		out = append(out, rows[:i]...)
		out = append(out, rows[end:pend]...)
		for _, r := range rows[i:end] {
			r.Depth--
			out = append(out, r)
		}
		return append(out, rows[pend:]...), "Item moved out. It is not saved yet."
	case "up":
		start := -1
		for k := i - 1; k >= 0; k-- {
			if rows[k].Depth < d {
				break
			}
			if rows[k].Depth == d {
				start = k
				break
			}
		}
		if start < 0 {
			return rows, "This item is already first."
		}
		return swapBlocks(rows, start, i, end), "Item moved up. It is not saved yet."
	case "down":
		if end >= len(rows) || rows[end].Depth != d {
			return rows, "This item is already last."
		}
		return swapBlocks(rows, i, end, blockEnd(rows, end)), "Item moved down. It is not saved yet."
	}
	return rows, ""
}

// moveMenuBlock moves rows[i:end] (an item and its sub-items) to the end of the
// group of the top-level row named by target, or to the end of the top level
// for "top".
func moveMenuBlock(rows []menuRow, i, end int, target string) ([]menuRow, string) {
	t := -1
	if target != "top" {
		n, err := strconv.Atoi(target)
		if err != nil || n < 0 || n >= len(rows) || rows[n].Depth != 0 {
			return rows, "Choose where to move this item, then use Move to again."
		}
		if n >= i && n < end {
			return rows, "An item cannot move under itself."
		}
		t = n
	}
	newDepth := 0
	if t >= 0 {
		newDepth = 1
	}
	delta := newDepth - rows[i].Depth
	for _, r := range rows[i:end] {
		if r.Depth+delta > maxMenuDepth {
			return rows, "This item has too many levels under it to move there."
		}
	}
	rest := make([]menuRow, 0, len(rows))
	rest = append(rest, rows[:i]...)
	rest = append(rest, rows[end:]...)
	at := len(rest)
	if t >= 0 {
		if t > i {
			t -= end - i
		}
		at = blockEnd(rest, t)
	}
	out := make([]menuRow, 0, len(rows))
	out = append(out, rest[:at]...)
	for _, r := range rows[i:end] {
		r.Depth += delta
		out = append(out, r)
	}
	return append(out, rest[at:]...), "Item moved. It is not saved yet."
}

// swapBlocks exchanges rows[a:b] and rows[b:c].
func swapBlocks(rows []menuRow, a, b, c int) []menuRow {
	out := make([]menuRow, 0, len(rows))
	out = append(out, rows[:a]...)
	out = append(out, rows[b:c]...)
	out = append(out, rows[a:b]...)
	return append(out, rows[c:]...)
}

func (a *Admin) handleMenuSave(w http.ResponseWriter, r *http.Request) {
	m, ok := a.loadMenu(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	f := menuForm{ID: m.ID.String(), Name: strings.TrimSpace(r.PostFormValue("name")), Location: r.PostFormValue("location"),
		Items: parseMenuItems(r.PostForm), Errors: map[string]string{}, AddType: r.PostFormValue("add_type")}
	if op := r.PostFormValue("_op"); op != "" && op != "save" {
		before := slices.Clone(f.Items)
		f.Items, f.Notice = applyMenuOp(f.Items, op, f.AddType, r.PostFormValue("moveto_target"))
		f.NoticeKind = menuNoticeKind(op, f.Notice, before, f.Items)
		normaliseDepths(f.Items)
		f.Errors = nil
		a.renderMenuForm(w, r, http.StatusOK, f)
		return
	}
	if f.Name == "" {
		f.Errors["name"] = "Enter a name"
	} else if utf8.RuneCountInString(f.Name) > maxNameLen {
		f.Errors["name"] = "Use at most 100 characters"
	}
	if !menu.ValidLocation(f.Location) {
		f.Location = ""
	}
	if len(f.Items) > maxMenuItems {
		f.Errors["name"] = "A menu holds at most 200 items"
		a.renderMenuForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	items := a.menuReplaceItems(r.Context(), f.Items)
	for _, it := range items.rows {
		if len(it.Err) > 0 {
			f.Errors["items"] = "Some items need attention."
		}
	}
	f.Items = items.rows
	if len(f.Errors) > 0 {
		a.renderMenuForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	loc := f.Location
	_, err := a.deps.Growth.Menus.ReplaceMenu(r.Context(), m.ID, &menu.UpdateMenuInput{Name: &f.Name, Location: &loc}, items.out)
	switch {
	case errors.Is(err, menu.ErrEntityMissing):
		f.Errors["name"] = "One of the linked pages no longer exists. Choose it again."
		a.renderMenuForm(w, r, http.StatusUnprocessableEntity, f)
		return
	case errors.Is(err, menu.ErrMenuNotFound):
		a.ErrorBack(w, r, http.StatusNotFound, "Menu not found", "That menu was deleted.", a.adminPath+"/theme/menus", "Back to menus")
		return
	case err != nil:
		// Keep the editor's tree: a failed save never costs the unsaved rows.
		a.logger().Error("save menu failed", "err", err)
		f.Errors["name"] = "The menu could not be saved. Your changes are still here; try again."
		a.renderMenuForm(w, r, http.StatusInternalServerError, f)
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Menu saved.")
	http.Redirect(w, r, a.adminPath+"/theme/menus/"+m.ID.String(), http.StatusSeeOther)
}

// menuNoticeKind is "warning" for a refused structural edit: a message with
// the rows unchanged. refresh is never a refusal.
func menuNoticeKind(op, notice string, before, after []menuRow) string {
	if notice == "" || op == "refresh" {
		return "info"
	}
	if slices.EqualFunc(before, after, func(a, b menuRow) bool { return reflect.DeepEqual(a, b) }) {
		return "warning"
	}
	return "info"
}

type replaceResult struct {
	rows []menuRow
	out  []menu.ReplaceItem
}

// safeMenuURL accepts a site path, an anchor, or an address with a scheme that
// cannot run script. javascript: and data: never get into a menu.
func safeMenuURL(u string) bool {
	if u == "" || utf8.RuneCountInString(u) > maxURLLen || strings.ContainsAny(u, " \t\r\n\\<>\"") {
		return false
	}
	if strings.HasPrefix(u, "//") {
		return false
	}
	if u[0] == '/' || u[0] == '#' || u[0] == '?' {
		return true
	}
	p, err := url.Parse(u)
	if err != nil {
		return false
	}
	switch strings.ToLower(p.Scheme) {
	case "http", "https":
		return p.Host != ""
	case "mailto", "tel":
		return p.Opaque != ""
	}
	return false
}

// menuReplaceItems validates the rows (recording per-field errors on each) and
// builds the ReplaceMenu items: Parent is the index of the nearest earlier item
// one level up.
func (a *Admin) menuReplaceItems(ctx context.Context, rows []menuRow) replaceResult {
	res := replaceResult{rows: rows}
	parents := make([]int, maxMenuDepth+2)
	for i := range rows {
		it := &rows[i]
		it.Err = map[string]string{}
		if it.Label == "" {
			it.Err["label"] = "Enter a label"
		} else if utf8.RuneCountInString(it.Label) > maxLabelLen {
			it.Err["label"] = "Use at most 100 characters"
		}
		ri := menu.ReplaceItem{Label: it.Label, OpenInNewTab: it.NewTab, Parent: -1}
		if it.Depth > 0 {
			ri.Parent = parents[it.Depth-1]
		}
		parents[it.Depth] = i
		it.Warn = ""
		switch it.Type {
		case menu.LinkTypeNone:
			ri.LinkType = menu.LinkTypeNone
		case "entity":
			ri.LinkType = menu.LinkTypeEntity
			id, label := a.resolvePick(ctx, "entity", it.Entity, it.EntityLb, it.EntityQ)
			switch eid, err := uuid.Parse(id); {
			case id != "" && err == nil:
				ri.EntityID = &eid
				it.Entity, it.EntityLb = id, label
			case id != "" || strings.TrimSpace(it.EntityQ) != "":
				it.Err["entity"] = "Choose the page or post this item links to"
			default:
				it.Warn = menuNoLinkWarning
			}
		case "archive":
			ri.LinkType = menu.LinkTypeArchive
			path, label := a.resolvePick(ctx, "term", it.Term, it.TermLb, it.TermQ)
			switch {
			case path != "" && safeMenuURL(path):
				ri.URL = &path
				it.Term, it.TermLb = path, label
			case path != "" || strings.TrimSpace(it.TermQ) != "":
				it.Err["term"] = "Choose a category or tag"
			default:
				it.Warn = menuNoLinkWarning
			}
		default:
			ri.LinkType = menu.LinkTypeCustom
			switch {
			case it.URL == "":
				it.Warn = menuNoLinkWarning
			case safeMenuURL(it.URL):
				if !strings.HasPrefix(it.URL, "/") && !strings.HasPrefix(it.URL, "#") && !strings.HasPrefix(it.URL, "?") {
					ri.LinkType = menu.LinkTypeExternal
				}
				u := it.URL
				ri.URL = &u
			default:
				it.Err["url"] = "Enter a path like /about or an address like https://example.com"
			}
		}
		a.menuItemSettings(it, &ri)
		res.out = append(res.out, ri)
	}
	if res.out == nil {
		res.out = []menu.ReplaceItem{}
	}
	return res
}

// menuItemSettings validates the item's classes and settings, recording each
// problem on the field it belongs to, and copies the valid ones to ri. Preset
// ids are kept even when the active theme does not declare them (the theme may
// change back); the theme ignores what it does not know.
func (a *Admin) menuItemSettings(it *menuRow, ri *menu.ReplaceItem) {
	check := func(key string, at menu.MenuItemAttrs) {
		if err := at.Validate(); err != nil {
			msg := strings.TrimPrefix(err.Error(), menu.ErrInvalidAttrs.Error()+": ")
			it.Err[key] = strings.ToUpper(msg[:1]) + msg[1:] + "."
		}
	}
	check("style", menu.MenuItemAttrs{Style: it.Style})
	check("color", menu.MenuItemAttrs{Color: it.Color})
	check("badge", menu.MenuItemAttrs{Badge: it.Badge})
	check("icon", menu.MenuItemAttrs{Icon: it.Icon})
	check("show", menu.MenuItemAttrs{ShowOn: it.Show})
	check("id", menu.MenuItemAttrs{ID: it.HTMLID})
	check("title", menu.MenuItemAttrs{Title: it.Title})
	check("aria", menu.MenuItemAttrs{AriaLabel: it.Aria})
	check("rel", menu.MenuItemAttrs{Rel: it.Rel})
	if it.Class != "" {
		if classes, err := menu.ValidateCSSClasses(it.Class); err != nil {
			msg := strings.TrimPrefix(err.Error(), menu.ErrInvalidAttrs.Error()+": ")
			it.Err["class"] = strings.ToUpper(msg[:1]) + msg[1:] + "."
		} else if classes != "" {
			it.Class = classes
			ri.CSSClasses = &classes
		}
	}
	ri.Attrs = menu.MenuItemAttrs{Style: it.Style, Color: it.Color, Badge: it.Badge, Icon: it.Icon, ShowOn: it.Show,
		ID: it.HTMLID, Title: it.Title, AriaLabel: it.Aria, Rel: it.Rel}.Normalize()
}

// resolvePick returns the chosen value. A search box left with its original
// text keeps the picked value; edited text (no JavaScript) is looked up and
// must match exactly one result by name or slug (or the value itself).
func (a *Admin) resolvePick(ctx context.Context, kind, value, prevLabel, typed string) (string, string) {
	typed = strings.TrimSpace(typed)
	if value != "" && (typed == "" || typed == prevLabel) {
		return value, prevLabel
	}
	if typed == "" || a.deps.Growth.Pickers == nil {
		return "", ""
	}
	var opts []PickOption
	var err error
	if kind == "term" {
		opts, err = a.deps.Growth.Pickers.SearchTerms(ctx, typed, 10)
	} else {
		opts, err = a.deps.Growth.Pickers.SearchEntities(ctx, typed, 10)
	}
	if err != nil {
		return "", ""
	}
	var hit *PickOption
	for i := range opts {
		o := &opts[i]
		if strings.EqualFold(o.Label, typed) || (o.Slug != "" && strings.EqualFold(o.Slug, strings.TrimPrefix(typed, "/"))) || o.Value == typed {
			if hit != nil {
				return "", "" // ambiguous: ask the editor to pick
			}
			hit = o
		}
	}
	if hit == nil {
		return "", ""
	}
	return hit.Value, hit.Label
}

func (a *Admin) handleMenuDeleteForm(w http.ResponseWriter, r *http.Request) {
	m, ok := a.loadMenu(w, r)
	if !ok {
		return
	}
	a.renderConfirm(w, r, confirmData{
		Title: "Delete the menu " + m.Name + "?", Message: "The menu and its items are removed. Pages that show it lose the navigation.",
		Post: a.adminPath + "/theme/menus/" + m.ID.String() + "/delete", Label: "Delete menu", Back: a.adminPath + "/theme/menus", Danger: true,
	})
}

func (a *Admin) handleMenuDelete(w http.ResponseWriter, r *http.Request) {
	if a.deps.Growth.Menus == nil {
		a.unavailable(w, r, "Menus")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err == nil {
		err = a.deps.Growth.Menus.DeleteMenu(r.Context(), id)
	}
	switch {
	case errors.Is(err, menu.ErrMenuNotFound) || (err != nil && id == uuid.Nil):
		a.ErrorBack(w, r, http.StatusNotFound, "Menu not found", "That menu does not exist.", a.adminPath+"/theme/menus", "Back to menus")
		return
	case err != nil:
		a.logger().Error("delete menu failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The menu could not be deleted.")
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Menu deleted.")
	Redirect(w, r, a.adminPath+"/theme/menus")
}

type pickJSON struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`
}

func (a *Admin) handlePickEntities(w http.ResponseWriter, r *http.Request) {
	a.pick(w, r, func(ctx context.Context, q string) ([]PickOption, error) {
		return a.deps.Growth.Pickers.SearchEntities(ctx, q, 10)
	})
}

func (a *Admin) handlePickTerms(w http.ResponseWriter, r *http.Request) {
	a.pick(w, r, func(ctx context.Context, q string) ([]PickOption, error) {
		return a.deps.Growth.Pickers.SearchTerms(ctx, q, 10)
	})
}

// pick answers the search-select partial: a JSON list of options.
func (a *Admin) pick(w http.ResponseWriter, r *http.Request, search func(context.Context, string) ([]PickOption, error)) {
	if a.deps.Growth.Pickers == nil {
		http.Error(w, "not available", http.StatusServiceUnavailable)
		return
	}
	opts, err := search(r.Context(), strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		a.logger().Error("pick failed", "err", err)
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}
	out := make([]pickJSON, 0, len(opts))
	for _, o := range opts {
		out = append(out, pickJSON{Value: o.Value, Label: o.Label, Hint: o.Hint})
	}
	a.writeJSON(w, http.StatusOK, out)
}
