package reviewflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/reviewstate"
)

// Errors of the decisions. The engine's own (changeset.ErrSelfDecision,
// ErrNotStaged, ErrStageRejected, ErrStagesPending) pass through.
var (
	ErrNotDecider    = errors.New("you cannot decide this stage")
	ErrUnknownGroup  = errors.New("this stage needs a group this site does not define")
	ErrEditStale     = errors.New("the entry changed after this was sent for review")
	ErrNotRequester  = errors.New("only the person who sent this change can withdraw it")
	ErrCommentNeeded = errors.New("say what to change: a comment is required")
)

// Outcome is what an approval or a publish retry did to the live entry.
type Outcome struct {
	// Published is true when the change went live.
	Published bool
	// NotApplied is true when the entry moved between the stale check and the
	// publish: nothing was written, the change stays readable.
	NotApplied bool
	Conflicts  []changeset.ConflictItem
	// Pending is true when stages remain to be decided.
	Pending bool
}

// edit is a pending edit as the decisions read it.
type edit struct {
	row  changeset.EditRow
	item changeset.StoredItem
}

// load reads staged edit csID. ErrNotStaged when it is gone, closed or not an edit.
func (f *Flow) load(ctx context.Context, csID int64) (edit, error) {
	h, items, err := f.Engine.Get(ctx, csID)
	if err != nil {
		return edit{}, err
	}
	if h.Kind != changeset.ChangesetEdit {
		return edit{}, changeset.ErrNotEdit
	}
	if h.State != changeset.StateStaged || len(items) == 0 {
		return edit{}, changeset.ErrNotStaged
	}
	row, ok, err := f.Engine.PendingEdit(ctx, items[0].TargetID)
	if err != nil {
		return edit{}, err
	}
	if !ok || row.ID != csID {
		return edit{}, changeset.ErrNotStaged
	}
	return edit{row: row, item: items[0]}, nil
}

func subject(ctx context.Context) (authz.Subject, error) {
	s, ok := authzctx.From(ctx)
	if !ok || s.UserID == "" {
		return authz.Subject{}, fmt.Errorf("%w: not signed in", ErrNotDecider)
	}
	return s, nil
}

// mayDecide maps a stage group to a right: the default group "reviewers" needs
// entity.review.decide, any other group names nobody core knows.
func mayDecide(ctx context.Context, s authz.Subject, group, target string) error {
	if group != reviewstate.DefaultGroup {
		return fmt.Errorf("%w: %q", ErrUnknownGroup, group)
	}
	return mayDecideAny(ctx, s, target)
}

func mayDecideAny(ctx context.Context, s authz.Subject, target string) error {
	ok, err := authz.Can(ctx, s, authz.ActionEntityReviewDecide, authz.Object{Kind: "entity", ID: target})
	if err != nil {
		return fmt.Errorf("failed to check the review right: %w", err)
	}
	if !ok {
		return ErrNotDecider
	}
	return nil
}

// Approve records the subject's approval of the current stage and publishes
// when every stage is approved. The requester cannot approve their own change
// (changeset.ErrSelfDecision). The base token is compared with the live row
// first: a moved row refuses with ErrEditStale and records nothing. A publish
// failure after the decision is returned with the decision kept; Publish retries it.
func (f *Flow) Approve(ctx context.Context, csID int64, comment string) (Outcome, error) {
	s, err := subject(ctx)
	if err != nil {
		return Outcome{}, err
	}
	ed, err := f.load(ctx, csID)
	if err != nil {
		return Outcome{}, err
	}
	if ed.row.Requester == s.UserID {
		return Outcome{}, changeset.ErrSelfDecision
	}
	if ed.row.Rejected {
		return Outcome{}, changeset.ErrStageRejected
	}
	stage := ed.row.Approvals
	if stage >= len(ed.row.Stages) {
		return Outcome{}, fmt.Errorf("%w: every stage is approved, publish it instead", changeset.ErrStageOrder)
	}
	if err := mayDecide(ctx, s, ed.row.Stages[stage].Group, ed.item.TargetID); err != nil {
		return Outcome{}, err
	}
	if err := f.checkFresh(ctx, ed.item); err != nil {
		return Outcome{}, err
	}
	err = f.Engine.DecideStage(ctx, csID, changeset.StageDecision{
		Stage: stage, Reviewer: s.UserID, Decision: changeset.StageApproved, Comment: strings.TrimSpace(comment)})
	if err != nil {
		return Outcome{}, err
	}
	if stage+1 < len(ed.row.Stages) {
		return Outcome{Pending: true}, nil
	}
	return f.publish(ctx, s, ed, comment)
}

// Publish retries the publish of a change whose stages are all approved but
// that is still staged (the publish failed after the last approval).
func (f *Flow) Publish(ctx context.Context, csID int64) (Outcome, error) {
	s, err := subject(ctx)
	if err != nil {
		return Outcome{}, err
	}
	ed, err := f.load(ctx, csID)
	if err != nil {
		return Outcome{}, err
	}
	if ed.row.Requester == s.UserID {
		return Outcome{}, changeset.ErrSelfDecision
	}
	if ed.row.Rejected {
		return Outcome{}, changeset.ErrStageRejected
	}
	if ed.row.Approvals < len(ed.row.Stages) {
		return Outcome{}, fmt.Errorf("%w: %d approved of %d", changeset.ErrStagesPending, ed.row.Approvals, len(ed.row.Stages))
	}
	if err := mayDecideAny(ctx, s, ed.item.TargetID); err != nil {
		return Outcome{}, err
	}
	return f.publish(ctx, s, ed, "")
}

// publish runs the engine's Publish (never PublishReviewed) and mails the requester.
func (f *Flow) publish(ctx context.Context, s authz.Subject, ed edit, comment string) (Outcome, error) {
	res, err := f.Engine.Publish(ctx, ed.row.ID)
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{Published: res.Applied > 0, Conflicts: res.Conflicts}
	if res.Applied == 0 && len(res.Conflicts) > 0 {
		out.Published, out.NotApplied = false, true
		f.mailRequester(ctx, s, ed, email.ReviewNotApplied, comment)
		return out, nil
	}
	f.mailRequester(ctx, s, ed, email.ReviewApproved, comment)
	return out, nil
}

// checkFresh refuses an approval when the live row is no longer the one the
// edit was based on.
func (f *Flow) checkFresh(ctx context.Context, it changeset.StoredItem) error {
	id, err := uuid.Parse(it.TargetID)
	if err != nil {
		return fmt.Errorf("the staged change has no entry: %w", err)
	}
	live, err := f.Entities.GetEntity(ctx, id)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrEditStale, err)
	}
	if changeset.EntityToken(live.Version, live.UpdatedAt) != it.BaseToken {
		return ErrEditStale
	}
	return nil
}

// SendBack records a rejection with the reviewer's comment. The edit stays
// staged with its payload, so the requester sees their version and the comment;
// it leaves the review queue. It never discards.
func (f *Flow) SendBack(ctx context.Context, csID int64, comment string) error {
	s, err := subject(ctx)
	if err != nil {
		return err
	}
	comment = strings.TrimSpace(comment)
	if comment == "" {
		return ErrCommentNeeded
	}
	ed, err := f.load(ctx, csID)
	if err != nil {
		return err
	}
	if ed.row.Requester == s.UserID {
		return changeset.ErrSelfDecision
	}
	if ed.row.Rejected {
		return changeset.ErrStageRejected
	}
	stage := ed.row.Approvals
	if stage >= len(ed.row.Stages) {
		return fmt.Errorf("%w: every stage is approved", changeset.ErrStageOrder)
	}
	if err := mayDecide(ctx, s, ed.row.Stages[stage].Group, ed.item.TargetID); err != nil {
		return err
	}
	err = f.Engine.DecideStage(ctx, csID, changeset.StageDecision{
		Stage: stage, Reviewer: s.UserID, Decision: changeset.StageRejected, Comment: comment})
	if err != nil {
		return err
	}
	f.mailRequester(ctx, s, ed, email.ReviewSentBack, comment)
	return nil
}

// Withdraw lets the requester drop their own waiting or sent-back change.
func (f *Flow) Withdraw(ctx context.Context, csID int64) error {
	s, err := subject(ctx)
	if err != nil {
		return err
	}
	ed, err := f.load(ctx, csID)
	if err != nil {
		return err
	}
	if ed.row.Requester != s.UserID {
		return ErrNotRequester
	}
	return f.Engine.Discard(ctx, csID)
}

// Discard lets a holder of the decide right drop any waiting change, including
// one whose stage group this site does not define.
func (f *Flow) Discard(ctx context.Context, csID int64) error {
	s, err := subject(ctx)
	if err != nil {
		return err
	}
	ed, err := f.load(ctx, csID)
	if err != nil {
		return err
	}
	if err := mayDecideAny(ctx, s, ed.item.TargetID); err != nil {
		return err
	}
	return f.Engine.Discard(ctx, csID)
}

// mailRequester tells the requester what happened. A missing requester, an
// unreadable user or a mail error is logged and never fails the decision.
func (f *Flow) mailRequester(ctx context.Context, s authz.Subject, ed edit, outcome, comment string) {
	if f.Mail == nil || f.Users == nil {
		return
	}
	log := f.logger()
	to, err := uuid.Parse(ed.row.Requester)
	if err != nil {
		log.WarnContext(ctx, "review mail skipped: the requester is not a user", "changeset", ed.row.ID)
		return
	}
	req, err := f.Users.GetByID(ctx, to)
	if err != nil || req == nil || req.Email == "" {
		log.WarnContext(ctx, "review mail skipped: the requester cannot be read", "changeset", ed.row.ID)
		return
	}
	d := email.ReviewDecisionData{EntryName: ed.row.Name, Comment: strings.TrimSpace(comment), Outcome: outcome}
	if d.EntryName == "" {
		d.EntryName = payloadName(ed.item)
	}
	if id, err := uuid.Parse(s.UserID); err == nil {
		if rv, err := f.Users.GetByID(ctx, id); err == nil && rv != nil {
			d.Reviewer = reviewerName(rv.Name, rv.Email)
		}
	}
	if f.Site != nil {
		var admin string
		d.SiteName, admin = f.Site(ctx)
		d.URL = admin + "/review/" + fmt.Sprint(ed.row.ID)
	}
	if err := f.Mail.SendReviewDecision(ctx, req.Email, d); err != nil {
		log.WarnContext(ctx, "review mail failed", "changeset", ed.row.ID, "error", err)
	}
}

func reviewerName(name *string, addr string) string {
	if name != nil && strings.TrimSpace(*name) != "" {
		return strings.TrimSpace(*name)
	}
	return addr
}

func payloadName(it changeset.StoredItem) string {
	var p struct {
		Name string `json:"name"`
	}
	if jsonx.Unmarshal(it.Payload, &p) != nil || p.Name == "" {
		return "the entry"
	}
	return p.Name
}
