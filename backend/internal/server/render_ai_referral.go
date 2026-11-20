package server

import (
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/httpx"
)

// aiReferralMiddleware counts a human visit that an AI assistant sent to a
// Go-rendered page: a successful HTML GET whose Referer host (or utm_source)
// the agent registry lists as an AI assistant. It feeds the same buffered
// daily rollup as pageviews, keeping only the assistant name; the path,
// referrer, IP and user agent are discarded. Known bots are skipped, and so is
// everything that is not an HTML page (robots.txt, sitemaps, feeds, ...).
func (s *Server) aiReferralMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.analyticsService == nil || r.Method != http.MethodGet || isPrefetch(r) {
			next.ServeHTTP(w, r)
			return
		}
		ww := httpx.NewRecorder(w)
		next.ServeHTTP(ww, r)
		// A revalidated page answers 304, which carries no Content-Type; only
		// the page handler produces one on these routes, and it is the same
		// visit the 200 would have been.
		st := ww.Status()
		if st != 0 && st != http.StatusOK && st != http.StatusNotModified {
			return
		}
		if st != http.StatusNotModified && !strings.HasPrefix(ww.Header().Get("Content-Type"), "text/html") {
			return
		}
		reg := agents.Default()
		if reg.Match(r.UserAgent()) != nil {
			return
		}
		if source, ok := reg.ClassifyReferral(r.Referer(), r.URL.Query().Get("utm_source")); ok {
			s.analyticsService.RecordAIReferral(source)
		}
	})
}
