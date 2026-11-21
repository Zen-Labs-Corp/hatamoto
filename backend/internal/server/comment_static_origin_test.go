package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"hatamoto.dev/core/internal/statichost"
)

func TestCommentCrossSiteStaticOrigin(t *testing.T) {
	t.Parallel()
	static := statichost.StaticHosting{Static: true, Live: testStaticLive, Site: testStaticSite}
	for _, c := range []struct {
		name   string
		hs     statichost.StaticHosting
		origin string
		sfs    string
		want   bool
	}{
		{"served, S, cross-site", statichost.StaticHosting{}, testStaticSite, "cross-site", true},
		{"static, S, cross-site", static, testStaticSite, "cross-site", false},
		{"static, S, same-site", static, testStaticSite, "same-site", false},
		{"static, S, no fetch metadata", static, testStaticSite, "", false},
		{"static, S, same-origin", static, testStaticSite, "same-origin", true},
		{"static, evil, cross-site", static, "https://evil.example", "cross-site", true},
		{"static, null, cross-site", static, "null", "cross-site", true},
		{"static, no origin, same-origin", static, "", "same-origin", false},
	} {
		r := httptest.NewRequest(http.MethodPost, "http://"+staticLiveHost+"/-/comments/x", nil)
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if c.sfs != "" {
			r.Header.Set("Sec-Fetch-Site", c.sfs)
		}
		if got := commentCrossSite(r, c.hs); got != c.want {
			t.Errorf("%s: commentCrossSite = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCommentPostFromStaticSiteIsStored(t *testing.T) {
	r := newPostRig(t)
	r.srv.setStaticHosting(statichost.StaticHosting{Static: true, Live: testStaticLive, Site: testStaticSite})
	fromSite := []func(*http.Request){header("Origin", testStaticSite), header("Sec-Fetch-Site", "cross-site")}
	rec := r.do(r.form(time.Minute), fromSite...)
	if rec.Code != http.StatusSeeOther || len(r.c.stored) != 1 {
		t.Fatalf("static: status %d stored %d", rec.Code, len(r.c.stored))
	}

	// Served mode still drops it as a cross-site post, with nothing stored.
	r = newPostRig(t)
	rec = r.do(r.form(time.Minute), fromSite...)
	if len(r.c.stored) != 0 {
		t.Fatalf("served: stored %d, status %d", len(r.c.stored), rec.Code)
	}
}

func TestCommentEditFromStaticSiteIsRefused(t *testing.T) {
	r := newEditRig()
	r.srv.setStaticHosting(statichost.StaticHosting{Static: true, Live: testStaticLive, Site: testStaticSite})
	w := r.do(false, editForm("x"), header("Origin", testStaticSite), header("Sec-Fetch-Site", "cross-site"))
	if w.Code != http.StatusForbidden || r.ed.edited != nil {
		t.Errorf("edit from S: %d", w.Code)
	}
	w = r.do(true, nil, header("Origin", testStaticSite), header("Sec-Fetch-Site", "same-site"))
	if w.Code != http.StatusForbidden || r.ed.trashed {
		t.Errorf("delete from S: %d", w.Code)
	}
}
