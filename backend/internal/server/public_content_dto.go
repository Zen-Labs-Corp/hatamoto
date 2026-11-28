package server

import (
	"net/http"
	"strings"
	"time"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/taxonomy"
)

// The public content API answers with these types only. They are deliberately
// narrow: an entity is never marshalled as stored, so no user id, email,
// metadata, editorial data, SEO document, page layout or free attribute can
// reach the response.

type contentAuthor struct {
	Name string `json:"name"`
	Slug string `json:"slug,omitempty"`
}

type contentTerm struct {
	Kind string `json:"kind"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type contentItem struct {
	ID          string         `json:"id"`
	Schema      string         `json:"schema"`
	Slug        string         `json:"slug"`
	URL         string         `json:"url,omitempty"`
	Title       string         `json:"title"`
	Excerpt     string         `json:"excerpt,omitempty"`
	PublishedAt *time.Time     `json:"published_at,omitempty"`
	UpdatedAt   time.Time      `json:"updated_at"`
	Author      *contentAuthor `json:"author,omitempty"`
	Terms       []contentTerm  `json:"terms"`
}

type publicTermItem struct {
	Kind        string `json:"kind"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Count       int    `json:"count"`
}

type publicMeta struct {
	Total      int  `json:"total"`
	Page       int  `json:"page"`
	PerPage    int  `json:"per_page"`
	TotalPages int  `json:"total_pages"`
	HasNext    bool `json:"has_next"`
}

type publicList[T any] struct {
	Data []T        `json:"data"`
	Meta publicMeta `json:"meta"`
}

type publicOne[T any] struct {
	Data T `json:"data"`
}

// publicContentCacheControl is the cache policy of every API answer.
const publicContentCacheControl = "public, max-age=60, s-maxage=60"

// newPublicMeta describes one page of total items; total_pages is at least 1.
func newPublicMeta(total, page, perPage int) publicMeta {
	pages := 1
	if perPage > 0 && total > 0 {
		pages = (total + perPage - 1) / perPage
	}
	return publicMeta{Total: total, Page: page, PerPage: perPage, TotalPages: pages, HasNext: page < pages}
}

// contentExcerpt is the first non-empty string of attributes excerpt, summary
// or description, as stored.
func contentExcerpt(attrs map[string]any) string {
	for _, k := range []string{"excerpt", "summary", "description"} {
		if s, ok := attrs[k].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// newContentItem builds the public view of e. schema is the canonical schema
// slug and absURL the absolute address ("" when the schema has no public
// route). Of the author only the name and, for a public person, the slug show.
func newContentItem(e *entity.Entity, schema, absURL string, a *render.AuthorView, terms []taxonomy.EntityTermRef) contentItem {
	it := contentItem{
		ID:        e.ID.String(),
		Schema:    schema,
		Slug:      e.Slug,
		URL:       absURL,
		Title:     e.Name,
		Excerpt:   contentExcerpt(attrsForView(e.Attributes)),
		UpdatedAt: e.UpdatedAt.UTC(),
		Terms:     make([]contentTerm, 0, len(terms)),
	}
	if e.PublishedAt != nil {
		t := e.PublishedAt.UTC()
		it.PublishedAt = &t
	}
	if a != nil && a.Name != "" {
		it.Author = &contentAuthor{Name: a.Name, Slug: a.Slug}
	}
	for _, t := range terms {
		it.Terms = append(it.Terms, contentTerm{Kind: t.Kind, Slug: t.Slug, Name: t.Name})
	}
	return it
}

// writePublicJSON sends v with a one-minute cache policy and a weak body
// validator, or a bodiless 304 when If-None-Match names it. The validator is
// weak because response compression may re-encode the body.
func writePublicJSON(w http.ResponseWriter, r *http.Request, v any) {
	body, err := jsonx.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	fp := fingerprintSum(body)
	h := w.Header()
	h.Set("Cache-Control", publicContentCacheControl)
	h.Set("ETag", pageETag(fp))
	if ifNoneMatchHits(r, bareETag(fp)) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
