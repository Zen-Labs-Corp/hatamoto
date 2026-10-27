package packages

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

// Actor is who made a change: a signed-in admin user, or the CLI.
type Actor struct {
	ID    uuid.UUID // uuid.Nil for the CLI
	Email string    // "cli" for the CLI
}

type actorKey struct{}

// WithActor returns ctx carrying who is making package changes; the audit log
// records it. Without one the actor is "unknown".
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

func actorFrom(ctx context.Context) Actor {
	if a, ok := ctx.Value(actorKey{}).(Actor); ok {
		return a
	}
	return Actor{Email: "unknown"}
}

// Audit writes one row of hatamoto_admin_audit (and a log line) for a change
// to packages: install, update, uninstall, activate, grants, the unsigned
// opt-in. The detail never holds a secret.
func (m *Manager) Audit(ctx context.Context, action, target string, detail map[string]any) {
	if detail == nil {
		detail = map[string]any{}
	}
	a := actorFrom(ctx)
	raw, _ := json.Marshal(detail)
	var actorID *uuid.UUID
	if a.ID != uuid.Nil {
		actorID = &a.ID
	}
	// The write must outlive a cancelled request: the change already happened.
	if _, err := m.Pool.Exec(context.WithoutCancel(ctx), `INSERT INTO hatamoto_admin_audit (actor_id, actor, action, target, detail) VALUES ($1, $2, $3, $4, $5)`,
		actorID, a.Email, action, target, raw); err != nil {
		m.Logger.Error("packages: audit write failed", "action", action, "error", err)
	}
	m.Logger.Info("admin audit", "action", action, "actor", a.Email, "target", target, "detail", string(raw))
}
