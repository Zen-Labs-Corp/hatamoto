package admin

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"hatamoto.dev/core/internal/entity"
)

// Audit actions of the Translations tab.
const (
	auditTranslationLink     = "entity.translation_link"
	auditTranslationUnlink   = "entity.translation_unlink"
	auditTranslationOriginal = "entity.translation_original"
)

const candidateLimit = 10

// translationsURL is the Translations tab of entity id.
func (a *Admin) translationsURL(id uuid.UUID) string {
	return a.adminPath + "/entities/" + id.String() + "/translations"
}

// translationTarget loads the entity of the route and refuses the request when
// the group store is off. ok is false once the response is written.
func (a *Admin) translationTarget(w http.ResponseWriter, r *http.Request) (*entity.Entity, bool) {
	e, sc, ok := a.loadEntity(w, r)
	if !ok {
		return nil, false
	}
	e.Schema = sc
	if a.deps.Content.Groups == nil {
		a.notFound(w, r, "Entity", a.adminPath+"/entities", "Back to entities")
		return nil, false
	}
	return e, true
}

// groupMember reports whether member is in the group of e (e itself counts).
func (a *Admin) groupMember(r *http.Request, e *entity.Entity, member uuid.UUID) (bool, error) {
	if member == e.ID {
		return true, nil
	}
	ms, err := a.deps.Content.Groups.Members(r.Context(), e.ID)
	if err != nil {
		return false, err
	}
	for _, m := range ms {
		if m.ID == member {
			return true, nil
		}
	}
	return false, nil
}

// translationDone ends a successful write: purge cached pages, flash, go back.
func (a *Admin) translationDone(w http.ResponseWriter, r *http.Request, e *entity.Entity, message string) {
	// Group writes emit no entity events, and the alternate links of every
	// copy change, so purge the render cache here.
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(r.Context())
	}
	a.SetFlash(w, r, "success", message)
	Redirect(w, r, a.translationsURL(e.ID))
}

// handleTranslationCandidates answers the search box of "Link an existing
// page": pages of the same type, without a group, in a locale this entry's
// group does not have yet.
func (a *Admin) handleTranslationCandidates(w http.ResponseWriter, r *http.Request) {
	e, ok := a.translationTarget(w, r)
	if !ok {
		return
	}
	cands, err := a.deps.Content.Groups.LinkCandidates(r.Context(), e.ID, r.URL.Query().Get("q"), candidateLimit)
	if err != nil {
		a.logger().Error("link candidates failed", "err", err)
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}
	out := make([]pickJSON, 0, len(cands))
	for _, c := range cands {
		out = append(out, pickJSON{Value: c.ID.String(), Label: c.Name, Hint: c.Locale + ", /" + c.Slug})
	}
	a.writeJSON(w, http.StatusOK, out)
}

// handleTranslationLink links the page picked in the dialog to this entry.
// Linking two ungrouped pages makes this entry the original.
func (a *Admin) handleTranslationLink(w http.ResponseWriter, r *http.Request) {
	e, ok := a.translationTarget(w, r)
	if !ok {
		return
	}
	g := a.deps.Content.Groups
	other, err := uuid.Parse(r.PostFormValue("page"))
	if err != nil || other == e.ID {
		a.SetFlash(w, r, "error", "Pick the page to link first.")
		Redirect(w, r, a.translationsURL(e.ID))
		return
	}
	// The picker only offers pages without a group; do not trust the client.
	if taken, err := g.Members(r.Context(), other); err != nil {
		a.serverError(w, r, "The translations could not be linked", err)
		return
	} else if len(taken) > 1 { // a one-member group counts as none
		a.SetFlash(w, r, "error", "That page already has translations. Unlink it first.")
		Redirect(w, r, a.translationsURL(e.ID))
		return
	}
	if _, err := g.Link(r.Context(), e.ID, other); err != nil {
		if errors.Is(err, entity.ErrInvalidGroup) || isUniqueViolation(err) {
			a.SetFlash(w, r, "error", "That page cannot be linked: it needs the same type and a locale this page does not have yet.")
			Redirect(w, r, a.translationsURL(e.ID))
			return
		}
		a.serverError(w, r, "The translations could not be linked", err)
		return
	}
	a.auditEntity(r, auditTranslationLink, e.ID, e, map[string]any{"linked": other.String()})
	a.translationDone(w, r, e, "Linked the page.")
}

// isUniqueViolation reports a Postgres unique-index violation, such as a
// concurrent write winning the translation_original index.
func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// handleTranslationUnlinkConfirm is the no-JS confirmation of an unlink.
func (a *Admin) handleTranslationUnlinkConfirm(w http.ResponseWriter, r *http.Request) {
	e, ok := a.translationTarget(w, r)
	if !ok {
		return
	}
	a.renderConfirm(w, r, confirmData{
		Title: "Unlink this copy of “" + e.Name + "”?", Message: "The page stays as it is, but it is no longer listed as a translation, and search engines stop seeing the link between them.",
		Post: a.translationsURL(e.ID) + "/unlink", Label: "Unlink", Back: a.translationsURL(e.ID),
		Hidden: []hiddenField{{Name: "member", Value: r.URL.Query().Get("member")}},
	})
}

// handleTranslationUnlink takes one member out of this entry's group.
func (a *Admin) handleTranslationUnlink(w http.ResponseWriter, r *http.Request) {
	e, ok := a.translationTarget(w, r)
	if !ok {
		return
	}
	member, ok := a.postedMember(w, r, e)
	if !ok {
		return
	}
	if err := a.deps.Content.Groups.Unlink(r.Context(), member); err != nil {
		a.serverError(w, r, "The page could not be unlinked", err)
		return
	}
	a.auditEntity(r, auditTranslationUnlink, e.ID, e, map[string]any{"member": member.String()})
	a.translationDone(w, r, e, "Unlinked the page.")
}

// handleTranslationOriginal makes one member the original of the group.
func (a *Admin) handleTranslationOriginal(w http.ResponseWriter, r *http.Request) {
	e, ok := a.translationTarget(w, r)
	if !ok {
		return
	}
	member, ok := a.postedMember(w, r, e)
	if !ok {
		return
	}
	if err := a.deps.Content.Groups.SetOriginal(r.Context(), member); err != nil {
		if errors.Is(err, entity.ErrNotGrouped) {
			a.SetFlash(w, r, "error", "Link a translation first, then choose the original.")
			Redirect(w, r, a.translationsURL(e.ID))
			return
		}
		a.serverError(w, r, "The original could not be changed", err)
		return
	}
	a.auditEntity(r, auditTranslationOriginal, e.ID, e, map[string]any{"member": member.String()})
	a.translationDone(w, r, e, "Changed the original.")
}

// postedMember reads the member field and checks it is in e's group.
func (a *Admin) postedMember(w http.ResponseWriter, r *http.Request, e *entity.Entity) (uuid.UUID, bool) {
	member, err := uuid.Parse(r.FormValue("member")) // the unlink dialog posts it in the query
	if err != nil {
		a.notFound(w, r, "Translation", a.translationsURL(e.ID), "Back to translations")
		return uuid.Nil, false
	}
	in, err := a.groupMember(r, e, member)
	if err != nil {
		a.serverError(w, r, "The translations could not be loaded", err)
		return uuid.Nil, false
	}
	if !in {
		a.notFound(w, r, "Translation", a.translationsURL(e.ID), "Back to translations")
		return uuid.Nil, false
	}
	return member, true
}
