package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/user"
)

const (
	// commentNotifyJob is the WORKER_DISABLE_JOBS name of the notification mail job.
	commentNotifyJob = "comment_notify"
	modPerCommentGap = 10 * time.Minute
	modDigestGap     = time.Hour
	replyClaimBatch  = 100 // commenters per run
	modMaxListed     = 20
	// replyMailGap is the most a commenter is mailed about replies: one mail per window.
	replyMailGap   = 15 * time.Minute
	replyMaxListed = 20
)

// notifyStore is the part of *comment.Service the notifier uses.
type notifyStore interface {
	Settings(ctx context.Context) (comment.Settings, error)
	ClaimModeration(ctx context.Context, gap time.Duration) ([]comment.ModerationBatch, error)
	ClearModeration(ctx context.Context) error
	ClaimReplyBatches(ctx context.Context, limit int, window time.Duration) ([]comment.ReplyBatch, error)
	PruneReplyQueue(ctx context.Context) (int64, error)
}

// notifyMailer is the part of *email.Service the notifier uses.
type notifyMailer interface {
	IsConfigured() bool
	SendCommentModeration(ctx context.Context, to string, d email.CommentModerationData) error
	SendCommentReply(ctx context.Context, to string, d email.CommentReplyData) error
	SendCommentReplyDigest(ctx context.Context, to string, d email.CommentReplyDigestData) error
}

// commentNotifier is the comment_notify job. The comment observers only queue
// work; this job, run by the scheduler, is the only place that sends mail.
type commentNotifier struct {
	store      notifyStore
	mail       notifyMailer
	moderators func(ctx context.Context) ([]*user.User, error) // owners and editors with an address and current access
	site       func(ctx context.Context) (name, base, adminPath string)
	entity     func(ctx context.Context, id uuid.UUID) (title, path string, ok bool)
	unsubKey   []byte
	log        *slog.Logger
}

// run sends the queued moderator and reply mail. Without mail set up or a
// configured site address the queues are left alone for a later run.
func (n *commentNotifier) run(ctx context.Context) error {
	if !n.mail.IsConfigured() {
		return nil
	}
	name, base, adminPath := n.site(ctx)
	base = strings.TrimRight(base, "/")
	if base == "" {
		n.log.WarnContext(ctx, "comment notifications need the site address")
		return nil
	}
	if _, err := n.store.PruneReplyQueue(ctx); err != nil {
		n.log.WarnContext(ctx, "comment notification queue prune", slog.Any("error", err))
	}
	set, err := n.store.Settings(ctx)
	if err != nil {
		return fmt.Errorf("failed to read comment settings: %w", err)
	}
	var errs []error
	if err := n.moderation(ctx, set.NotifyMode, name, base, adminPath); err != nil {
		errs = append(errs, err)
	}
	if err := n.replies(ctx, name, base); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (n *commentNotifier) moderation(ctx context.Context, mode comment.NotifyMode, name, base, adminPath string) error {
	gap := modPerCommentGap
	switch mode {
	case comment.NotifyOff:
		return n.store.ClearModeration(ctx)
	case comment.NotifyDigest:
		gap = modDigestGap
	}
	batches, err := n.store.ClaimModeration(ctx, gap)
	if err != nil || len(batches) == 0 {
		return err
	}
	mods, err := n.moderators(ctx)
	if err != nil {
		return fmt.Errorf("failed to list moderators: %w", err)
	}
	byID := make(map[uuid.UUID]*user.User, len(mods))
	for _, u := range mods {
		byID[u.ID] = u
	}
	titles := map[uuid.UUID]string{}
	title := func(id uuid.UUID) string {
		t, ok := titles[id]
		if !ok {
			t, _, ok = n.entity(ctx, id)
			if !ok {
				t = "a page"
			}
			titles[id] = t
		}
		return t
	}
	for _, b := range batches {
		u := byID[b.UserID]
		if u == nil || len(b.Comments) == 0 {
			continue
		}
		d := email.CommentModerationData{
			SiteName:       name,
			ModerateURL:    base + adminPath + "/comments?status=pending",
			UnsubscribeURL: base + commentUnsubscribePrefix + comment.UnsubscribeToken(n.unsubKey, comment.UnsubscribeModerator, u.ID),
		}
		for i, c := range b.Comments {
			if i == modMaxListed {
				d.More = len(b.Comments) - modMaxListed
				break
			}
			d.Items = append(d.Items, email.CommentModerationItem{
				AuthorName: c.AuthorName, EntityTitle: title(c.EntityID),
				Held: strings.Join(admin.HeldReasonLabels(c.SpamReasons), ", "),
			})
		}
		if err := n.mail.SendCommentModeration(ctx, u.Email, d); err != nil {
			n.log.WarnContext(ctx, "comment moderation mail not sent", slog.Any("error", err))
		}
	}
	return nil
}

func (n *commentNotifier) replies(ctx context.Context, name, base string) error {
	// Batches claimed before an error are already stamped: mail them anyway.
	batches, claimErr := n.store.ClaimReplyBatches(ctx, replyClaimBatch, replyMailGap)
	type entityInfo struct {
		title, path string
		ok          bool
	}
	entities := map[uuid.UUID]entityInfo{}
	lookup := func(id uuid.UUID) entityInfo {
		e, seen := entities[id]
		if !seen {
			e.title, e.path, e.ok = n.entity(ctx, id)
			entities[id] = e
		}
		return e
	}
	for _, b := range batches {
		unsub := base + commentUnsubscribePrefix + comment.UnsubscribeToken(n.unsubKey, comment.UnsubscribeReplies, b.CommenterID)
		var items []email.CommentReplyItem
		for _, r := range b.Replies {
			e := lookup(r.EntityID)
			if !e.ok {
				continue
			}
			items = append(items, email.CommentReplyItem{
				ReplierName: r.AuthorName, EntityTitle: e.title,
				URL: base + e.path + "#comment-" + r.ID.String(),
			})
		}
		var err error
		switch {
		case len(items) == 0:
			continue
		case len(items) == 1:
			err = n.mail.SendCommentReply(ctx, b.Email, email.CommentReplyData{
				SiteName: name, EntityTitle: items[0].EntityTitle, ReplierName: items[0].ReplierName,
				URL: items[0].URL, UnsubscribeURL: unsub,
			})
		default:
			d := email.CommentReplyDigestData{SiteName: name, UnsubscribeURL: unsub}
			if len(items) > replyMaxListed {
				d.More = len(items) - replyMaxListed
				items = items[:replyMaxListed]
			}
			d.Items = items
			err = n.mail.SendCommentReplyDigest(ctx, b.Email, d)
		}
		if err != nil {
			n.log.WarnContext(ctx, "comment reply mail not sent", slog.Any("error", err))
		}
	}
	if claimErr != nil {
		return fmt.Errorf("failed to claim reply mail: %w", claimErr)
	}
	return nil
}
