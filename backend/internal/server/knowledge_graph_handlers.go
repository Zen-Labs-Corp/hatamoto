package server

import (
	"net/http"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/seo"
)

// entityIncludesInLLMsTxt reports whether an entity should appear in the
// llms.txt / knowledge-graph feed, based on the SEO tab toggle. Missing or
// unparseable values default to true — matching the admin UI default.
func entityIncludesInLLMsTxt(ent *entity.Entity) bool {
	if ent == nil || len(ent.Attributes) == 0 {
		return true
	}
	var attrs struct {
		SEO struct {
			IncludeInLLMsTxt *bool `json:"include_in_llms_txt"`
		} `json:"seo"`
	}
	if err := jsonx.Unmarshal(ent.Attributes, &attrs, jsonx.StoredLoose); err != nil {
		return true
	}
	if attrs.SEO.IncludeInLLMsTxt == nil {
		return true
	}
	return *attrs.SEO.IncludeInLLMsTxt
}

// llmsEntries keeps the entries listed in llms.txt and the knowledge graph:
// those that opt in and are not noindex, per entry or per type.
func llmsEntries(all []*entity.SitemapEntry, idx *seo.Settings) []*entity.SitemapEntry {
	out := make([]*entity.SitemapEntry, 0, len(all))
	for _, e := range all {
		if e.IncludeInLLMs && !e.NoIndex && !idx.NoIndexType(e.SchemaSlug) {
			out = append(out, e)
		}
	}
	return out
}

// handleKnowledgeGraph returns a JSON-LD @graph array of all published entities for the tenant.
// Supports pagination via ?page=1&per_page=50 and filtering by schema via ?schema=blog.
func (s *Server) handleKnowledgeGraph(w http.ResponseWriter, r *http.Request) {

	// Parse pagination
	pp := httpx.NewPaging(r.URL.Query(), 0)
	page := pp.Page("page", 0)
	perPage := pp.Size("per_page", "limit", 50)
	if pp.Err() != nil {
		writeError(w, http.StatusBadRequest, pp.Err().Error())
		return
	}

	schemaFilter := r.URL.Query().Get("schema")

	publicRepo := entity.NewPostgresPublicRepository(s.db)

	var entities []*entity.Entity
	var total int

	if schemaFilter != "" {
		// Filtered by schema. ListPublished doesn't know about the toggle,
		// so we filter the page in Go. Note: this can leave fewer items than
		// perPage on a given page; total reflects the unfiltered count. We
		// accept that imprecision since the toggle is a rare opt-out.
		result, err := publicRepo.ListPublished(r.Context(), schemaFilter, page, perPage)
		if err != nil {
			s.logger.Error("failed to list published entities for knowledge graph", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to build knowledge graph")
			return
		}
		entities = make([]*entity.Entity, 0, len(result.Items))
		idx := s.seoSettings(r.Context())
		for _, ent := range result.Items {
			if entityIncludesInLLMsTxt(ent) && !entityNoIndex(ent, idx) {
				entities = append(entities, ent)
			}
		}
		total = result.Total
	} else {
		// All schemas — use the sitemap listing (which carries the toggle)
		// and then fetch full entities for the current page.
		allEntries, err := publicRepo.ListAllPublished(r.Context())
		if err != nil {
			s.logger.Error("failed to list all published entities", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to build knowledge graph")
			return
		}
		entries := llmsEntries(allEntries, s.seoSettings(r.Context()))
		total = len(entries)

		// Manual pagination over the entries — fetch entities in the page range
		start := (page - 1) * perPage
		end := start + perPage
		if start >= len(entries) {
			entities = []*entity.Entity{}
		} else {
			if end > len(entries) {
				end = len(entries)
			}
			pageEntries := entries[start:end]
			for _, entry := range pageEntries {
				ent, err := publicRepo.GetPublishedBySlug(r.Context(), entry.Slug)
				if err != nil {
					continue
				}
				entities = append(entities, ent)
			}
		}
	}

	baseURL := s.publicBaseURL(r)

	// Build the @graph array
	ids := make([]uuid.UUID, len(entities))
	for i, ent := range entities {
		ids[i] = ent.ID
	}
	bylines := s.bylinesOf(r.Context(), ids...)
	graph := make([]map[string]interface{}, 0, len(entities))
	for _, ent := range entities {
		jsonLD, err := entity.GenerateJSONLD(ent, baseURL)
		if err != nil {
			continue
		}
		bylines.applyJSONLD(r.Context(), ent.ID, baseURL, jsonLD)
		// Remove @context from individual items (it goes at the top level)
		delete(jsonLD, "@context")
		graph = append(graph, jsonLD)
	}

	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}

	response := map[string]interface{}{
		"@context": "https://schema.org",
		"@graph":   graph,
		"_meta": map[string]interface{}{
			"total":       total,
			"page":        page,
			"per_page":    perPage,
			"total_pages": totalPages,
			"has_next":    page < totalPages,
		},
	}

	w.Header().Set("Content-Type", "application/ld+json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600, s-maxage=3600")
	writeJSON(w, http.StatusOK, response)
}
