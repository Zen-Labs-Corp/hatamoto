package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"hatamoto.dev/core/internal/commentsubmit"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/user"
)

// surgeMailTimeout bounds one surge mail run, which happens off the request.
const surgeMailTimeout = time.Minute

// surgeQuiet is the quiet time after the last heartbeat that ends an episode.
// The heartbeat only records the last reported surge comment: reports are at
// most SurgeBeat apart, and one can be dropped while a run is in flight (up to
// surgeMailTimeout). So the heartbeat can trail the local surge window by that
// much; without the padding the same surge could claim a second mail.
const surgeQuiet = commentsubmit.SurgeWindow + commentsubmit.SurgeBeat + surgeMailTimeout

// surgeClaimer is the part of *comment.Service the surge notifier uses.
type surgeClaimer interface {
	NoteSurge(ctx context.Context, quiet time.Duration) (bool, error)
}

// surgeMailer is the part of *email.Service the surge notifier uses.
type surgeMailer interface {
	IsConfigured() bool
	SendCommentSurge(ctx context.Context, to string, d email.CommentSurgeData) error
}

// commentSurgeNotifier mails the owners once per surge episode.
type commentSurgeNotifier struct {
	claim   surgeClaimer
	mail    surgeMailer
	owners  func(ctx context.Context) ([]*user.User, error)
	site    func(ctx context.Context) (name, base, adminPath string)
	log     *slog.Logger
	running atomic.Bool
}

// run checks it can send before claiming, so a server without mail never uses
// up the episode.
func (n *commentSurgeNotifier) run(ctx context.Context) error {
	if !n.mail.IsConfigured() {
		return nil
	}
	name, base, adminPath := n.site(ctx)
	base = strings.TrimRight(base, "/")
	if base == "" {
		n.log.WarnContext(ctx, "comment surge mail needs the site address")
		return nil
	}
	owners, err := n.owners(ctx)
	if err != nil {
		return fmt.Errorf("failed to list owners: %w", err)
	}
	if len(owners) == 0 {
		return nil
	}
	won, err := n.claim.NoteSurge(ctx, surgeQuiet)
	if err != nil || !won {
		return err
	}
	d := email.CommentSurgeData{
		SiteName: name, Limit: commentsubmit.SurgeLimit, Window: commentsubmit.SurgeWindow,
		ModerateURL: base + adminPath + "/comments?status=pending",
	}
	for _, u := range owners {
		if err := n.mail.SendCommentSurge(ctx, u.Email, d); err != nil {
			n.log.WarnContext(ctx, "comment surge mail failed", slog.Any("error", err))
		}
	}
	return nil
}

// async runs the notifier off the request goroutine. A run still in flight
// absorbs further calls.
func (n *commentSurgeNotifier) async(ctx context.Context) {
	if !n.running.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer n.running.Store(false)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), surgeMailTimeout)
		defer cancel()
		if err := n.run(ctx); err != nil {
			n.log.WarnContext(ctx, "comment surge mail not sent", slog.Any("error", err))
		}
	}()
}

// commentOwners lists owners with an address whose access has not ended.
func (s *Server) commentOwners(ctx context.Context) ([]*user.User, error) {
	all, err := s.commentModerators(ctx)
	var out []*user.User
	for _, u := range all {
		if u.Role == user.RoleOwner {
			out = append(out, u)
		}
	}
	return out, err
}

// commentSurgeHook is Pipeline.OnSurge: nil unless this server sends comment mail.
func (s *Server) commentSurgeHook() func(context.Context) {
	if s.comments == nil || s.emailService == nil || s.userRepo == nil || !workerJobEnabled(commentNotifyJob) {
		return nil
	}
	n := &commentSurgeNotifier{claim: s.comments, mail: s.emailService, owners: s.commentOwners, site: s.digestSite, log: s.logger}
	return n.async
}
