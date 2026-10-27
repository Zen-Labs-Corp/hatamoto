package server

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/siteconfig"
)

// NavigationItem represents a menu item.
type NavigationItem struct {
	Label    string           `json:"label"`
	Href     string           `json:"href"`
	Target   string           `json:"target,omitempty"`
	Children []NavigationItem `json:"children,omitempty"`
}

// PublicPageResponse represents a public page API response.
type PublicPageResponse struct {
	Entity     *entity.Entity           `json:"entity"`
	JSONLD     map[string]interface{}   `json:"json_ld,omitempty"`
	Meta       PageMeta                 `json:"meta"`
	Breadcrumb []BreadcrumbItem         `json:"breadcrumb"`
	Editorial  json.RawMessage          `json:"editorial,omitempty"`
	Segments   []ContentSegmentResponse `json:"segments,omitempty"`
	// AICrawlerComment is the HTML comment block embedded in the page for
	// AI/LLM crawlers. Empty when the entity has been opted out via the
	// SEO tab's "Include in /llms.txt" toggle.
	AICrawlerComment string `json:"ai_crawler_comment,omitempty"`
}

// PageMeta represents SEO metadata for a page.
type PageMeta struct {
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	OGImage      string `json:"og_image,omitempty"`
	CanonicalURL string `json:"canonical_url,omitempty"`
	NoIndex      bool   `json:"no_index,omitempty"`
}

// BreadcrumbItem represents a breadcrumb navigation item.
type BreadcrumbItem struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}

// ContentSegmentResponse represents a content segment in the public API.
type ContentSegmentResponse struct {
	SegmentIndex      int      `json:"segment_index"`
	SegmentType       string   `json:"segment_type"`
	Heading           string   `json:"heading,omitempty"`
	ContentText       string   `json:"content_text"`
	ContentHTML       string   `json:"content_html,omitempty"`
	TopicLabels       []string `json:"topic_labels,omitempty"`
	IsAnswerCandidate bool     `json:"is_answer_candidate"`
	WordCount         int      `json:"word_count"`
}

// SitemapIndex represents an XML sitemap index (container for sub-sitemaps).
type SitemapIndex struct {
	XMLName  xml.Name            `xml:"sitemapindex"`
	XMLNS    string              `xml:"xmlns,attr"`
	Sitemaps []SitemapIndexEntry `xml:"sitemap"`
}

// SitemapIndexEntry represents a single sub-sitemap reference in the index.
type SitemapIndexEntry struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

// URLSet represents an XML sitemap with optional xhtml namespace for alternates.
type URLSet struct {
	XMLName    xml.Name `xml:"urlset"`
	XMLNS      string   `xml:"xmlns,attr"`
	XMLNSXhtml string   `xml:"xmlns:xhtml,attr,omitempty"`
	URLs       []URL    `xml:"url"`
}

// URL represents a single URL entry in the sitemap.
type URL struct {
	Loc        string     `xml:"loc"`
	LastMod    string     `xml:"lastmod,omitempty"`
	ChangeFreq string     `xml:"changefreq,omitempty"`
	Priority   float64    `xml:"priority"`
	Links      []HrefLang `xml:"xhtml:link,omitempty"`
}

// HrefLang represents an xhtml:link alternate element (added by plugin filters).
type HrefLang struct {
	Rel      string `xml:"rel,attr"`
	HrefLang string `xml:"hreflang,attr"`
	Href     string `xml:"href,attr"`
}

// publicTenantMiddleware is the public-request edge hook. Multi-tenancy
// was removed in Path B; this middleware survives as the
// agent-hit capture point and as a hardening step that strips any
// client-supplied X-Tenant-ID header so a future handler can't accidentally trust it.
func (s *Server) publicTenantMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		middleware.Trace(r, "tenant")
		r.Header.Del("X-Tenant-ID")
		ctx := r.Context()

		// Crawler-facing files (sitemaps, llms.txt) are recorded after the
		// handler ran so the real status/bytes/duration are known.
		if p, ok := agents.PublicPath(r.URL.Path); ok && s.agentWriter != nil {
			ww := httpx.NewRecorder(w)
			start := time.Now()
			next.ServeHTTP(ww, r.WithContext(ctx))
			status := ww.Status()
			if status == 0 {
				status = http.StatusOK // handler never wrote a status
			}
			s.agentWriter.Record(agents.RawHit{
				At:         start,
				UA:         r.UserAgent(),
				IP:         middleware.GetClientIP(r),
				Method:     r.Method,
				Path:       p,
				Status:     status,
				Bytes:      ww.BytesWritten(),
				DurationMS: int(time.Since(start).Milliseconds()),
				Source:     agents.SourceBackend,
			})
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// fallbackNavigationWith is fallbackNavigation with the page link builder
// injected; pageHref reports false to leave a page out of the navigation.
func (s *Server) fallbackNavigationWith(ctx context.Context, pageHref func(*entity.Entity) (string, bool)) []NavigationItem {
	var navigation []NavigationItem
	publicRepo := entity.NewPostgresPublicRepository(s.db)
	pages, err := publicRepo.ListPublished(ctx, "page", 1, 50)

	if err == nil && pages != nil {
		for _, p := range pages.Items {
			// Parse attributes to check if item should be in nav
			var attrs map[string]interface{}
			if err := json.Unmarshal(p.Attributes, &attrs); err == nil {
				// Skip items marked as hidden from nav
				if hidden, ok := attrs["hide_from_nav"].(bool); ok && hidden {
					continue
				}
			}

			href, ok := pageHref(p)
			if !ok {
				continue
			}
			navigation = append(navigation, NavigationItem{
				Label: p.Name,
				Href:  href,
			})
		}
	}

	// Add the post archive to navigation if posts exist. The post family
	// (a schema plus its aliases) and its archive path come from the
	// URL registry rather than a hard-coded schema slug.
	if cfg, ok := s.urlSnapshot(ctx).Config("post"); ok && cfg.ArchivePath != "" {
		slugs := append([]string{cfg.SchemaSlug}, cfg.Aliases...)
		posts, err := publicRepo.ListPublishedBySchemas(ctx, slugs, 1, 1)
		if err == nil && posts != nil && posts.Total > 0 {
			navigation = append(navigation, NavigationItem{
				Label: "Blog",
				Href:  cfg.ArchivePath,
			})
		}
	}
	return navigation
}

// sitemapXSLPI is the XML processing instruction that references the XSLT stylesheet.
const sitemapXSLPI = `<?xml-stylesheet type="text/xsl" href="/sitemap.xsl"?>` + "\n"

// loadSitemapInput gathers everything the pure sitemap builder needs.
func (s *Server) loadSitemapInput(r *http.Request) (sitemapInput, error) {
	return s.loadSitemapInputFor(r.Context(), s.publicBaseURL(r))
}

// loadSitemapInputFor is loadSitemapInput without a request: the base URL is
// given (the preloader has no request to derive it from).
func (s *Server) loadSitemapInputFor(ctx context.Context, baseURL string) (sitemapInput, error) {
	snap := s.urlSnapshot(ctx)
	repo := entity.NewPostgresPublicRepository(s.db)
	entries, err := repo.ListAllPublishedWithAttrs(ctx, snap.OverrideAttributes())
	if err != nil {
		return sitemapInput{}, err
	}
	// The entity listed as "/" is the one "/" serves: the configured homepage,
	// else the home / homepage / first-published-page fallback.
	var homepageID uuid.UUID
	if home, err := repo.GetHomepage(ctx); err == nil && home != nil {
		homepageID = home.ID
	}
	var redirected func(string) bool
	if s.redirectService != nil {
		redirected = func(path string) bool { return s.redirectService.Peek(ctx, path) != nil }
	}
	return sitemapInput{
		HomepageID: homepageID,
		Redirected: redirected,
		Snap:       snap,
		Entries:    entries,
		BaseURL:    baseURL,
		Today:      time.Now().Format("2006-01-02"),
		TenantSlug: canonicalTenantSlug,
		Plugins:    sitemapHooksSnapshot(),
	}, nil
}

// handlePublicSitemap returns the sitemap index listing all sub-sitemaps.
// The <lastmod> for each sub-sitemap is computed from the most recent entity update
// within that group rather than using today's date.
func (s *Server) handlePublicSitemap(w http.ResponseWriter, r *http.Request) {
	cacheKey := cache.SitemapKey("index")
	if cached, hit := s.getCachedSitemapXML(r, w, cacheKey); hit {
		writeCachedXML(w, cached)
		return
	}

	in, err := s.loadSitemapInput(r)
	if err != nil {
		s.logger.Error("failed to generate sitemap index", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to generate sitemap")
		return
	}

	xmlBytes, err := writeXMLWithXSL(w, planSitemap(in).Index())
	if err != nil {
		s.logger.Error("failed to marshal sitemap index XML", slog.String("error", err.Error()))
		return
	}
	s.cacheSitemapXML(r, "index", xmlBytes)
}

// sitemapChild extracts <sub> from a "sitemap-<sub>.xml" file name. ServeMux
// wildcards span a whole path segment, so the routes capture "{file}" and the
// shape is checked here.
func sitemapChild(file string) (string, bool) {
	sub, ok := strings.CutPrefix(file, "sitemap-")
	if !ok {
		return "", false
	}
	sub, ok = strings.CutSuffix(sub, ".xml")
	return sub, ok && sub != ""
}

// handlePublicSubSitemap serves one child sitemap: group keys (from schema
// settings and plugins, plus misc) are valid, unknown values return 404.
func (s *Server) handlePublicSubSitemap(w http.ResponseWriter, r *http.Request) {
	sub, ok := sitemapChild(r.PathValue("file"))
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	// Only valid children are ever cached, so a hit needs no database work.
	cacheKey := cache.SitemapKey(sub)
	if cached, hit := s.getCachedSitemapXML(r, w, cacheKey); hit {
		writeCachedXML(w, cached)
		return
	}

	in, err := s.loadSitemapInput(r)
	if err != nil {
		s.logger.Error("failed to generate sitemap", slog.String("error", err.Error()), slog.String("sub", sub))
		writeError(w, http.StatusInternalServerError, "failed to generate sitemap")
		return
	}
	plan := planSitemap(in)

	if !plan.HasGroup(sub) {
		writeError(w, http.StatusNotFound, "sitemap not found")
		return
	}
	groupURLs, _ := plan.GroupURLs(sub)
	urls := s.applySitemapFilter(r.Context(), sub, groupURLs)

	// The xhtml namespace is declared only when a sitemap.urls filter added alternates.
	urlSet := URLSet{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: urls}
	if sitemapHasLinks(urls) {
		urlSet.XMLNSXhtml = "http://www.w3.org/1999/xhtml"
	}
	xmlBytes, err := writeXMLWithXSL(w, urlSet)
	if err != nil {
		s.logger.Error("failed to marshal sitemap XML", slog.String("error", err.Error()), slog.String("sub", sub))
		return
	}
	s.cacheSitemapXML(r, sub, xmlBytes)
}

// writeXMLWithXSL marshals v to indented XML, prepends the XSLT stylesheet
// processing instruction, and writes the response. Returns the raw XML bytes
// for caching, or an error if marshaling fails.
func writeXMLWithXSL(w http.ResponseWriter, v interface{}) ([]byte, error) {
	xmlOutput, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate sitemap")
		return nil, fmt.Errorf("xml marshal: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	buf.WriteString(sitemapXSLPI)
	buf.Write(xmlOutput)

	result := buf.Bytes()

	w.Header().Set("Cache-Control", "public, max-age=3600, s-maxage=3600")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(result); err != nil {
		slog.Debug("sitemap: failed to write response body", slog.String("error", err.Error()))
	}
	return result, nil
}

// writeCachedXML writes pre-serialized XML bytes directly to the response.
func writeCachedXML(w http.ResponseWriter, data []byte) {
	w.Header().Set("Cache-Control", "public, max-age=3600, s-maxage=3600")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		slog.Debug("sitemap: failed to write cached response", slog.String("error", err.Error()))
	}
}

// sitemapCacheable reports whether sitemap XML may be cached. The body embeds
// the base URL, so without a configured PUBLIC_BASE_URL (where it comes from
// the request Host) one requester's host would be served to everyone. Same rule
// as the rendered-page cache (renderHandlers.baseURLPinned).
func (s *Server) sitemapCacheable() bool {
	return s.infra != nil && s.infra.Cache != nil &&
		s.cfg != nil && strings.TrimSpace(s.cfg.PublicSite.BaseURL) != ""
}

// getCachedSitemapXML attempts to retrieve cached sitemap XML.
// Returns the cached bytes and true on cache hit, nil and false on miss.
func (s *Server) getCachedSitemapXML(r *http.Request, w http.ResponseWriter, cacheKey string) ([]byte, bool) {
	if !s.sitemapCacheable() {
		return nil, false
	}

	data, err := s.infra.Cache.Get(r.Context(), cacheKey)
	if err == nil && len(data) > 0 {
		w.Header().Set("X-Cache", "HIT")
		return data, true
	}
	return nil, false
}

// cacheSitemapXML stores serialized sitemap XML in the cache.
func (s *Server) cacheSitemapXML(r *http.Request, sub string, data []byte) {
	if !s.sitemapCacheable() || len(data) == 0 {
		return
	}
	cacheKey := cache.SitemapKey(sub)
	_ = s.infra.Cache.Set(r.Context(), cacheKey, data, cache.TTLSitemap)
}

// tenantNameForComment fetches the site display name for embedding in
// the AI Crawler comment block. Returns an empty string on lookup failure
// so the comment generator falls back gracefully.
//
// Reads hatamoto_site_config.company_name via siteconfig.Store (cached).
func (s *Server) tenantNameForComment(ctx context.Context) string {
	if s.siteConfig == nil {
		return ""
	}
	cfg, err := s.siteConfig.Get(ctx)
	if err != nil {
		return ""
	}
	return cfg.CompanyName
}

// decodeThemeExtra unmarshals the hatamoto_site_config.theme_extra JSONB blob
// into the siteconfig.ThemeConfig shape. Falls back to the default theme on
// missing or malformed data so a partial migration never blanks the design
// tokens.
func decodeThemeExtra(raw json.RawMessage) siteconfig.ThemeConfig {
	if len(raw) == 0 {
		return siteconfig.DefaultThemeConfig()
	}
	var t siteconfig.ThemeConfig
	if err := json.Unmarshal(raw, &t); err != nil {
		return siteconfig.DefaultThemeConfig()
	}
	return t
}

// effectiveTheme is the theme every renderer and the admin theme view use:
// the decoded theme_extra blob, with the singleton primary_color column
// promoted into it when the blob didn't supply one — preserves the legacy
// behaviour where the branding colour and Colors.Primary were one value.
func effectiveTheme(cfg *siteconfig.Config) siteconfig.ThemeConfig {
	theme := decodeThemeExtra(cfg.ThemeExtra)
	if theme.Colors.Primary == "" && cfg.PrimaryColor != "" {
		theme.Colors.Primary = cfg.PrimaryColor
	}
	return theme
}

// publicBaseURL is the origin used for public URLs (canonicals, breadcrumbs,
// JSON-LD, sitemaps, llms.txt). The configured PUBLIC_BASE_URL wins when set:
// behind a server-side frontend fetch the request host is the internal API
// address (e.g. 127.0.0.1:8080), which must never leak into public URLs.
// Unset → the address the owner set in the admin (setup wizard / site
// settings), else derived from the request as before.
func (s *Server) publicBaseURL(r *http.Request) string {
	if s.cfg != nil {
		if b := strings.TrimRight(strings.TrimSpace(s.cfg.PublicSite.BaseURL), "/"); b != "" {
			return b
		}
	}
	if s.siteConfig != nil {
		if cfg, err := s.siteConfig.Get(r.Context()); err == nil && cfg.PublicBaseURL != "" {
			return strings.TrimRight(cfg.PublicBaseURL, "/")
		}
	}
	return determineBaseURL(r)
}

// configuredBaseURL is the site address the operator or owner set, with no
// request-derived fallback. Anything that carries a secret to an inbox (reset
// and invitation links) must use this, never publicBaseURL.
func (s *Server) configuredBaseURL(r *http.Request) string {
	if s.cfg != nil {
		if b := strings.TrimRight(strings.TrimSpace(s.cfg.PublicSite.BaseURL), "/"); b != "" {
			return b
		}
	}
	if s.siteConfig != nil {
		if cfg, err := s.siteConfig.Get(r.Context()); err == nil {
			return strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")
		}
	}
	return ""
}

// determineBaseURL constructs the base URL from the request.
func determineBaseURL(r *http.Request) string {
	// X-Forwarded-* is only believed from a trusted proxy; from anyone else it
	// is just a client-chosen string.
	trusted := middleware.IsTrustedProxy(r.RemoteAddr)
	scheme := "https"
	if r.TLS == nil {
		if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" && trusted {
			scheme = forwarded
		} else {
			scheme = "http"
		}
	}

	// Prefer X-Forwarded-Host (set by reverse proxies like Caddy/Nginx)
	// over r.Host which may be localhost when behind a Next.js rewrite proxy.
	host := r.Host
	if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" && trusted {
		host = fwdHost
		scheme = "https" // forwarded requests are always behind TLS
	}

	return fmt.Sprintf("%s://%s", scheme, host)
}

func getMenuItemHref(item *menu.MenuItem) string {
	if item.URL != nil && *item.URL != "" {
		return *item.URL
	}
	if item.EntitySlug != nil && *item.EntitySlug != "" {
		return "/" + *item.EntitySlug
	}
	return "#"
}

// archiveHeading resolves an archive's title and description. The title is,
// in order: settings.archive_title, the schema's display name, then the
// humanised slug. cfg may be nil (unknown schema). The description is
// settings.archive_description or empty.
func archiveHeading(cfg *entity.SchemaURLConfig, slug string) (title, description string) {
	if cfg != nil {
		description = cfg.ArchiveDescription
		if t := strings.TrimSpace(cfg.ArchiveTitle); t != "" {
			return t, description
		}
		if n := strings.TrimSpace(cfg.Name); n != "" {
			return n, description
		}
	}
	return humanizeSchemaSlug(slug), description
}

// humanizeSchemaSlug turns a schema slug into a display name ("team_member" -> "Team member").
func humanizeSchemaSlug(slug string) string {
	if slug == "" {
		return ""
	}
	return strings.ToUpper(slug[:1]) + strings.ReplaceAll(slug[1:], "_", " ")
}
