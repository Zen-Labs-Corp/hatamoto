package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

// maxPreviewBodySize bounds a preview request: unsaved attributes of a long
// page, never an upload.
const maxPreviewBodySize = 4 << 20

// themeRenderer is the part of *render.Engine the admin endpoints use.
type themeRenderer interface {
	RenderPage(w io.Writer, v *render.PageView) error
	PrefetchExt(v *render.PageView, nodes []*builder.Node)
	RenderTop(v *render.PageView, n *builder.Node) (render.Fragment, bool, error)
	RenderNode(w io.Writer, v *render.PageView, n *builder.Node, canvas bool) error
	NodeCSSFor(v *render.PageView, n, parent *builder.Node) map[string]string
	ThemeInfo() render.ThemeInfo
}

// renderAdminHandlers serve the editor-facing render API: the theme's section
// schemas and a stateless draft preview. Neither writes anything.
type renderAdminHandlers struct {
	engine themeRenderer
	logger *slog.Logger
	// chrome loads the site-wide data and the forms the sections need.
	chrome  func(ctx context.Context, sections []render.Section) (render.SiteView, map[string]render.FormView, error)
	baseURL func(r *http.Request) string
	// directories fills the views of plugin_directory and theme_directory
	// sections so the builder canvas and preview show the cards; nil for a
	// kind the draft has none of.
	directories func(ctx context.Context, sections []render.Section, doc *builder.Doc) (*render.PluginDirectoryView, *render.ThemeDirectoryView)
	// resolve, loadHome and buildView are the public renderer's own route
	// resolution and view assembly. Unset (or failing to match the previewed
	// entity), the preview is the attributes-only view.
	resolve   func(ctx context.Context, path string, r *http.Request) (*routeResult, pageOutcome, error)
	loadHome  func(r *http.Request) (*PublicPageResponse, pageOutcome, error)
	buildView func(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error)
	// captchaProvider is the configured captcha provider, so the preview's CSP
	// lets the widget load as it does on the public site. Nil means none.
	captchaProvider func(ctx context.Context) string
}

func (s *Server) newRenderAdminHandlers(engine themeRenderer) *renderAdminHandlers {
	return &renderAdminHandlers{
		engine:          engine,
		logger:          s.logger,
		captchaProvider: s.captchaProvider,
		chrome: func(ctx context.Context, sections []render.Section) (render.SiteView, map[string]render.FormView, error) {
			site, err := s.buildSiteView(ctx)
			if err != nil {
				return render.SiteView{}, nil, err
			}
			return site, s.formsForSections(ctx, sections), nil
		},
		baseURL:     s.publicBaseURL,
		directories: s.directoriesFor,
		resolve:     s.resolveRouteForRender,
		loadHome:    s.loadHomepage,
		buildView:   s.buildPageView,
	}
}

// handleRenderTheme answers GET /api/v1/render/theme.
func (h *renderAdminHandlers) handleRenderTheme(w http.ResponseWriter, _ *http.Request) {
	info := h.engine.ThemeInfo()
	writeJSON(w, http.StatusOK, info)
}

// previewRequest is the POST /api/v1/render/preview body.
type previewRequest struct {
	EntityID   string          `json:"entity_id"`
	Schema     string          `json:"schema"`
	Attributes json.RawMessage `json:"attributes"`
	Path       string          `json:"path"`
	// Canvas injects the editor bridge script into the document.
	Canvas bool `json:"canvas"`
	// Name, PublishedAt and UpdatedAt are the draft's own, for builder
	// bindings (entity.name, entity.published_at, ...) in a view that has no
	// public route. All optional.
	Name        string     `json:"name,omitempty"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
}

// previewSandboxCSP sandboxes preview and canvas responses so a direct
// navigation never runs in the admin origin. Deliberately not sandboxCSP
// (render_handlers.go): previews need scripts for the bridge only, never
// forms, popups or same-origin. The script-src source list is appended per
// response: the injected canvas bridge by hash, otherwise nothing, so page,
// section and partial scripts (and on* handlers) never run in an editor render.
const previewSandboxCSP = "sandbox allow-scripts; script-src "

// handleRenderPreview renders unsaved attributes as a full HTML document. The
// posted section content is sanitised exactly like stored content; nothing is
// persisted or cached.
func (h *renderAdminHandlers) handleRenderPreview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPreviewBodySize)
	var req previewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "preview body too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	v, previewContext, err := h.previewView(r, &req)
	if err != nil {
		h.logger.Error("render preview: site chrome", "error", err)
		writeError(w, http.StatusInternalServerError, "preview failed")
		return
	}
	v.Preview = true
	v.Canvas = req.Canvas
	v.Page.NoIndex = true

	var buf bytes.Buffer
	if err := h.engine.RenderPage(&buf, v); err != nil {
		h.logger.Error("render preview: render", "error", err)
		writeError(w, http.StatusInternalServerError, "preview failed")
		return
	}
	body := buf.Bytes()
	scriptSrc := "'none'"
	if req.Canvas {
		body = render.InjectCanvasBridge(body)
		scriptSrc = render.CanvasBridgeScriptHash()
	}
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Frame-Options", "SAMEORIGIN")
	hd.Set("Content-Security-Policy", cspWithProvider(renderCSP, providerOf(r.Context(), h.captchaProvider)))
	hd.Add("Content-Security-Policy", previewSandboxCSP+scriptSrc) // a second header: every CSP header is enforced
	hd.Set("X-Robots-Tag", "noindex")
	hd.Set(previewContextHeader, previewContext)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// previewView builds the sanitised view of a draft: the public route's own
// context when the path resolves to the entity, else the attributes-only view.
// The second result names which one ("route" or "attributes").
func (h *renderAdminHandlers) previewView(r *http.Request, req *previewRequest) (*render.PageView, string, error) {
	if v := h.routeView(r, req); v != nil {
		sanitizeView(v)
		v.NoParts = req.Schema == entity.PartSchemaSlug
		return v, "route", nil
	}
	v, err := h.attributesView(r, req)
	if err != nil {
		return nil, "", err
	}
	sanitizeView(v)
	v.NoParts = req.Schema == entity.PartSchemaSlug // a part's own canvas must not draw the assigned part twice
	return v, "attributes", nil
}

// sanitizeView sanitises posted content exactly like stored content.
func sanitizeView(v *render.PageView) {
	for i := range v.Sections {
		v.Sections[i].Content = entity.SanitizeSectionContent(v.Sections[i].Type, v.Sections[i].Content)
	}
	entity.SanitizeDocComponents(v.Doc, nil)
}

// previewContextHeader tells the caller which view a preview used: "route"
// (the public route's own context) or "attributes" (posted attributes only).
// Tools that need route fidelity must require "route".
const previewContextHeader = "X-Hatamoto-Preview-Context"

// attributesView is the preview of a draft that has no public route (new
// page, renamed slug, foreign path): only the posted attributes and the site
// chrome, no route context.
func (h *renderAdminHandlers) attributesView(r *http.Request, req *previewRequest) (*render.PageView, error) {
	sections, doc := contentFromAttributes(req.Attributes, "")
	var meta struct {
		Title       string `json:"meta_title"`
		Description string `json:"meta_description"`
		Name        string `json:"name"`
	}
	_ = json.Unmarshal(req.Attributes, &meta)

	v := &render.PageView{
		BaseURL:    h.baseURL(r),
		Path:       safeLocalPath(req.Path),
		EntityID:   req.EntityID,
		SchemaSlug: req.Schema,
		Sections:   sections,
		Doc:        doc,
		Attrs:      attrsForView(req.Attributes),
		Page:       render.PageMeta{Name: firstNonEmpty(meta.Name, req.Name), Title: meta.Title, Description: meta.Description},
	}
	if req.PublishedAt != nil {
		v.PublishedAt = *req.PublishedAt
	}
	if req.UpdatedAt != nil {
		v.UpdatedAt = *req.UpdatedAt
	}
	site, forms, err := h.chrome(r.Context(), formSections(sections, doc))
	if err != nil {
		return nil, err
	}
	v.Site, v.Forms = site, forms
	v.PluginDirectory, v.ThemeDirectory = h.directoryFor(r.Context(), sections, doc)
	v.Related = noRelated
	return v, nil
}

// directoryFor is the directory views of a draft, nil for a kind it has none
// of or when the handler has no provider (tests).
func (h *renderAdminHandlers) directoryFor(ctx context.Context, sections []render.Section, doc *builder.Doc) (*render.PluginDirectoryView, *render.ThemeDirectoryView) {
	if h.directories == nil {
		return nil, nil
	}
	return h.directories(ctx, sections, doc)
}

// routeView builds the preview through the public renderer's own path: the
// route the public catch-all resolves for req.Path, loaded as an anonymous
// visitor, with req.Attributes substituted for the entity's stored attributes
// inside the loaders (see applyPreviewAttributes). It returns nil, meaning
// "use the attributes-only view", unless the path resolves to a page of
// exactly req.EntityID: a foreign, missing, redirected or archive route never
// contributes anything.
func (h *renderAdminHandlers) routeView(r *http.Request, req *previewRequest) *render.PageView {
	id, err := uuid.Parse(req.EntityID)
	if err != nil || req.Path == "" || h.resolve == nil || h.loadHome == nil || h.buildView == nil || len(req.Attributes) == 0 {
		return nil
	}
	path := safeLocalPath(req.Path)
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}
	pr := previewRouteRequest(r, path, id, req.Attributes)

	var rr *routeResult
	if path == "/" {
		rr, _, err = homeRoute(h.loadHome(pr))
	} else {
		rr, _, err = h.resolve(pr.Context(), path, pr)
	}
	warn := func(reason string, err error) *render.PageView {
		h.logger.Warn("render preview: route view unavailable, using attributes only",
			"reason", reason, "error", err, "path", path, "entity_id", req.EntityID)
		return nil
	}
	if err != nil {
		return warn("resolve or load error", err)
	}
	if rr == nil || rr.Redirect != nil || rr.NotFound || routeEntityID(rr) != id {
		return warn("path does not resolve to the entity", nil)
	}
	v, err := h.buildView(pr.Context(), pr, rr)
	if err != nil || v == nil {
		return warn("view build failed", err)
	}
	return v
}

// routeEntityID is the entity a resolved page route renders (uuid.Nil for
// archives and other entity-less routes).
func routeEntityID(rr *routeResult) uuid.UUID {
	if rr.Page != nil && rr.Page.Entity != nil {
		return rr.Page.Entity.ID
	}
	return uuid.Nil
}

// previewRouteRequest is the request the public loaders see for a preview: a
// plain GET of path by an anonymous visitor (no cookies, credentials, Accept
// or query), carrying the attribute override for entityID.
func previewRouteRequest(r *http.Request, path string, entityID uuid.UUID, attrs json.RawMessage) *http.Request {
	pr := r.Clone(withPreviewAttributes(r.Context(), entityID, attrs))
	pr.Method = http.MethodGet
	u := *r.URL
	u.Path, u.RawPath, u.RawQuery = path, "", ""
	pr.URL = &u
	for _, h := range []string{"Cookie", "Authorization", "Accept", "X-Api-Key"} {
		pr.Header.Del(h)
	}
	return pr
}
