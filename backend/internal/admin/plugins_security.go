package admin

import (
	"net/http"

	"hatamoto.dev/core/internal/httpx"
)

// mountPluginSecurity registers the Package security page and the route that
// turns the unsigned-package opt-in off (owner only).
func (a *Admin) mountPluginSecurity(owner *httpx.Group) {
	owner.HandleFunc("GET /plugins/security", a.handlePackageSecurity)
	owner.HandleFunc("POST /plugins/unsigned/off", a.handleStopUnsigned)
}

func (a *Admin) handlePackageSecurity(w http.ResponseWriter, r *http.Request) {
	a.notBuilt(w, r)
}

func (a *Admin) handleStopUnsigned(w http.ResponseWriter, r *http.Request) {
	a.notBuilt(w, r)
}
