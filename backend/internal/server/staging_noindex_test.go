package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"hatamoto.dev/core/internal/runenv"
)

func TestStagingNoindexMiddleware(t *testing.T) {
	h := stagingNoindex(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex" {
		t.Fatalf("X-Robots-Tag = %q", got)
	}
}

func TestStagingNoindexOnEveryResponse(t *testing.T) {
	paths := []string{"/", "/zz-no-such-page-7", "/robots.txt", "/wp-admin/setup-config.php"}
	for _, staging := range []bool{true, false} {
		env := ""
		if staging {
			env = "staging"
		}
		t.Setenv(runenv.EnvVar, env)
		h, _ := wiredHandler(t, false, tablePrefix)
		for _, p := range paths {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
			got := rec.Header().Get("X-Robots-Tag")
			if staging && got != "noindex" {
				t.Errorf("staging %s (%d): X-Robots-Tag = %q", p, rec.Code, got)
			}
			if !staging && got != "" {
				t.Errorf("production %s: X-Robots-Tag = %q", p, got)
			}
		}
	}
}
