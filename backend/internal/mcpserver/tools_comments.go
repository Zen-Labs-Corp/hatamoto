package mcpserver

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	hmauth "hatamoto.dev/core/internal/auth"
)

// Errors a Comments implementation returns; their text reaches the agent.
var (
	ErrCommentNotFound     = errors.New("comment not found")
	ErrCommentState        = errors.New("comment cannot change to that status")
	ErrCommentInvalid      = errors.New("comment text is empty or too long")
	ErrCommentsUnavailable = errors.New("comments are not available")
)

// Comments is the comment store the tools use. Implementations never return an
// email, a hash, an IP address or a user agent.
type Comments interface {
	List(ctx context.Context, f CommentFilter) (rows []CommentRow, hasMore bool, err error)
	// Approve is idempotent: an approved comment stays approved.
	Approve(ctx context.Context, id uuid.UUID, actor *uuid.UUID) (CommentRow, error)
	// Spam is idempotent: a spam comment stays spam.
	Spam(ctx context.Context, id uuid.UUID, actor *uuid.UUID) (CommentRow, error)
	Reply(ctx context.Context, parentID, actor uuid.UUID, body string) (CommentRow, error)
}

// CommentFilter selects the comments to list.
type CommentFilter struct {
	Status   string
	EntityID *uuid.UUID
	Page     int
	Limit    int
}

// CommentRow is a comment as the tools show it.
type CommentRow struct {
	ID          string    `json:"id"`
	EntityID    string    `json:"entity_id"`
	EntityTitle string    `json:"entity_title,omitempty"`
	ParentID    string    `json:"parent_id,omitempty"`
	AuthorName  string    `json:"author_name"`
	Staff       bool      `json:"staff,omitempty"`
	BodyHTML    string    `json:"body_html"`
	Status      string    `json:"status"`
	HasLinks    bool      `json:"has_links,omitempty"`
	Trust       string    `json:"trust,omitempty"`
	SpamScore   int       `json:"spam_score,omitempty"`
	HeldReasons []string  `json:"held_reasons,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

const (
	commentsRead  = hmauth.ScopeReadComments
	commentsWrite = hmauth.ScopeWriteComments

	commentsDefaultLimit = 20
	commentsMaxLimit     = 100
)

func (s *Server) registerComments() {
	svc := func() (Comments, error) {
		if s.d.Comments == nil {
			return nil, userErr("comments tools are not available")
		}
		return s.d.Comments, nil
	}

	type listIn struct {
		Status   string `json:"status,omitempty" jsonschema:"pending (default), approved, spam or trash"`
		EntityID string `json:"entity_id,omitempty" jsonschema:"only comments on this entity (UUID)"`
		Page     int    `json:"page,omitempty" jsonschema:"1-based page"`
		Limit    int    `json:"limit,omitempty" jsonschema:"page size, default 20, max 100"`
	}
	register(s, toolSpec{name: "comments_list", description: "List comments by status (default pending, the moderation queue), with why each was held. Shows commenter names and comment text, never email addresses.", scope: commentsRead, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in listIn) (any, error) {
			sv, err := svc()
			if err != nil {
				return nil, err
			}
			f := CommentFilter{Status: in.Status, Page: in.Page, Limit: in.Limit}
			switch f.Status {
			case "":
				f.Status = "pending"
			case "pending", "approved", "spam", "trash":
			default:
				return nil, userErr("status must be pending, approved, spam or trash")
			}
			if in.EntityID != "" {
				id, err := parseID("entity_id", in.EntityID)
				if err != nil {
					return nil, err
				}
				f.EntityID = &id
			}
			if f.Page < 0 {
				return nil, userErr("page must be 1 or more")
			}
			if f.Page == 0 {
				f.Page = 1
			}
			if f.Limit <= 0 {
				f.Limit = commentsDefaultLimit
			}
			f.Limit = min(f.Limit, commentsMaxLimit)
			rows, more, err := sv.List(ctx, f)
			if err != nil {
				return nil, err
			}
			if rows == nil {
				rows = []CommentRow{}
			}
			return object{"comments": rows, "page": f.Page, "has_more": more}, nil
		})

	type idIn struct {
		ID string `json:"id" jsonschema:"comment id (UUID)"`
	}
	register(s, toolSpec{name: "comments_approve", description: "Approve a comment so it shows on the page. Approving an approved comment does nothing.", scope: commentsWrite, write: true, idempotent: true},
		func(ctx context.Context, key *hmauth.APIKey, in idIn) (any, error) {
			sv, err := svc()
			if err != nil {
				return nil, err
			}
			id, err := parseID("id", in.ID)
			if err != nil {
				return nil, err
			}
			return sv.Approve(ctx, id, key.CreatedBy)
		})
	register(s, toolSpec{name: "comments_spam", description: "Marks a comment as spam: hides it and blocks its commenter.", scope: commentsWrite, write: true, idempotent: true, destructive: true},
		func(ctx context.Context, key *hmauth.APIKey, in idIn) (any, error) {
			sv, err := svc()
			if err != nil {
				return nil, err
			}
			id, err := parseID("id", in.ID)
			if err != nil {
				return nil, err
			}
			return sv.Spam(ctx, id, key.CreatedBy)
		})

	type replyIn struct {
		ID   string `json:"id" jsonschema:"comment to reply to (UUID)"`
		Body string `json:"body" jsonschema:"reply text"`
	}
	register(s, toolSpec{name: "comments_reply", description: "Reply to a comment as the key's creator.", scope: commentsWrite, write: true},
		func(ctx context.Context, key *hmauth.APIKey, in replyIn) (any, error) {
			sv, err := svc()
			if err != nil {
				return nil, err
			}
			id, err := parseID("id", in.ID)
			if err != nil {
				return nil, err
			}
			if key.CreatedBy == nil {
				return nil, userErr("comments_reply needs a key created by a user")
			}
			return sv.Reply(ctx, id, *key.CreatedBy, in.Body)
		})
}
