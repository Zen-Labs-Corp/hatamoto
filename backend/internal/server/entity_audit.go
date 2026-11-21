package server

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/internal/auditctx"
	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/middleware"
)

// entityAuditTx is the entity repository's in-transaction audit writer. Only a
// registered sink's refusal is returned (it fails the write); a failed default
// row is logged.
func (s *Server) entityAuditTx(ctx context.Context, tx pgx.Tx, ev auditstate.Event) error {
	if err := auditlog.TxHook(s.logger)(ctx, tx, ev); err != nil {
		return err
	}
	s.logger.Info("admin audit", "action", ev.Action, "actor", ev.Actor, "target", ev.Target)
	return nil
}

// restAuditActor names who is behind a REST request: the API key when one
// authenticated it, else the signed-in user. It is false for an anonymous request.
func restAuditActor(r *http.Request) (auditctx.Actor, bool) {
	a := auditctx.Actor{Via: "api", IP: middleware.GetClientIP(r), UserAgent: r.UserAgent(), RequestID: r.Header.Get("X-Request-ID")}
	var key *auth.APIKey
	if k, ok := middleware.GetAPIKey(r.Context()); ok && middleware.GetAuthType(r.Context()) == middleware.AuthTypeAPIKey {
		key = k
	}
	claims, _ := middleware.GetClaims(r.Context())
	return fillRestActor(a, key, claims)
}

func fillRestActor(a auditctx.Actor, key *auth.APIKey, claims *auth.Claims) (auditctx.Actor, bool) {
	if key != nil {
		a.Kind, a.KeyID, a.Name = audit.KindAPIKey, key.ID.String(), "api-key:"+key.Name
		if key.CreatedBy != nil {
			a.ID = key.CreatedBy.String()
		}
		return a, true
	}
	if claims != nil && claims.UserID != uuid.Nil {
		a.Kind, a.ID, a.Name = audit.KindUser, claims.UserID.String(), claims.Email
		return a, true
	}
	return a, false
}

// auditActorMiddleware puts the REST actor in the request context so the
// entity repository and the content services audit the write.
func (s *Server) auditActorMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a, ok := restAuditActor(r); ok {
			r = r.WithContext(auditctx.With(r.Context(), a))
		}
		next.ServeHTTP(w, r)
	})
}

// auditFromCtx is the content services' hook (taxonomy, menu, redirect, media):
// one pool row, best-effort, only when ctx carries an actor (REST, MCP). The
// admin sets none: it writes its own rows.
func (s *Server) auditFromCtx(ctx context.Context, action, target string, detail map[string]any) {
	a, ok := auditctx.From(ctx)
	if !ok || s.db == nil {
		return
	}
	if detail == nil {
		detail = map[string]any{}
	}
	if a.Via != "" {
		detail["via"] = a.Via
	}
	err := auditlog.Recorder{Pool: s.db.Pool()}.Record(ctx, nil, auditstate.Event{
		ActorID: a.ID, Actor: a.Name, ActorKind: a.Kind, KeyID: a.KeyID,
		Action: action, Target: target, Detail: detail,
		IP: a.IP, UserAgent: a.UserAgent, RequestID: a.RequestID,
	})
	if err != nil {
		s.logger.Error("admin audit write failed", "action", action, "error", err)
	}
}
