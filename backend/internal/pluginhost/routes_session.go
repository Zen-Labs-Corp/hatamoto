package pluginhost

import (
	"errors"
	"log/slog"
	"net/http"

	"hatamoto.dev/sdk"
)

// CommenterSessionFunc applies a plugin's commenter_session: it checks the
// request is same-origin and writes the commenter Set-Cookie. Set by the server.
type CommenterSessionFunc func(w http.ResponseWriter, r *http.Request, plugin string, s sdk.CommenterSession) error

var (
	// ErrSessionCrossOrigin is returned by a CommenterSessionFunc for a request that is not same-origin (403).
	ErrSessionCrossOrigin = errors.New("pluginhost: commenter session refused: not a same-origin request")
	// ErrSessionUnavailable is returned when commenter sessions cannot be issued (503).
	ErrSessionUnavailable = errors.New("pluginhost: commenter sessions unavailable")
)

// sessionStatus returns 0 when cs may be applied, else the refusal status.
func (g *RouteRegistry) sessionStatus(r *http.Request, plugin string, cs *sdk.CommenterSession) int {
	switch {
	case !g.public, r.Method == http.MethodGet, r.Method == http.MethodHead,
		g.identity == nil || !g.identity(plugin), cs.Valid() != nil:
		return http.StatusBadGateway
	case g.session == nil:
		return http.StatusServiceUnavailable
	}
	return 0
}

// applySession runs the gate for a response carrying commenter_session. On any
// refusal it drops every header set so far (including the plugin's cookies),
// writes the error and reports false.
func (g *RouteRegistry) applySession(w http.ResponseWriter, r *http.Request, plugin string, cs *sdk.CommenterSession) bool {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	status := g.sessionStatus(r, plugin, cs)
	if status == 0 {
		err := g.session(w, r, plugin, *cs)
		switch {
		case err == nil:
			return true
		case errors.Is(err, ErrSessionCrossOrigin):
			status = http.StatusForbidden
		case errors.Is(err, ErrSessionUnavailable):
			status = http.StatusServiceUnavailable
		default:
			status = http.StatusBadGateway
		}
	}
	g.log.Warn("plugin route: commenter_session refused", slog.String("plugin", plugin), slog.Int("status", status))
	for k := range h {
		delete(h, k)
	}
	writeJSONError(w, status, "plugin error")
	return false
}
