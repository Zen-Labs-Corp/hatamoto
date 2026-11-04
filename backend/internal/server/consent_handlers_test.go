package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"hatamoto.dev/core/internal/consent"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/render"
)

type consentRow struct{ raw []byte }

func (r consentRow) Scan(dest ...any) error {
	*(dest[0].(*[]byte)) = r.raw
	return nil
}

// consentQuerier serves one settings document and records counter writes.
type consentQuerier struct {
	raw  string
	incs [][]any
}

func (q *consentQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	return consentRow{raw: []byte(q.raw)}
}
func (q *consentQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, io.EOF
}
func (q *consentQuerier) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	q.incs = append(q.incs, args)
	return pgconn.CommandTag{}, nil
}

func consentServer(raw string) (*Server, *consentQuerier, *http.ServeMux) {
	q := &consentQuerier{raw: raw}
	s := &Server{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		consentStore:   consent.NewStore(q),
		consentCounter: consent.NewCounter(q),
	}
	mux := http.NewServeMux()
	s.mountCorePublicCMSRoutes(httpx.NewGroup(mux, ""))
	return s, q, mux
}

func regionShow(t *testing.T, mux *http.ServeMux, hdr, val string) bool {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/consent/region", nil)
	if hdr != "" {
		req.Header.Set(hdr, val)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d, cache-control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	switch strings.TrimSpace(rec.Body.String()) {
	case `{"show":true}`:
		return true
	case `{"show":false}`:
		return false
	}
	t.Fatalf("body %q", rec.Body.String())
	return false
}

func TestConsentRegion(t *testing.T) {
	t.Parallel()
	_, _, eu := consentServer(`{"enabled":true,"region":"eu_uk"}`)
	cases := []struct {
		hdr, val string
		want     bool
	}{
		{"CF-IPCountry", "DE", true},
		{"CF-IPCountry", "GB", true},
		{"CF-IPCountry", "US", false},
		{"CloudFront-Viewer-Country", "FR", true},
		{"CloudFront-Viewer-Country", "US", false},
		{"", "", true},
	}
	for _, c := range cases {
		if got := regionShow(t, eu, c.hdr, c.val); got != c.want {
			t.Errorf("eu_uk %s=%s: show=%v, want %v", c.hdr, c.val, got, c.want)
		}
	}
	_, _, all := consentServer(`{"enabled":true,"region":"all"}`)
	if !regionShow(t, all, "CF-IPCountry", "US") {
		t.Error("mode all must show everywhere")
	}
}

func TestConsentLogCountsChoice(t *testing.T) {
	t.Parallel()
	_, q, mux := consentServer(`{}`)
	post := func(body, ua string) int {
		req := httptest.NewRequest(http.MethodPost, "/consent", strings.NewReader(body))
		req.Header.Set("User-Agent", ua)
		req.RemoteAddr = "203.0.113.20:1000"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	const browser = "Mozilla/5.0 (X11; Linux x86_64) Chrome/120.0 Safari/537.36"
	if c := post(`{"choice":"none"}`, browser); c != http.StatusNoContent {
		t.Fatalf("valid choice: %d", c)
	}
	if len(q.incs) != 1 || q.incs[0][1] != "none" {
		t.Fatalf("writes = %v", q.incs)
	}
	for _, bad := range []string{`{"choice":"yes"}`, `{}`, `nope`, `{"choice":"all","pad":"` + strings.Repeat("x", 300) + `"}`} {
		if c := post(bad, browser); c != http.StatusBadRequest {
			t.Errorf("%.30q: status %d, want 400", bad, c)
		}
	}
	if c := post(`{"choice":"all"}`, "Googlebot/2.1"); c != http.StatusNoContent || len(q.incs) != 1 {
		t.Errorf("bot counted: status %d, writes %d", c, len(q.incs))
	}
}

func TestConsentLogIsRateLimited(t *testing.T) {
	t.Parallel()
	_, _, mux := consentServer(`{}`)
	var last int
	for i := 0; i < 12; i++ {
		req := httptest.NewRequest(http.MethodPost, "/consent", strings.NewReader(`{"choice":"all"}`))
		req.Header.Set("User-Agent", "Mozilla/5.0 Chrome/120.0")
		req.RemoteAddr = "203.0.113.30:1000"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		last = rec.Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("12th request: %d, want 429", last)
	}
}

func TestConsentRenderHook(t *testing.T) {
	s, _, _ := consentServer(`{"enabled":true,"region":"eu_uk","categories":["analytics"],"title":"T","text":"X"}`)
	s.installConsentRender()
	t.Cleanup(func() { render.SetConsent(nil) })
	e, err := render.New(render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	e.AssetHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, render.AssetPrefix+render.ConsentScriptName, nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `"categories":["analytics"]`) || !strings.Contains(body, `"accept":"Accept all"`) {
		t.Errorf("status %d body %.200s", rec.Code, body)
	}

	off, _, _ := consentServer(`{"enabled":false}`)
	off.installConsentRender()
	rec = httptest.NewRecorder()
	e.AssetHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, render.AssetPrefix+render.ConsentScriptName, nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("disabled: status %d, want 404", rec.Code)
	}
}
