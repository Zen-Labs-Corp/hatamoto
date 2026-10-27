// Package server — CMS core route mounts.
//
// This file holds the route mounters for what Hatamoto considers the
// "CMS core": entity / schema / media / forms / menus / redirects / public
// site rendering / auth + user management / taxonomy / analytics admin /
// linking / 404 monitor / API keys.
package server

import (
	"net/http"

	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
)

// mountCoreUnauthedAPIRoutes mounts the /api/v1 routes that bypass the
// admin auth middleware: the public auth endpoints (login/refresh/etc.)
// behind the auth rate limiter and the anonymous analytics /track ingest.
func (s *Server) mountCoreUnauthedAPIRoutes(r *httpx.Group) {
	// Rate limiter for auth endpoints
	authLimiter := s.authRateLimiter()

	// Public auth routes
	{
		r := r.Group("", authLimiter.Middleware)
		r.HandleFunc("POST /auth/login", s.handleLogin)
	}

	s.mountTrack(r)
}

// mountTrack mounts the pageview beacon on r: anonymous-allowed, rate-limited
// per IP (each mount has its own bucket). It stores only a daily count, so
// there is no cookie, lead or session to protect.
func (s *Server) mountTrack(r *httpx.Group) {
	trackLimiter := middleware.NewRateLimiter(20, 50)
	handleCORS(r, http.MethodPost, "/track", s.analyticsHandlers.Track, trackLimiter.Middleware)
}

// mountCoreProtectedRoutes mounts the CMS-core route subset that sits
// behind AuthMiddleware in the /api/v1 protected group. The caller is
// responsible for applying AuthMiddleware once at the group level;
// individual sub-routers add their own RequireRole gates where needed.
func (s *Server) mountCoreProtectedRoutes(r *httpx.Group) {
	// Auth
	r.HandleFunc("POST /auth/logout", s.handleLogout)

	// User profile
	r.HandleFunc("GET /me", s.handleGetMe)
	r.HandleFunc("PATCH /me", s.handleUpdateMe)

	// Cache management (owner only). Flushing the cache is a tenant-wide,
	// potentially disruptive operation; gate it behind owner like /tenant.
	{
		r := r.Group("", middleware.RequireRole("owner"))
		r.HandleFunc("POST /cache/flush", s.handleFlushCache)
	}

	// Render API (theme schema + draft preview; owner/editor)
	s.mountRenderAdminRoutes(r)

	// Site settings: name, branding, homepage, notifications, theme (owner only).
	// Interim until the Go-rendered settings UI (ROADMAP v1-10).
	s.mountSiteSettingsRoutes(r)

	// User management (owner only). Creating/deleting users and changing roles
	// is a privilege-granting surface; restrict to owner.
	{
		r := r.Group("/users", middleware.RequireRole("owner"))
		r.HandleFunc("GET /", s.handleListUsers)
		r.HandleFunc("POST /", s.handleCreateUser)
		r.HandleFunc("GET /{id}", s.handleGetUser)
		r.HandleFunc("PATCH /{id}", s.handleUpdateUser)
		r.HandleFunc("DELETE /{id}", s.handleDeleteUser)
	}

	// Plugin inspector and settings (owner only): which plugins loaded, which
	// failed (and why), and which hooks each handles.
	{
		r := r.Group("/plugins", middleware.RequireRole("owner"))
		r.HandleFunc("GET /", s.handleListPlugins)
		r.HandleFunc("GET /{name}/settings", s.handleGetPluginSettings)
		r.HandleFunc("PUT /{name}/settings", s.handlePutPluginSettings)
		r.HandleFunc("POST /{name}/grant", s.handleGrantPlugin)
	}

	// Plugin routes (declared in manifests): auth, CSRF and the route's roles
	// apply; the catch-all itself is fixed.
	s.mountPluginAdminRoutes(r)

	// Entity Schema management (owner only). Schemas define the
	// shape of every entity in the system — a malicious or
	// careless editor swapping a property type out from under
	// 500 published pages would corrupt the entire site. Phase 3
	// shipped a visual builder that makes this surface load-
	// bearing for the threat model, so the sub-router is gated
	// at the same level as /tenant and /users.
	{
		r := r.Group("/schemas", middleware.RequireRole("owner"))
		r.HandleFunc("GET /", s.handleListSchemas)
		r.HandleFunc("POST /", s.handleCreateSchema)
		r.HandleFunc("GET /{id}", s.handleGetSchema)
		r.HandleFunc("PUT /{id}", s.handleUpdateSchema)
		r.HandleFunc("PATCH /{id}", s.handleUpdateSchema) // Support both PUT and PATCH
		r.HandleFunc("DELETE /{id}", s.handleDeleteSchema)
	}

	// Entity management
	{
		r := r.Group("/entities")
		r.HandleFunc("GET /", s.handleListEntities)
		r.HandleFunc("POST /", s.handleCreateEntity)
		r.HandleFunc("POST /validate", s.handleValidateEntityAttributes)
		r.HandleFunc("GET /{id}", s.handleGetEntity)
		r.HandleFunc("PUT /{id}", s.handleUpdateEntity)
		r.HandleFunc("PATCH /{id}", s.handleUpdateEntity) // Support both PUT and PATCH
		r.HandleFunc("PATCH /{id}/status", s.handleUpdateEntityStatus)
		r.HandleFunc("DELETE /{id}", s.handleDeleteEntity)

		// Version management
		r.HandleFunc("GET /{id}/versions", s.handleListEntityVersions)
		r.HandleFunc("GET /{id}/versions/{version}", s.handleGetEntityVersion)
		r.HandleFunc("POST /{id}/restore/{version}", s.handleRestoreEntityVersion)
	}

	// Media management
	{
		r := r.Group("/media")
		r.HandleFunc("POST /upload", s.handleMediaUpload)
		r.HandleFunc("GET /", s.handleMediaList)
		r.HandleFunc("GET /{id}", s.handleMediaGet)
		r.HandleFunc("PUT /{id}", s.handleMediaUpdate)
		r.HandleFunc("DELETE /{id}", s.handleMediaDelete)
	}

	// Menu management
	{
		r := r.Group("/menus")
		r.HandleFunc("GET /", s.handleListMenus)
		r.HandleFunc("POST /", s.handleCreateMenu)
		r.HandleFunc("GET /{id}", s.handleGetMenu)
		r.HandleFunc("PUT /{id}", s.handleUpdateMenu)
		r.HandleFunc("DELETE /{id}", s.handleDeleteMenu)
		r.HandleFunc("POST /{id}/items", s.handleCreateMenuItem)
		r.HandleFunc("PUT /{id}/items/{itemId}", s.handleUpdateMenuItem)
		r.HandleFunc("DELETE /{id}/items/{itemId}", s.handleDeleteMenuItem)
		r.HandleFunc("POST /{id}/items/reorder", s.handleReorderMenuItems)
	}

	// Section template management
	{
		r := r.Group("/section-templates")
		r.HandleFunc("GET /", s.handleListSectionTemplates)
		r.HandleFunc("POST /", s.handleCreateSectionTemplate)
		r.HandleFunc("DELETE /{id}", s.handleDeleteSectionTemplate)
		r.HandleFunc("POST /{id}/use", s.handleRecordSectionTemplateUsage)
	}

	// Form management
	{
		r := r.Group("/forms")
		r.HandleFunc("GET /", s.handleListForms)
		r.HandleFunc("POST /", s.handleCreateForm)
		r.HandleFunc("GET /{id}", s.handleGetForm)
		r.HandleFunc("PUT /{id}", s.handleUpdateForm)
		r.HandleFunc("DELETE /{id}", s.handleDeleteForm)

		// Submissions
		r.HandleFunc("GET /{id}/submissions", s.handleListFormSubmissions)
		r.HandleFunc("GET /{id}/submissions/{submissionId}", s.handleGetFormSubmission)
		r.HandleFunc("PATCH /{id}/submissions/{submissionId}", s.handleUpdateFormSubmission)
		r.HandleFunc("DELETE /{id}/submissions/{submissionId}", s.handleDeleteFormSubmission)
		r.HandleFunc("POST /{id}/submissions/{submissionId}/email", s.handleEmailFormSubmission)
	}

	// API Key management (owner only)
	{
		r := r.Group("/api-keys", middleware.RequireRole("owner"))
		r.HandleFunc("GET /", s.handleListAPIKeys)
		r.HandleFunc("POST /", s.handleCreateAPIKey)
		r.HandleFunc("GET /scopes", s.handleGetAPIKeyScopes)
		r.HandleFunc("GET /{id}", s.handleGetAPIKey)
		r.HandleFunc("PATCH /{id}", s.handleUpdateAPIKey)
		r.HandleFunc("POST /{id}/revoke", s.handleRevokeAPIKey)
		r.HandleFunc("POST /{id}/regenerate", s.handleRegenerateAPIKey)
		r.HandleFunc("DELETE /{id}", s.handleDeleteAPIKey)
	}

	// Internal linking management
	{
		r := r.Group("/linking")
		// Rules: alias data for the v2 suggester. ADR plans/ADR-internal-linking-v2.md §2.3.
		r.HandleFunc("GET /rules", s.handleListLinkRules)
		r.HandleFunc("POST /rules", s.handleCreateLinkRule)
		r.HandleFunc("DELETE /rules/all", s.handleDeleteAllRules)
		r.HandleFunc("POST /rules/bulk-delete", s.handleBulkDeleteRules)
		r.HandleFunc("POST /rules/delete-by-source", s.handleDeleteRulesBySource)
		r.HandleFunc("POST /rules/preview", s.handlePreviewLinkRule)
		r.HandleFunc("GET /rules/{id}", s.handleGetLinkRule)
		r.HandleFunc("PUT /rules/{id}", s.handleUpdateLinkRule)
		r.HandleFunc("PATCH /rules/{id}", s.handleUpdateLinkRule)
		r.HandleFunc("DELETE /rules/{id}", s.handleDeleteLinkRule)

		// v2 endpoints. ADR §5.1.
		//   /resolve             — picker URL preview.
		//   /insert              — server-side wrap mutation.
		//   /suggest/{entity_id} — non-mutating proposals.
		//   /equity, /inbound    — admin equity dashboard.
		r.HandleFunc("GET /resolve", s.handleLinkingV2Resolve)
		r.HandleFunc("POST /insert", s.handleLinkingV2Insert)
		r.HandleFunc("POST /suggest/{entity_id}", s.handleLinkingV2Suggest)
		r.HandleFunc("GET /equity", s.handleLinkingV2Equity)
		r.HandleFunc("GET /inbound/{id}", s.handleLinkingV2InboundLinks)
	}

	// Redirect / 404 monitor
	{
		r := r.Group("/404-log")
		r.HandleFunc("GET /", s.handleList404Entries)
		r.HandleFunc("POST /{id}/ignore", s.handleIgnore404Entry)
		r.HandleFunc("DELETE /{id}", s.handleDelete404Entry)
	}
	{
		r := r.Group("/redirects")
		r.HandleFunc("GET /", s.handleListRedirects)
		// What happens to URL X, and why.
		r.HandleFunc("GET /explain", s.handleRedirectExplain)
		r.HandleFunc("POST /", s.handleCreateRedirect)
		r.HandleFunc("PUT /{id}", s.handleUpdateRedirect)
		r.HandleFunc("DELETE /{id}", s.handleDeleteRedirect)
	}

	// Agent analytics (AI Agents admin). Any signed-in admin user may read.
	{
		r := r.Group("/agent-analytics")
		r.HandleFunc("GET /overview", s.agentAdminHandlers.HandleOverview)
		r.HandleFunc("GET /pages", s.agentAdminHandlers.HandlePages)
		r.HandleFunc("GET /agents", s.agentAdminHandlers.HandleAgents)
		r.HandleFunc("GET /agents/{id}", s.agentAdminHandlers.HandleAgentDetail)
		r.HandleFunc("GET /errors", s.agentAdminHandlers.HandleErrors)
		r.HandleFunc("GET /spoofed", s.agentAdminHandlers.HandleSpoofed)
		r.HandleFunc("GET /entities/{id}", s.agentAdminHandlers.HandleEntityActivity)
		r.HandleFunc("GET /robots-policy", s.agentAdminHandlers.HandleGetRobotsPolicy)
		// Changes what every crawler is told site-wide: owner only.
		r.Group("", middleware.RequireRole("owner")).HandleFunc("PUT /robots-policy", s.agentAdminHandlers.HandlePutRobotsPolicy)
	}

	// Authors: bylines (content, not accounts) with a bio and an avatar.
	if s.authorHandlers != nil {
		r := r.Group("/authors", middleware.RequireRole("owner", "editor"))
		r.HandleFunc("GET /", s.authorHandlers.List)
		r.HandleFunc("POST /", s.authorHandlers.Create)
		r.HandleFunc("GET /{id}", s.authorHandlers.Get)
		r.HandleFunc("PUT /{id}", s.authorHandlers.Update)
		r.HandleFunc("DELETE /{id}", s.authorHandlers.Delete)
	}

	// Taxonomy admin endpoints (Phase 2). Owner-only — the term
	// catalog is shared across every editor, so a stray editor
	// shouldn't be able to delete a term that's attached to
	// hundreds of public-facing pages.
	if s.taxonomyHandlers != nil {
		{
			r := r.Group("/terms", middleware.RequireRole("owner"))
			r.HandleFunc("GET /", s.taxonomyHandlers.ListTerms)
			r.HandleFunc("POST /", s.taxonomyHandlers.CreateTerm)
			r.HandleFunc("PUT /{id}", s.taxonomyHandlers.UpdateTerm)
			r.HandleFunc("DELETE /{id}", s.taxonomyHandlers.DeleteTerm)
		}
		// Entity-term attachments. Mounted under /admin/entities
		// rather than nested under the existing /entities routes
		// to keep the taxonomy handler co-located. Owner-only
		// because attaching a term affects public archive URLs.
		{
			r := r.Group("/entities/{entity_id}/terms", middleware.RequireRole("owner", "editor"))
			r.HandleFunc("GET /", s.taxonomyHandlers.GetEntityTerms)
			r.HandleFunc("PUT /", s.taxonomyHandlers.ReplaceEntityTerms)
		}
	}
}

// mountCorePublicMetaRoutes mounts the small handful of /public/* routes
// that sit OUTSIDE the publicTenantMiddleware group: agent access-log ingest. Kept out of the tenant group so they don't
// double-log via the user-agent inspector.
func (s *Server) mountCorePublicMetaRoutes(r *httpx.Group) {
	// Access-log ingest (Logpush / Caddy NDJSON): own secret + rate limit,
	// mounted only when AGENT_LOG_INGEST is on with a secret (else 404).
	mountAgentLogRoute(r, s.agentLogHandlers)
}

// mountCorePublicCMSRoutes mounts the public CMS surface inside the
// publicTenantMiddleware group: site config,
// pages, archives, sitemaps, public forms, author
// pages, taxonomy archives, redirect lookup, slug resolution.
func (s *Server) mountCorePublicCMSRoutes(r *httpx.Group) {
	r.HandleFunc("GET /sitemap.xml", s.handlePublicSitemap)
	r.HandleFunc("GET /knowledge-graph", s.handleKnowledgeGraph)
	r.HandleFunc("GET /llms.txt", s.handleLLMsTxt)

	// Public form routes
	r.HandleFunc("GET /forms/{slug}", s.handleGetPublicForm)
	handleCORS(r, http.MethodGet, "/forms/{slug}/token", s.handleFormToken)
	// Per-IP limit (0.1 rps, burst 5): submit is anonymous and can trigger a
	// confirmation email, so it must not be an open relay.
	formSubmitLimiter := middleware.NewRateLimiter(0.1, 5)
	handleCORS(r, http.MethodPost, "/forms/{slug}/submit", s.handlePublicFormSubmit, formSubmitLimiter.Middleware)

	// Public taxonomy archive (Phase 2): list published
	// entities tagged with {kind}/{slug}. Rate-limited per
	// IP — same posture as other public archive endpoints.
	if s.taxonomyHandlers != nil {
		termLimiter := middleware.NewRateLimiter(10, 30)
		r.Group("", termLimiter.Middleware).HandleFunc(
			"GET /terms/{kind}/{slug}/entities",
			s.taxonomyHandlers.PublicTermArchive,
		)
	}
}
