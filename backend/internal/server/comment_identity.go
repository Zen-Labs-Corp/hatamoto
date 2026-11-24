package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/commentsubmit"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/spam"
	"hatamoto.dev/sdk"
)

// commentConfirmPrefix is the path of the comment confirmation link.
const commentConfirmPrefix = "/-/comments/confirm/"

// commentIdentity is the narrow surface the confirm handler needs
// (*identity.Service satisfies it; tests use a fake).
type commentIdentity interface {
	Enabled(ctx context.Context) bool
	Peek(ctx context.Context, raw string) (identity.Pending, error)
	Confirm(ctx context.Context, raw string) (identity.Confirmed, error)
	Cookie(c identity.Confirmed, secure bool) *http.Cookie
}

// commentEntityLookup returns the title and public path of an entity.
type commentEntityLookup func(ctx context.Context, id uuid.UUID) (title, path string, ok bool)

// commentEntity is the production lookup. It is false without an entity service.
func (s *Server) commentEntity(ctx context.Context, id uuid.UUID) (title, path string, ok bool) {
	if s.entityService == nil {
		return "", "", false
	}
	e, err := s.entityService.GetEntity(ctx, id)
	if err != nil || e == nil {
		return "", "", false
	}
	return e.Name, s.entityPublicPath(ctx, e), true
}

// commentCookieSecure is the Secure attribute of the commenter cookie: the same
// rule as the theme preview cookie.
func (s *Server) commentCookieSecure(r *http.Request) bool {
	if s.cfg != nil && s.cfg.AdminCookieSecure || r.TLS != nil {
		return true
	}
	return middleware.IsTrustedProxy(r.RemoteAddr) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// commentConfirmSameOrigin admits a POST only when the browser says it is
// same-origin. Origin "null" is what a same-origin form navigation sends under
// Referrer-Policy no-referrer, so it passes; Sec-Fetch-Site still decides.
func commentConfirmSameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") != "same-origin" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	}
	return true
}

// commentConfirmHandler serves GET/HEAD (the confirm page, never consuming the
// link) and POST (consume, set the device cookie, redirect to the comment).
func (s *Server) commentConfirmHandler(ci commentIdentity, lookup commentEntityLookup) http.HandlerFunc {
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
			s.logger.ErrorContext(r.Context(), "comment confirm: "+what, "error", err)
		}
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
	}
	invalid := func(w http.ResponseWriter, r *http.Request) {
		s.writeCommentConfirm(w, r, http.StatusNotFound, &render.CommentConfirmView{Invalid: true},
			identity.PageData{State: identity.PageInvalid})
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if ci == nil || !ci.Enabled(ctx) {
			notFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Robots-Tag", "noindex")
		h.Set("Referrer-Policy", "no-referrer")
		raw := r.PathValue("token")

		switch r.Method {
		case http.MethodGet, http.MethodHead:
			p, err := ci.Peek(ctx, raw)
			if errors.Is(err, identity.ErrTokenInvalid) {
				invalid(w, r)
				return
			}
			if err != nil {
				fail(w, r, "peek", err)
				return
			}
			title, _, ok := lookup(ctx, p.EntityID)
			if !ok {
				invalid(w, r)
				return
			}
			name, _, _ := s.digestSite(ctx)
			action := commentConfirmPrefix + raw
			s.writeCommentConfirm(w, r, http.StatusOK, &render.CommentConfirmView{EntityTitle: title, Action: action},
				identity.PageData{SiteName: name, EntityTitle: title, Action: action, State: identity.PageConfirm})
		case http.MethodPost:
			if !commentConfirmSameOrigin(r) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			c, err := ci.Confirm(ctx, raw)
			if errors.Is(err, identity.ErrTokenInvalid) {
				invalid(w, r)
				return
			}
			if err != nil {
				fail(w, r, "confirm", err)
				return
			}
			http.SetCookie(w, ci.Cookie(c, s.commentCookieSecure(r)))
			s.commentBypass.forget(c.CommenterID) // the confirmed comment shows at once, not after the 30 s cache
			_, path, ok := lookup(ctx, c.Comment.EntityID)
			if !ok || path == "" {
				path = "/"
			}
			http.Redirect(w, r, path+"#comment-"+c.Comment.ID.String(), http.StatusSeeOther)
		default:
			h.Set("Allow", "GET, HEAD, POST")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// commentDraftsPurgeJob is the WORKER_DISABLE_JOBS name of the hourly purge.
const commentDraftsPurgeJob = "comment_drafts_purge"

// setupComments builds the comment store and the identity service. Without a
// database, a server secret or a mail service the identity service stays nil
// and the confirm route answers 404.
func (s *Server) setupComments() {
	if s.db == nil || s.cfg == nil || s.cfg.Secret == "" {
		return
	}
	s.comments = comment.New(s.db, s.cfg.Secret, s.logger)
	secret := s.cfg.Secret

	// The edit and delete routes read the switch from the same cached settings
	// the page render uses. An unreadable setting counts as on: the routes check
	// the device cookie and ownership anyway.
	on := func() bool {
		set, err := s.comments.Settings(context.Background())
		return err != nil || set.Enabled
	}
	devKey := identity.DeviceKey(secret)
	s.commentSlot = &commentSlot{
		tokenKey: spam.CommentTokenKey(secret), nameKey: spam.FieldNameKey(secret),
		hashIP: s.comments.Keys().HashIP, now: time.Now, on: on,
	}
	// A visitor with a comment of their own that is not public yet, or still
	// editable, gets an uncached page.
	s.commentBypass = newCommentBypass(
		func(v string, t time.Time) (identity.DeviceClaims, bool) { return identity.VerifyDevice(devKey, v, t) },
		func(ctx context.Context, id uuid.UUID) (bool, error) {
			if has, err := s.comments.HasNonPublic(ctx, id); has || err != nil {
				return has, err
			}
			if has, err := s.comments.HasEditable(ctx, id); has || err != nil {
				return has, err
			}
			return anyShowDead(ctx, s.comments, s.pluginMayModerate, secret, id)
		}, nil)
	s.comments.Register(commentCacheObserver{
		drop: s.dropRenderedEntity, all: s.invalidateRendered,
		forget:   s.commentBypass.forget,
		coalesce: newDropCoalescer(s.dropRenderedEntity, commentDropWindow),
	})
	if s.hooks != nil {
		s.comments.Register(newCommentEventObserver(s.hooks))
	}
	if s.emailService == nil {
		return
	}
	svc, err := identity.New(identity.Config{
		DB: s.db, Comments: s.comments, Mailer: s.emailService,
		Site: func(ctx context.Context) (string, string) {
			n, b, _ := s.digestSite(ctx)
			return n, b
		},
		Secret: s.cfg.Secret, Now: time.Now, Logger: s.logger,
	})
	if err != nil {
		s.logger.Error("comment identity disabled", "error", err)
		return
	}
	s.commentIdentity = svc
	s.commentSubmit = &commentsubmit.Pipeline{
		Comments: s.comments, Identity: svc, Spam: s.spam,
		TokenKey: spam.CommentTokenKey(secret), NameKey: spam.FieldNameKey(secret),
		Limits:   commentsubmit.NewLimits(time.Now),
		OnSurge:  s.commentSurgeHook(),
		Checkers: []comment.Checker{commentsubmit.HookChecker{Engine: s.hooks}},
		Log:      s.logger,
	}
}

// showDeadFlags is the slice of *comment.Service the show-dead checks read.
type showDeadFlags interface {
	FlaggedPlugins(ctx context.Context) ([]string, error)
	ShowDeadPlugins(ctx context.Context, keys map[string]string) ([]string, error)
}

// viewerShowsDead reports whether plugin flagged the viewer to read dead
// comments and may moderate. A lookup error is no reveal (fail closed).
func viewerShowsDead(ctx context.Context, f showDeadFlags, mayModerate func(plugin string) bool, secret, plugin string, commenterID uuid.UUID) bool {
	if !mayModerate(plugin) {
		return false
	}
	got, err := f.ShowDeadPlugins(ctx, map[string]string{plugin: comment.PluginCommenterKey(secret, plugin, commenterID)})
	return err == nil && slices.Contains(got, plugin)
}

// anyShowDead reports whether any plugin that may moderate flagged the viewer:
// such a viewer's pages are rendered privately (the bypass check).
func anyShowDead(ctx context.Context, f showDeadFlags, mayModerate func(plugin string) bool, secret string, commenterID uuid.UUID) (bool, error) {
	flagged, err := f.FlaggedPlugins(ctx)
	if err != nil {
		return false, err
	}
	keys := map[string]string{}
	for _, p := range flagged {
		if mayModerate(p) {
			keys[p] = comment.PluginCommenterKey(secret, p, commenterID)
		}
	}
	if len(keys) == 0 {
		return false, nil
	}
	got, err := f.ShowDeadPlugins(ctx, keys)
	return len(got) > 0, err
}

// pluginMayModerate reports whether the owner granted the plugin comments.moderate.
func (s *Server) pluginMayModerate(plugin string) bool {
	if s.plugins == nil {
		return false
	}
	g := s.plugins.Grants()
	return g != nil && g.Has(plugin, sdk.PermCommentsModerate)
}

// handleCommentConfirm is the mounted handler: 404 until setupComments built
// the service.
func (s *Server) handleCommentConfirm(w http.ResponseWriter, r *http.Request) {
	var ci commentIdentity
	if s.commentIdentity != nil { // avoid a typed-nil interface
		ci = s.commentIdentity
	}
	s.commentConfirmHandler(ci, s.commentEntity)(w, r)
}

// purgeCommentDrafts is the hourly job: unconfirmed drafts and tokens, then the
// used form tokens. A failed form-token purge is logged and does not fail the job.
func (s *Server) purgeCommentDrafts(ctx context.Context) error {
	err := s.commentIdentity.Purge(ctx)
	if _, e := s.comments.PurgeFormTokens(ctx); e != nil {
		s.logger.Warn("comment form tokens purge", "error", e)
	}
	return err
}

// scheduleCommentDraftsPurge schedules the hourly purge unless there is no
// scheduler, database or identity service, or WORKER_DISABLE_JOBS names it.
func (s *Server) scheduleCommentDraftsPurge() bool {
	if s.db == nil || s.scheduler == nil || s.commentIdentity == nil || !workerJobEnabled(commentDraftsPurgeJob) {
		return false
	}
	err := s.scheduler.ScheduleJitter(commentDraftsPurgeJob, time.Hour, 10*time.Minute, 3*time.Minute, s.purgeCommentDrafts)
	if err != nil {
		s.logger.Error("comment drafts purge job not scheduled", "error", err)
	}
	return err == nil
}
