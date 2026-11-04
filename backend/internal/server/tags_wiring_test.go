package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/consent"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/tags"
)

// docQuerier answers a settings read per table; fail makes every read error.
type docQuerier struct {
	tracking, privacy string
	fail              bool
}

func (q docQuerier) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if q.fail {
		return errRow{}
	}
	if strings.Contains(sql, "hatamoto_tracking_settings") {
		return consentRow{raw: []byte(q.tracking)}
	}
	return consentRow{raw: []byte(q.privacy)}
}
func (docQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, io.EOF }
func (docQuerier) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func tagsServer(q docQuerier) *Server {
	return &Server{
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		consentStore: consent.NewStore(q),
		tagsStore:    tags.NewStore(q),
	}
}

func tagsPage(t *testing.T, q docQuerier) (page, csp string) {
	t.Helper()
	s := tagsServer(q)
	s.installTagsRender()
	t.Cleanup(func() { render.SetTags(nil) })
	f := newRenderFixture(t)
	f.next = okPage("About")
	f.h.cspExtras = s.cspExtras
	w := f.get("/about")
	return w.Body.String(), w.Header().Get("Content-Security-Policy-Report-Only")
}

// The fixture page carries a fresh entity id per render.
var uuidRE = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

const (
	ga4Only  = `{"vendors":{"ga4":{"enabled":true,"id":"G-ABC123XYZ9"}}}`
	gtmOnly  = `{"vendors":{"gtm":{"enabled":true,"id":"GTM-ABC123"}}}`
	consentO = `{"enabled":true,"region":"all","categories":["analytics","marketing","functional"]}`
)

func TestTagsGA4PageAndCSPNameOnlyGA4Hosts(t *testing.T) {
	page, csp := tagsPage(t, docQuerier{tracking: ga4Only, privacy: `{}`})
	if !strings.Contains(page, "/theme/assets/hm-tags.js?v=") {
		t.Fatalf("no tag script in page")
	}
	for _, want := range []string{"https://www.googletagmanager.com", "https://*.google-analytics.com", "https://*.analytics.google.com"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %s: %s", want, csp)
		}
	}
	for _, not := range []string{"facebook", "clarity", "tiktok", "hubspot", "crisp", "tawk"} {
		if strings.Contains(csp, not) {
			t.Errorf("CSP names a vendor that is off (%s): %s", not, csp)
		}
	}
	if !strings.Contains(csp, "script-src 'self' https://www.googletagmanager.com") {
		t.Errorf("strict script-src not extended in place: %s", csp)
	}
}

func TestTagsNoneEnabledPageAndCSPEqualBaseline(t *testing.T) {
	basePage, baseCSP := tagsPage(t, docQuerier{tracking: `{}`, privacy: `{}`})
	if strings.Contains(basePage, "hm-tags") || baseCSP != renderCSPStrict {
		t.Fatalf("baseline carries tag output (csp %q)", baseCSP)
	}
	page, csp := tagsPage(t, docQuerier{tracking: `{"vendors":{"ga4":{"enabled":false,"id":"G-ABC123XYZ9"}}}`, privacy: `{}`})
	if uuidRE.ReplaceAllString(page, "ID") != uuidRE.ReplaceAllString(basePage, "ID") || csp != baseCSP {
		t.Errorf("a vendor that is off changed the page (%v) or the CSP (%v)", page != basePage, csp != baseCSP)
	}
}

func TestTagsNoscriptFollowsConsent(t *testing.T) {
	cases := map[string]struct {
		privacy string
		want    bool
	}{
		"banner off":                     {`{}`, true},
		"banner on, marketing asked":     {consentO, false},
		"banner on, marketing not asked": {`{"enabled":true,"categories":["analytics"]}`, true},
	}
	for name, c := range cases {
		page, _ := tagsPage(t, docQuerier{tracking: gtmOnly, privacy: c.privacy})
		got := strings.Contains(page, "<noscript><iframe src=\"https://www.googletagmanager.com/ns.html?id=GTM-ABC123\"")
		if got != c.want {
			t.Errorf("%s: noscript = %v, want %v", name, got, c.want)
		}
		if !strings.Contains(page, "hm-tags.js") {
			t.Errorf("%s: no script tag", name)
		}
	}
}

func TestTagsUnreadableStoreEmitsNothing(t *testing.T) {
	page, csp := tagsPage(t, docQuerier{fail: true})
	if strings.Contains(page, "hm-tags") || csp != renderCSPStrict {
		t.Errorf("unreadable store emitted tag output (csp %q)", csp)
	}
}

func TestTagsRenderHookServesScriptAndNilStoreIsOff(t *testing.T) {
	s := tagsServer(docQuerier{tracking: ga4Only, privacy: `{}`})
	s.installTagsRender()
	t.Cleanup(func() { render.SetTags(nil) })
	e, err := render.New(render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	serve := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		e.AssetHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, render.AssetPrefix+render.TagsScriptName, nil))
		return rec
	}
	if rec := serve(); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"src":"https://www.googletagmanager.com/gtag/js?id=G-ABC123XYZ9"`) ||
		!strings.Contains(rec.Body.String(), `"member":"hm_member"`) {
		t.Errorf("status %d body %.300s", rec.Code, rec.Body.String())
	}
	(&Server{logger: s.logger}).installTagsRender()
	if rec := serve(); rec.Code != http.StatusNotFound {
		t.Errorf("nil store: status %d, want 404", rec.Code)
	}
}

func TestTagsHashChangesWithSettings(t *testing.T) {
	tagOf := func(raw string) string {
		page, _ := tagsPage(t, docQuerier{tracking: raw, privacy: `{}`})
		i := strings.Index(page, "hm-tags.js?v=")
		return page[i : i+len("hm-tags.js?v=")+12]
	}
	if tagOf(ga4Only) == tagOf(gtmOnly) || tagOf(ga4Only) != tagOf(ga4Only) {
		t.Error("script hash must follow the settings")
	}
}
