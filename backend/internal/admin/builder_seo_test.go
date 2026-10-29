package admin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

// htmxHdr is what the drawer sends: only the page's main comes back.
var htmxHdr = map[string]string{"HX-Request": "true", "HX-Target": "hm-bs-seo"}

const seoPreviewHTML = `<html><head><title>T</title></head><body>
<header><nav><a href="/">Zzheader</a></nav></header>
<main><h1>Guide</h1><p>Mainwords here and <a href="/other">a link</a>.</p></main>
<footer><p>Zzfooter words</p></footer></body></html>`

// failingEntities fails every write, so a handler that stored anything shows.
type failingEntities struct{ *recordingEntities }

func (f failingEntities) UpdateEntity(ctx context.Context, id uuid.UUID, by *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error) {
	f.inputs = append(f.inputs, *in)
	return nil, errors.New("analyze must not write")
}

type bseoRig struct {
	*reuseRig
	asked []byte
}

func newBSEORig(t *testing.T, html string) *bseoRig {
	r := &bseoRig{reuseRig: newReuseRig(t)}
	r.a.deps.Content.Preview = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.asked, _ = io.ReadAll(req.Body)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	})
	return r
}

func (r *bseoRig) analyze(token string, e *entity.Entity, doc string) *httptest.ResponseRecorder {
	return r.do("POST", r.url(e, "/builder/analyze"), token, url.Values{"doc": {doc}, "seo_title": {"A title"}}, htmxHdr)
}

func TestBuilderAnalyzeReadsMainOfTheDraft(t *testing.T) {
	t.Parallel()

	r := newBSEORig(t, seoPreviewHTML)
	rec := r.analyze("owner-token", r.page, stageDoc)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	has(t, body, "Search result preview", "A title")
	// 6 words and 1 internal link are in <main>; the header and footer add more of both.
	has(t, body, "6 words", "1 links to your own pages")
	if !strings.Contains(string(r.asked), `"h1"`) || strings.Contains(string(r.asked), `"old"`) {
		t.Errorf("preview was not asked for the posted draft: %s", r.asked)
	}
	if strings.Contains(string(r.asked), `"canvas":true`) {
		t.Errorf("analyze must not render the canvas: %s", r.asked)
	}
}

func TestBuilderAnalyzeFallsBackToBody(t *testing.T) {
	t.Parallel()

	got := mainHTML([]byte(`<html><body><p>Only body</p></body></html>`))
	if !strings.Contains(got, "Only body") {
		t.Errorf("fallback = %q", got)
	}
	if got := mainHTML([]byte(`<main><p>a</p></main><main><p>b</p></main>`)); !strings.Contains(got, "a") || strings.Contains(got, "b") {
		t.Errorf("first main only: %q", got)
	}
}

func TestBuilderAnalyzeWritesNothing(t *testing.T) {
	t.Parallel()

	r := newBSEORig(t, seoPreviewHTML)
	f := failingEntities{r.rec}
	r.a.deps.Content.Entities = f
	version := r.page.Version
	if rec := r.analyze("owner-token", r.page, stageDoc); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(r.rec.inputs) != 0 || r.ents.ents[r.page.ID].Version != version {
		t.Errorf("analyze wrote: %d updates", len(r.rec.inputs))
	}
}

func TestBuilderSEORefusals(t *testing.T) {
	t.Parallel()

	r := newBSEORig(t, seoPreviewHTML)
	if rec := r.analyze("owner-token", r.page, `{"v":2,"nodes":[{"id":"x","t":"nope"}]}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("malformed doc: %d", rec.Code)
	}
	if rec := r.analyze("", r.page, stageDoc); rec.Code == http.StatusOK {
		t.Error("analysed without a session")
	}
	if rec := r.do("GET", r.url(r.page, "/builder/seo"), "", nil, nil); rec.Code == http.StatusOK {
		t.Error("fragment served without a session")
	}
	// A viewer is refused exactly as the save route refuses one.
	r.editor.Role = "viewer"
	save := r.postJSON(r.url(r.page, "/builder/save"), "editor-token", saveBody(r.page.Version, saveDoc, "save", ""), nil).Code
	if got := r.analyze("editor-token", r.page, stageDoc).Code; got != save {
		t.Errorf("viewer analyze %d, save %d", got, save)
	}
	if got := r.do("GET", r.url(r.page, "/builder/seo"), "editor-token", nil, nil).Code; got != save {
		t.Errorf("viewer fragment %d, save %d", got, save)
	}
	// Reuse blocks have no SEO.
	if rec := r.analyze("owner-token", r.block, stageDoc); rec.Code != http.StatusNotFound {
		t.Errorf("block analyze: %d", rec.Code)
	}
	if rec := r.do("GET", r.url(r.block, "/builder/seo"), "owner-token", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("block fragment: %d", rec.Code)
	}
	r.a.deps.Content.Preview = nil
	if rec := r.analyze("owner-token", r.page, stageDoc); rec.Code != http.StatusNotFound {
		t.Errorf("no preview: %d", rec.Code)
	}
}

func TestBuilderSEOFragment(t *testing.T) {
	t.Parallel()

	r := newBSEORig(t, seoPreviewHTML)
	r.page.Attributes = []byte(`{"meta_title":"Stored title","page_doc":{"v":2,"nodes":[{"id":"old","t":"container"}]}}`)
	rec := r.do("GET", r.url(r.page, "/builder/seo"), "owner-token", nil, htmxHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	has(t, body, `name="seo_title"`, "Stored title", `role="tab"`, `id="hm-bs-seo"`, "/builder/analyze")
	lacks(t, body, "<html", "<nav", "hm-sidebar")
}
