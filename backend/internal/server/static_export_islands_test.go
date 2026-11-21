package server

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/spam"
	"hatamoto.dev/core/internal/statichost"
)

// The export renders through the router with the Site host and the export
// mark, so a baked page carries the loader placeholder (no token), the live
// origin meta and root-relative links, and is never redirected.
func TestStaticExportBakesNoCommentToken(t *testing.T) {
	f := newStaticRenderFixture(t, true)
	f.next = okPage("About")
	s := &Server{router: http.HandlerFunc(f.h.handleRenderCatchAll)}
	base, _ := url.Parse(testStaticSite)
	var body bytes.Buffer
	resp, err := (&staticSource{s: s, base: base}).Fetch(context.Background(), "/about", &body)
	if err != nil {
		t.Fatal(err)
	}
	out := body.String()
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no 301)", resp.Status)
	}
	for _, want := range []string{
		`data-hm-live-form="` + testStaticLive + commentFormLivePrefix,
		`<meta name="hm-live-origin" content="` + testStaticLive + `">`,
		`<a href="/about">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("export lacks %q\n%s", want, out)
		}
	}
	p := render.CommentFormPlaceholders
	for _, bad := range []string{"data-hm-cform", "hm_ct", render.CommentFormMarker, p.Token, "__hmcf_tok__"} {
		if strings.Contains(out, bad) {
			t.Errorf("export contains %q", bad)
		}
	}
}

// Same host (no live origin): the export points every island at the Site origin
// itself and never at another origin, and no CORS is added for it.
func TestStaticExportSameHostIslands(t *testing.T) {
	f := newStaticRenderFixture(t, false)
	hs := statichost.StaticHosting{Static: true, SameHost: true, Site: testStaticSite}
	f.h.hosting.Store(hs)
	s := &Server{router: http.HandlerFunc(f.h.handleRenderCatchAll)}
	base, _ := url.Parse(testStaticSite)
	var body bytes.Buffer
	resp, err := (&staticSource{s: s, base: base}).Fetch(context.Background(), "/about", &body)
	if err != nil {
		t.Fatal(err)
	}
	out := body.String()
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no 301)", resp.Status)
	}
	for _, want := range []string{
		`data-hm-live-form="` + testStaticSite + commentFormLivePrefix,
		`<meta name="hm-live-origin" content="` + testStaticSite + `">`,
		`action="` + testStaticSite + `/forms/contact/submit"`,
		`<a href="/about">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("export lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, testStaticLive) {
		t.Errorf("export names the split live origin:\n%s", out)
	}
	if why := f.h.hosting.Load().InactiveReason(); why != "" {
		t.Errorf("same host reports a warning reason: %q", why)
	}

	srv, _ := newSpamServer(t, spam.Defaults(), nil)
	srv.setStaticHosting(hs)
	rec := corsDo(corsMux(srv), http.MethodOptions, "/api/v1/public/forms/contact/submit", testStaticSite, "", "")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("same host added CORS: ACAO = %q", got)
	}
}
