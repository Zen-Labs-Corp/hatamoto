package server

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/render"
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
