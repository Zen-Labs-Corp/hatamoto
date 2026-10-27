package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"hatamoto.dev/core/internal/analytics"
)

type referralRepo struct{ keys []string }

func (r *referralRepo) Add(_ context.Context, _ time.Time, kind, key string, n int64) error {
	for ; n > 0; n-- {
		r.keys = append(r.keys, kind+":"+key)
	}
	return nil
}

func TestAIReferralMiddleware(t *testing.T) {
	html := func(status int, ctype string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ctype)
			w.WriteHeader(status)
		})
	}
	const browser = "Mozilla/5.0 (X11; Linux x86_64) Firefox/130.0"
	tests := map[string]struct {
		method, target, referer, ua string
		h                           http.Handler
		want                        []string
	}{
		"chatgpt referrer":   {http.MethodGet, "/platform", "https://chatgpt.com/", browser, html(200, "text/html; charset=utf-8"), []string{"ai_referral:chatgpt"}},
		"utm only":           {http.MethodGet, "/?utm_source=chatgpt.com", "", browser, html(200, "text/html"), []string{"ai_referral:chatgpt"}},
		"search referrer":    {http.MethodGet, "/", "https://www.google.com/", browser, html(200, "text/html"), nil},
		"no referrer":        {http.MethodGet, "/", "", browser, html(200, "text/html"), nil},
		"bot with referrer":  {http.MethodGet, "/", "https://chatgpt.com/", captureGPTUA, html(200, "text/html"), nil},
		"not found":          {http.MethodGet, "/x", "https://chatgpt.com/", browser, html(404, "text/html"), nil},
		"sitemap not a page": {http.MethodGet, "/sitemap.xml", "https://chatgpt.com/", browser, html(200, "application/xml"), nil},
		"HEAD not counted":   {http.MethodHead, "/", "https://chatgpt.com/", browser, html(200, "text/html"), nil},
		"implicit 200 status": {http.MethodGet, "/", "https://claude.ai/", browser, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("x"))
		}), []string{"ai_referral:claude"}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			repo := &referralRepo{}
			s := &Server{analyticsService: analytics.NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))}
			req := httptest.NewRequest(tc.method, tc.target, nil)
			req.Header.Set("User-Agent", tc.ua)
			if tc.referer != "" {
				req.Header.Set("Referer", tc.referer)
			}
			s.aiReferralMiddleware(tc.h).ServeHTTP(httptest.NewRecorder(), req)
			if err := s.analyticsService.Flush(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(repo.keys) != len(tc.want) || (len(tc.want) == 1 && repo.keys[0] != tc.want[0]) {
				t.Fatalf("recorded %v, want %v", repo.keys, tc.want)
			}
		})
	}
}
