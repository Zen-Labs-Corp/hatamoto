package httpx

import (
	"net/http"
	"sync/atomic"
)

var refuser atomic.Pointer[http.HandlerFunc]

// RegisterRefuser sets the handler Refuse calls. The server registers its
// themed standard not-found responder at construction. Registration is atomic
// and the last writer wins.
func RegisterRefuser(h http.HandlerFunc) { refuser.Store(&h) }

// Refuse answers a request the admin refuses to acknowledge (a sealed admin
// path, an unknown path under the admin prefix) with the site's standard
// not-found response, so a probe learns nothing. Until a responder is
// registered it writes a plain 404. It is safe for concurrent use.
func Refuse(w http.ResponseWriter, r *http.Request) {
	if p := refuser.Load(); p != nil {
		(*p)(w, r)
		return
	}
	http.NotFound(w, r)
}
