package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/spam"
)

// Paths of the standalone commenter sign-in. Like the confirm link they sit
// under the reserved "-" segment and outside the page cache.
const (
	commentSignInPath   = "/-/comments/signin"
	commentSignInPrefix = "/-/comments/signin/"
	commentSignOutPath  = "/-/comments/signout"
)

// commentSignInEmailField is the name of the sign-in form's email input.
const commentSignInEmailField = "email"

// commentSignIn is the narrow surface the sign-in handler needs
// (*identity.Service satisfies it; tests use a fake).
type commentSignIn interface {
	Enabled(ctx context.Context) bool
	RequestSignIn(ctx context.Context, req identity.SignInRequest) (identity.Outcome, error)
	PeekSignIn(ctx context.Context, raw string) error
	ConfirmSignIn(ctx context.Context, raw string) (identity.Confirmed, error)
	Cookie(c identity.Confirmed, secure bool) *http.Cookie
	ClearCookie(secure bool) *http.Cookie
	Device(ctx context.Context, val string) (uuid.UUID, bool)
}

// commentSignInRenderer is the part of *render.Engine that draws the sign-in page.
type commentSignInRenderer interface {
	RenderCommentSignIn(w io.Writer, v *render.PageView, status int) error
}

// commentSignInDeps are what the sign-in handler takes besides the identity
// service: the single-use form token plumbing and the client network hash.
type commentSignInDeps struct {
	tokenKey []byte
	tokens   spam.TokenStore
	hashIP   func(ip string) (ipHash, netHash string)
	now      func() time.Time
}

// handleCommentSignIn is the mounted handler: 404 until setupComments built
// the identity service.
func (s *Server) handleCommentSignIn(w http.ResponseWriter, r *http.Request) {
	var ci commentSignIn
	if s.commentIdentity != nil && s.commentSlot != nil && s.comments != nil { // avoid a typed-nil interface
		ci = s.commentIdentity
	}
	var d commentSignInDeps
	if s.commentSlot != nil {
		d = commentSignInDeps{tokenKey: s.commentSlot.tokenKey, hashIP: s.commentSlot.hashIP, now: s.commentSlot.now}
	}
	if s.comments != nil {
		d.tokens = s.comments
	}
	s.commentSignInHandler(ci, d)(w, r)
}

// commentSignInHandler serves the sign-in form, the mailed link and the
// sign-out button. GET never signs anyone in or out; every POST needs a
// same-origin browser and a single-use form token.
func (s *Server) commentSignInHandler(ci commentSignIn, d commentSignInDeps) http.HandlerFunc {
	const (
		subjectIn  = "signin"
		subjectOut = "signout"
	)
	notFound := func(w http.ResponseWriter, r *http.Request) {
		if h := s.renderHandlers; h != nil {
			h.writeNotFound(w, r, requestPath(r))
			return
		}
		http.NotFound(w, r)
	}
	fail := func(w http.ResponseWriter, r *http.Request, what string, err error) {
		// The token is a bearer secret: it is never logged.
		if s.logger != nil {
			s.logger.ErrorContext(r.Context(), "comment sign-in: "+what, "error", err)
		}
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if ci == nil || !ci.Enabled(ctx) {
			notFound(w, r)
			return
		}
		setCommentConfirmHeaders(w.Header())
		_, netHash := d.hashIP(middleware.GetClientIP(r))
		issue := func(subject string) string {
			return spam.IssueCommentToken(d.tokenKey, spam.CommentSubject(subject), netHash, d.now())
		}
		checkToken := func(subject string) bool {
			_, res := spam.CheckCommentToken(ctx, d.tokenKey, d.tokens, spam.CommentSubject(subject), netHash,
				r.PostFormValue("hm_ct"), d.now())
			return res == spam.TokenOK
		}
		view := func(state, action, token string) *render.CommentSignInView {
			return &render.CommentSignInView{State: state, Action: action, Token: token, EmailName: commentSignInEmailField}
		}
		raw := r.PathValue("token")
		isOut := r.URL.Path == commentSignOutPath

		switch {
		case r.Method == http.MethodGet || r.Method == http.MethodHead:
			if isOut {
				w.Header().Set("Allow", "POST")
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if raw != "" {
				if err := ci.PeekSignIn(ctx, raw); err != nil {
					if errors.Is(err, identity.ErrTokenInvalid) {
						s.writeCommentSignIn(w, r, http.StatusNotFound, view(render.CommentSignInInvalid, "", ""),
							identity.PageData{State: identity.PageSignInInvalid})
						return
					}
					fail(w, r, "peek", err)
					return
				}
				s.writeCommentSignIn(w, r, http.StatusOK, view(render.CommentSignInLink, commentSignInPrefix+raw, ""),
					identity.PageData{State: identity.PageSignInLink, Action: commentSignInPrefix + raw})
				return
			}
			if r.URL.Query().Get("out") == "1" {
				s.writeCommentSignIn(w, r, http.StatusOK, view(render.CommentSignInOut, "", ""),
					identity.PageData{State: identity.PageSignedOut})
				return
			}
			if c, err := r.Cookie(identity.CookieName); err == nil {
				if _, ok := ci.Device(ctx, c.Value); ok {
					tok := issue(subjectOut)
					s.writeCommentSignIn(w, r, http.StatusOK, view(render.CommentSignInIn, commentSignOutPath, tok),
						identity.PageData{State: identity.PageSignedIn, Action: commentSignOutPath, Token: tok})
					return
				}
			}
			tok := issue(subjectIn)
			s.writeCommentSignIn(w, r, http.StatusOK, view(render.CommentSignInForm, commentSignInPath, tok),
				identity.PageData{State: identity.PageSignInForm, Action: commentSignInPath, Token: tok})

		case r.Method == http.MethodPost:
			if !commentConfirmSameOrigin(r) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
			switch {
			case isOut:
				if !checkToken(subjectOut) {
					http.Redirect(w, r, commentSignInPath, http.StatusSeeOther)
					return
				}
				http.SetCookie(w, ci.ClearCookie(s.commentCookieSecure(r)))
				http.Redirect(w, r, commentSignInPath+"?out=1", http.StatusSeeOther)
			case raw != "":
				c, err := ci.ConfirmSignIn(ctx, raw)
				if errors.Is(err, identity.ErrTokenInvalid) {
					s.writeCommentSignIn(w, r, http.StatusNotFound, view(render.CommentSignInInvalid, "", ""),
						identity.PageData{State: identity.PageSignInInvalid})
					return
				}
				if err != nil {
					fail(w, r, "confirm", err)
					return
				}
				http.SetCookie(w, ci.Cookie(c, s.commentCookieSecure(r)))
				s.commentBypass.forget(c.CommenterID)
				http.Redirect(w, r, commentSignInPath, http.StatusSeeOther)
			default:
				addr := r.PostFormValue(commentSignInEmailField)
				if !checkToken(subjectIn) {
					tok := issue(subjectIn)
					s.writeCommentSignIn(w, r, http.StatusOK, view(render.CommentSignInForm, commentSignInPath, tok),
						identity.PageData{State: identity.PageSignInForm, Action: commentSignInPath, Token: tok})
					return
				}
				ipHash, nh := d.hashIP(middleware.GetClientIP(r))
				_, err := ci.RequestSignIn(ctx, identity.SignInRequest{Email: addr, IPHash: ipHash, NetHash: nh})
				if errors.Is(err, identity.ErrEmailInvalid) || errors.Is(err, identity.ErrEmailNotAccepted) {
					tok := issue(subjectIn)
					v := view(render.CommentSignInForm, commentSignInPath, tok)
					v.Email, v.Error = addr, "invalid"
					s.writeCommentSignIn(w, r, http.StatusOK, v,
						identity.PageData{State: identity.PageSignInForm, Action: commentSignInPath, Token: tok,
							Error: "Please enter an email address we can send to."})
					return
				}
				if err != nil {
					fail(w, r, "request", err)
					return
				}
				s.writeCommentSignIn(w, r, http.StatusOK, view(render.CommentSignInSent, "", ""),
					identity.PageData{State: identity.PageSignInSent})
			}
		default:
			w.Header().Set("Allow", "GET, HEAD, POST")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// writeCommentSignIn renders the themed sign-in page, falling back to
// identity's plain page without a themed engine or on a render error. The page
// is never stored, indexed or sent a referrer: the link token may be in the URL.
func (s *Server) writeCommentSignIn(w http.ResponseWriter, r *http.Request, status int,
	cv *render.CommentSignInView, fallback identity.PageData) {
	if h := s.renderHandlers; h != nil && h.engine != nil {
		if cr, ok := h.engine.(commentSignInRenderer); ok {
			v := h.errorView(r, &routeResult{NotFound: true})
			v.CommentSignIn = cv
			v.Page.Title = "" // the layout sets its own title
			var buf bytes.Buffer
			if err := cr.RenderCommentSignIn(&buf, v, status); err == nil {
				hd := w.Header()
				hd.Set("Content-Type", "text/html; charset=utf-8")
				applyRenderSecurityHeaders(hd, h.csp(), cspExtras{})
				setCommentConfirmHeaders(hd)
				w.WriteHeader(status)
				if r.Method != http.MethodHead {
					_, _ = w.Write(buf.Bytes())
				}
				return
			} else if s.logger != nil {
				s.logger.ErrorContext(r.Context(), "render: comment sign-in page", "error", err)
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	setCommentConfirmHeaders(w.Header())
	w.WriteHeader(status)
	_ = identity.RenderPage(w, fallback)
}
