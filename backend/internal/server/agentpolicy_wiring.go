package server

import (
	"context"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/agentpolicystate"
	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/auditstate"
)

// wireAgentPolicy gives the agent policy package its audit writer and the
// per-key change reader. It only stores pointers; nothing runs unless a
// module registered a policy or calls agentpolicy.Changes.
func (s *Server) wireAgentPolicy() {
	if s.db == nil {
		return
	}
	pool := s.db.Pool()
	agentpolicystate.SetRuntime(agentpolicystate.Runtime{
		Record: func(ctx context.Context, ev auditstate.Event) {
			if err := (auditlog.Recorder{Pool: pool}).Record(ctx, nil, ev); err != nil {
				s.logger.Error("admin audit write failed", "action", ev.Action, "error", err)
			}
		},
		Changes: func(ctx context.Context, keyID string, since time.Time, limit int) ([]agentpolicystate.Change, error) {
			id, err := uuid.Parse(keyID)
			if err != nil {
				return nil, agentpolicystate.ErrInvalidKey
			}
			rows, err := auditlog.KeyChanges(ctx, pool, id, since, limit)
			if err != nil {
				return nil, err
			}
			return mapKeyChanges(rows), nil
		},
	})
}

func mapKeyChanges(rows []auditlog.KeyChange) []agentpolicystate.Change {
	out := make([]agentpolicystate.Change, len(rows))
	for i, r := range rows {
		out[i] = agentpolicystate.Change{
			ID: r.ID, At: r.At, Action: r.Action, Target: r.Target,
			Result: r.Result, Via: r.Via, Actor: r.Actor, Detail: r.Detail,
		}
	}
	return out
}
