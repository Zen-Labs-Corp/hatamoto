//go:build hatamoto_qa

package server

import (
	"encoding/json"
	"html"
	"net/http"
	"os"
	"strings"
	"sync"
)

// This file is the browser acceptance seam for the opaque-origin sandbox
// (scripts/admin-qa/opaque-origin.mjs). It is compiled only with
// -tags hatamoto_qa and must never ship: it forces the sandbox and serves a
// fixture page plus a record of what reached /admin.

// qaSandboxForced makes every public HTML response carry the sandbox CSP.
func qaSandboxForced() bool { return os.Getenv("HATAMOTO_QA_SANDBOX_ALL") == "1" }

type qaSeen struct {
	Path      string `json:"path"`
	Cookie    bool   `json:"cookie"`
	Origin    string `json:"origin"`
	FetchSite string `json:"fetchSite"`
}

const qaPage = `<!doctype html><html><head><meta charset="utf-8"><title>qa</title></head><body>
<pre id="origin">pending</pre><pre id="admin-fetch">pending</pre>
<form class="hm-form" data-hm-form="%SLUG%" action="/forms/%SLUG%/submit" method="post">
<input type="email" name="email" value="qa@example.test"><button type="submit">Send</button></form>
<script>
document.getElementById('origin').textContent = self.origin;
fetch('/admin/qa-probe', {credentials: 'include'}).then(
  function (r) { document.getElementById('admin-fetch').textContent = 'status ' + r.status; },
  function () { document.getElementById('admin-fetch').textContent = 'blocked'; });
</script>
<script src="/theme/assets/hm.js" defer></script></body></html>`

// qaWrap serves /__qa/opaque?form=<slug> (through writeHTML, so it gets the
// sandbox header) and /__qa/seen (what reached /admin or /api/v1/track, from
// the server's side), and records those requests.
func (s *Server) qaWrap(h http.Handler) http.Handler {
	var mu sync.Mutex
	var seen []qaSeen
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/__qa/opaque" && s.renderHandlers != nil:
			page := strings.ReplaceAll(qaPage, "%SLUG%", html.EscapeString(r.URL.Query().Get("form")))
			s.renderHandlers.writeHTML(w, r, http.StatusOK, []byte(page), true)
		case r.URL.Path == "/__qa/seen":
			mu.Lock()
			defer mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(seen)
		default:
			if strings.HasPrefix(r.URL.Path, "/admin") || r.URL.Path == "/api/v1/track" {
				mu.Lock()
				seen = append(seen, qaSeen{r.URL.Path, r.Header.Get("Cookie") != "", r.Header.Get("Origin"), r.Header.Get("Sec-Fetch-Site")})
				mu.Unlock()
			}
			h.ServeHTTP(w, r)
		}
	})
}
