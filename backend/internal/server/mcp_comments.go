package server

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/mcpserver"
)

// mcpCommentStore is the part of commentsAdmin the MCP tools use (tests fake
// it). Going through the admin adapter keeps MCP moderation identical to the
// Comments screen: drafts are skipped, spam blocks the commenter and learns
// the hashes, a staff reply is auto-approved.
type mcpCommentStore interface {
	Queue(ctx context.Context, f admin.CommentFilter) (admin.CommentPage, error)
	Comment(ctx context.Context, id string) (admin.CommentRow, error)
	Approve(ctx context.Context, ids []string, actor string) (int, error)
	MarkSpam(ctx context.Context, ids []string, teach admin.SpamTeach, actor string) (int, error)
	Reply(ctx context.Context, parentID, bodyRaw, actor, name string) (string, error)
}

// mcpComments implements mcpserver.Comments.
type mcpComments struct {
	store mcpCommentStore
	name  func(ctx context.Context, user uuid.UUID) string // public name of a reply
}

var _ mcpserver.Comments = mcpComments{}

func (m mcpComments) List(ctx context.Context, f mcpserver.CommentFilter) ([]mcpserver.CommentRow, bool, error) {
	af := admin.CommentFilter{Status: f.Status, Page: f.Page, PerPage: f.Limit}
	if f.EntityID != nil {
		af.EntityID = f.EntityID.String()
	}
	page, err := m.store.Queue(ctx, af)
	if err != nil {
		return nil, false, mcpCommentErr(err)
	}
	rows := make([]mcpserver.CommentRow, 0, len(page.Rows))
	for _, r := range page.Rows {
		rows = append(rows, mcpCommentRow(r))
	}
	return rows, page.HasMore, nil
}

func (m mcpComments) Approve(ctx context.Context, id uuid.UUID, actor *uuid.UUID) (mcpserver.CommentRow, error) {
	return m.moderate(ctx, id, comment.StatusApproved, func(a string) (int, error) {
		return m.store.Approve(ctx, []string{id.String()}, a)
	}, actor)
}

func (m mcpComments) Spam(ctx context.Context, id uuid.UUID, actor *uuid.UUID) (mcpserver.CommentRow, error) {
	return m.moderate(ctx, id, comment.StatusSpam, func(a string) (int, error) {
		return m.store.MarkSpam(ctx, []string{id.String()}, admin.SpamTeach{}, a)
	}, actor)
}

// moderate runs op, then returns the comment. Nothing changed and the status
// is not the wanted one (a draft, or a move the model refuses): ErrCommentState.
// Already in the wanted status is a success.
func (m mcpComments) moderate(ctx context.Context, id uuid.UUID, want comment.Status, op func(actor string) (int, error), actor *uuid.UUID) (mcpserver.CommentRow, error) {
	by := ""
	if actor != nil {
		by = actor.String()
	}
	n, err := op(by)
	if err != nil {
		return mcpserver.CommentRow{}, mcpCommentErr(err)
	}
	row, err := m.store.Comment(ctx, id.String())
	if err != nil {
		return mcpserver.CommentRow{}, mcpCommentErr(err)
	}
	if n == 0 && row.Status != string(want) {
		return mcpserver.CommentRow{}, mcpserver.ErrCommentState
	}
	return mcpCommentRow(row), nil
}

func (m mcpComments) Reply(ctx context.Context, parent, actor uuid.UUID, body string) (mcpserver.CommentRow, error) {
	// Like the Comments screen, which offers Reply on pending and approved only:
	// a reply under any other parent would be counted but never shown.
	p, err := m.store.Comment(ctx, parent.String())
	if err != nil {
		return mcpserver.CommentRow{}, mcpCommentErr(err)
	}
	if p.Status != string(comment.StatusPending) && p.Status != string(comment.StatusApproved) {
		return mcpserver.CommentRow{}, mcpserver.ErrCommentState
	}
	id, err := m.store.Reply(ctx, parent.String(), body, actor.String(), m.name(ctx, actor))
	if err != nil {
		return mcpserver.CommentRow{}, mcpCommentErr(err)
	}
	row, err := m.store.Comment(ctx, id)
	if err != nil {
		return mcpserver.CommentRow{}, mcpCommentErr(err)
	}
	return mcpCommentRow(row), nil
}

// mcpCommentRow copies only what an agent may see: never the raw body, the
// commenter id or the author's user id.
func mcpCommentRow(r admin.CommentRow) mcpserver.CommentRow {
	return mcpserver.CommentRow{
		ID: r.ID, EntityID: r.EntityID, EntityTitle: r.EntityTitle, ParentID: r.ParentID,
		AuthorName: r.AuthorName, Staff: r.AuthorUserID != "", BodyHTML: r.BodyHTML,
		Status: r.Status, HasLinks: r.HasLinks, Trust: r.Trust, SpamScore: r.Score,
		HeldReasons: r.Reasons, CreatedAt: r.CreatedAt,
	}
}

func mcpCommentErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, admin.ErrCommentNotFound):
		return mcpserver.ErrCommentNotFound
	case errors.Is(err, admin.ErrCommentEmpty), errors.Is(err, admin.ErrCommentTooLong):
		return mcpserver.ErrCommentInvalid
	case errors.Is(err, comment.ErrBadTransition):
		return mcpserver.ErrCommentState
	}
	return err
}

// mcpReplyName is the public name of a reply: the user's display name, or the
// site name when it is empty or just the login email (never published).
func (s *Server) mcpReplyName(ctx context.Context, id uuid.UUID) string {
	if s.userRepo != nil {
		if u, err := s.userRepo.GetByID(ctx, id); err == nil && u != nil && u.Name != nil {
			if n := strings.TrimSpace(*u.Name); n != "" && !strings.EqualFold(n, u.Email) {
				return n
			}
		}
	}
	name, _, _ := s.digestSite(ctx)
	return name
}
