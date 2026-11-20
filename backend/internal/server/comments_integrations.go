package server

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/sdk"
)

// commentEventObserver turns comment transitions into the SDK comment actions
// on the hook engine; webhooks and plugins both listen there. One call fires at
// most one event. A draft never fires; a comment stored or confirmed directly
// as spam (a bot) fires nothing, only a moderator's spam transition does.
type commentEventObserver struct {
	comment.NopObserver // OnUpdated (edits, unapproval) and OnSettingsChanged fire nothing
	fire                func(ctx context.Context, name string, ev sdk.CommentEvent)
	quietSpam           sync.Map // comment id -> struct{}: the OnSpam that follows a spam store/confirm
}

func newCommentEventObserver(h *hooks.Engine) *commentEventObserver {
	return &commentEventObserver{fire: func(ctx context.Context, name string, ev sdk.CommentEvent) {
		h.Fire(ctx, name, ev)
	}}
}

func (o *commentEventObserver) OnCreated(ctx context.Context, c comment.Comment) { o.arrived(ctx, c) }
func (o *commentEventObserver) OnConfirmed(ctx context.Context, c comment.Comment) {
	o.arrived(ctx, c)
}

func (o *commentEventObserver) arrived(ctx context.Context, c comment.Comment) {
	switch c.Status {
	case comment.StatusPending, comment.StatusApproved:
		o.fire(ctx, sdk.CommentCreated.Name, commentEvent(c))
	case comment.StatusSpam:
		o.quietSpam.Store(c.ID, struct{}{}) // the service calls OnSpam next, synchronously
	}
}

func (o *commentEventObserver) OnApproved(ctx context.Context, c comment.Comment) {
	o.fire(ctx, sdk.CommentApproved.Name, commentEvent(c))
}

func (o *commentEventObserver) OnSpam(ctx context.Context, c comment.Comment) {
	if _, quiet := o.quietSpam.LoadAndDelete(c.ID); quiet {
		return
	}
	o.fire(ctx, sdk.CommentSpam.Name, commentEvent(c))
}

// OnDeleted carries ids and status only: the comment is gone.
func (o *commentEventObserver) OnDeleted(ctx context.Context, c comment.Comment) {
	if c.Status == comment.StatusDraft {
		return // never announced
	}
	o.fire(ctx, sdk.CommentDeleted.Name, sdk.CommentEvent{
		ID: c.ID.String(), EntityID: c.EntityID.String(), ParentID: idString(c.ParentID),
		Status: string(c.Status), CreatedAt: c.CreatedAt.UTC(),
	})
}

func idString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// commentEvent copies only public fields: never the raw body, hashes, IP,
// user agent, spam fields or the commenter id.
func commentEvent(c comment.Comment) sdk.CommentEvent {
	return sdk.CommentEvent{
		ID: c.ID.String(), EntityID: c.EntityID.String(), ParentID: idString(c.ParentID),
		AuthorName: c.AuthorName, Staff: c.AuthorUserID != nil, BodyHTML: c.BodyHTML,
		Status: string(c.Status), Locale: c.Locale, CreatedAt: c.CreatedAt.UTC(),
	}
}

// pluginCommentReader implements pluginhost.CommentReader over the server's
// comment service, read at call time (plugins are set up before comments).
// Only approved comments of published entities whose comments are not hidden
// are returned; anything else (comments not set up, site switch off, unknown
// or unpublished entity) is an empty thread or a zero count, never an error,
// so a plugin cannot probe what is not public.
type pluginCommentReader struct{ s *Server }

func (r pluginCommentReader) Thread(ctx context.Context, entityID uuid.UUID, page int) (sdk.CommentThread, error) {
	empty := sdk.CommentThread{Items: []sdk.CommentItem{}, Page: max(page, 1)}
	ok, err := r.visible(ctx, entityID)
	if err != nil || !ok {
		return empty, err
	}
	tp, err := r.s.comments.Thread(ctx, entityID, page)
	if err != nil {
		return empty, err
	}
	return pluginThread(tp), nil
}

func (r pluginCommentReader) Counts(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]int, error) {
	out := make(map[uuid.UUID]int, len(ids))
	for _, id := range ids {
		out[id] = 0
	}
	if r.s.comments == nil || r.s.db == nil || len(ids) == 0 {
		return out, nil
	}
	pub, err := entity.NewPostgresPublicRepository(r.s.db).GetPublishedByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	var shown []uuid.UUID
	for _, e := range pub {
		mode, err := r.s.comments.EntityMode(ctx, e.ID)
		if errors.Is(err, comment.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if mode != comment.ModeOff && mode != comment.ModeHidden {
			shown = append(shown, e.ID)
		}
	}
	if len(shown) == 0 {
		return out, nil
	}
	counts, err := r.s.comments.Counts(ctx, shown)
	if err != nil {
		return nil, err
	}
	for _, id := range shown {
		out[id] = counts[id].Approved // never Pending
	}
	return out, nil
}

// visible reports whether the entity's thread is public.
func (r pluginCommentReader) visible(ctx context.Context, id uuid.UUID) (bool, error) {
	if r.s.comments == nil || r.s.db == nil {
		return false, nil
	}
	mode, err := r.s.comments.EntityMode(ctx, id)
	if errors.Is(err, comment.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if mode == comment.ModeOff || mode == comment.ModeHidden {
		return false, nil
	}
	if _, err := entity.NewPostgresPublicRepository(r.s.db).GetPublishedByID(ctx, id); err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// pluginThread keeps only the fields a plugin may see.
func pluginThread(tp comment.ThreadPage) sdk.CommentThread {
	items := make([]sdk.CommentItem, 0, len(tp.Comments))
	for _, c := range tp.Comments {
		items = append(items, sdk.CommentItem{
			ID: c.ID.String(), ParentID: idString(c.ParentID), Depth: c.Depth,
			AuthorName: c.AuthorName, Staff: c.IsSiteAuthor, BodyHTML: c.BodyHTML,
			CreatedAt: c.CreatedAt.UTC(),
		})
	}
	return sdk.CommentThread{Items: items, Page: tp.Page, HasMore: tp.HasMore}
}
