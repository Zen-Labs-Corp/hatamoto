// Package auditctx carries who is acting into layers that write audit events
// themselves (the entity repository, the content services). The caller that
// knows the principal (a handler, a command) puts it in the context; the writer
// reads it.
package auditctx

import "context"

// Actor is the principal behind a write.
type Actor struct {
	ID        string // user uuid; "" when none (for an API key: its creator)
	Name      string // display actor: email, "api-key:<name>", "cli"
	Kind      string // user, api_key, cli or system
	KeyID     string // API key uuid
	Via       string // "api", "mcp", "cli"; "" for the admin
	IP        string
	UserAgent string
	RequestID string
	Detail    map[string]any // extra detail the caller knows; merged by the writer
}

type key struct{}

// With returns ctx carrying a.
func With(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, key{}, a)
}

// From returns the actor in ctx. It is false when there is none or its Kind is
// empty.
func From(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(key{}).(Actor)
	return a, ok && a.Kind != ""
}
