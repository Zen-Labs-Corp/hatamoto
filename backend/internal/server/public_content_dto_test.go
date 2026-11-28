package server

import (
	"encoding/json/jsontext"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/taxonomy"
)

func TestPublicContentMeta(t *testing.T) {
	for _, c := range []struct {
		total, page, per, pages int
		next                    bool
	}{
		{0, 1, 20, 1, false},
		{41, 1, 2, 21, true},
		{41, 21, 2, 21, false},
		{40, 2, 20, 2, false},
		{5, 9, 20, 1, false},
	} {
		m := newPublicMeta(c.total, c.page, c.per)
		if m.TotalPages != c.pages || m.HasNext != c.next || m.Total != c.total || m.Page != c.page || m.PerPage != c.per {
			t.Errorf("newPublicMeta(%d,%d,%d) = %+v, want pages %d next %v", c.total, c.page, c.per, m, c.pages, c.next)
		}
	}
}

func TestPublicContentExcerpt(t *testing.T) {
	for name, c := range map[string]struct {
		attrs map[string]any
		want  string
	}{
		"none":           {map[string]any{"title": "x"}, ""},
		"excerpt first":  {map[string]any{"excerpt": "E", "summary": "S", "description": "D"}, "E"},
		"blank skipped":  {map[string]any{"excerpt": "  ", "summary": "S"}, "S"},
		"description":    {map[string]any{"description": "D"}, "D"},
		"not a string":   {map[string]any{"excerpt": 5, "summary": "S"}, "S"},
		"stored as is":   {map[string]any{"excerpt": " <p>Hi</p> "}, " <p>Hi</p> "},
		"nil attributes": {nil, ""},
	} {
		if got := contentExcerpt(c.attrs); got != c.want {
			t.Errorf("%s: contentExcerpt = %q, want %q", name, got, c.want)
		}
	}
}

func TestWritePublicJSONCaching(t *testing.T) {
	v := publicOne[string]{Data: "hello"}
	rec := httptest.NewRecorder()
	writePublicJSON(rec, httptest.NewRequest(http.MethodGet, "/x", nil), v)
	etag := rec.Header().Get("ETag")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=60, s-maxage=60" || !strings.HasPrefix(etag, `W/"`) {
		t.Fatalf("200 = %d cc=%q etag=%q", rec.Code, rec.Header().Get("Cache-Control"), etag)
	}
	if rec.Header().Get("Content-Type") != "application/json" || strings.TrimSpace(rec.Body.String()) != `{"data":"hello"}` {
		t.Errorf("body = %q type = %q", rec.Body, rec.Header().Get("Content-Type"))
	}

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	writePublicJSON(rec, req, v)
	if rec.Code != 304 || rec.Body.Len() != 0 || rec.Header().Get("ETag") != etag || rec.Header().Get("Cache-Control") != "public, max-age=60, s-maxage=60" {
		t.Errorf("304 = %d body=%q etag=%q cc=%q", rec.Code, rec.Body, rec.Header().Get("ETag"), rec.Header().Get("Cache-Control"))
	}

	req.Header.Set("If-None-Match", `W/"0000000000000000"`)
	rec = httptest.NewRecorder()
	writePublicJSON(rec, req, v)
	if rec.Code != 200 {
		t.Errorf("stale validator = %d, want 200", rec.Code)
	}
}

func TestContentItemLeaksNothing(t *testing.T) {
	secret := uuid.New()
	now := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	e := &entity.Entity{
		ID: uuid.New(), Name: "Hello", Slug: "hello",
		CreatedBy: &secret, UpdatedBy: &secret, AuthorID: &secret, BylinePersonID: &secret,
		Metadata:    jsontext.Value(`{"no_index":false}`),
		Editorial:   jsontext.Value(`{"note":"internal"}`),
		Attributes:  jsontext.Value(`{"email":"a@example.com","excerpt":"Short","seo":{"title":"t"},"page_doc":{}}`),
		PublishedAt: &now, UpdatedAt: now,
	}
	it := newContentItem(e, "post", "https://example.com/hello/", &render.AuthorView{Name: "Ana", Slug: "ana", URL: "/u", Bio: "private bio"},
		[]taxonomy.EntityTermRef{{Kind: "tag", Slug: "news", Name: "News"}})
	body, err := jsonx.Marshal(it)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, bad := range []string{secret.String(), "a@example.com", "created_by", "updated_by", "author_id", "byline_person_id", "metadata", "editorial", "seo", "page_doc", "attributes", "private bio", "internal"} {
		if strings.Contains(got, bad) {
			t.Errorf("body contains %q: %s", bad, got)
		}
	}
	for _, want := range []string{`"id":"` + e.ID.String() + `"`, `"schema":"post"`, `"excerpt":"Short"`, `"author":{"name":"Ana","slug":"ana"}`, `"terms":[{"kind":"tag","slug":"news","name":"News"}]`, `"url":"https://example.com/hello/"`} {
		if !strings.Contains(got, want) {
			t.Errorf("body lacks %s: %s", want, got)
		}
	}

	bare := newContentItem(&entity.Entity{ID: uuid.New(), Name: "N", Slug: "n", UpdatedAt: now}, "post", "", nil, nil)
	body, _ = jsonx.Marshal(bare)
	if s := string(body); !strings.Contains(s, `"terms":[]`) || strings.Contains(s, `"author"`) || strings.Contains(s, `"url"`) || strings.Contains(s, `"published_at"`) {
		t.Errorf("bare item = %s", s)
	}
}
