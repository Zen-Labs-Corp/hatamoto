package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/person"
)

// reviewSentNote is what a status change or schedule that was staged adds to the
// save flash. It is a notice, not a failure: see noteKind.
const reviewSentNote = "Sent for review."

// reviewSentFlash is the flash after a save or publish that was staged.
const reviewSentFlash = "Sent for review. It goes live when a reviewer approves it."

// reviewState is what an editor shows about review for one entry and viewer.
// The zero value is the editor as it is with review off.
type reviewState struct {
	SendsForReview bool         // the viewer's publish or update waits for a reviewer
	Pending        *PendingEdit // a change waiting (or sent back) for this entry
	Mine           bool         // the viewer sent it
	Banner         string       // what the editor says about the pending change
	Kind           string       // callout kind of the banner: info, or warning when sent back
}

// reviewStateFor reads what the editor of e says about review for the signed-in
// viewer. It is the zero value when the site has no review flow or e is new.
func (a *Admin) reviewStateFor(ctx context.Context, e *entity.Entity) reviewState {
	rv := a.deps.Review
	if rv == nil || e == nil {
		return reviewState{}
	}
	st := reviewState{SendsForReview: rv.Plan(ctx, e)}
	p, ok, err := rv.Pending(ctx, e.ID.String())
	if err != nil {
		a.logger().Error("review: read the pending change", "entity", e.ID, "err", err)
		return st
	}
	if !ok {
		return st
	}
	if u := userFrom(ctx); u != nil {
		st.Mine = p.Requester == u.ID
	}
	loc := a.siteLocation(ctx)
	since := fmtWhen(p.Since.In(loc), time.Now().In(loc))
	switch {
	case st.Mine && p.Rejected:
		who := "Sent back"
		if p.Reviewer != "" {
			who += " by " + p.Reviewer
		}
		st.Pending, st.Banner, st.Kind = &p, who+": "+p.Comment, "warning"
	case st.Mine:
		st.Pending, st.Banner, st.Kind = &p, "Waiting for review since "+since+". Your change goes live when approved.", "info"
	case !p.Rejected:
		who := "Another change"
		if p.RequesterName != "" {
			who = p.RequesterName + "'s change"
		}
		st.Pending, st.Banner, st.Kind = &p, who+" to this entry is waiting for review since "+since+".", "info"
	}
	return st
}

// fmtWhen is a time as "10:42" on the same day as now, else with the date.
func fmtWhen(t, now time.Time) string {
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return fmtTime(t)
	}
	return fmtDateTime(t)
}

// withPendingChange returns e with the viewer's own pending change (name, slug,
// attributes) applied, for editing. Any other state returns e itself.
func withPendingChange(e *entity.Entity, st reviewState) *entity.Entity {
	if !st.Mine || st.Pending == nil || st.Pending.Payload == nil {
		return e
	}
	c := *e
	pl := st.Pending.Payload
	c.Name, c.Slug = pl.Name, pl.Slug
	if len(pl.Attributes) > 0 {
		c.Attributes = pl.Attributes
	}
	return &c
}

// sentForReview answers a save that was staged instead of written: a flash and a
// redirect to the entry's form. It reports whether it answered. A refusal
// (another person's change is waiting, ...) is not answered here: the form is
// re-rendered with what the user typed (entityFormError).
func (a *Admin) sentForReview(w http.ResponseWriter, r *http.Request, e *entity.Entity, err error) bool {
	return a.sentForReviewAt(w, r, a.adminPath+"/entities/"+e.ID.String(), err)
}

// sentForReviewAt is sentForReview for an editor that lives at another address.
func (a *Admin) sentForReviewAt(w http.ResponseWriter, r *http.Request, to string, err error) bool {
	switch {
	case errors.Is(err, entity.ErrSentForReview):
		a.SetFlash(w, r, "success", reviewSentFlash)
	default:
		return false
	}
	Redirect(w, r, to)
	return true
}

// heldExtras names what a posted save of a published entry would change that a
// review request does not carry: terms, a status change, the schedule and the
// scheduled unpublish. "" when the save is not held or carries nothing extra.
// The save is refused before anything is written, so nothing is dropped
// silently and nothing goes live without a reviewer.
func (a *Admin) heldExtras(r *http.Request, p *posted, e *entity.Entity, sc *entity.EntitySchema) string {
	rv := a.deps.Review
	if rv == nil || (e.Status != entity.StatusPublished && e.Status != entity.StatusScheduled) || !rv.Plan(r.Context(), e) {
		return ""
	}
	ctx := r.Context()
	var what []string
	want := entity.EntityStatus(p.f.Status)
	if sc.Slug != person.SchemaSlug && want != e.Status && entity.ValidStatus(want) {
		what = append(what, "the status")
	} else if want == entity.StatusScheduled {
		if at, err := time.ParseInLocation(scheduleLayout, p.f.Schedule, a.siteLocation(ctx)); err == nil && (e.PublishedAt == nil || !e.PublishedAt.Equal(at)) {
			what = append(what, "the publish date")
		}
	}
	if st := a.deps.Content.Unpublish; st != nil && p.f.UnpublishPosted {
		stored, _ := st.UnpublishFor(ctx, e.ID)
		at, err := time.ParseInLocation(scheduleLayout, p.f.UnpublishAt, a.siteLocation(ctx))
		switch {
		case p.f.UnpublishAt == "" && stored != nil:
			what = append(what, "the scheduled unpublish")
		case p.f.UnpublishAt != "" && err == nil && (stored == nil || !stored.At.Equal(at) || stored.RedirectTarget != p.f.UnpublishRedirect):
			what = append(what, "the scheduled unpublish")
		}
	}
	if c := a.deps.Content; c.Terms != nil && p.termsSet && (len(p.f.NewTerms) > 0 || p.f.NewTermName != "" || a.termsChanged(ctx, e, p.picked)) {
		what = append(what, "the terms")
	}
	if len(what) == 0 {
		return ""
	}
	return "A change to " + strings.Join(what, ", ") + " cannot be sent for review yet. Put it back as it was to send your other changes; what you typed is kept below."
}

// termsChanged reports whether picked differs from the terms e has now.
func (a *Admin) termsChanged(ctx context.Context, e *entity.Entity, picked map[string]bool) bool {
	ts, err := a.deps.Content.Terms.ListTermsForEntity(ctx, e.ID)
	if err != nil {
		return true
	}
	if len(ts) != len(picked) {
		return true
	}
	for _, t := range ts {
		if !picked[t.ID.String()] {
			return true
		}
	}
	return false
}

// noteKind is the flash kind of a save note: a notice that a change was sent for
// review is not an error.
func noteKind(note string) string {
	if strings.TrimSpace(strings.ReplaceAll(note, reviewSentNote, "")) == "" {
		return "success"
	}
	return "error"
}
