package admin

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/user"
)

// reviewHolding is an entity store whose gate holds: a person's publish, schedule,
// restore or edit of live content is answered with SentForReviewError and nothing is
// written, as entity.Service does with review on. A call without a subject (the
// system) passes through.
type reviewHolding struct {
	EntityStore
	held *bool
}

func (s reviewHolding) hold(ctx context.Context, id uuid.UUID, to entity.EntityStatus) error {
	if _, ok := authzctx.From(ctx); !ok || !*s.held {
		return nil
	}
	e, err := s.EntityStore.GetEntity(ctx, id)
	if err != nil {
		return err
	}
	live := func(st entity.EntityStatus) bool { return st == entity.StatusPublished || st == entity.StatusScheduled }
	if live(e.Status) || live(to) {
		return &entity.SentForReviewError{ChangesetID: 7, Since: time.Now()}
	}
	return nil
}

func (s reviewHolding) UpdateEntity(ctx context.Context, id uuid.UUID, by *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error) {
	if err := s.hold(ctx, id, entity.StatusDraft); err != nil {
		return nil, err
	}
	return s.EntityStore.UpdateEntity(ctx, id, by, in)
}

func (s reviewHolding) UpdateEntityStatus(ctx context.Context, id uuid.UUID, by *uuid.UUID, st entity.EntityStatus) (*entity.Entity, error) {
	if err := s.hold(ctx, id, st); err != nil {
		return nil, err
	}
	return s.EntityStore.UpdateEntityStatus(ctx, id, by, st)
}

func (s reviewHolding) ScheduleEntity(ctx context.Context, id uuid.UUID, by *uuid.UUID, at time.Time) (*entity.Entity, error) {
	if err := s.hold(ctx, id, entity.StatusScheduled); err != nil {
		return nil, err
	}
	return s.EntityStore.ScheduleEntity(ctx, id, by, at)
}

func (s reviewHolding) RestoreVersion(ctx context.Context, id uuid.UUID, by *uuid.UUID, n int) (*entity.Entity, error) {
	if err := s.hold(ctx, id, entity.StatusDraft); err != nil {
		return nil, err
	}
	return s.EntityStore.RestoreVersion(ctx, id, by, n)
}

// newReviewEditorRig is a roles rig whose entity store holds for review and whose
// ReviewFlow is the fake rv. Held switches the store's gate on.
func newReviewEditorRig(t *testing.T, rv *fakeReviewFlow) (*contentRig, map[user.Role]*user.User, *bool) {
	t.Helper()
	held := new(bool)
	c, users := newRolesRig(t, func(d *Deps) {
		d.Content.Entities = reviewHolding{EntityStore: d.Content.Entities, held: held}
		d.Review = rv
	})
	return c, users, held
}

func TestReviewEditorSaveIsStaged(t *testing.T) {
	rv := &fakeReviewFlow{held: true}
	c, users, held := newReviewEditorRig(t, rv)
	*held = true
	author := users[user.RoleAuthor]
	e := c.entityBy(author, entity.StatusPublished)
	path := "/admin/entities/" + e.ID.String()
	tok := c.freshToken(author.ID)

	form := parseForm(t, c.get(t, tok, path, ""))
	form.Set("name", "Changed by author")
	rec := c.do("POST", path, tok, form, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != path {
		t.Fatalf("save = %d %s\n%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	has(t, c.next(rec, path), "Sent for review. It goes live when a reviewer approves it.")
	if got, _ := c.ents.GetEntity(t.Context(), e.ID); got.Name != e.Name || got.Status != entity.StatusPublished {
		t.Errorf("the live row changed: %q %s", got.Name, got.Status)
	}
}

func TestReviewEditorPublishOfDraftSaysSentForReview(t *testing.T) {
	rv := &fakeReviewFlow{held: true}
	c, users, held := newReviewEditorRig(t, rv)
	*held = true
	author := users[user.RoleAuthor]
	e := c.entityBy(author, entity.StatusDraft)
	path := "/admin/entities/" + e.ID.String()
	tok := c.freshToken(author.ID)

	body := c.get(t, tok, path, "")
	has(t, body, "Publishing sends this entry for review.", `value="published"`)
	form := parseForm(t, body)
	form.Set("status", "published")
	rec := c.do("POST", path, tok, form, nil)
	if rec.Code != 303 {
		t.Fatalf("save = %d\n%s", rec.Code, rec.Body.String())
	}
	next := c.next(rec, path)
	has(t, next, "Saved. Sent for review.")
	lacks(t, next, "could not be set")
	if got, _ := c.ents.GetEntity(t.Context(), e.ID); got.Status != entity.StatusDraft {
		t.Errorf("status = %s, want draft", got.Status)
	}
}

func TestReviewEditorBanners(t *testing.T) {
	c, users, _ := newReviewEditorRig(t, nil)
	author := users[user.RoleAuthor]
	e := c.entityBy(author, entity.StatusPublished)
	path := "/admin/entities/" + e.ID.String()
	since := time.Date(2026, 11, 2, 10, 42, 0, 0, time.UTC)
	payload, _ := newPayload("Waiting title")
	rv := &fakeReviewFlow{pending: map[string]PendingEdit{e.ID.String(): {
		ID: 7, EntityID: e.ID.String(), Requester: author.ID.String(), RequesterName: "Author Sam", Since: since, Payload: payload,
	}}}
	c.a.deps.Review = rv

	t.Run("the requester edits their own change and sees the banner", func(t *testing.T) {
		body := c.get(t, c.freshToken(author.ID), path, "")
		has(t, body, `value="Waiting title"`, "Your change goes live when approved.", "Waiting for review since 2 Nov 2026 10:42.")
	})
	t.Run("someone else sees the live row and a notice naming the change", func(t *testing.T) {
		body := c.get(t, "owner-token", path, "")
		has(t, body, "Author Sam&#39;s change to this entry is waiting for review since")
		lacks(t, body, `value="Waiting title"`, "Your change goes live")
	})
	t.Run("a sent-back change shows the comment as a warning", func(t *testing.T) {
		p := rv.pending[e.ID.String()]
		p.Rejected, p.Comment, p.Reviewer = true, "Fix the intro.", "Alex"
		rv.pending[e.ID.String()] = p
		body := c.get(t, c.freshToken(author.ID), path, "")
		has(t, body, "Sent back by Alex: Fix the intro.", "hm-callout--warning", `value="Waiting title"`)
		if body := c.get(t, "owner-token", path, ""); strings.Contains(body, "Fix the intro.") {
			t.Error("a sent-back change is shown to someone else")
		}
	})
}

func TestReviewEditorRowAndBulkPublish(t *testing.T) {
	rv := &fakeReviewFlow{held: true}
	c, users, held := newReviewEditorRig(t, rv)
	*held = true
	author := users[user.RoleAuthor]
	d1, d2 := c.entityBy(author, entity.StatusDraft), c.entityBy(author, entity.StatusDraft)
	tok := c.freshToken(author.ID)

	rec := c.do("POST", "/admin/entities/"+d1.ID.String()+"/status", tok, url.Values{"status": {"published"}}, nil)
	if rec.Code != 303 {
		t.Fatalf("row publish = %d\n%s", rec.Code, rec.Body.String())
	}
	has(t, c.next(rec, "/admin/entities"), "Sent for review.")

	rec = c.do("POST", "/admin/entities/bulk", tok, url.Values{"action": {"publish"}, "id": {d1.ID.String(), d2.ID.String()}}, nil)
	if rec.Code != 303 {
		t.Fatalf("bulk = %d\n%s", rec.Code, rec.Body.String())
	}
	next := c.next(rec, "/admin/entities")
	has(t, next, "2 entries were sent for review.")
	lacks(t, next, "0 entities published", "could not be changed")
	for _, e := range []*entity.Entity{d1, d2} {
		if got, _ := c.ents.GetEntity(t.Context(), e.ID); got.Status != entity.StatusDraft {
			t.Errorf("%s status = %s, want draft", e.Name, got.Status)
		}
	}
}

func TestReviewEditorOffRendersAsBefore(t *testing.T) {
	plain := newContentRig(t)
	rv := newContentRigWith(t, func(d *Deps) { d.Review = &fakeReviewFlow{} })
	for _, path := range []string{"/admin/entities/" + fid(11).String(), "/admin/entities/" + fid(12).String()} {
		want := plain.get(t, "owner-token", path, "")
		if got := rv.get(t, "owner-token", path, ""); got != want {
			t.Errorf("%s differs with review off", path)
		}
	}
}

func TestEntityErrorTextForReview(t *testing.T) {
	t.Parallel()

	for err, want := range map[error]string{
		&entity.SentForReviewError{ChangesetID: 1}: "Sent for review.",
		entity.ErrReviewUnsupported:                "this change cannot be sent for review.",
		entity.ErrReviewHold:                       "the review policy refused the change.",
	} {
		if got := entityErrorText(err); got != want {
			t.Errorf("entityErrorText(%v) = %q, want %q", err, got, want)
		}
	}
}

// newPayload is a pending change's payload with the given name.
func newPayload(name string) (*entity.Entity, error) {
	return &entity.Entity{Name: name, Slug: "waiting-slug", Attributes: []byte(`{"summary":"Waiting summary"}`)}, nil
}

// A held save of a published entry cannot carry a status change: it is refused
// with the typed input kept, never staged with the status silently dropped.
func TestReviewEditorHeldSaveRefusesWhatItCannotCarry(t *testing.T) {
	rv := &fakeReviewFlow{held: true}
	c, users, held := newReviewEditorRig(t, rv)
	*held = true
	author := users[user.RoleAuthor]
	tok := c.freshToken(author.ID)

	t.Run("classic form", func(t *testing.T) {
		e := c.entityBy(author, entity.StatusPublished)
		path := "/admin/entities/" + e.ID.String()
		form := parseForm(t, c.get(t, tok, path, ""))
		form.Set("name", "Typed by author")
		form.Set("status", "draft")
		rec := c.do("POST", path, tok, form, nil)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("save = %d, want 422\n%s", rec.Code, rec.Body.String())
		}
		has(t, rec.Body.String(), "cannot be sent for review yet", "the status", `value="Typed by author"`)
	})
	t.Run("blog editor", func(t *testing.T) {
		_, path := blogRulesPost(c, author, entity.StatusPublished)
		form := parseForm(t, c.get(t, tok, path, ""))
		form.Set("name", "Typed by author")
		form.Set("status", "draft")
		form.Set("_op", "save")
		rec := c.do("POST", path, tok, form, nil)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("save = %d, want 422\n%s", rec.Code, rec.Body.String())
		}
		has(t, rec.Body.String(), "cannot be sent for review yet", "Typed by author")
	})
}

// reviewRefusing is an entity store whose gate refuses every person's update and
// status change with err (another change waiting, an unsupported change, a policy hold).
type reviewRefusing struct {
	EntityStore
	err error
}

func (s reviewRefusing) UpdateEntity(context.Context, uuid.UUID, *uuid.UUID, *entity.UpdateEntityInput) (*entity.Entity, error) {
	return nil, s.err
}

func (s reviewRefusing) UpdateEntityStatus(context.Context, uuid.UUID, *uuid.UUID, entity.EntityStatus) (*entity.Entity, error) {
	return nil, s.err
}

// Another person's change is waiting: the save is refused with what the user
// typed still in the form (422), and the blog editor keeps the recovery copy.
func TestReviewEditorPendingRefusalKeepsInput(t *testing.T) {
	rec := newFakeRecovery()
	c, users := newRolesRig(t, func(d *Deps) {
		d.Content.Entities = reviewRefusing{EntityStore: d.Content.Entities, err: entity.ErrReviewPending}
		d.Content.Recovery = rec
		d.Review = &fakeReviewFlow{}
	})
	editor := users[user.RoleAuthor]
	tok := c.freshToken(editor.ID)

	t.Run("classic form", func(t *testing.T) {
		e := c.entityBy(editor, entity.StatusPublished)
		path := "/admin/entities/" + e.ID.String()
		form := parseForm(t, c.get(t, tok, path, ""))
		form.Set("name", "Ten minutes of typing")
		w := c.do("POST", path, tok, form, nil)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("save = %d, want 422\n%s", w.Code, w.Body.String())
		}
		has(t, w.Body.String(), "Another change to this entry is waiting for review.", `value="Ten minutes of typing"`)
	})
	t.Run("blog editor", func(t *testing.T) {
		e, path := blogRulesPost(c, editor, entity.StatusPublished)
		if err := rec.Put(t.Context(), editor.ID, e.ID, RecoveryCopy{}); err != nil {
			t.Fatal(err)
		}
		form := parseForm(t, c.get(t, tok, path, ""))
		form.Set("name", "Ten minutes of typing")
		form.Set("_op", "save")
		w := c.do("POST", path, tok, form, nil)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("save = %d, want 422\n%s", w.Code, w.Body.String())
		}
		has(t, w.Body.String(), "Another change to this entry is waiting for review.", "Ten minutes of typing")
		if rec.count() != 1 {
			t.Errorf("recovery copies = %d, the refusal must keep the copy", rec.count())
		}
	})
}
