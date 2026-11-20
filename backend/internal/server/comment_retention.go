package server

import "context"

// retentionAdder is the part of worker.RetentionPurgeWorker this file uses.
type retentionAdder interface {
	AddPurge(name string, fn func(context.Context) (int64, error))
}

// addCommentRetention makes the daily purge delete old spam and trash comments
// old reply-queue rows and moderator-queue rows of comments no longer held. The worker is built before setupComments, so the
// comment service is read when the purge runs; without it the purge does
// nothing. It does not depend on mail being set up.
func (s *Server) addCommentRetention(w retentionAdder) {
	w.AddPurge("comment spam and trash", func(ctx context.Context) (int64, error) {
		if s.comments == nil {
			return 0, nil
		}
		return s.comments.PurgeSpamTrash(ctx)
	})
	w.AddPurge("comment notification queue", func(ctx context.Context) (int64, error) {
		if s.comments == nil {
			return 0, nil
		}
		moderation, err := s.comments.PruneModerationQueue(ctx)
		if err != nil {
			return moderation, err
		}
		replies, err := s.comments.PruneReplyQueue(ctx)
		return moderation + replies, err
	})
}
