package taxonomy

import "context"

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

func termDetail(t *Term) map[string]any {
	return map[string]any{"name": t.Name, "slug": t.Slug, "kind": t.Kind}
}
