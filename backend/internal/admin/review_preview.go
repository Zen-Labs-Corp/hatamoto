package admin

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzsubject"
	"hatamoto.dev/core/internal/authzuser"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/user"
)

// mayPreviewChangeset is the check of the sealed preview frame. Holders of
// plugin.changeset.review (owners by default) may preview any changeset. An edit
// changeset, a change to an entry waiting for review, may also be previewed by its
// requester and by holders of entity.review.decide on that entry. A plugin's changeset never
// widens: it stays with plugin.changeset.review.
func (a *Admin) mayPreviewChangeset(ctx context.Context, u *user.User, csID int64) bool {
	if u == nil {
		return false
	}
	if authzuser.Can(ctx, a.logger(), u, authz.ActionPluginChangesetReview) {
		return true
	}
	if a.deps.Growth.Changesets == nil {
		return false
	}
	h, err := a.deps.Growth.Changesets.Header(ctx, csID)
	if err != nil || h.Kind != changeset.ChangesetEdit {
		return false
	}
	if h.Requester == u.ID.String() {
		return true
	}
	// The decide right is checked on the entry the change is for, as the flow does.
	items, err := a.deps.Growth.Changesets.ItemPage(ctx, csID, 1, 0)
	if err != nil || len(items) == 0 {
		return false
	}
	ok, err := authz.Can(ctx, authzsubject.ForSession(u.ID.String(), string(u.Role)), authz.ActionEntityReviewDecide, reviewObject(items[0].TargetID))
	return err == nil && ok
}

// handleReviewPreview is the sealed frame of one change waiting for review: the
// site as it would be once the change is approved. Each view mints a fresh
// one-time ticket; the frame itself is served by the changeset frame route.
func (a *Admin) handleReviewPreview(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadReview(w, r)
	if !ok {
		return
	}
	back := a.reviewURL("/", strconv.FormatInt(d.ID, 10))
	if a.deps.Growth.Changesets == nil || a.deps.Growth.ChangesetPreview == nil {
		a.unavailable(w, r, "Review preview")
		return
	}
	if d.Rejected {
		a.ErrorBack(w, r, http.StatusConflict, "Preview not available", "A change that was sent back can no longer be previewed.", back, "Back to the change")
		return
	}
	seqs, err := a.deps.Growth.Changesets.PreviewSeqs(r.Context(), d.ID)
	if err != nil {
		a.serverError(w, r, "The change could not be loaded", err)
		return
	}
	page := changesetPreviewPage{ID: d.ID, Back: back, Empty: len(seqs) == 0,
		FrameTitle: "Isolated preview of " + d.EntityName}
	if !page.Empty {
		seq := seqs[0]
		tok, ok := a.mintChangesetPreview(r, d.ID, seq, a.requestOrigin(r))
		if !ok {
			a.refuse(w, r)
			return
		}
		path := "/changesets/" + strconv.FormatInt(d.ID, 10) + "/frame/" + strconv.Itoa(seq)
		base := a.adminPath
		if a.deps.PreviewHost != "" {
			base = "https://" + a.deps.PreviewHost
		}
		page.Src, page.Shown = base+path+"/"+url.PathEscape(tok), base+path
	}
	v := a.NewView(w, r, "Preview: "+d.EntityName)
	v.Width = "narrow"
	v.Crumbs = []Crumb{{Label: "Review", Href: a.reviewURL()}, {Label: d.EntityName, Href: back}, {Label: "Preview"}}
	v.Data = page
	// This page alone may frame the preview host.
	w.Header().Set("Content-Security-Policy", cspHeader(Nonce(r.Context()), false, a.deps.PreviewHost))
	a.Render(w, r, http.StatusOK, "review/preview", v)
}
