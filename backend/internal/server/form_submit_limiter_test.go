package server

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/httpx"
)

// v6Addr returns host n of the /64 2001:db8:<prefix>::/64 as a RemoteAddr.
func v6Addr(prefix string, n int) string {
	return net.JoinHostPort(fmt.Sprintf("2001:db8:%s::%x", prefix, n), "1234")
}

// postFrom sends a POST to path from remote and returns the status.
func postFrom(h http.Handler, path, remote string) int {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	req.RemoteAddr = remote
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestFormSubmitLimiterByIPv6Prefix(t *testing.T) {
	t.Parallel()
	routes := map[string]func() http.Handler{
		"api route": func() http.Handler {
			mux := http.NewServeMux()
			r := httpx.NewGroup(mux, "").Group("", func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					defer func() { // formService is nil here; swallow the handler panic
						if recover() != nil {
							w.WriteHeader(http.StatusInternalServerError)
						}
					}()
					next.ServeHTTP(w, req)
				})
			})
			newThrottleTestServer(nil).mountCorePublicCMSRoutes(r)
			return mux
		},
		"site route": func() http.Handler {
			r, h := baseRouter()
			mountRenderRoutes(r, renderRoutes{
				pages: newRenderFixture(t).h, assets: stub("assets"), robots: stub("robots"), sitemap: stub("sitemap"),
				subSitemap: stub("sub"), llms: stub("llms"), xsl: stub("xsl"), formPost: stub("form"),
			})
			return h
		},
	}
	for name, build := range routes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := build()
			for i := 1; i <= 5; i++ {
				if code := postFrom(h, "/forms/contact/submit", v6Addr("1:2", i)); code == http.StatusTooManyRequests {
					t.Fatalf("send %d from the /64 was limited", i)
				}
			}
			if code := postFrom(h, "/forms/contact/submit", v6Addr("1:2", 6)); code != http.StatusTooManyRequests {
				t.Fatalf("sixth send in the /64 = %d, want 429", code)
			}
			if code := postFrom(h, "/forms/contact/submit", v6Addr("1:3", 1)); code == http.StatusTooManyRequests {
				t.Fatalf("another /64 was limited")
			}
		})
	}
}

func TestFormRateCheckIPv6Bucket(t *testing.T) {
	t.Parallel()
	srv, repo := settingsServer(t, form.FormSettings{RateLimitPerHour: 2})
	send := func(remote string) {
		req := httptest.NewRequest(http.MethodPost, "/forms/contact/submit", strings.NewReader(`{"name":"Ada"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remote
		req.SetPathValue("slug", "contact")
		srv.handlePublicFormSubmit(httptest.NewRecorder(), req)
	}
	for i := 1; i <= 3; i++ {
		send(v6Addr("1:2", i))
	}
	send(v6Addr("1:3", 1)) // another /64 is unaffected
	send("203.0.113.7:4000")
	var got []string
	for _, sub := range repo.subs {
		got = append(got, sub.Status)
	}
	want := []string{"new", "new", "spam", "new", "new"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("statuses = %v, want %v", got, want)
	}
}
