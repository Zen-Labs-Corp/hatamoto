package server

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/pluginkey"
	"hatamoto.dev/sdk"
)

// commentEventObserver turns comment transitions into the SDK comment actions
// on the hook engine; webhooks and plugins both listen there. One call fires at
// most one event. A draft never fires; a comment stored or confirmed directly
// as spam (a bot) fires nothing, only a moderator's spam transition does. A
// rank write never fires (no OnUpdated), and OnBatch is cache work only.
type commentEventObserver struct {
	comment.NopObserver // OnBatch and OnSettingsChanged fire nothing
	fire                func(ctx context.Context, name string, ev sdk.CommentEvent)
	quietSpam           sync.Map // comment id -> struct{}: the OnSpam that follows a spam store/confirm
}

func newCommentEventObserver(h *hooks.Engine) *commentEventObserver {
	return &commentEventObserver{fire: func(ctx context.Context, name string, ev sdk.CommentEvent) {
		if name == sdk.CommentDeleted.Name && comment.InBatch(ctx) {
			h.FireQueued(ctx, name, ev) // a purge or cascade: one goroutine, not one per comment
			return
		}
		h.Fire(ctx, name, ev)
	}}
}

func (o *commentEventObserver) OnCreated(ctx context.Context, c comment.Comment) { o.arrived(ctx, c) }
func (o *commentEventObserver) OnConfirmed(ctx context.Context, c comment.Comment) {
	o.arrived(ctx, c)
}

// withCommenter puts the commenter's id on the context of an event. Only the id
// travels: pluginhost derives each plugin's key from it per delivery, so the
// shared event value stays keyless and webhooks never see a key or an id.
func withCommenter(ctx context.Context, c comment.Comment) context.Context {
	if c.CommenterID == nil {
		return ctx
	}
	return pluginkey.WithCommenter(ctx, *c.CommenterID)
}

func (o *commentEventObserver) arrived(ctx context.Context, c comment.Comment) {
	ctx = withCommenter(ctx, c)
	switch c.Status {
	case comment.StatusPending, comment.StatusApproved:
		o.fire(ctx, sdk.CommentCreated.Name, commentEvent(c))
	case comment.StatusSpam:
		o.quietSpam.Store(c.ID, struct{}{}) // the service calls OnSpam next, synchronously
	}
}

func (o *commentEventObserver) OnApproved(ctx context.Context, c comment.Comment) {
	o.fire(withCommenter(ctx, c), sdk.CommentApproved.Name, commentEvent(c))
}

func (o *commentEventObserver) OnSpam(ctx context.Context, c comment.Comment) {
	if _, quiet := o.quietSpam.LoadAndDelete(c.ID); quiet {
		return
	}
	o.fire(withCommenter(ctx, c), sdk.CommentSpam.Name, commentEvent(c))
}

// OnUpdated announces an edit, an unapproval and a move to or from dead; the
// payload carries the new status ("dead" included).
func (o *commentEventObserver) OnUpdated(ctx context.Context, _, after comment.Comment) {
	if after.Status == comment.StatusDraft {
		return
	}
	o.fire(ctx, sdk.CommentUpdated.Name, commentEvent(after))
}

// OnDeleted carries ids and status only: the comment is gone.
func (o *commentEventObserver) OnDeleted(ctx context.Context, c comment.Comment) {
	if c.Status == comment.StatusDraft {
		return // never announced
	}
	o.fire(ctx, sdk.CommentDeleted.Name, sdk.CommentEvent{
		ID: c.ID.String(), EntityID: c.EntityID.String(), ParentID: idString(c.ParentID),
		Status: string(c.Status),
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
type pluginCommentReader struct {
	s    *Server
	keys *pluginkey.Deriver // nil: items carry no commenter key
}

func (r pluginCommentReader) Thread(ctx context.Context, entityID uuid.UUID, page int, opts pluginhost.ThreadOpts) (sdk.CommentThread, error) {
	empty := sdk.CommentThread{Items: []sdk.CommentItem{}, Page: max(page, 1)}
	info, ok, err := r.visible(ctx, entityID)
	if err != nil || !ok {
		return empty, err
	}
	tp, err := r.s.comments.Thread(ctx, entityID, page)
	if err != nil {
		return empty, err
	}
	key := func(id uuid.UUID) string { return r.keys.Commenter(opts.Plugin, id) }
	return pluginThread(tp, revealDead(info, opts), key), nil
}

// revealDead reports whether dead comments are shown in full. The owner is the
// content type's origin plugin read in this call; the schema slug the plugin
// names is never trusted alone.
func revealDead(info comment.EntityConfig, opts pluginhost.ThreadOpts) bool {
	return opts.IncludeDead && info.OwnerPlugin != "" &&
		info.OwnerPlugin == opts.Plugin && info.SchemaSlug == opts.Schema
}

func (r pluginCommentReader) Counts(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]int, error) {
	out := make(map[uuid.UUID]int, len(ids))
	for _, id := range ids {
		out[id] = 0
	}
	if r.s.comments == nil || r.s.db == nil || len(ids) == 0 {
		return out, nil
	}
	pub, err := entity.NewPostgresPublicRepository(r.s.db).GetPublishedByIDs(r.scoped(ctx), ids)
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

// scoped lets the published check see every public locale of a multi-locale
// site (and shared content), as the sitemap does; without it only the site
// language passes.
func (r pluginCommentReader) scoped(ctx context.Context) context.Context {
	if set := r.s.urlSnapshot(ctx).Locales(); set.Multi() {
		return entity.WithVisibleLocaleSet(ctx, set.PublicTags()...)
	}
	return ctx
}

// visible reports whether the entity's thread is public, with the entity's
// comment config.
func (r pluginCommentReader) visible(ctx context.Context, id uuid.UUID) (comment.EntityConfig, bool, error) {
	if r.s.comments == nil || r.s.db == nil {
		return comment.EntityConfig{}, false, nil
	}
	info, err := r.s.comments.EntityConfig(ctx, id)
	if errors.Is(err, comment.ErrNotFound) {
		return info, false, nil
	}
	if err != nil {
		return info, false, err
	}
	if info.Mode == comment.ModeOff || info.Mode == comment.ModeHidden {
		return info, false, nil
	}
	if _, err := entity.NewPostgresPublicRepository(r.s.db).GetPublishedByID(r.scoped(ctx), id); err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			return info, false, nil
		}
		return info, false, err
	}
	return info, true, nil
}

// pluginThread keeps only the fields a plugin may see. A dead comment keeps
// its author, body and commenter key only with reveal; otherwise they are
// blanked. key derives the calling plugin's pseudonymous key for a commenter
// (nil: no keys); staff rows have no commenter and so no key.
func pluginThread(tp comment.ThreadPage, reveal bool, key func(uuid.UUID) string) sdk.CommentThread {
	items := make([]sdk.CommentItem, 0, len(tp.Comments))
	for _, c := range tp.Comments {
		it := sdk.CommentItem{
			ID: c.ID.String(), ParentID: idString(c.ParentID), Depth: c.Depth,
			AuthorName: c.AuthorName, Staff: c.IsSiteAuthor, BodyHTML: c.BodyHTML,
			CreatedAt: c.CreatedAt.UTC(), Rank: c.Rank, Status: "approved",
		}
		if c.Status == comment.StatusDead {
			it.Status = "dead"
			if !reveal {
				it.AuthorName, it.BodyHTML, it.Staff = "", "", false
				items = append(items, it)
				continue
			}
		}
		if key != nil && c.CommenterID != nil {
			it.CommenterKey = key(*c.CommenterID)
		}
		items = append(items, it)
	}
	return sdk.CommentThread{Items: items, Page: tp.Page, HasMore: tp.HasMore}
}
