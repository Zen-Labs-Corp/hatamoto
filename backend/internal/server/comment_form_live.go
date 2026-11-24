package server

import (
	"bytes"
	"errors"
	"html"
	"net/http"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
)

// handleCommentFormLive answers GET /-/comments/form/{entityID} in static mode:
// the comment form fragment with a fresh per-visitor token (IP-bound,
// single-use, 4h) for the static page's loader. Served mode: 404, because the
// page carries the form itself. The answer is private and no-store, as the
// token is per visitor. CORS headers come from the handleCORS mount.
func (s *Server) handleCommentFormLive(w http.ResponseWriter, r *http.Request) {
	if !s.hosting.Load().IslandsOn() || s.commentSubmit == nil || s.renderEngine == nil || s.commentSlot == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	id, err := uuid.Parse(r.PathValue("entityID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	lookup := s.commentPost.entity
	if lookup == nil {
		lookup = s.commentEntity
	}
	_, path, ok := lookup(ctx, id)
	if !ok || path == "" {
		http.NotFound(w, r)
		return
	}
	cs := s.commentSubmit.Comments
	cfg, err := cs.EntityConfig(ctx, id)
	mode := cfg.Mode
	if errors.Is(err, comment.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.commentFail(w, r, "comment form: mode", err)
		return
	}
	q := r.URL.Query()
	var notice, errCode string
	if n := q.Get("cnotice"); commentNotices[n] {
		notice = n
	}
	if e := q.Get("cerror"); commentErrors[e] {
		errCode = e
	}
	if mode != comment.ModeOpen {
		errCode = "closed"
	}
	var parent *uuid.UUID
	if pid, err := uuid.Parse(q.Get("creply")); err == nil && errCode != "closed" {
		usable, err := cs.ReplyParent(ctx, id, pid)
		if err != nil {
			s.commentFail(w, r, "comment form: parent", err)
			return
		}
		if usable {
			parent = &pid
		}
	}
	var locale string
	if s.entityService != nil {
		if tag, _, ok := s.urlSnapshot(ctx).Locales().Resolve(path); ok {
			locale = tag
		}
	}
	v := s.commentFormView(ctx, id, path, locale, parent, notice, errCode, nil, cfg.Policy.Enabled)
	var buf bytes.Buffer
	if err := s.renderEngine.RenderCommentsFragment(&buf, v, nil); err != nil {
		s.commentFail(w, r, "comment form: render", err)
		return
	}
	body, ok := s.commentSlot.fill(r, buf.Bytes())
	if v.CommentForm != nil && !ok {
		// Never send a form that still holds the placeholders.
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(liveFormActions(body, s.hosting.Load().IslandBase()))
}

// liveFormActions points the filled form at the live origin: the relative
// comment action gets the live origin in front, and hx-post is renamed so a
// theme's htmx never sends a cross-origin XHR (the route has no preflight for
// it); the form posts natively. It runs AFTER commentSlot.fill, which reads
// the relative action to find the entity.
func liveFormActions(body []byte, live string) []byte {
	body = bytes.ReplaceAll(body, []byte(commentActionPrefix), []byte(`action="`+html.EscapeString(live)+`/-/comments/`))
	return bytes.ReplaceAll(body, []byte(` hx-post=`), []byte(` data-hm-hxpost=`))
}
