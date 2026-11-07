package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/render"
)

// formPageRig mounts the hosted form route over the render fixture, with the
// test form (slug "t") behind the form service.
func formPageRig(t *testing.T, status string) (*renderFixture, http.Handler) {
	t.Helper()
	srv, repo := newTypesServer(t, &form.Form{Name: "Contact us", Fields: []form.FormField{
		{ID: "n", Type: form.FieldTypeText, Name: "name", Label: "Your name"},
	}})
	repo.forms["t"].Status = status
	f := newRenderFixture(t)
	f.next = func() (*routeResult, pageOutcome, error) {
		return &routeResult{NotFound: true}, pageOutcome{Status: http.StatusNotFound, NotFound: true}, nil
	}
	base := f.h.buildView
	f.h.buildView = func(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
		v, err := base(ctx, r, rr)
		if v != nil {
			v.Submitted = r.URL.Query().Get("submitted") == "1"
		}
		return v, err
	}
	srv.renderHandlers = f.h
	r, h := baseRouter()
	mountRenderRoutes(r, renderRoutes{
		pages: f.h, assets: stub("assets"), robots: stub("robots"), sitemap: stub("sitemap"),
		subSitemap: stub("sub"), llms: stub("llms"), xsl: stub("xsl"), formPost: stub("form"),
		formPage: srv.handleFormPage,
	})
	return f, h
}

func TestFormPageActive(t *testing.T) {
	_, h := formPageRig(t, form.StatusActive)
	w := getPath(h, "/f/t")
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, "Your name") || !strings.Contains(body, `action="/forms/t/submit"`) {
		t.Fatalf("status %d, body: %s", w.Code, body)
	}
	if got := w.Header().Get("X-Robots-Tag"); got != "noindex" {
		t.Errorf("X-Robots-Tag = %q", got)
	}
	if !strings.Contains(body, `name="robots" content="noindex`) {
		t.Errorf("no robots meta in head: %s", body)
	}
	if !strings.Contains(body, `value="/f/t"`) {
		t.Errorf("form does not post back to /f/t")
	}
}

func TestFormPageSubmittedNote(t *testing.T) {
	_, h := formPageRig(t, form.StatusActive)
	plain := getPath(h, "/f/t").Body.String()
	done := getPath(h, "/f/t?submitted=1").Body.String()
	if plain == done {
		t.Error("?submitted=1 changes nothing on the page")
	}
}

func TestFormPageNotServedWhenNotActive(t *testing.T) {
	for _, status := range []string{form.StatusInactive, form.StatusArchived} {
		_, h := formPageRig(t, status)
		w := getPath(h, "/f/t")
		if w.Code != http.StatusNotFound || !isHTML(w) || strings.Contains(w.Body.String(), "Your name") {
			t.Errorf("%s: status %d html %v", status, w.Code, isHTML(w))
		}
	}
	_, h := formPageRig(t, form.StatusActive)
	if w := getPath(h, "/f/nope"); w.Code != http.StatusNotFound || !isHTML(w) {
		t.Errorf("unknown slug: status %d", w.Code)
	}
}

func TestFormPageYieldsToEntity(t *testing.T) {
	f, h := formPageRig(t, form.StatusActive)
	f.next = okPage("Entity at f")
	w := getPath(h, "/f/t")
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, "Entity at f") || strings.Contains(body, "Your name") {
		t.Fatalf("entity under /f/ was shadowed: %d %s", w.Code, body)
	}
}
