package admin

import "net/http"

// handleEntityComments is a stub until its screen lands.
func (a *Admin) handleEntityComments(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}

// handleSchemaComments is a stub until its screen lands.
func (a *Admin) handleSchemaComments(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}
