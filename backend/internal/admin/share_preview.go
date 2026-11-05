package admin

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/previewlink"
)

// Audit actions for shared draft preview links. The token is never recorded.
const (
	auditPreviewLinkCreate = "entity.preview_link_create"
	auditPreviewLinkRevoke = "entity.preview_link_revoke"
)

// PreviewLinkStore is previewlink.Store as the share page uses it. Nil in
// ContentDeps hides the page and every link to it.
type PreviewLinkStore interface {
	Create(ctx context.Context, entityID uuid.UUID, createdBy *uuid.UUID, name string, ttl time.Duration) (string, *previewlink.Link, error)
	List(ctx context.Context, entityID uuid.UUID) ([]previewlink.Link, error)
	Revoke(ctx context.Context, entityID, linkID uuid.UUID) (bool, error)
}

// shareLifetimes are the lifetimes on offer, in display order.
var shareLifetimes = []struct {
	Value, Label string
	TTL          time.Duration
}{
	{"1h", "1 hour", time.Hour},
	{"1d", "1 day", 24 * time.Hour},
	{"7d", "7 days", 7 * 24 * time.Hour},
	{"30d", "30 days", 30 * 24 * time.Hour},
}

// shareData feeds entities/share.
type shareData struct {
	Name      string
	Action    string
	Back      string
	Published bool
	PublicURL string
	Lifetimes []ui.RadioCardItem
	Error     string
	// NewLink is the full address of a link just made. It is shown once: only
	// its hash is stored.
	NewLink string
	Table   ui.Table
}

// shareHrefFor is the share page of an entity, "" when sharing is off for it
// (no store, or the entity is published or archived).
func (a *Admin) shareHrefFor(id, status string) string {
	if a.deps.Content.PreviewLinks == nil || status == string(entity.StatusPublished) || status == string(entity.StatusArchived) {
		return ""
	}
	return a.adminPath + "/entities/" + id + "/share"
}

func (a *Admin) handleShare(w http.ResponseWriter, r *http.Request) {
	e, _, ok := a.loadSharable(w, r)
	if !ok {
		return
	}
	a.renderShare(w, r, http.StatusOK, e, "1d", "", "")
}

func (a *Admin) handleShareCreate(w http.ResponseWriter, r *http.Request) {
	e, _, ok := a.loadSharable(w, r)
	if !ok {
		return
	}
	if e.Status == entity.StatusPublished {
		a.renderShare(w, r, http.StatusConflict, e, "1d", "", "")
		return
	}
	pick := r.PostFormValue("lifetime")
	ttl := time.Duration(0)
	for _, l := range shareLifetimes {
		if l.Value == pick {
			ttl = l.TTL
		}
	}
	if ttl == 0 {
		a.renderShare(w, r, http.StatusUnprocessableEntity, e, "1d", "Pick how long the link works.", "")
		return
	}
	name := ""
	if u := userFrom(r.Context()); u != nil {
		name = u.Name
	}
	tok, l, err := a.deps.Content.PreviewLinks.Create(r.Context(), e.ID, actorID(r), name, ttl)
	switch {
	case errors.Is(err, previewlink.ErrTooMany):
		a.renderShare(w, r, http.StatusUnprocessableEntity, e, pick, "This page already has 10 active links. Revoke one first.", "")
		return
	case err != nil:
		a.serverError(w, r, "The link could not be created", err)
		return
	}
	a.auditEntity(r, auditPreviewLinkCreate, e.ID, e, map[string]any{"link_id": l.ID.String(), "lifetime": pick})
	a.renderShare(w, r, http.StatusOK, e, pick, "", a.addressOrigin(r)+"/preview/"+tok)
}

func (a *Admin) handleShareRevoke(w http.ResponseWriter, r *http.Request) {
	e, _, ok := a.loadEntity(w, r)
	if !ok || a.deps.Content.PreviewLinks == nil {
		return
	}
	link, good := uuidParam(r, "link")
	if !good {
		a.notFound(w, r, "Link", a.adminPath+"/entities/"+e.ID.String()+"/share", "Back to sharing")
		return
	}
	revoked, err := a.deps.Content.PreviewLinks.Revoke(r.Context(), e.ID, link)
	if err != nil {
		a.serverError(w, r, "The link could not be revoked", err)
		return
	}
	if revoked {
		a.auditEntity(r, auditPreviewLinkRevoke, e.ID, e, map[string]any{"link_id": link.String()})
		a.SetFlash(w, r, "success", "The link no longer works.")
	}
	Redirect(w, r, a.adminPath+"/entities/"+e.ID.String()+"/share")
}

// loadSharable is loadEntity for the share routes: 404 when sharing is not wired.
func (a *Admin) loadSharable(w http.ResponseWriter, r *http.Request) (*entity.Entity, *entity.EntitySchema, bool) {
	if a.deps.Content.PreviewLinks == nil {
		a.notFound(w, r, "Page", a.adminPath+"/entities", "Back to entities")
		return nil, nil, false
	}
	e, schema, ok := a.loadEntity(w, r)
	if ok && e.Status == entity.StatusArchived {
		a.notFound(w, r, "Page", a.adminPath+"/entities", "Back to entities")
		return nil, nil, false
	}
	return e, schema, ok
}

func (a *Admin) renderShare(w http.ResponseWriter, r *http.Request, status int, e *entity.Entity, pick, errMsg, newLink string) {
	ctx := r.Context()
	id := e.ID.String()
	base := a.adminPath + "/entities/" + id
	d := shareData{Name: e.Name, Action: base + "/share", Back: base, Error: errMsg, NewLink: newLink, Published: e.Status == entity.StatusPublished}
	if d.Published && a.deps.Content.PublicPath != nil {
		d.PublicURL = a.deps.Content.PublicPath(ctx, e)
	}
	for _, l := range shareLifetimes {
		d.Lifetimes = append(d.Lifetimes, ui.RadioCardItem{Value: l.Value, Title: l.Label, Checked: l.Value == pick})
	}
	links, err := a.deps.Content.PreviewLinks.List(ctx, e.ID)
	if err != nil {
		a.serverError(w, r, "The links could not be loaded", err)
		return
	}
	loc := a.siteLocation(ctx)
	t := ui.Table{
		ID: "preview-links", Caption: "Active preview links", BaseURL: base + "/share", CSRF: csrfToken(ctx),
		Columns: []ui.Col{{Key: "link", Label: "Link"}, {Key: "by", Label: "Made by"}, {Key: "expires", Label: "Works until"}},
		Empty:   ui.Empty{Icon: "link", Title: "No active links", Body: "A link you make shows up here."},
	}
	for _, l := range links {
		by := l.CreatedByName
		if by == "" {
			by = "Unknown user"
		}
		t.Rows = append(t.Rows, ui.Row{ID: l.ID.String(), Label: "link ending " + l.Tail, Cells: []ui.Cell{
			{Text: "…" + l.Tail}, {Text: by}, {Text: fmtDateTime(l.ExpiresAt.In(loc))},
		}, Actions: []ui.Action{{Label: "Revoke", Post: base + "/share/" + l.ID.String() + "/revoke", Danger: true,
			Confirm: "Anyone using this link loses access straight away.", ConfirmLabel: "Revoke link", Href: base + "/share"}}})
	}
	d.Table = t
	v := a.NewView(w, r, "Share a preview of "+e.Name)
	v.Data = d
	v.Crumbs = a.entityCrumbs(e.Name, id, Crumb{Label: "Share preview"})
	w.Header().Set("Cache-Control", "no-store")
	a.Render(w, r, status, "entities/share", v)
}
