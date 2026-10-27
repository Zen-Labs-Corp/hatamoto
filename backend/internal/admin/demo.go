package admin

import "net/http"

// demoGuard refuses, in public demo mode, the admin actions listed in
// config.DemoConfig: 403 with a clear message (an error page, or a toast for
// an htmx request), never a 500. Outside demo mode it is a pass-through.
func (a *Admin) demoGuard(next http.Handler) http.Handler {
	if !a.deps.Demo.Enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if msg, refused := a.deps.Demo.Refusal(r.Method, r.URL.Path); refused {
			a.Error(w, r, http.StatusForbidden, "Not available in the public demo", msg)
			return
		}
		next.ServeHTTP(w, r)
	})
}
