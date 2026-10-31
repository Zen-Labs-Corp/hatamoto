package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
)

type versionsData struct {
	Name, ID string
	Tabs     []ui.Tab
	Table    ui.Table
	Actions  []ui.Button
	History  ui.Tracker
	Compare  struct {
		Options  []ui.Option
		Options2 []ui.Option
	}
	Count int
}

const versionLimit = 100

func (a *Admin) handleVersions(w http.ResponseWriter, r *http.Request) {
	e, sc, ok := a.loadEntity(w, r)
	if !ok {
		return
	}
	c := a.deps.Content
	vs, err := c.Entities.ListVersions(r.Context(), e.ID, versionLimit)
	if err != nil {
		a.serverError(w, r, "The versions could not be loaded", err)
		return
	}
	loc := a.siteLocation(r.Context())
	id := e.ID.String()
	names := map[uuid.UUID]string{}
	who := func(u *uuid.UUID) string {
		if u == nil {
			return "—"
		}
		if n, ok := names[*u]; ok {
			return n
		}
		n := "Unknown user"
		if a.deps.Users != nil {
			if usr, err := a.deps.Users.GetByID(r.Context(), *u); err == nil {
				n = displayName(usr)
			}
		}
		names[*u] = n
		return n
	}
	back := ui.Button{Label: "Back to details", Href: a.adminPath + "/entities/" + id}
	if usesVisualEditor(sc, e) {
		back = ui.Button{Label: "Back to editor", Href: a.adminPath + "/entities/" + id + "/edit"}
	}
	d := versionsData{Name: e.Name, ID: id, Tabs: a.entityTabsFor(r.Context(), e, "versions"), Count: len(vs), Actions: []ui.Button{back}}
	t := ui.Table{
		ID: "versions", Caption: "Versions of " + e.Name, BaseURL: a.adminPath + "/entities/" + id + "/versions", CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "version", Label: "Version"}, {Key: "saved", Label: "Saved"}, {Key: "by", Label: "By"}, {Key: "summary", Label: "Summary"}},
		Empty:   ui.Empty{Icon: "file-text", Title: "No earlier versions", Body: "A version is kept each time the content changes."},
	}
	t.Rows = append(t.Rows, ui.Row{ID: "current", Label: "current version", Cells: []ui.Cell{
		{Text: "v" + strconv.Itoa(e.Version), Tag: &ui.Badge{Label: "Current", Kind: "success"}}, {Text: fmtDateTime(e.UpdatedAt.In(loc))}, {Text: who(e.UpdatedBy)}, {Text: "Live content"},
	}})
	d.Compare.Options = append(d.Compare.Options, ui.Option{Value: "0", Label: fmt.Sprintf("v%d (current)", e.Version)})
	d.Compare.Options2 = append(d.Compare.Options2, ui.Option{Value: "0", Label: fmt.Sprintf("v%d (current)", e.Version)})
	for i, v := range vs {
		vn := strconv.Itoa(v.Version)
		summary := ""
		if v.ChangeSummary != nil {
			summary = *v.ChangeSummary
		}
		if v.Version == 1 {
			summary = "Created" // the snapshot of the content as first saved
		}
		t.Rows = append(t.Rows, ui.Row{ID: "v" + vn, Label: "version " + vn, Cells: []ui.Cell{
			{Text: "v" + vn, Tag: &ui.Badge{Label: "Earlier", Kind: "neutral"}}, {Text: fmtDateTime(v.CreatedAt.In(loc))}, {Text: who(v.ChangedBy)}, {Text: summary},
		}, Actions: []ui.Action{
			{Label: "View", Href: a.adminPath + "/entities/" + id + "/versions/" + vn},
			{Label: "Compare with current", Href: a.adminPath + "/entities/" + id + "/versions/compare?from=" + vn + "&to=0"},
			{Label: "Restore", Href: a.adminPath + "/entities/" + id + "/versions/" + vn + "/restore", Post: a.adminPath + "/entities/" + id + "/versions/" + vn + "/restore",
				Confirm: restoreConfirm(v.Version), ConfirmLabel: "Restore version"},
		}})
		sel := i == 0
		d.Compare.Options = append(d.Compare.Options, ui.Option{Value: vn, Label: "v" + vn, Selected: sel})
		d.Compare.Options2 = append(d.Compare.Options2, ui.Option{Value: vn, Label: "v" + vn})
	}
	d.Table = t
	d.History = versionHistory(e, vs, loc)
	v := a.NewView(w, r, "Versions of "+e.Name)
	v.Data = d
	v.Crumbs = a.entityCrumbs(e.Name, id, Crumb{Label: "Versions"})
	a.Render(w, r, http.StatusOK, "entities/versions", v)
}

func restoreConfirm(n int) string {
	return "The content becomes version " + strconv.Itoa(n) + " again. The current content is kept as a new version, so nothing is lost."
}

// restoreButton is a page header action that asks (the same words as the row menu) before it restores.
func (a *Admin) restoreButton(id string, n int, label string) ui.Button {
	href := a.adminPath + "/entities/" + id + "/versions/" + strconv.Itoa(n) + "/restore"
	return ui.Button{Label: label, Href: href, Attrs: map[string]string{
		"data-hm-confirm": restoreConfirm(n), "data-hm-confirm-label": "Restore version", "data-hm-post": href,
	}}
}

// versionHistory is the tracker strip above the table: one block per version, oldest on the
// left, the current content last.
func versionHistory(e *entity.Entity, vs []*entity.EntityVersion, loc *time.Location) ui.Tracker {
	t := ui.Tracker{Label: "Versions, oldest first", Legend: []ui.TrackerKey{{Kind: "success", Label: "Current"}, {Kind: "info", Label: "Earlier"}}}
	for i := len(vs) - 1; i >= 0; i-- {
		v := vs[i]
		tip := "v" + strconv.Itoa(v.Version) + ", " + fmtDate(v.CreatedAt.In(loc))
		if v.ChangeSummary != nil && *v.ChangeSummary != "" {
			tip += ": " + *v.ChangeSummary
		}
		t.Blocks = append(t.Blocks, ui.TrackerBlock{Kind: "info", Tip: tip})
	}
	t.Blocks = append(t.Blocks, ui.TrackerBlock{Kind: "success", Tip: "v" + strconv.Itoa(e.Version) + ", " + fmtDate(e.UpdatedAt.In(loc)) + ": current"})
	return t
}

// diffRow is one changed attribute in a version comparison.
type diffRow struct {
	Path, Kind, Left, Right string // Kind: changed | added | removed
}

// OpLabel and BadgeKind are the Change column's Badge, the same words and tones as the layout table.
func (d diffRow) OpLabel() string   { return opLabel(d.Kind) }
func (d diffRow) BadgeKind() string { return opBadge(d.Kind) }

type compareData struct {
	Name, ID, FromLabel, ToLabel string
	Tabs                         []ui.Tab
	Rows                         []diffRow
	Same                         int
	Identical                    bool
	Layout                       *layoutView // builder pages: the per-node table
	Actions                      []ui.Button
}

// flatten turns a decoded JSON value into dotted-path leaves ("faqs.0.q").
func flatten(prefix string, v any, out map[string]string) {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 && prefix != "" {
			out[prefix] = "{}"
		}
		for k, c := range x {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			flatten(p, c, out)
		}
	case []any:
		if len(x) == 0 {
			out[prefix] = "[]"
		}
		for i, c := range x {
			flatten(prefix+"."+strconv.Itoa(i), c, out)
		}
	case string:
		out[prefix] = x
	case nil:
		out[prefix] = "null"
	default:
		b, _ := json.Marshal(x)
		out[prefix] = string(b)
	}
}

// diffAttributes compares two attribute documents leaf by leaf; changed rows
// are returned in path order, same counts the untouched leaves.
func diffAttributes(left, right json.RawMessage) (rows []diffRow, same int) {
	var l, r any
	_ = json.Unmarshal(left, &l)
	_ = json.Unmarshal(right, &r)
	lf, rf := map[string]string{}, map[string]string{}
	flatten("", l, lf)
	flatten("", r, rf)
	seen := map[string]bool{}
	var paths []string
	for p := range lf {
		seen[p] = true
		paths = append(paths, p)
	}
	for p := range rf {
		if !seen[p] {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		lv, lok := lf[p]
		rv, rok := rf[p]
		switch {
		case lok && rok && lv == rv:
			same++
		case lok && rok:
			rows = append(rows, diffRow{p, "changed", clipValue(lv), clipValue(rv)})
		case lok:
			rows = append(rows, diffRow{p, "removed", clipValue(lv), ""})
		default:
			rows = append(rows, diffRow{p, "added", "", clipValue(rv)})
		}
	}
	return rows, same
}

func clipValue(s string) string {
	r := []rune(s)
	if len(r) > 600 {
		return string(r[:600]) + "…"
	}
	return s
}

// versionAttrs returns the attributes of version n (0 is the current one).
func (a *Admin) versionAttrs(r *http.Request, e *entity.Entity, n int) (json.RawMessage, string, bool) {
	if n == 0 || n == e.Version {
		return e.Attributes, fmt.Sprintf("v%d (current)", e.Version), true
	}
	v, err := a.deps.Content.Entities.GetVersion(r.Context(), e.ID, n)
	if err != nil {
		return nil, "", false
	}
	return v.Attributes, fmt.Sprintf("v%d", n), true
}

func (a *Admin) handleVersionCompare(w http.ResponseWriter, r *http.Request) {
	e, _, ok := a.loadEntity(w, r)
	if !ok {
		return
	}
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	to, _ := strconv.Atoi(r.URL.Query().Get("to"))
	la, ll, ok1 := a.versionAttrs(r, e, from)
	ra, rl, ok2 := a.versionAttrs(r, e, to)
	id := e.ID.String()
	if !ok1 || !ok2 {
		a.ErrorBack(w, r, http.StatusNotFound, "Version not found", "One of those versions no longer exists.", a.adminPath+"/entities/"+id+"/versions", "Back to versions")
		return
	}
	rows, same := diffAttributes(la, ra)
	layout := a.layoutView(r, e, la, ra, from, to)
	if layout != nil && layout.Diffed {
		rows = dropPrefixes(rows, "page_doc", "page_sections")
	}
	actions := []ui.Button{{Label: "Back to versions", Href: a.adminPath + "/entities/" + id + "/versions"}}
	if from > 0 && from != e.Version { // restoring the current content is a no-op
		actions = append(actions, a.restoreButton(id, from, "Restore this version"))
	}
	v := a.NewView(w, r, "Compare "+ll+" and "+rl)
	v.Data = compareData{Name: e.Name, ID: id, FromLabel: ll, ToLabel: rl, Tabs: a.entityTabsFor(r.Context(), e, "versions"), Rows: rows, Same: same, Identical: len(rows) == 0 && (layout == nil || len(layout.Rows) == 0), Layout: layout, Actions: actions}
	v.Crumbs = a.entityCrumbs(e.Name, id, Crumb{Label: "Versions", Href: a.adminPath + "/entities/" + id + "/versions"}, Crumb{Label: "Compare"})
	a.Render(w, r, http.StatusOK, "entities/compare", v)
}

func (a *Admin) versionTarget(w http.ResponseWriter, r *http.Request) (*entity.Entity, int, bool) {
	e, _, ok := a.loadEntity(w, r)
	if !ok {
		return nil, 0, false
	}
	n, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || n < 1 {
		a.ErrorBack(w, r, http.StatusNotFound, "Version not found", "That version does not exist.", a.adminPath+"/entities/"+e.ID.String()+"/versions", "Back to versions")
		return nil, 0, false
	}
	return e, n, true
}

func (a *Admin) handleVersionRestoreConfirm(w http.ResponseWriter, r *http.Request) {
	e, n, ok := a.versionTarget(w, r)
	if !ok {
		return
	}
	id := e.ID.String()
	a.renderConfirm(w, r, confirmData{
		Title: fmt.Sprintf("Restore version %d of “%s”?", n, e.Name), Message: "The content becomes this version again. The current content is kept as a new version, so nothing is lost.",
		Post: a.adminPath + "/entities/" + id + "/versions/" + strconv.Itoa(n) + "/restore", Label: "Restore version", Back: a.adminPath + "/entities/" + id + "/versions",
	})
}

func (a *Admin) handleVersionRestore(w http.ResponseWriter, r *http.Request) {
	e, n, ok := a.versionTarget(w, r)
	if !ok {
		return
	}
	id := e.ID.String()
	if _, err := a.deps.Content.Entities.RestoreVersion(r.Context(), e.ID, actorID(r), n); err != nil {
		switch {
		case errors.Is(err, entity.ErrVersionNotFound):
			a.ErrorBack(w, r, http.StatusNotFound, "Version not found", "That version no longer exists.", a.adminPath+"/entities/"+id+"/versions", "Back to versions")
		case errors.Is(err, entity.ErrValidationFailed) || errors.Is(err, entity.ErrRestoreInvalid):
			a.SetFlash(w, r, "error", "That version no longer fits this entity type's fields, so it cannot be restored.")
			Redirect(w, r, a.adminPath+"/entities/"+id+"/versions")
		case errors.Is(err, entity.ErrInvalidDoc):
			a.SetFlash(w, r, "error", "That version's page layout is invalid, so it cannot be restored.")
			Redirect(w, r, a.adminPath+"/entities/"+id+"/versions")
		default:
			a.serverError(w, r, "The version could not be restored", err)
		}
		return
	}
	a.auditEntity(r, auditEntityRestoreVersion, e.ID, e, map[string]any{"version": n})
	a.SetFlash(w, r, "success", fmt.Sprintf("Restored version %d.", n))
	Redirect(w, r, a.adminPath+"/entities/"+id)
}

type versionViewData struct {
	Name, ID, Label string
	N               int
	Tabs            []ui.Tab
	Fields          []diffRow // Left holds the value
	Actions         []ui.Button
}

// handleVersionView shows one version's content read-only.
func (a *Admin) handleVersionView(w http.ResponseWriter, r *http.Request) {
	e, n, ok := a.versionTarget(w, r)
	if !ok {
		return
	}
	attrs, label, found := a.versionAttrs(r, e, n)
	id := e.ID.String()
	if !found {
		a.ErrorBack(w, r, http.StatusNotFound, "Version not found", "That version no longer exists.", a.adminPath+"/entities/"+id+"/versions", "Back to versions")
		return
	}
	var doc any
	_ = json.Unmarshal(attrs, &doc)
	flat := map[string]string{}
	flatten("", doc, flat)
	d := versionViewData{Name: e.Name, ID: id, Label: label, N: n, Tabs: a.entityTabsFor(r.Context(), e, "versions"), Actions: []ui.Button{
		{Label: "Back to versions", Href: a.adminPath + "/entities/" + id + "/versions"},
		{Label: "Compare with current", Href: a.adminPath + fmt.Sprintf("/entities/%s/versions/compare?from=%d&to=0", id, n)},
	}}
	paths := make([]string, 0, len(flat))
	for p := range flat {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		d.Fields = append(d.Fields, diffRow{Path: p, Left: clipValue(flat[p])})
	}
	v := a.NewView(w, r, "Version "+label+" of "+e.Name)
	v.Data = d
	v.Crumbs = a.entityCrumbs(e.Name, id, Crumb{Label: "Versions", Href: a.adminPath + "/entities/" + id + "/versions"}, Crumb{Label: label})
	a.Render(w, r, http.StatusOK, "entities/version", v)
}

// entityCrumbs is the top bar trail of an entity sub-page: Entities, the
// entity itself, then tail.
func (a *Admin) entityCrumbs(name, id string, tail ...Crumb) []Crumb {
	return append([]Crumb{{Label: "Entities", Href: a.adminPath + "/entities"}, {Label: name, Href: a.adminPath + "/entities/" + id}}, tail...)
}
