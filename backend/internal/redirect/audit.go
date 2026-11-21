package redirect

import (
	"context"
	"strings"
)

// AuditFunc records a finished write. The server wires it to a writer that
// records only when ctx carries an acting principal (REST, MCP, CLI); the
// admin writes its own rows, so calls from the admin record nothing here.
type AuditFunc func(ctx context.Context, action, target string, detail map[string]any)

// SetAudit turns the hook on. Nil (the default) records nothing.
func (s *Service) SetAudit(f AuditFunc) { s.audit = f }

func (s *Service) record(ctx context.Context, action, target string, detail map[string]any) {
	if s.audit != nil {
		s.audit(ctx, action, target, detail)
	}
}

// redirectDetail describes a redirect for the audit row. A query string is
// cut from both paths: it can carry a token.
func redirectDetail(rd *Redirect) map[string]any {
	return map[string]any{
		"source":      withoutQuery(rd.SourcePath),
		"target":      withoutQuery(rd.TargetURL),
		"status_code": rd.StatusCode,
		"enabled":     rd.IsActive,
	}
}

func withoutQuery(s string) string {
	before, _, _ := strings.Cut(s, "?")
	return before
}
