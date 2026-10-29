package admin

import (
	"net/http"

	"hatamoto.dev/core/internal/httpx"
)

// mountPluginBrowse registers the Browse pages and the registry refresh
// (owner only).
func (a *Admin) mountPluginBrowse(owner *httpx.Group) {
	owner.HandleFunc("GET /plugins/browse", a.handleBrowse)
	// The detail page is /plugins/<name>/about, not under /plugins/browse/:
	// GET /plugins/browse/{name} is ambiguous with GET /plugins/{name}/settings
	// and the like, and the mux refuses such a pair at mount.
	owner.HandleFunc("GET /plugins/{name}/about", a.handleBrowseDetail)
	owner.HandleFunc("POST /plugins/check-updates", a.handleCheckUpdates)
}

func (a *Admin) handleBrowse(w http.ResponseWriter, r *http.Request) {
	a.notBuilt(w, r)
}

func (a *Admin) handleBrowseDetail(w http.ResponseWriter, r *http.Request) {
	a.notBuilt(w, r)
}

func (a *Admin) handleCheckUpdates(w http.ResponseWriter, r *http.Request) {
	a.notBuilt(w, r)
}

// notBuilt answers a route whose page is not implemented yet.
func (a *Admin) notBuilt(w http.ResponseWriter, r *http.Request) {
	a.Error(w, r, http.StatusNotImplemented, "Not available yet", "This page is not available yet.")
}
