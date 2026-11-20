package server

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
)

// commentUnsubscribePrefix is the unsubscribe link of comment mail. The token
// is the authority: it carries a kind, an id and a MAC, and never expires.
const commentUnsubscribePrefix = "/-/comments/unsubscribe/"

// commentUnsubscriber is the part of *comment.Service the route uses.
type commentUnsubscriber interface {
	UnsubscribeReplies(ctx context.Context, commenterID uuid.UUID) (int64, error)
	SetModeratorMuted(ctx context.Context, userID uuid.UUID, muted bool) error
}

type unsubscribeState int

const (
	unsubscribeAsk unsubscribeState = iota
	unsubscribeDone
	unsubscribeInvalid
)

type unsubscribePage struct {
	SiteName  string
	Action    string
	State     unsubscribeState
	Moderator bool
}

// unsubscribeTmpl is a standalone, unthemed page like the comment confirm page:
// no script and no external asset.
var unsubscribeTmpl = template.Must(template.New("unsubscribe").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<meta name="referrer" content="no-referrer">
<title>{{if eq .State 2}}Link not valid{{else}}Comment emails{{end}}</title>
<style>body{font:1rem/1.5 system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem}button{font:inherit;padding:.5rem 1.25rem}</style>
</head>
<body>
<main>
{{if eq .State 2}}<h1>Link not valid</h1>
<p>This link is not valid.</p>
{{else if eq .State 1}}<h1>You are unsubscribed</h1>
<p>You will not get these emails from {{.SiteName}} any more.</p>
{{if .Moderator}}<form method="post" action="{{.Action}}"><input type="hidden" name="action" value="resubscribe"><button type="submit">Turn them back on</button></form>{{end}}
{{else}}<h1>Stop these emails?</h1>
<p>{{.SiteName}} sends you comment emails. Confirm to stop them.</p>
<form method="post" action="{{.Action}}"><button type="submit">Stop these emails</button></form>
{{end}}</main>
</body>
</html>
`))

// handleCommentUnsubscribe is the mounted handler: 404 until setupComments built
// the comment service.
func (s *Server) handleCommentUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if s.comments == nil || s.cfg == nil {
		http.NotFound(w, r)
		return
	}
	commentUnsubscribeHandler(s.comments, comment.UnsubscribeKey(s.cfg.Secret), s.digestSite, s.logger)(w, r)
}

// commentUnsubscribeHandler serves GET/HEAD (a page that never changes anything,
// so mail scanners that fetch links do not unsubscribe anyone) and POST (the
// change; also the RFC 8058 one-click request). There is no same-origin check:
// the token is the authority and carries no session.
func commentUnsubscribeHandler(store commentUnsubscriber, key []byte, site func(context.Context) (string, string, string), log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Robots-Tag", "noindex")
		h.Set("Referrer-Policy", "no-referrer")
		name, _, _ := site(ctx)
		page := func(code int, p unsubscribePage) {
			p.SiteName = name
			h.Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(code)
			if r.Method != http.MethodHead {
				_ = unsubscribeTmpl.Execute(w, p)
			}
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
			h.Set("Allow", "GET, HEAD, POST")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		token := r.PathValue("token")
		kind, id, ok := comment.VerifyUnsubscribe(key, token)
		if !ok {
			page(http.StatusNotFound, unsubscribePage{State: unsubscribeInvalid})
			return
		}
		action := commentUnsubscribePrefix + token
		moderator := kind == comment.UnsubscribeModerator
		if r.Method != http.MethodPost {
			page(http.StatusOK, unsubscribePage{State: unsubscribeAsk, Action: action, Moderator: moderator})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Request too large", http.StatusRequestEntityTooLarge)
			return
		}
		var err error
		resub := false
		if moderator {
			resub = r.PostForm.Get("action") == "resubscribe"
			err = store.SetModeratorMuted(ctx, id, !resub)
		} else {
			_, err = store.UnsubscribeReplies(ctx, id)
		}
		if err != nil {
			// The token is a bearer secret: it is never logged.
			log.ErrorContext(ctx, "comment unsubscribe", "error", err)
			http.Error(w, "Something went wrong", http.StatusInternalServerError)
			return
		}
		if resub {
			page(http.StatusOK, unsubscribePage{State: unsubscribeAsk, Action: action, Moderator: true})
			return
		}
		page(http.StatusOK, unsubscribePage{State: unsubscribeDone, Action: action, Moderator: moderator})
	}
}
