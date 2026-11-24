package server

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authzsubject"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/commentsubmit"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/statichost"
)

// maxCommentPostBody caps a comment post (the body field alone is 5000 runes).
const maxCommentPostBody = 64 << 10

// parentField is the static form field that names the comment replied to.
const parentField = "parent"

// commentPostHooks replace the production lookups in tests; a nil field means
// the real one.
type commentPostHooks struct {
	entity commentEntityLookup
	admin  func(r *http.Request) (id uuid.UUID, name string, ok bool)
	device func(ctx context.Context, val string) (uuid.UUID, bool)
}

// handleCommentPost takes a public comment post (POST /-/comments/{entityID}).
// The route carries its own per-IP limiter (mounted with the route), so the
// handler does not rate-limit invalid tokens itself; the submit pipeline
// limits everything behind the token check. An error the sender can fix comes
// back as the page (or the htmx fragment) with their own values in the response
// body, private and no-store; the values are never put in a URL or a cookie.
// Every other answer is a redirect or a fragment built from fixed codes.
func (s *Server) handleCommentPost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p := s.commentSubmit
	id, err := uuid.Parse(r.PathValue("entityID"))
	if p == nil || err != nil {
		s.commentNotFound(w, r)
		return
	}
	ctx := r.Context()
	r.Body = http.MaxBytesReader(w, r.Body, maxCommentPostBody)
	if err := r.ParseForm(); err != nil {
		s.commentNotFound(w, r)
		return
	}
	lookup := s.commentPost.entity
	if lookup == nil {
		lookup = s.commentEntity
	}
	title, path, ok := lookup(ctx, id)
	if !ok || path == "" {
		s.commentNotFound(w, r)
		return
	}

	now := time.Now().UTC()
	req := commentsubmit.Request{
		EntityID: id, EntityTitle: title, Post: r.PostForm,
		IP: middleware.GetClientIP(r), UserAgent: r.UserAgent(),
		CrossSite: commentCrossSite(r, s.hosting.Load()), Now: now,
	}
	if raw := r.PostForm.Get(parentField); raw != "" {
		if pid, err := uuid.Parse(raw); err == nil {
			// Before the token is spent: a parent that is gone, on another
			// entity or not approved makes the post top-level, never an error.
			usable, err := p.Comments.ReplyParent(ctx, id, pid)
			if err != nil {
				s.commentFail(w, r, "comment submit: parent", err)
				return
			}
			if usable {
				req.ParentID = &pid
			}
		}
	}
	if s.entityService != nil {
		if tag, _, ok := s.urlSnapshot(ctx).Locales().Resolve(path); ok {
			req.Locale = tag
		}
	}
	if uid, name, ok := s.commentAdminAuthor(r); ok {
		req.AdminUserID, req.AdminName = &uid, name
	} else if cid, ok := s.commentDevice(r); ok {
		req.DeviceCommenter = &cid
	}

	out, err := p.Submit(ctx, req)
	if err != nil {
		if s.logger != nil {
			s.logger.ErrorContext(ctx, "comment submit failed", "error", err)
		}
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}
	if cid := out.Comment.CommenterID; cid != nil {
		s.commentBypass.forget(*cid) // the sender must see the new item on the next load
		if s.commentSlot != nil {
			s.commentSlot.names.forget(*cid) // and the name the comment.draft hook just gave
		}
	}

	var notice, errCode string
	target := path
	switch out.Kind {
	case commentsubmit.NotFound:
		s.commentNotFound(w, r)
		return
	case commentsubmit.Closed:
		errCode, target = "closed", path+"?cerror=closed#hm-comments"
	case commentsubmit.Invalid:
		errCode, target = out.Code, path+"?cerror="+url.QueryEscape(out.Code)+"#hm-comment-form"
	case commentsubmit.CheckEmail:
		notice, target = "check_email", path+"?cnotice=check_email#hm-comments"
	case commentsubmit.Posted:
		target = path + "#comment-" + out.Comment.ID.String()
	default: // FakeSuccess: the shape of a post the sender cannot see
		notice, target = "posted", path+"?cnotice=posted#hm-comments"
	}
	var echo *commentEcho
	if out.Kind == commentsubmit.Invalid {
		e := commentEcho{EntityID: id, Code: out.Code, Typed: out.Typed, Message: out.Message}
		if req.ParentID != nil {
			e.ParentID = req.ParentID.String()
		}
		echo = &e
	}
	if r.Header.Get("HX-Request") == "true" {
		if s.writeCommentFragment(w, r, id, path, req, out, notice, errCode, echo) {
			return
		}
	} else if echo != nil && s.writeCommentPage(w, r, path, *echo) {
		return
	}
	// The ?cerror= redirect stays as the fallback (page handlers not mounted).
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// commentNotFound answers the themed 404 (or the plain one without a theme).
func (s *Server) commentNotFound(w http.ResponseWriter, r *http.Request) {
	if h := s.renderHandlers; h != nil && h.engine != nil {
		h.writeNotFoundQuiet(w, r)
		return
	}
	http.NotFound(w, r)
}

// commentCrossSite is true unless the browser vouches for a same-origin post
// or, in static mode, the post comes from the static site: its pages carry the
// live form, and the token is IP-bound and single-use, so accepting that one
// origin adds no ambient credential to ride.
// Origin "null" is what a same-origin form post sends under Referrer-Policy
// no-referrer, so it passes only with Sec-Fetch-Site: same-origin.
func commentCrossSite(r *http.Request, hs statichost.StaticHosting) bool {
	if hs.AllowsOrigin(r.Header.Get("Origin")) {
		switch r.Header.Get("Sec-Fetch-Site") {
		case "", "cross-site", "same-site":
			return false
		}
	}
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" {
		return true
	}
	if o := r.Header.Get("Origin"); o != "" {
		if o == "null" { // a no-referrer same-origin post; only Sec-Fetch-Site can vouch for it
			return r.Header.Get("Sec-Fetch-Site") != "same-origin"
		}
		u, err := url.Parse(o)
		return err != nil || u.Host != r.Host
	}
	return false
}

// commentAdminAuthor returns the signed-in owner or editor who posts as the site.
func (s *Server) commentAdminAuthor(r *http.Request) (uuid.UUID, string, bool) {
	if f := s.commentPost.admin; f != nil {
		return f(r)
	}
	if s.authService == nil {
		return uuid.Nil, "", false
	}
	c, err := r.Cookie(auth.SessionCookieName)
	if err != nil || c.Value == "" {
		return uuid.Nil, "", false
	}
	claims, err := s.authService.ValidateSession(r.Context(), c.Value)
	if err != nil || !s.commentStaff(r.Context(), claims) {
		return uuid.Nil, "", false
	}
	name := ""
	if s.userRepo != nil {
		if u, err := s.userRepo.GetByID(database.WithSuperadmin(r.Context()), claims.UserID); err == nil && u.Name != nil {
			name = *u.Name
		}
	}
	if name == "" {
		name = "Site team" // never the account's email
	}
	return claims.UserID, name, true
}

// commentStaff reports whether the session may reply as the site team. An
// authorizer error counts as no and is logged.
func (s *Server) commentStaff(ctx context.Context, claims *auth.Claims) bool {
	ok, err := authz.Can(ctx, authzsubject.ForSession(claims.UserID.String(), claims.Role), authz.ActionCommentReplyAsStaff, authz.Object{})
	if err != nil {
		s.logger.WarnContext(ctx, "authz check failed", "action", string(authz.ActionCommentReplyAsStaff), "err", err)
		return false
	}
	return ok
}

// commentDevice returns the commenter the device cookie belongs to.
func (s *Server) commentDevice(r *http.Request) (uuid.UUID, bool) {
	c, err := r.Cookie(identity.CookieName)
	if err != nil {
		return uuid.Nil, false
	}
	if f := s.commentPost.device; f != nil {
		return f(r.Context(), c.Value)
	}
	if s.commentIdentity == nil {
		return uuid.Nil, false
	}
	return s.commentIdentity.Device(r.Context(), c.Value)
}

// commentFormView builds the view the comments fragment renders: the thread
// notice/error and, unless closed, the form with placeholders (filled later by
// commentSlot.fill). Callers must have checked that renderEngine, commentSlot
// and commentSubmit are wired.
func (s *Server) commentFormView(ctx context.Context, id uuid.UUID, path, locale string, parent *uuid.UUID,
	notice, errCode string, echo *commentEcho, locked bool) *render.PageView {
	v := &render.PageView{Path: path, Locale: locale, EntityID: id.String()}
	v.Comments = &render.CommentThread{Notice: notice, Error: errCode}
	if echo != nil && echo.Code == commentsubmit.CodeDraft && errCode == commentsubmit.CodeDraft {
		v.Comments.ErrorText = echo.Message // the response body only, never a URL
	}
	if errCode != "closed" {
		form := render.CommentFormPlaceholders
		form.Action = "/-/comments/" + id.String()
		form.MaxBody = commentsubmit.MaxBodyRunes
		if parent != nil {
			form.ParentID = parent.String()
		}
		if echo != nil {
			form.Focus = commentFocus(echo.Code)
		}
		if echo != nil && echo.Typed != nil {
			vp := render.CommentValuePlaceholders
			if echo.Typed.Body != "" {
				form.BodyValue = vp.BodyValue
			}
			if echo.Typed.Name != "" {
				form.NameValue = vp.NameValue
			}
			if echo.Typed.Email != "" {
				form.EmailValue = vp.EmailValue
			}
			form.Notify = echo.Typed.Notify
		}
		if set, err := s.commentSubmit.Comments.Settings(ctx); err == nil && set.Captcha {
			if cw := s.captchaWidgetFor(ctx); cw.provider != "" {
				form.Captcha = &render.CommentCaptcha{Provider: cw.provider, SiteKey: cw.siteKey}
			}
		}
		if locked {
			lockCommentForm(&form)
		}
		v.CommentForm = &form
		v.CommentsOpen = true
	}
	s.applyStrings(ctx, v)
	return v
}

// logCommentErr logs a non-fatal comment read failure.
func (s *Server) logCommentErr(ctx context.Context, msg string, err error) {
	if s.logger != nil {
		s.logger.WarnContext(ctx, msg, "error", err)
	}
}

// writeCommentFragment answers an htmx post: the form partial with a fresh
// token and, for a post the sender may see, the new comment as an
// out-of-band insert. It returns false (nothing written) when it cannot, and
// the caller then redirects like a plain post.
func (s *Server) writeCommentFragment(w http.ResponseWriter, r *http.Request, id uuid.UUID, path string,
	req commentsubmit.Request, out commentsubmit.Outcome, notice, errCode string, echo *commentEcho) bool {
	if s.renderEngine == nil || s.commentSlot == nil || s.commentSubmit == nil {
		return false
	}
	ctx := r.Context()
	var parent *uuid.UUID
	if out.Kind == commentsubmit.Invalid {
		parent = req.ParentID // keep the reply target on a retry
	}
	cfg, err := s.commentSubmit.Comments.EntityConfig(ctx, id)
	if err != nil { // a failed read must not offer a policy schema's free name field
		s.logCommentErr(ctx, "comments: policy", err)
		cfg.Policy.Enabled = true
	}
	v := s.commentFormView(ctx, id, path, req.Locale, parent, notice, errCode, echo, cfg.Policy.Enabled)

	var item *render.CommentItem
	if out.Kind == commentsubmit.Posted && out.Comment.ParentID == nil && req.ParentID == nil {
		it := commentItem(comment.ThreadComment{
			ID: out.Comment.ID, Depth: out.Comment.Depth, AuthorName: out.Comment.AuthorName,
			IsSiteAuthor: out.Comment.AuthorUserID != nil, BodyHTML: out.Comment.BodyHTML, CreatedAt: out.Comment.CreatedAt,
		})
		if out.Comment.Status != comment.StatusApproved {
			it.Own = true
		}
		item = &it
	}
	var buf bytes.Buffer
	if err := s.renderEngine.RenderCommentsFragment(&buf, v, item); err != nil {
		if s.logger != nil {
			s.logger.ErrorContext(ctx, "comment fragment", "error", err)
		}
		return false
	}
	fr := r
	if echo != nil {
		fr = r.WithContext(withCommentEcho(ctx, *echo))
	}
	body, ok := s.commentSlot.fill(fr, buf.Bytes())
	if v.CommentForm != nil && !ok {
		return false // never send a form that still holds the placeholders
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	return true
}

// writeCommentPage answers a no-JS post the sender can fix with the page
// itself: 200 on the POST, private and no-store, the form holding the error,
// their values, a fresh token and new field names. False (nothing written)
// when the page handlers are not mounted or do not answer 200 (a redirect, a
// 404 or a failure never answers the post).
func (s *Server) writeCommentPage(w http.ResponseWriter, r *http.Request, path string, echo commentEcho) bool {
	h := s.renderHandlers
	if h == nil || h.engine == nil || s.commentSlot == nil {
		return false
	}
	hp := *h // copy: no 404 log row for a re-render
	hp.log404 = nil
	req := r.Clone(withCommentEcho(withUncachedRequest(r.Context()), echo))
	req.Method, req.URL, req.RequestURI = http.MethodGet, &url.URL{Path: path}, path
	req.Body, req.ContentLength = http.NoBody, 0
	req.Form, req.PostForm = nil, nil
	req.Header.Del("HX-Request")
	req.Header.Del("Content-Type")
	// Seeded with the outgoing headers (the security middleware's CSP) so the
	// page handlers adjust them as they do on a normal GET.
	bw := &bufferedResponse{h: w.Header().Clone()}
	if path == "/" {
		hp.handleRenderHome(bw, req)
	} else {
		hp.handleRenderCatchAll(bw, req)
	}
	if bw.status != http.StatusOK {
		return false // a redirect, 404 or 5xx must not answer the post
	}
	clear(w.Header())
	for k, v := range bw.h {
		w.Header()[k] = v
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(bw.body)
	return true
}
