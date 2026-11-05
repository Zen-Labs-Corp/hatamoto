package server

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

// shareTokenLen is the length of a preview token: 32 bytes, base64url, no
// padding. Anything else cannot be one of ours, so it is refused before any
// database read.
const shareTokenLen = 43

// previewPathPrefix is where shared draft previews are served. It is
// reserved in adminpath.ReservedPrefixes.
const previewPathPrefix = "/preview/"

// handlePreviewShare serves the last saved draft of an entity to whoever holds
// a valid preview token. It is anonymous by design (the token is the
// credential), so every answer is uncacheable and carries no referrer, and
// every failure is the same plain 404: an expired, revoked, unknown or
// tampered token and a deleted entity look alike.
//
// The page is the one a changeset preview renders, through the public route
// loaders with a one-entity staged set, so the draft looks as it will live.
// It never enters the page cache (the route sits outside the cache path), the
// sitemap, the feeds or llms.txt (they list published rows only).
func (s *Server) handlePreviewShare(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex")

	token := r.PathValue("token")
	if s.previewLinks == nil || s.renderEngine == nil || len(token) != shareTokenLen {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	id, ok := s.previewLinks.Resolve(ctx, token)
	if !ok {
		http.NotFound(w, r)
		return
	}
	e, err := s.entityRepo.GetByID(ctx, id)
	if err != nil || e.Schema == nil {
		http.NotFound(w, r)
		return
	}
	path := locateEntity(ctx, s.urlSnapshot(ctx), entity.NewPostgresPublicRepository(s.db), e).Path
	if e.Status == entity.StatusPublished {
		// Publishing ends the draft: the link now leads to the public page,
		// never to anything else.
		http.Redirect(w, r, path, http.StatusFound)
		return
	}

	se := &stagedEntity{Ent: e, Path: path}
	o := &stagedChangeset{
		ents:   map[uuid.UUID]*stagedEntity{e.ID: se},
		byPath: map[string]*stagedEntity{path: se},
	}
	body, err := s.renderStagedWith(withStaged(withHiddenLocalesServed(ctx), o), o, se, stagedRenderOpts{draftBanner: true})
	if err != nil {
		s.logger.Warn("draft preview: not shown", slog.String("entity_id", id.String()), slog.String("error", err.Error()))
		http.NotFound(w, r)
		return
	}
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", cspWithExtras(renderCSPStrict, s.cspExtras(ctx)))
	h.Add("Content-Security-Policy", previewShareCSP) // a second header: every CSP header is enforced
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// previewShareCSP stops every script of a draft from running, whatever the
// first policy allows, and forbids forms and popups. allow-same-origin only
// lets the page load its own stylesheets, images and fonts.
const previewShareCSP = "sandbox allow-same-origin; script-src 'none'"

// redactPreviewToken hides the token of a preview path in a logged path.
func redactPreviewToken(p string) string {
	if strings.HasPrefix(p, previewPathPrefix) {
		return previewPathPrefix + "<token>"
	}
	return p
}
