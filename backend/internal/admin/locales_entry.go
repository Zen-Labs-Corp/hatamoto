package admin

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/siteconfig"
)

// entryRow is one entry path input of the Visitor entry panel.
type entryRow struct {
	ID    string
	Label string
	Value string
	// Index is the position posted by the row's Remove button; Removable is
	// false for the blank row waiting for a new path.
	Index     string
	Removable bool
	Error     string
}

// visitorEntryForm is the Visitor entry panel of the Locales tab: the site
// wide switch, the entry paths and the old cookie name.
type visitorEntryForm struct {
	Enabled bool
	Rows    []entryRow
	Cookie  string
	Action  string
	// CookieError is the message shown on the old cookie name field.
	CookieError string
	Summary     []ui.FieldError
}

// newVisitorEntryForm lays out paths as one input each, plus one blank row to
// type a new path into. errMsg is the validation message to show, placed on
// the cookie field when it is about the cookie and on the first path otherwise.
func (a *Admin) newVisitorEntryForm(enabled bool, paths []string, cookie, errMsg string) visitorEntryForm {
	f := visitorEntryForm{Enabled: enabled, Cookie: cookie, Action: a.url("settings", "locales", "entry")}
	for i, p := range append(slices.Clone(paths), "") {
		n := strconv.Itoa(i)
		row := entryRow{ID: "entry-path-" + n, Label: "Entry path " + strconv.Itoa(i+1), Value: p, Index: n, Removable: p != ""}
		f.Rows = append(f.Rows, row)
	}
	if errMsg != "" {
		if strings.HasPrefix(errMsg, "the old cookie name") {
			f.CookieError = errMsg
			f.Summary = []ui.FieldError{{ID: "entry-cookie", Label: "Old cookie name", Message: errMsg}}
		} else {
			f.Rows[0].Error = errMsg
			f.Summary = []ui.FieldError{{ID: f.Rows[0].ID, Label: "Entry paths", Message: errMsg}}
		}
	}
	return f
}

// postedEntryPaths are the non-blank entry paths of the form, trimmed.
func postedEntryPaths(r *http.Request) []string {
	var out []string
	for _, p := range r.PostForm["entry_path"] {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// handleVisitorEntrySave stores the Visitor entry panel. Its Remove and Add
// path buttons only re-render the panel with the row taken out or one more
// blank row, so the panel works without JavaScript; Save validates and stores.
func (a *Admin) handleVisitorEntrySave(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	if r.ParseForm() != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	if len(cfg.Locales) < 2 || a.deps.ValidateVisitorEntry == nil {
		a.Error(w, r, http.StatusUnprocessableEntity, "Not saved", "Add a second locale first: visitor entry applies to sites with two or more locales.")
		return
	}
	enabled, cookie := r.PostFormValue("enabled") == "true", strings.TrimSpace(r.PostFormValue("legacy_cookie"))
	paths := postedEntryPaths(r)
	if i, err := strconv.Atoi(r.PostFormValue("remove_entry_path")); err == nil {
		// Index into the posted rows, blank ones included.
		rows := slices.Clone(r.PostForm["entry_path"])
		if i >= 0 && i < len(rows) {
			rows = slices.Delete(rows, i, i+1)
		}
		paths = nil
		for _, p := range rows {
			if p = strings.TrimSpace(p); p != "" {
				paths = append(paths, p)
			}
		}
		a.renderLocalesEntry(w, r, http.StatusOK, cfg, a.newVisitorEntryForm(enabled, paths, cookie, ""))
		return
	}
	if r.PostForm.Has("add_entry_path") {
		// The blank row the panel always ends with is already there: keep what
		// was typed and add one more blank row beside it.
		f := a.newVisitorEntryForm(enabled, paths, cookie, "")
		f.Rows = append(f.Rows, entryRow{ID: "entry-path-" + strconv.Itoa(len(f.Rows)), Label: "Entry path " + strconv.Itoa(len(f.Rows)+1), Index: strconv.Itoa(len(f.Rows))})
		a.renderLocalesEntry(w, r, http.StatusOK, cfg, f)
		return
	}
	v, err := a.deps.ValidateVisitorEntry(r.Context(), cfg.Locales, localeSetOf(cfg).Default(), cfg.PrefixAll, siteconfig.VisitorEntry{Enabled: enabled, Paths: paths, LegacyCookie: cookie})
	if err != nil {
		a.renderLocalesEntry(w, r, http.StatusUnprocessableEntity, cfg, a.newVisitorEntryForm(enabled, paths, cookie, err.Error()))
		return
	}
	if _, err := a.deps.Site.Update(r.Context(), siteconfig.UpdateInput{VisitorEntry: &v}); err != nil {
		a.logger().Error("save visitor entry failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The visitor entry settings could not be saved.")
		return
	}
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(r.Context())
	}
	a.SetFlash(w, r, "success", "Visitor entry saved.")
	http.Redirect(w, r, a.url("settings", "locales"), http.StatusSeeOther)
}
