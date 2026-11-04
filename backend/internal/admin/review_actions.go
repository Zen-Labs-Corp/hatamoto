package admin

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/httpx"
)

const (
	reviewItemPrefix       = "item_"
	reviewSuggestionPrefix = "suggestion_"
	reviewGroupField       = "group_redirects"
)

// mountReviewActions registers what the owner does with a staged changeset:
// decide risky items, decide suggestions, publish, discard. Approving a
// suggestion changes site settings, so that route alone asks for the password
// again; a POST is never replayed after that page.
func (a *Admin) mountReviewActions(owner *httpx.Group) {
	owner.HandleFunc("POST /plugins/{name}/changesets/{id}/decide", a.handleReviewDecide)
	owner.HandleFunc("POST /plugins/{name}/changesets/{id}/publish", a.handleReviewPublish)
	owner.HandleFunc("POST /plugins/{name}/changesets/{id}/discard", a.handleReviewDiscard)
	sudo := owner.Group("", a.RequireSudo)
	sudo.HandleFunc("POST /plugins/{name}/changesets/{id}/suggestions", a.handleReviewSuggestions)
}

// reviewTarget checks the reviewer is wired and the changeset is the plugin's.
func (a *Admin) reviewTarget(w http.ResponseWriter, r *http.Request) (changeset.Header, ChangesetReviewer, bool) {
	rv := a.deps.Growth.Reviewer
	if rv == nil {
		a.unavailable(w, r, "Changeset review")
		return changeset.Header{}, nil, false
	}
	h, ok := a.loadChangeset(w, r)
	return h, rv, ok
}

// reviewError answers the reviewer's refusals. It reports whether err was one.
func (a *Admin) reviewError(w http.ResponseWriter, r *http.Request, h changeset.Header, err error) bool {
	back, label := a.changesetURL(h.Plugin, h.ID, ""), "Back to the changes"
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrReviewNotFound):
		a.ErrorBack(w, r, http.StatusNotFound, "Changeset not found", "That changeset does not exist.", a.adminPath+"/plugins", "Back to plugins")
	case errors.Is(err, ErrReviewNotStaged):
		a.ErrorBack(w, r, http.StatusConflict, "Not waiting for review", "These changes are no longer waiting for a decision.", back, label)
	case errors.Is(err, ErrReviewRunning):
		a.ErrorBack(w, r, http.StatusConflict, "Still running", "The run has not finished, so these changes cannot be reviewed or published yet.", back, label)
	case errors.Is(err, ErrReviewExpired):
		a.ErrorBack(w, r, http.StatusConflict, "Changes expired", "Nobody decided in time, so these changes expired and were deleted.", back, label)
	default:
		a.serverError(w, r, "The changes could not be updated", err)
	}
	return true
}

func (a *Admin) handleReviewDecide(w http.ResponseWriter, r *http.Request) {
	h, rv, ok := a.reviewTarget(w, r)
	if !ok {
		return
	}
	n, ok := a.saveDecisions(w, r, h, rv)
	if !ok {
		return
	}
	if n > 0 {
		a.SetFlash(w, r, "success", "Saved your decisions.")
	}
	back := a.changesetURL(h.Plugin, h.ID, "")
	if page := reviewPageNumber(r); page > 1 {
		back += "?page=" + strconv.Itoa(page)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// saveDecisions records the Keep/Drop choices posted with the form and reports
// how many there were. It has answered the request itself when ok is false.
func (a *Admin) saveDecisions(w http.ResponseWriter, r *http.Request, h changeset.Header, rv ChangesetReviewer) (n int, ok bool) {
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return 0, false
	}
	bySeq := map[int]string{}
	for k, v := range r.PostForm {
		seq, ok := strings.CutPrefix(k, reviewItemPrefix)
		if !ok || len(v) == 0 {
			continue
		}
		num, err := strconv.Atoi(seq)
		if err != nil || num <= 0 || !validDecision(v[0], "dropped") {
			a.Error(w, r, http.StatusBadRequest, "Bad request", "A decision in the form is not valid.")
			return 0, false
		}
		bySeq[num] = v[0]
	}
	if g := r.PostFormValue(reviewGroupField); g != "" {
		if !validDecision(g, "dropped") {
			a.Error(w, r, http.StatusBadRequest, "Bad request", "A decision in the form is not valid.")
			return 0, false
		}
		rd, err := rv.Review(r.Context(), h.ID, 1)
		if a.reviewError(w, r, h, err) {
			return 0, false
		}
		if rd.RedirectGroup != nil {
			for _, seq := range rd.RedirectGroup.Seqs {
				bySeq[seq] = g
			}
		}
	}
	for _, decision := range []string{"approved", "dropped"} {
		var seqs []int
		for seq, d := range bySeq {
			if d == decision {
				seqs = append(seqs, seq)
			}
		}
		if len(seqs) == 0 {
			continue
		}
		sort.Ints(seqs)
		if a.reviewError(w, r, h, rv.Decide(r.Context(), h.ID, seqs, decision)) {
			return 0, false
		}
	}
	return len(bySeq), true
}

// validDecision reports whether v is "approved" or the other allowed word.
func validDecision(v, other string) bool { return v == "approved" || v == other }

func (a *Admin) handleReviewSuggestions(w http.ResponseWriter, r *http.Request) {
	h, rv, ok := a.reviewTarget(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	byID := map[int64]string{}
	var ids []int64
	for k, v := range r.PostForm {
		raw, ok := strings.CutPrefix(k, reviewSuggestionPrefix)
		if !ok || len(v) == 0 {
			continue
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 || !validDecision(v[0], "rejected") {
			a.Error(w, r, http.StatusBadRequest, "Bad request", "A decision in the form is not valid.")
			return
		}
		byID[id] = v[0]
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		decision := byID[id]
		if err := rv.DecideSuggestion(r.Context(), h.ID, id, decision); errors.Is(err, ErrReviewSuggestionDecided) || errors.Is(err, ErrReviewNotApplicable) {
			a.SetFlash(w, r, "error", err.Error()+".")
			http.Redirect(w, r, a.changesetURL(h.Plugin, h.ID, ""), http.StatusSeeOther)
			return
		} else if a.reviewError(w, r, h, err) {
			return
		}
		action := "changeset.suggestion.reject"
		if decision == "approved" {
			action = "changeset.suggestion.approve"
		}
		a.audit(r, action, strconv.FormatInt(h.ID, 10), map[string]any{"plugin": h.Plugin, "changeset": h.ID, "suggestion": id})
	}
	if len(ids) > 0 {
		a.SetFlash(w, r, "success", "Saved the suggestion decisions.")
	}
	http.Redirect(w, r, a.changesetURL(h.Plugin, h.ID, ""), http.StatusSeeOther)
}

func (a *Admin) handleReviewPublish(w http.ResponseWriter, r *http.Request) {
	h, rv, ok := a.reviewTarget(w, r)
	if !ok {
		return
	}
	review := a.changesetURL(h.Plugin, h.ID, "")
	// The Keep/Drop choices on screen may not be saved yet: they decide what goes live.
	if _, ok := a.saveDecisions(w, r, h, rv); !ok {
		return
	}
	applied, dropped, err := rv.Publish(r.Context(), h.ID)
	if errors.Is(err, ErrReviewUndecided) {
		a.SetFlash(w, r, "error", a.undecidedText(r, rv, h.ID))
		http.Redirect(w, r, review, http.StatusSeeOther)
		return
	}
	if a.reviewError(w, r, h, err) {
		return
	}
	a.audit(r, "changeset.publish", strconv.FormatInt(h.ID, 10), map[string]any{"plugin": h.Plugin, "changeset": h.ID, "applied": applied, "dropped": dropped})
	msg := fmt.Sprintf("Published changeset #%d: %s live", h.ID, countNoun(applied, "change"))
	if dropped > 0 {
		msg += fmt.Sprintf(", %d dropped by you", dropped)
	}
	a.SetFlash(w, r, "success", msg+".")
	http.Redirect(w, r, a.adminPath+"/settings/changes", http.StatusSeeOther)
}

// undecidedText says how much is still undecided, from the reviewer's own count.
func (a *Admin) undecidedText(r *http.Request, rv ChangesetReviewer, id int64) string {
	rd, err := rv.Review(r.Context(), id, 1)
	if err != nil {
		return "Some risky items or suggestions still need a decision."
	}
	suggestions := 0
	for _, s := range rd.Suggestions {
		if s.Decision == "" {
			suggestions++
		}
	}
	return fmt.Sprintf("%s and %s still need a decision.", countNoun(max(0, rd.High+rd.Critical-rd.HighDecided), "risky item"), countNoun(suggestions, "suggestion"))
}

func (a *Admin) handleReviewDiscard(w http.ResponseWriter, r *http.Request) {
	h, rv, ok := a.reviewTarget(w, r)
	if !ok {
		return
	}
	if a.reviewError(w, r, h, rv.Discard(r.Context(), h.ID)) {
		return
	}
	a.audit(r, "changeset.discard", strconv.FormatInt(h.ID, 10), map[string]any{"plugin": h.Plugin, "changeset": h.ID})
	a.SetFlash(w, r, "success", fmt.Sprintf("Discarded changeset #%d. Nothing reached the site.", h.ID))
	http.Redirect(w, r, a.pluginURL(h.Plugin, "runs"), http.StatusSeeOther)
}
