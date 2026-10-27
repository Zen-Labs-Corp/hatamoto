package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
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
	if err := json.Unmarshal(ent.Attributes, &attrs); err != nil {
		return true
	}
	if attrs.SEO.IncludeInLLMsTxt == nil {
		return true
	}
	return *attrs.SEO.IncludeInLLMsTxt
}

// handleKnowledgeGraph returns a JSON-LD @graph array of all published entities for the tenant.
// Supports pagination via ?page=1&per_page=50 and filtering by schema via ?schema=blog.
func (s *Server) handleKnowledgeGraph(w http.ResponseWriter, r *http.Request) {

	// Parse pagination
	page := 1
	perPage := 50
	if p := r.URL.Query().Get("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}
	if pp := r.URL.Query().Get("per_page"); pp != "" {
		if parsed, err := strconv.Atoi(pp); err == nil && parsed > 0 && parsed <= 100 {
			perPage = parsed
		}
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
		for _, ent := range result.Items {
			if entityIncludesInLLMsTxt(ent) {
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
		entries := make([]*entity.SitemapEntry, 0, len(allEntries))
		for _, entry := range allEntries {
			if entry.IncludeInLLMs {
				entries = append(entries, entry)
			}
		}
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

	if entities == nil {
		entities = []*entity.Entity{}
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

// handleLLMsTxt returns an llmstxt.org-style manifest describing the
// knowledge base endpoints. See https://llmstxt.org for the format.
func (s *Server) handleLLMsTxt(w http.ResponseWriter, r *http.Request) {

	publicRepo := entity.NewPostgresPublicRepository(s.db)
	allEntries, err := publicRepo.ListAllPublished(r.Context())
	if err != nil {
		s.logger.Error("failed to list published entities for llms.txt", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to build llms.txt")
		return
	}

	// Filter out entities the editor opted out of via the
	// SEO tab's "Include in /llms.txt" toggle. Default is true (see SQL).
	entries := make([]*entity.SitemapEntry, 0, len(allEntries))
	for _, entry := range allEntries {
		if entry.IncludeInLLMs {
			entries = append(entries, entry)
		}
	}

	lastUpdated := time.Now().UTC().Format("2006-01-02")
	if len(entries) > 0 && len(entries[0].UpdatedAt) >= 10 {
		// ListAllPublished sorts by updated_at DESC, so the first entry is most recent.
		lastUpdated = entries[0].UpdatedAt[:10]
	}

	// Count entities per schema for the manifest.
	counts := make(map[string]int)
	var schemas []string
	for _, entry := range entries {
		if _, seen := counts[entry.SchemaSlug]; !seen {
			schemas = append(schemas, entry.SchemaSlug)
		}
		counts[entry.SchemaSlug]++
	}

	// Path B PR 2b: site display name comes from hatamoto_site_config
	// via siteconfig.Store. Empty CompanyName falls back to the legacy
	// "Knowledge Base" placeholder so the llms.txt header always renders.
	siteName := "Knowledge Base"
	if s.siteConfig != nil {
		if cfg, cerr := s.siteConfig.Get(r.Context()); cerr == nil {
			if cfg.CompanyName != "" {
				siteName = cfg.CompanyName
			}
		} else {
			s.logger.Warn("failed to load site config for llms.txt", "error", cerr)
		}
	}

	body := buildLLMsTxt(siteName, s.publicBaseURL(r), lastUpdated, len(entries), schemas, counts)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600, s-maxage=3600")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(body))
}

// buildLLMsTxt renders the llms.txt body. schemas lists the content types in
// first-seen order; counts holds the entities per schema.
func buildLLMsTxt(siteName, baseURL, lastUpdated string, total int, schemas []string, counts map[string]int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n\n", siteName)
	fmt.Fprintf(&sb, "> Machine-readable index of all published content for LLM and AI crawler consumption. Last updated %s. %d total published entities across %d content types.\n\n",
		lastUpdated, total, len(schemas))

	sb.WriteString("## Knowledge Graph\n\n")
	fmt.Fprintf(&sb, "- [All entities (JSON-LD @graph)](%s/api/v1/public/knowledge-graph): paginated `application/ld+json` feed of every published entity. Use `?page=N&per_page=50` (max 100) to walk the full set.\n",
		baseURL)
	fmt.Fprintf(&sb, "- [Sitemap (XML)](%s/sitemap.xml): canonical URL index.\n", baseURL)
	if counts[feedPostSchema] > 0 {
		fmt.Fprintf(&sb, "- [Posts feed (RSS)](%s/feed.xml): the newest posts with full text. An Atom version is at %s/atom.xml.\n", baseURL, baseURL)
	}
	sb.WriteString("\n")

	if len(schemas) > 0 {
		sb.WriteString("## Content Types\n\n")
		for _, schema := range schemas {
			humanName := titleCase(strings.ReplaceAll(schema, "-", " "))
			fmt.Fprintf(&sb, "- [%s](%s/api/v1/public/knowledge-graph?schema=%s): %d entities as JSON-LD.\n",
				humanName, baseURL, schema, counts[schema])
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Per-Page Structured Data\n\n")
	sb.WriteString("Every HTML page on this site embeds Schema.org JSON-LD in a `<script type=\"application/ld+json\">` block in the document head. Parse the HTML or request the page with `Accept: application/ld+json` for the structured payload only.\n")
	return sb.String()
}

// titleCase upper-cases the first letter of each word and leaves the other
// letters as they are. It reproduces strings.Title exactly, including its
// word boundaries: a letter that follows a digit or underscore does not start a
// word ("3d-models" gives "3d-Models").
func titleCase(s string) string {
	prev := ' '
	return strings.Map(func(r rune) rune {
		start := isWordBoundary(prev)
		prev = r
		if start {
			return unicode.ToTitle(r)
		}
		return r
	}, s)
}

// isWordBoundary is the separator test strings.Title uses.
func isWordBoundary(r rune) bool {
	if r <= 0x7F {
		switch {
		case '0' <= r && r <= '9', 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', r == '_':
			return false
		}
		return true
	}
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return false
	}
	return unicode.IsSpace(r)
}
