package server

import (
	"context"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/user"
)

// commentNotifyObserver queues mail work; it never sends and never blocks long.
type commentNotifyObserver struct {
	comment.NopObserver
	moderation func(ctx context.Context, c comment.Comment)
	reply      func(ctx context.Context, c comment.Comment)
}

// OnCreated and OnConfirmed queue a comment that is held for moderation.
func (o commentNotifyObserver) OnCreated(ctx context.Context, c comment.Comment) {
	if c.Status == comment.StatusPending {
		o.moderation(ctx, c)
	}
}

func (o commentNotifyObserver) OnConfirmed(ctx context.Context, c comment.Comment) {
	if c.Status == comment.StatusPending {
		o.moderation(ctx, c)
	}
}

// OnUpdated queues a comment that an edit sent back to moderation.
func (o commentNotifyObserver) OnUpdated(ctx context.Context, before, after comment.Comment) {
	if after.Status == comment.StatusPending && before.Status != comment.StatusPending {
		o.moderation(ctx, after)
	}
}

// OnApproved queues a reply for its parent's commenter.
func (o commentNotifyObserver) OnApproved(ctx context.Context, c comment.Comment) {
	if c.ParentID != nil {
		o.reply(ctx, c)
	}
}

// commentModerators lists owners and editors with an address whose access has
// not ended.
func (s *Server) commentModerators(ctx context.Context) ([]*user.User, error) {
	const page = 200
	now := time.Now()
	var out []*user.User
	for offset := 0; ; offset += page {
		users, err := s.userRepo.List(ctx, page, offset)
		if err != nil {
			return out, err
		}
		for _, u := range users {
			if (u.Role == user.RoleOwner || u.Role == user.RoleEditor) && u.Email != "" && !u.AccessEnded(now) {
				out = append(out, u)
			}
		}
		if len(users) < page {
			return out, nil
		}
	}
}

// queueCommentModeration notes a held comment for every moderator. With
// notifications off nothing is queued. The work is database-only and outlives
// the request.
func (s *Server) queueCommentModeration(ctx context.Context, c comment.Comment) {
	ctx = context.WithoutCancel(ctx)
	set, err := s.comments.Settings(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "comment notification not queued", "error", err)
		return
	}
	if set.NotifyMode == comment.NotifyOff {
		return
	}
	mods, err := s.commentModerators(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "comment notification not queued", "error", err)
		return
	}
	ids := make([]uuid.UUID, 0, len(mods))
	for _, u := range mods {
		ids = append(ids, u.ID)
	}
	if err := s.comments.QueueModeration(ctx, c.ID, ids); err != nil {
		s.logger.WarnContext(ctx, "comment notification not queued", "error", err)
	}
}

func (s *Server) queueCommentReply(ctx context.Context, c comment.Comment) {
	ctx = context.WithoutCancel(ctx)
	if err := s.comments.QueueReply(ctx, c); err != nil {
		s.logger.WarnContext(ctx, "comment reply notification not queued", "error", err)
	}
}

// setupCommentNotify registers the observer and schedules comment_notify.
// Without comments, mail, users or a scheduler, or with the job disabled,
// nothing is registered, so nothing queues that would never be sent.
func (s *Server) setupCommentNotify() bool {
	if s.comments == nil || s.emailService == nil || s.userRepo == nil || s.scheduler == nil || s.cfg == nil || !workerJobEnabled(commentNotifyJob) {
		return false
	}
	n := &commentNotifier{
		store: s.comments, mail: s.emailService, moderators: s.commentModerators, site: s.digestSite,
		entity: s.commentEntity, unsubKey: comment.UnsubscribeKey(s.cfg.Secret), log: s.logger,
	}
	s.comments.Register(commentNotifyObserver{moderation: s.queueCommentModeration, reply: s.queueCommentReply})
	err := s.scheduler.ScheduleJitter(commentNotifyJob, time.Minute, 10*time.Second, time.Minute, n.run)
	if err != nil {
		s.logger.Error("comment notify job not scheduled", "error", err)
	}
	return err == nil
}
