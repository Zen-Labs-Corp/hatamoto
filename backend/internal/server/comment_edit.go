package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/commentfmt"
	"hatamoto.dev/core/internal/commentsubmit"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/statichost"
)

// editBodyField is the static field of the edit form. It is safe to name it
// plainly: the request is only honoured behind a verified device cookie.
const editBodyField = "body"

// commentEditor is the slice of *comment.Service the edit and delete paths
// need (tests use a fake).
type commentEditor interface {
	Get(context.Context, uuid.UUID) (comment.Comment, error)
	EntityConfig(context.Context, uuid.UUID) (comment.EntityConfig, error)
	Edit(context.Context, uuid.UUID, comment.Edited) (comment.Comment, error)
	Trash(context.Context, uuid.UUID) (comment.Comment, error)
}

// handleCommentEdit takes POST /-/comments/edit/{commentID}.
func (s *Server) handleCommentEdit(w http.ResponseWriter, r *http.Request) {
	s.serveOwnComment(w, r, s.commentEditorOrNil(), false, time.Now())
}

// handleCommentDelete takes POST /-/comments/delete/{commentID}. The comment
// goes to the trash (moderators still see it until the purge), never a hard delete.
func (s *Server) handleCommentDelete(w http.ResponseWriter, r *http.Request) {
	s.serveOwnComment(w, r, s.commentEditorOrNil(), true, time.Now())
}

func (s *Server) commentEditorOrNil() commentEditor {
	if s.comments == nil { // avoid a typed-nil interface
		return nil
	}
	return s.comments
}

// serveOwnComment applies an edit or a delete when the signed device cookie
// belongs to the comment's author and the comment is still inside
// the edit window of its content type (cfg.Policy.EditWindow). Ownership never comes from the form. Neither the body
// nor the cookie is logged.
func (s *Server) serveOwnComment(w http.ResponseWriter, r *http.Request, ed commentEditor, del bool, now time.Time) {
	w.Header().Set("Cache-Control", "no-store")
	id, err := uuid.Parse(r.PathValue("commentID"))
	if ed == nil || err != nil || s.commentSlot.off() {
		s.commentNotFound(w, r)
		return
	}
	// Edit and delete stay same-origin only: they are per visitor and never appear on static pages.
	if commentCrossSite(r, statichost.StaticHosting{}) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	ctx := r.Context()
	commenter, ok := s.commentDevice(r)
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	c, err := ed.Get(ctx, id)
	if errors.Is(err, comment.ErrNotFound) {
		s.commentNotFound(w, r)
		return
	}
	if err != nil {
		s.commentFail(w, r, "comment edit: get", err)
		return
	}
	cfg, err := ed.EntityConfig(ctx, c.EntityID)
	if errors.Is(err, comment.ErrNotFound) {
		s.commentNotFound(w, r)
		return
	}
	if err != nil {
		s.commentFail(w, r, "comment edit: policy", err)
		return
	}
	if c.CommenterID == nil || *c.CommenterID != commenter ||
		now.Sub(c.CreatedAt) >= cfg.Policy.EditWindow() || c.Status == comment.StatusTrash ||
		(c.Status == comment.StatusDead && !del) { // an own dead comment can be deleted, not edited
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxCommentPostBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	lookup := s.commentPost.entity
	if lookup == nil {
		lookup = s.commentEntity
	}
	_, path, found := lookup(ctx, c.EntityID)
	if !found || path == "" {
		path = "/"
	}

	if del {
		if _, err := ed.Trash(ctx, id); err != nil {
			s.commentFail(w, r, "comment delete", err)
			return
		}
		s.commentBypass.forget(commenter)
		http.Redirect(w, r, path+"#hm-comments", http.StatusSeeOther)
		return
	}

	body := r.PostForm.Get(editBodyField)
	res, code, err := renderEditBody(body)
	if err != nil {
		s.commentFail(w, r, "comment edit: render", err)
		return
	}
	if code != "" {
		http.Redirect(w, r, path+"?cerror="+url.QueryEscape(code)+"#hm-comments", http.StatusSeeOther)
		return
	}
	hold := false
	if p := s.commentSubmit; p != nil {
		hold, err = p.HoldEdit(ctx, commentsubmit.EditCheck{
			EntityID: c.EntityID, Name: c.AuthorName, Body: body, Rendered: res,
			BodyChanged: res.BodyHash != c.BodyHash, IP: middleware.GetClientIP(r), Now: now, Commenter: &commenter,
		})
		if err != nil {
			s.commentFail(w, r, "comment edit: checks", err)
			return
		}
	}
	_, err = ed.Edit(ctx, id, comment.Edited{
		BodyRaw: body, BodyHTML: res.HTML, HasLinks: res.HasLinks, LinkDomains: res.LinkDomains,
		BodyHash: res.BodyHash, ByAdmin: false, Hold: hold,
	})
	if err != nil {
		s.commentFail(w, r, "comment edit", err)
		return
	}
	s.commentBypass.forget(commenter) // an edit with links or a hold may have made it pending
	http.Redirect(w, r, path+"#hm-comments", http.StatusSeeOther)
}

// renderEditBody applies the submit limits (1..MaxBodyRunes runes) and formats
// the body. A limit hit returns the invalid code; err is an unexpected failure.
func renderEditBody(body string) (res commentfmt.Result, code string, err error) {
	switch n := utf8.RuneCountInString(strings.TrimSpace(body)); {
	case n == 0:
		return res, commentsubmit.CodeBodyEmpty, nil
	case n > commentsubmit.MaxBodyRunes:
		return res, commentsubmit.CodeBodyLong, nil
	}
	res, err = commentfmt.Render(body)
	switch {
	case errors.Is(err, commentfmt.ErrEmpty):
		return res, commentsubmit.CodeBodyEmpty, nil
	case errors.Is(err, commentfmt.ErrTooLong):
		return res, commentsubmit.CodeBodyLong, nil
	}
	return res, "", err
}

func (s *Server) commentFail(w http.ResponseWriter, r *http.Request, what string, err error) {
	if s.logger != nil {
		s.logger.ErrorContext(r.Context(), what, "error", err)
	}
	http.Error(w, "Something went wrong", http.StatusInternalServerError)
}
