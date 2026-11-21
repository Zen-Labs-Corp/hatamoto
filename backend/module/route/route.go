package route

import (
	"net/http"

	"hatamoto.dev/core/internal/modhost"
)

// Body size limits of a mount, in bytes.
const (
	DefaultMaxBody = modhost.DefaultRouteMaxBody
	MaxMaxBody     = modhost.MaxRouteMaxBody
)

// Mount hands every request under Prefix to Handler.
type Mount struct {
	// Module is the module name (lowercase letters, digits and dashes).
	Module string
	// Prefix starts and ends with a slash, for example "/example/".
	Prefix string
	// Handler serves GET, HEAD, POST, PUT, PATCH and DELETE under Prefix.
	Handler http.Handler
	// MaxBody caps the request body in bytes; 0 means DefaultMaxBody.
	MaxBody int64
}

// Register mounts m. Call it from an init function: it panics after app.Main
// has started, for a reserved or malformed prefix, a prefix that overlaps
// another module's, an invalid module name, a nil handler and a MaxBody
// outside 0..MaxMaxBody.
func Register(m Mount) {
	modhost.RegisterRoute(modhost.Route{Module: m.Module, Prefix: m.Prefix, Handler: m.Handler, MaxBody: m.MaxBody})
}
