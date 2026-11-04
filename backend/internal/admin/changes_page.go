package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/httpx"
)

const (
	changesRel     = "/settings/changes"
	changesPerPage = 25
)

// mountChangesSettings registers Settings > Changes: the list of plugin runs'
// changesets and the undo of a published one.
func (a *Admin) mountChangesSettings(g *httpx.Group) {
	g.HandleFunc("GET "+changesRel, a.handleChanges)
	// Undo rewrites pages and site settings, so it asks for the password
	// again; a POST is never replayed after that page.
	g.Group("", a.RequireSudo).HandleFunc("POST "+changesRel+"/{id}/undo", a.handleChangesUndo)
}

type changesData struct {
	Table   ui.Table
	Dialogs []undoDialog
}

// undoDialog is the confirmation of undoing one published run.
type undoDialog struct {
	ID, FormID, Action, Title, Body, Callout string
}

func changeStateBadge(state string) ui.Badge {
	switch state {
	case changeset.StateStaged:
		return ui.Badge{Label: "Waiting for you", Kind: "warning"}
	case changeset.StatePublished:
		return ui.Badge{Label: "Published", Kind: "success"}
	case changeset.StateReverted:
		return ui.Badge{Label: "Reverted", Kind: "muted"}
	case changeset.StateExpired:
		return ui.Badge{Label: "Expired, deleted", Kind: "muted"}
	}
	return ui.Badge{Label: "Discarded", Kind: "muted"}
}

func (a *Admin) handleChanges(w http.ResponseWriter, r *http.Request) {
	rv := a.deps.Growth.Reviewer
	if rv == nil {
		a.unavailable(w, r, "Changes")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	offset := (page - 1) * changesPerPage
	rows, err := rv.History(r.Context(), changesPerPage+1, offset)
	if err != nil {
		a.serverError(w, r, "The changes could not be listed", err)
		return
	}
	more := len(rows) > changesPerPage
	rows = rows[:min(len(rows), changesPerPage)]
	loc := a.siteLocation(r.Context())
	d := changesData{Table: ui.Table{
		ID: "changes", Caption: "Plugin changes", BaseURL: a.adminPath + changesRel,
		Columns: []ui.Col{{Key: "run", Label: "Run", Role: "title"}, {Key: "by", Label: "By"}, {Key: "when", Label: "When"}, {Key: "changes", Label: "Changes", Role: "num"}, {Key: "state", Label: "State"}},
		Empty:   ui.Empty{Icon: "list", Title: "Nothing yet.", Body: "Imports and other plugin runs show up here once they stage changes."},
	}}
	for _, h := range rows {
		badge := changeStateBadge(h.State)
		run := ui.Cell{Text: h.RunName, Sub: h.Plugin}
		if h.State == changeset.StateStaged {
			run.Link = a.changesetURL(h.Plugin, h.ID, "")
		}
		changes := "-"
		if h.State == changeset.StatePublished || h.State == changeset.StateReverted {
			changes = thousands(h.Applied)
		}
		row := ui.Row{ID: strconv.FormatInt(h.ID, 10), Label: h.RunName + " " + fmtDateTime(h.When.In(loc)), Cells: []ui.Cell{
			run, {Text: h.By}, {Text: fmtDateTime(h.When.In(loc)), Nowrap: true}, {Text: changes, Nowrap: true}, {Badge: &badge},
		}}
		switch h.State {
		case changeset.StateStaged:
			row.Actions = []ui.Action{{Label: "Review", Href: a.changesetURL(h.Plugin, h.ID, ""), Button: true}}
		case changeset.StatePublished:
			id := "undo-" + row.ID
			pv, err := rv.UndoPreview(r.Context(), h.ID)
			if err != nil {
				a.serverError(w, r, "The changes could not be listed", err)
				return
			}
			dlg := undoDialog{
				ID: id, FormID: id + "-form", Action: a.adminPath + changesRel + "/" + row.ID + "/undo",
				Title: "Undo " + h.RunName + "?",
				Body:  fmt.Sprintf("Removes the %s this import published and restores the %s.", countNoun(pv.Applied, "change"), countNoun(pv.Settings, "site-wide setting")),
			}
			if pv.EditedSince > 0 {
				dlg.Callout = fmt.Sprintf("%s edited by you since then. They are now yours and stay as they are.", countNoun(pv.EditedSince, "imported page")+" "+map[bool]string{true: "was", false: "were"}[pv.EditedSince == 1])
			}
			d.Dialogs = append(d.Dialogs, dlg)
			row.Actions = []ui.Action{{Label: "Undo whole run", Href: "#" + id, Open: id, Button: true, AriaLabel: "Undo whole run " + h.RunName}}
		}
		d.Table.Rows = append(d.Table.Rows, row)
	}
	total := offset + len(rows)
	if more {
		total++
	}
	d.Table.Page = ui.Pagination{Page: page, PerPage: changesPerPage, Total: total, BaseURL: a.adminPath + changesRel}
	v := a.NewView(w, r, "Changes")
	v.Section, v.Tab = "changes", "changes"
	v.Width = "wide"
	v.Data = d
	a.Render(w, r, http.StatusOK, "settings/changes", v)
}

func (a *Admin) handleChangesUndo(w http.ResponseWriter, r *http.Request) {
	rv := a.deps.Growth.Reviewer
	if rv == nil {
		a.unavailable(w, r, "Changes")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		a.Error(w, r, http.StatusNotFound, "Not found", "That changeset does not exist.")
		return
	}
	back := a.adminPath + changesRel
	restored, kept, err := rv.Undo(r.Context(), id)
	switch {
	case errors.Is(err, ErrReviewNotFound):
		a.ErrorBack(w, r, http.StatusNotFound, "Changeset not found", "That changeset does not exist.", back, "Back to the changes")
		return
	case errors.Is(err, ErrReviewNotPublished):
		a.ErrorBack(w, r, http.StatusConflict, "Nothing to undo", "Only published changes can be undone.", back, "Back to the changes")
		return
	case err != nil:
		a.serverError(w, r, "The changes could not be undone", err)
		return
	}
	a.audit(r, "changeset.revert", strconv.FormatInt(id, 10), map[string]any{"changeset": id, "restored": restored, "kept": kept})
	a.SetFlash(w, r, "success", fmt.Sprintf("Undid changeset #%d: %d restored, %d kept as you edited them.", id, restored, kept))
	http.Redirect(w, r, back, http.StatusSeeOther)
}
