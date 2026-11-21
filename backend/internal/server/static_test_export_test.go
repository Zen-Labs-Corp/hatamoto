package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/statichost"
)

func TestStaticPageHonoursContextOverride(t *testing.T) {
	f := newStaticRenderFixture(t, false)
	before := f.h.hosting.Load()
	hs := statichost.StaticHosting{Static: true, Live: "https://live.example.com/", Site: "https://www.example.com"}

	r := httptest.NewRequest(http.MethodGet, "/about", nil)
	if _, ok := f.h.staticPage(r, []byte(staticRenderDoc)); ok {
		t.Fatal("served mode rewrote without an override")
	}
	r = r.WithContext(statichost.WithHosting(context.Background(), hs))
	out, ok := f.h.staticPage(r, []byte(staticRenderDoc))
	if !ok {
		t.Fatal("override not applied")
	}
	if !strings.Contains(string(out), "https://live.example.com/forms/contact/submit") {
		t.Errorf("islands not pointed at the live origin: %s", out)
	}
	if after := f.h.hosting.Load(); after != before {
		t.Errorf("process snapshot changed: %+v -> %+v", before, after)
	}
}

func TestStaticPageSameHostOverride(t *testing.T) {
	f := newStaticRenderFixture(t, false)
	before := f.h.hosting.Load()
	const site = "https://www.example.com"
	hs := statichost.StaticHosting{Static: true, SameHost: true, Site: site}

	r := httptest.NewRequest(http.MethodGet, "/about", nil)
	r = r.WithContext(statichost.WithHosting(context.Background(), hs))
	out, ok := f.h.staticPage(r, []byte(staticRenderDoc))
	if !ok {
		t.Fatal("same-host override not applied")
	}
	for _, want := range []string{
		site + "/forms/contact/submit",
		`<meta name="hm-live-origin" content="` + site + `">`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	if after := f.h.hosting.Load(); after != before {
		t.Errorf("process snapshot changed: %+v -> %+v", before, after)
	}
}
