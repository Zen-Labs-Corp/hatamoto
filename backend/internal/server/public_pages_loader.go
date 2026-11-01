package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/redirect"
)

// pageOutcome carries what the public page handlers used to write straight
// to the response (status, redirect, cache posture) so the same data path can
// feed both the JSON API and the Go renderer.
type pageOutcome struct {
	Status int
	// Redirect is set when a redirect rule matched before any entity lookup.
	Redirect *redirect.Redirect
	// NotFound is set when no entity matched (homepage: no content at all).
	NotFound bool
	// LDJSON is set instead of a page response when the caller asked for
	// Accept: application/ld+json.
	LDJSON map[string]interface{}
}

// pageError is a loader failure that maps to a specific JSON error response.
type pageError struct {
	status int
	msg    string
}

func (e *pageError) Error() string { return e.msg }

// loadPublicPage assembles the published entity page for slug. The request is
// consulted for scope (context), query flags, Accept, base URL and lead/bot
// metadata; nothing is written to a response.
func (s *Server) loadPublicPage(r *http.Request, slug string) (*PublicPageResponse, pageOutcome, error) {
	ctx := r.Context()

	// Check for redirect before entity lookup
	// Skip redirect check if skip_redirects=true query param is present
	skipRedirects := r.URL.Query().Get("skip_redirects") == "true"
	scope, scoped := publicScopeFrom(ctx)
	if scoped && scope.SkipRedirects {
		skipRedirects = true
	}
	if !skipRedirects {
		if rd := s.matchRedirect(ctx, "/"+slug); rd != nil {
			return nil, pageOutcome{Status: http.StatusOK, Redirect: rd}, nil
		}
	}

	publicRepo := entity.NewPostgresPublicRepository(s.db)
	var ent *entity.Entity
	var err error
	if scoped && scope.EntityID != uuid.Nil {
		ent, err = publicRepo.GetPublishedByID(ctx, scope.EntityID)
	} else if scoped {
		ent, err = publicRepo.GetPublishedBySlugAndSchemas(ctx, slug, scope.SchemaSlugs)
	} else {
		ent, err = publicRepo.GetPublishedBySlug(ctx, slug)
	}
	if err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			if s.redirectService != nil {
				s.redirectService.Log404Async("/"+slug, r.Referer(), r.UserAgent())
			}
			return nil, pageOutcome{Status: http.StatusNotFound, NotFound: true}, nil
		}
		s.logger.Error("failed to get page", "error", err)
		return nil, pageOutcome{Status: http.StatusInternalServerError},
			&pageError{http.StatusInternalServerError, "failed to get page"}
	}

	applyPreviewAttributes(ctx, ent.ID, &ent.Attributes)
	ent.Attributes = entity.DerivedAttributes(ent.Attributes)
	s.applyAuthorName(ctx, ent)

	// Determine base URL
	baseURL := s.publicBaseURL(r)

	// FAQ blocks written in the blog editor (div.faq-item) become a FAQPage
	// next to the entity's own JSON-LD.
	faqItems := pageFAQItems(ent)

	// Canonical comes from the schema URL pattern.
	pageRepo := entity.NewPostgresPublicRepository(s.db)
	loc := locateEntity(ctx, s.urlSnapshot(ctx), pageRepo, ent)

	outcome := pageOutcome{Status: http.StatusOK}

	// Content negotiation: if Accept: application/ld+json, return pure JSON-LD
	acceptHeader := r.Header.Get("Accept")
	if strings.Contains(acceptHeader, "application/ld+json") {
		jsonLD, err := entity.GenerateJSONLDWithFAQ(ent, baseURL, faqItems)
		if err != nil {
			return nil, pageOutcome{Status: http.StatusInternalServerError},
				&pageError{http.StatusInternalServerError, "failed to generate JSON-LD"}
		}
		entity.ApplyCanonicalToJSONLD(jsonLD, ent.Schema.Slug, baseURL+loc.Path)
		s.bylinesOf(ctx, ent.ID).applyJSONLD(ctx, ent.ID, baseURL, jsonLD)
		outcome.LDJSON = jsonLD
		return nil, outcome, nil
	}

	// Generate JSON-LD with FAQ enrichment
	jsonLD, _ := entity.GenerateJSONLDWithFAQ(ent, baseURL, faqItems)
	if ent.Schema != nil {
		entity.ApplyCanonicalToJSONLD(jsonLD, ent.Schema.Slug, baseURL+loc.Path)
	}
	s.bylinesOf(ctx, ent.ID).applyJSONLD(ctx, ent.ID, baseURL, jsonLD)

	// Parse attributes for meta
	var attrs map[string]interface{}
	if err := json.Unmarshal(ent.Attributes, &attrs); err != nil {
		attrs = make(map[string]interface{})
	}

	// Build meta
	meta := PageMeta{
		CanonicalURL: baseURL + loc.Path,
	}
	if title, ok := attrs["meta_title"].(string); ok && title != "" {
		meta.Title = title
	} else {
		meta.Title = ent.Name
	}
	meta.Description, meta.OGImage = metaDescriptionAndImage(attrs, ent.Schema)
	if attrsNoIndex(ent.Attributes) {
		meta.NoIndex = true
	}

	// Build breadcrumb.
	//
	// The section crumb (the schema's archive_path) is prepended when the
	// schema has one; the terminal crumb href uses the pattern-derived
	// canonical path so the emitted BreadcrumbList `item` URLs match what
	// the frontend actually routes -- the bug production surfaced was
	// terminal crumbs of shape https://.../<slug> that dropped the section
	// prefix and 404'd in Google's Rich Results Test.
	breadcrumb := []BreadcrumbItem{
		{Label: "Home", Href: s.urlSnapshot(ctx).WithLocale(ent.Locale, "/")},
	}
	if loc.ArchivePath != "" {
		breadcrumb = append(breadcrumb, BreadcrumbItem{Label: loc.ArchiveName, Href: loc.ArchivePath})
	}
	entityPath := loc.Path
	breadcrumb = append(breadcrumb, BreadcrumbItem{Label: ent.Name, Href: entityPath})

	response := PublicPageResponse{
		Entity:     ent,
		JSONLD:     jsonLD,
		Meta:       meta,
		Breadcrumb: breadcrumb,
		// The entry's own JSON-LD block: extra scripts for the HTML page,
		// never merged into the generated document.
		ExtraJSONLD: entity.CustomJSONLDDocs(ent.Attributes),
	}

	// Include editorial provenance if present
	if len(ent.Editorial) > 0 && string(ent.Editorial) != "{}" {
		response.Editorial = ent.Editorial
	}

	// ?include=segments support removed in Phase 6 along with the
	// segmentation pipeline. The query-string flag is silently ignored
	// so any stale client that still passes it gets the entity payload
	// without the dropped Segments field.

	response.AICrawlerComment = entity.GenerateAICrawlerComment(
		entity.AICommentSourceFromEntity(ent),
		s.tenantNameForComment(ctx),
		baseURL,
	)

	return &response, outcome, nil
}

// loadHomepage assembles the homepage entity page.
// outcome.NotFound means no homepage content exists yet.
func (s *Server) loadHomepage(r *http.Request) (*PublicPageResponse, pageOutcome, error) {
	ctx := r.Context()
	publicRepo := entity.NewPostgresPublicRepository(s.db)
	ent, err := publicRepo.GetHomepage(ctx)
	if err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			return nil, pageOutcome{Status: http.StatusOK, NotFound: true}, nil
		}
		s.logger.Error("failed to get homepage", "error", err)
		return nil, pageOutcome{Status: http.StatusInternalServerError},
			&pageError{http.StatusInternalServerError, "failed to get homepage"}
	}

	applyPreviewAttributes(ctx, ent.ID, &ent.Attributes)
	ent.Attributes = entity.DerivedAttributes(ent.Attributes)
	s.applyAuthorName(ctx, ent)

	baseURL := s.publicBaseURL(r)
	home := s.urlSnapshot(ctx).WithLocale(ent.Locale, "/")
	jsonLD, _ := entity.GenerateJSONLD(ent, baseURL)
	if ent.Schema != nil {
		entity.ApplyCanonicalToJSONLD(jsonLD, ent.Schema.Slug, baseURL+home)
	}
	s.bylinesOf(ctx, ent.ID).applyJSONLD(ctx, ent.ID, baseURL, jsonLD)

	// Parse attributes for meta
	var attrs map[string]interface{}
	if err := json.Unmarshal(ent.Attributes, &attrs); err != nil {
		attrs = make(map[string]interface{})
	}

	meta := PageMeta{
		Title:        ent.Name,
		CanonicalURL: baseURL + home,
	}
	if desc, ok := attrs["meta_description"].(string); ok {
		meta.Description = desc
	}
	if ogImage, ok := attrs["og_image"].(string); ok {
		meta.OGImage = ogImage
	}

	response := PublicPageResponse{
		Entity: ent,
		JSONLD: jsonLD,
		Meta:   meta,
	}

	response.AICrawlerComment = entity.GenerateAICrawlerComment(
		entity.AICommentSourceFromEntity(ent),
		s.tenantNameForComment(ctx),
		baseURL,
	)

	return &response, pageOutcome{Status: http.StatusOK}, nil
}

// personDescriptionRunes bounds a meta description made from a bio.
const personDescriptionRunes = 160

// metaDescriptionAndImage picks the meta description and og:image of an entity
// from its attributes. Each is the first non-empty value (an empty or blank
// string is skipped, so a cleared field does not hide the next one):
// description from meta_description, description, excerpt, short_description;
// image from og_image, featured_image. A person also falls back to
// short_bio, then its bio as plain text, and to its photo.
func metaDescriptionAndImage(attrs map[string]any, sc *entity.EntitySchema) (description, image string) {
	isPerson := sc != nil && sc.Slug == person.SchemaSlug
	descKeys := []string{"meta_description", "description", "excerpt", "short_description"}
	imageKeys := []string{"og_image", "featured_image"}
	if isPerson {
		descKeys = append(descKeys, "short_bio")
		imageKeys = append(imageKeys, "photo")
	}
	description = firstNonBlankAttr(attrs, descKeys...)
	if description == "" && isPerson {
		description = plainText(strAttr(attrs, "bio"), personDescriptionRunes)
	}
	return description, firstNonBlankAttr(attrs, imageKeys...)
}

// firstNonBlankAttr is the first of keys whose attribute is a string with
// something in it.
func firstNonBlankAttr(attrs map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := attrs[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// pageFAQItems extracts the FAQ blocks of an entry's body, or nil when the
// schema turned FAQ extraction off (settings.faq_from_content=false).
func pageFAQItems(ent *entity.Entity) []entity.FAQItem {
	if st, err := ent.Schema.URLSettings(); err == nil && !st.FAQEnabled() {
		return nil
	}
	var body struct {
		Content string `json:"content"`
	}
	if json.Unmarshal(ent.Attributes, &body) != nil {
		return nil
	}
	return entity.ExtractFAQItems(body.Content)
}
