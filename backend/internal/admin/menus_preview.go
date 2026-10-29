package admin

import (
	"errors"
	"net/http"

	"hatamoto.dev/core/internal/menu"
)

// maxMenuPreviewBody bounds a preview request: the editor form, never an upload.
const maxMenuPreviewBody = 4 << 20

// ErrMenuPreviewOff is what Content.MenuPreview returns when page rendering is
// off, so the editor sees 503 rather than a failure.
var ErrMenuPreviewOff = errors.New("page rendering is off")

// handleMenuPreview renders the posted editor form as the site would show the
// menu, as a full HTML document for the editor's preview frame. Nothing is
// saved. Rows with errors still render (a broken link shows as text), so the
// preview follows the tree while the author is mid-edit.
func (a *Admin) handleMenuPreview(w http.ResponseWriter, r *http.Request) {
	if a.deps.Content.MenuPreview == nil {
		http.Error(w, "Menu preview is not available.", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMenuPreviewBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "The form could not be read.", http.StatusBadRequest)
		return
	}
	rows := parseMenuItems(r.PostForm)
	if len(rows) > maxMenuItems {
		http.Error(w, "A menu holds at most 200 items.", http.StatusUnprocessableEntity)
		return
	}
	location := r.PostFormValue("location")
	if !menu.ValidLocation(location) {
		location = ""
	}
	body, csp, err := a.deps.Content.MenuPreview(r, location, &menu.Menu{Items: a.previewMenuItems(r, rows)})
	switch {
	case errors.Is(err, ErrMenuPreviewOff):
		http.Error(w, "Page rendering is off.", http.StatusServiceUnavailable)
		return
	case err != nil:
		a.logger().Error("menu preview failed", "err", err)
		http.Error(w, "The preview could not be rendered.", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Content-Security-Policy", csp) // the theme's own policy replaces the admin's
	h.Set("X-Robots-Tag", "noindex")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// previewMenuItems builds the item tree of rows. Each item's Position is its
// ordinal in the editor (pre-order), which the renderer turns into the
// preview's click target id (menu-pv-<ordinal>). An item whose link has an error becomes plain text.
func (a *Admin) previewMenuItems(r *http.Request, rows []menuRow) []*menu.MenuItem {
	res := a.menuReplaceItems(r.Context(), rows)
	items := make([]*menu.MenuItem, len(res.out))
	var top []*menu.MenuItem
	for i, ri := range res.out {
		it := &menu.MenuItem{
			Label: ri.Label, LinkType: ri.LinkType, EntityID: ri.EntityID, URL: ri.URL,
			OpenInNewTab: ri.OpenInNewTab, CSSClasses: ri.CSSClasses, Attrs: ri.Attrs, Position: i,
		}
		if it.Label == "" {
			it.Label = "Untitled item"
		}
		if e := res.rows[i].Err; e["entity"] != "" || e["term"] != "" || e["url"] != "" {
			it.LinkType, it.EntityID, it.URL = menu.LinkTypeNone, nil, nil
		}
		items[i] = it
		if ri.Parent < 0 {
			top = append(top, it)
		} else {
			items[ri.Parent].Children = append(items[ri.Parent].Children, it)
		}
	}
	return top
}
