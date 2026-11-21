package media

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

// changedFields names the metadata fields an update set.
func changedFields(in *UpdateMediaInput) []string {
	var names []string
	if in.AltText != nil {
		names = append(names, "alt_text")
	}
	if in.Caption != nil {
		names = append(names, "caption")
	}
	if in.Credit != nil {
		names = append(names, "credit")
	}
	if in.Folder != nil {
		names = append(names, "folder")
	}
	return names
}
