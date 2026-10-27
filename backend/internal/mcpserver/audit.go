package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/database"
)

// maxAuditArgs caps the stored arguments of one call, so a base64 upload or a
// long page body never lands in the audit table whole.
const maxAuditArgs = 4096

// AuditEntry is one tool call.
type AuditEntry struct {
	KeyID   uuid.UUID
	KeyName string
	Tool    string
	Args    json.RawMessage
	Err     error
}

// Auditor records tool calls.
type Auditor interface {
	Record(ctx context.Context, e AuditEntry)
}

// PostgresAuditor writes hatamoto_mcp_audit and mirrors each row to the log.
type PostgresAuditor struct {
	db     *database.DB
	logger *slog.Logger
}

// NewPostgresAuditor creates the audit writer.
func NewPostgresAuditor(db *database.DB, logger *slog.Logger) *PostgresAuditor {
	return &PostgresAuditor{db: db, logger: logger}
}

// Record stores the entry; a failed insert is logged, never returned: an
// audit outage must not turn a finished write into an error for the agent.
func (a *PostgresAuditor) Record(ctx context.Context, e AuditEntry) {
	errText := ""
	if e.Err != nil {
		errText = e.Err.Error()
	}
	a.logger.Info("mcp tool call", "tool", e.Tool, "api_key_id", e.KeyID, "api_key_name", e.KeyName, "ok", e.Err == nil)
	// The caller's context may end with the request; the row must not.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	conn, err := a.db.GetConn(wctx)
	if err != nil {
		a.logger.Error("mcp audit: connection", "error", err)
		return
	}
	defer conn.Release()
	if _, err := conn.Exec(wctx, `INSERT INTO hatamoto_mcp_audit (api_key_id, api_key_name, tool, ok, error, args)
		VALUES ($1, $2, $3, $4, $5, $6)`, e.KeyID, e.KeyName, e.Tool, e.Err == nil, errText, []byte(e.Args)); err != nil {
		a.logger.Error("mcp audit: insert", "error", err)
	}
}

// auditArgs is the JSON of the call's input, with long strings shortened and
// the whole capped at maxAuditArgs (then replaced by a note).
func auditArgs(in any) json.RawMessage {
	raw, err := json.Marshal(in)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return json.RawMessage(`{}`)
	}
	raw, _ = json.Marshal(shorten(v))
	if len(raw) > maxAuditArgs {
		return json.RawMessage(fmt.Sprintf(`{"truncated":true,"bytes":%d}`, len(raw)))
	}
	return raw
}

func shorten(v any) any {
	switch t := v.(type) {
	case string:
		if len(t) > 200 {
			return t[:200] + fmt.Sprintf("… (%d bytes)", len(t))
		}
		return t
	case map[string]any:
		for k, x := range t {
			t[k] = shorten(x)
		}
	case []any:
		for i, x := range t {
			t[i] = shorten(x)
		}
	}
	return v
}
