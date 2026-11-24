package server

import (
	"context"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
)

// commentCacheObserver drops one entity's rendered pages when its public
// thread may have changed. Comment counts show on the entity page only, so
// listings are not touched. It never blocks: the callbacks only touch the
// in-process cache.
type commentCacheObserver struct {
	comment.NopObserver
	drop   func(ctx context.Context, entityID uuid.UUID) // InvalidateRenderedEntity + preload; logs its error
	all    func(ctx context.Context)                     // global drop, for a settings change
	forget func(commenterID uuid.UUID)                   // the commenter's cached "has own items" answer
	// coalesce rate-limits batch drops (rank, dead, restore); nil drops at once.
	coalesce *dropCoalescer
}

func (o commentCacheObserver) changed(ctx context.Context, c comment.Comment) {
	o.drop(ctx, c.EntityID)
	if c.CommenterID != nil && o.forget != nil {
		o.forget(*c.CommenterID)
	}
}

// OnApproved fires for an approved store, a confirm and a moderator approval.
func (o commentCacheObserver) OnApproved(ctx context.Context, c comment.Comment) { o.changed(ctx, c) }

// OnSpam and OnDeleted drop only for a comment that was public: the event does
// not carry the old status, but approved_at is kept when an approved comment is
// moved away, and never set for one that was not public. Dropping for spam that
// never showed would let a bot drive static exports.
//
// Inside a batch (purge, entity cascade) nothing is dropped: purge removes only
// spam and trash, and a cascade runs after the entity itself was dropped.
func (o commentCacheObserver) OnSpam(ctx context.Context, c comment.Comment) {
	if c.ApprovedAt != nil && !comment.InBatch(ctx) {
		o.changed(ctx, c)
	}
}

func (o commentCacheObserver) OnDeleted(ctx context.Context, c comment.Comment) {
	if c.ApprovedAt != nil && !comment.InBatch(ctx) {
		o.changed(ctx, c)
	}
}

// OnUpdated drops when the comment was or is shown (approved, or dead as a
// placeholder): an edit of an approved comment, an unapproval, a move to or
// from dead. OnCreated and OnConfirmed do nothing: an approved one also arrives
// as OnApproved. Inside a batch the drop waits for OnBatch (one per entity).
func (o commentCacheObserver) OnUpdated(ctx context.Context, before, after comment.Comment) {
	if !shown(before) && !shown(after) {
		return
	}
	if comment.InBatch(ctx) {
		if after.CommenterID != nil && o.forget != nil {
			o.forget(*after.CommenterID)
		}
		return
	}
	o.changed(ctx, after)
}

func shown(c comment.Comment) bool {
	return c.Status == comment.StatusApproved || c.Status == comment.StatusDead
}

// OnBatch drops each touched entity's pages once, through the coalescer.
func (o commentCacheObserver) OnBatch(ctx context.Context, entityIDs []uuid.UUID) {
	for _, id := range entityIDs {
		if o.coalesce != nil {
			o.coalesce.Drop(ctx, id)
		} else {
			o.drop(ctx, id)
		}
	}
}

// OnSettingsChanged drops every rendered page (the form and the thread appear
// or vanish site-wide).
func (o commentCacheObserver) OnSettingsChanged(ctx context.Context, _ comment.Settings) {
	o.all(ctx)
}

// OnModeChanged drops the entity's pages when its override changed, and every
// rendered page when a type default changed (any entity of the type).
func (o commentCacheObserver) OnModeChanged(ctx context.Context, entityID uuid.UUID) {
	if entityID == uuid.Nil {
		o.all(ctx)
		return
	}
	o.drop(ctx, entityID)
}

// dropRenderedEntity is the observer's drop: that entity's pages, then a
// debounced preload, and a static export is noted. Nil-safe without a cache (tests).
func (s *Server) dropRenderedEntity(ctx context.Context, id uuid.UUID) {
	s.staticTrigger.Notify("comment", false)
	if s.infra == nil || s.infra.CacheInvalidator == nil {
		return
	}
	if err := s.infra.CacheInvalidator.InvalidateRenderedEntity(ctx, id); err != nil && s.logger != nil {
		s.logger.Warn("comments: drop entity pages", "error", err)
	}
	s.pageCache.notifyContentChanged()
}
