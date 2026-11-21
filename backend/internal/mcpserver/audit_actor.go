package mcpserver

import (
	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/internal/auditctx"
	hmauth "hatamoto.dev/core/internal/auth"
)

// keyActor is the audit actor of a write made with key over MCP. The label
// carries the key's name, never its secret.
func keyActor(key *hmauth.APIKey) auditctx.Actor {
	a := auditctx.Actor{Kind: audit.KindAPIKey, KeyID: key.ID.String(), Name: "api-key:" + key.Name, Via: "mcp"}
	if key.CreatedBy != nil {
		a.ID = key.CreatedBy.String()
	}
	return a
}
