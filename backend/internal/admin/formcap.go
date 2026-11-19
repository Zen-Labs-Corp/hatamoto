package admin

import (
	"net/http"
	"strings"
)

// maxFormBody caps every admin form body that has no cap of its own. Plain
// forms never need more; the upload routes keep their own limits.
const maxFormBody = 8 << 20

// ownBodyLimit reports whether the route at p is capped by its own middleware.
func (a *Admin) ownBodyLimit(p string) bool {
	switch {
	case p == a.url("media")+"/upload", p == a.url(streamedUploadPath):
		return true
	case strings.HasPrefix(p, a.url("media")+"/") && strings.HasSuffix(p, "/replace"):
		return true
	case strings.HasPrefix(p, a.adminPath+csvBase), strings.HasPrefix(p, a.adminPath+importBase):
		return true // capped by limitImportBody, which runs first
	}
	return false
}

// limitFormBody caps request bodies before the CSRF check reads them.
func (a *Admin) limitFormBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !a.ownBodyLimit(r.URL.Path) {
				if r.ContentLength > maxFormBody {
					http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
			}
		}
		next.ServeHTTP(w, r)
	})
}
