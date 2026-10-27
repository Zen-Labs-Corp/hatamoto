package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	linkingv2 "hatamoto.dev/core/internal/linking/v2"
)

// entityLinkSyncAdapter satisfies entity.EntityLinkSync by delegating to the
// v2 package. Kept inside the server package so entity does not depend on
// v2 directly — preserves the one-way dependency rule.
type entityLinkSyncAdapter struct {
	db   *database.DB
	urls *entity.URLRegistry
	// changed runs after a rewrite touched other entities' content, which
	// is public HTML (nil in tests).
	changed func(ctx context.Context)
}

// Reindex refreshes the link index for sourceEntityID. Runs with
// superadmin context because the entity update hook fires for any admin
// regardless of role, and the index table itself has no RLS.
func (a *entityLinkSyncAdapter) Reindex(ctx context.Context, sourceEntityID uuid.UUID) error {
	return linkingv2.Reindex(database.WithSuperadmin(ctx), a.db, sourceEntityID)
}

// RewriteForEntity proxies to v2.RewriteForEntity with a superadmin context.
func (a *entityLinkSyncAdapter) RewriteForEntity(
	ctx context.Context,
	targetEntityID uuid.UUID,
	newCanonicalURL, reason string,
	actor *uuid.UUID,
) error {
	n, err := linkingv2.RewriteForEntityWithCount(database.WithSuperadmin(ctx), a.db, targetEntityID, newCanonicalURL, reason, actor)
	if n > 0 && a.changed != nil {
		a.changed(ctx)
	}
	return err
}

// ResolveCanonical builds the post-update canonical URL for ent. Schemas that
// are not linkable or unknown return ("", nil) so the rewriter is skipped —
// the entity update hook treats empty-URL as "no rewrite needed" rather than
// as an error.
func (a *entityLinkSyncAdapter) ResolveCanonical(ent *entity.Entity) (string, error) {
	if ent == nil || ent.Schema == nil {
		return "", nil
	}
	snap, serr := a.urls.Snapshot(context.Background())
	if serr != nil {
		return "", serr
	}
	url, err := linkingv2.ResolveWithAttrs(snap, ent.Schema.Slug, snap.VarsFor(ent, ancestorSlugs(context.Background(), snap, entity.NewPostgresPublicRepository(a.db), ent, ent.Schema.Slug)), entity.FlattenAttributes(ent.Attributes))
	if err != nil {
		// Schema-not-linkable / unknown-schema both mean
		// "nothing to rewrite" — swallow rather than propagating into the
		// caller's logs as a failure.
		if errors.Is(err, linkingv2.ErrSchemaNotLinkable) ||
			errors.Is(err, linkingv2.ErrUnknownSchema) {
			return "", nil
		}
		return "", err
	}
	return url, nil
}

// handleLinkingV2Resolve returns the canonical URL for an entity ID. The TipTap EntityLink extension calls this at insertion time
// so the href stored in attributes.content is already the final canonical —
// no middleware redirect on click. ADR §4.1 / §5.1.
//
// Query params:
//   - entity_id (required, UUID)
//
// Responds:
//   - 200 {"url": "/blog/seo"}
//   - 400 if entity_id is missing/malformed or schema is excluded
//   - 404 if entity does not exist
func (s *Server) handleLinkingV2Resolve(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("entity_id")
	if idStr == "" {
		writeError(w, http.StatusBadRequest, "entity_id is required")
		return
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid entity_id")
		return
	}

	ent, err := s.entityService.GetEntity(r.Context(), id)
	if err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			writeError(w, http.StatusNotFound, "entity not found")
			return
		}
		s.logger.Error("linking v2 resolve: get entity", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to resolve")
		return
	}
	if ent.Schema == nil {
		writeError(w, http.StatusInternalServerError, "entity schema missing")
		return
	}

	snap := s.urlSnapshot(r.Context())
	url, err := linkingv2.ResolveWithAttrs(snap, ent.Schema.Slug, snap.VarsFor(ent, ancestorSlugs(r.Context(), snap, entity.NewPostgresPublicRepository(s.db), ent, ent.Schema.Slug)), entity.FlattenAttributes(ent.Attributes))
	if err != nil {
		switch {
		case errors.Is(err, linkingv2.ErrSchemaNotLinkable):
			writeError(w, http.StatusBadRequest, "schema is not linkable")
		case errors.Is(err, linkingv2.ErrUnknownSchema):
			writeError(w, http.StatusBadRequest, "unknown schema")
		default:
			s.logger.Error("linking v2 resolve: resolver", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to resolve")
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// linkingV2InsertRequest is the body POST /linking/insert accepts. The server
// re-resolves the canonical URL from target_entity_id so the href
// stored in content is guaranteed to match whatever the resolver would emit
// today — admins can't accidentally pickle a stale URL by holding the picker
// open for hours.
type linkingV2InsertRequest struct {
	SourceEntityID uuid.UUID `json:"source_entity_id"`
	TargetEntityID uuid.UUID `json:"target_entity_id"`
	AnchorText     string    `json:"anchor_text"`
	// Content, when set, switches the endpoint to editor mode: the wrap is
	// applied to this HTML (the live TipTap document) and the result is
	// returned WITHOUT touching the database. The editor then applies
	// new_content locally and persists it on its normal save, so an open
	// editor can never overwrite a server-side insert with stale content.
	Content *string `json:"content,omitempty"`
}

// handleLinkingV2Insert is the server-side mutation path used by the
// suggestion panel ("Insert" button) and any other UX that wants to append a
// link without round-tripping through TipTap. ADR §4.2 / §5.1.
//
// Flow:
//  1. Resolve the canonical URL for the target — same logic the picker
//     used at selection time, re-run so a renamed slug picks up the new href.
//  2. Hand off to linkingv2.Insert which wraps the first matching anchor span
//     in the source's stored attributes.content (DOM-aware, skip-list aware)
//     and refreshes hatamoto_entity_link_index in the same transaction.
//  3. Surface the WrapAll skip taxonomy directly when nothing was inserted so
//     the panel can label the row (no_match / in_heading / already_linked).
func (s *Server) handleLinkingV2Insert(w http.ResponseWriter, r *http.Request) {
	var req linkingV2InsertRequest
	if err := parseJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SourceEntityID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "source_entity_id is required")
		return
	}
	if req.TargetEntityID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "target_entity_id is required")
		return
	}
	if req.AnchorText == "" {
		writeError(w, http.StatusBadRequest, "anchor_text is required")
		return
	}

	target, err := s.entityService.GetEntity(r.Context(), req.TargetEntityID)
	if err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			writeError(w, http.StatusNotFound, "target entity not found")
			return
		}
		s.logger.Error("linking v2 insert: get target entity", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load target")
		return
	}
	if target.Schema == nil {
		writeError(w, http.StatusInternalServerError, "target entity schema missing")
		return
	}

	snap := s.urlSnapshot(r.Context())
	canonical, err := linkingv2.ResolveWithAttrs(snap, target.Schema.Slug, snap.VarsFor(target, ancestorSlugs(r.Context(), snap, entity.NewPostgresPublicRepository(s.db), target, target.Schema.Slug)), entity.FlattenAttributes(target.Attributes))
	if err != nil {
		switch {
		case errors.Is(err, linkingv2.ErrSchemaNotLinkable):
			writeError(w, http.StatusBadRequest, "target schema is not linkable")
		case errors.Is(err, linkingv2.ErrUnknownSchema):
			writeError(w, http.StatusBadRequest, "unknown target schema")
		default:
			s.logger.Error("linking v2 insert: resolver", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to resolve target url")
		}
		return
	}

	if req.Content != nil {
		wrapped, err := linkingv2.WrapAll(*req.Content, []linkingv2.LinkSpec{{
			TargetEntityID:   req.TargetEntityID,
			TargetSlug:       target.Slug,
			TargetSchemaSlug: target.Schema.Slug,
			AnchorText:       req.AnchorText,
			CanonicalURL:     canonical,
		}})
		if err != nil {
			s.logger.Error("linking v2 insert (editor mode)", "error", err,
				"source", req.SourceEntityID, "target", req.TargetEntityID)
			writeError(w, http.StatusInternalServerError, "failed to insert link")
			return
		}
		resp := map[string]any{"inserted": wrapped.Inserted > 0, "url": canonical}
		if wrapped.Inserted > 0 {
			resp["new_content"] = wrapped.NewHTML
			resp["content_size"] = len(wrapped.NewHTML)
		} else {
			skip := linkingv2.SkipNoMatch
			for k := range wrapped.Skips {
				skip = k
				break
			}
			resp["skip_reason"] = skip
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	res, err := linkingv2.Insert(database.WithSuperadmin(r.Context()), s.db, linkingv2.InsertInput{
		SourceEntityID:   req.SourceEntityID,
		TargetEntityID:   req.TargetEntityID,
		TargetSlug:       target.Slug,
		TargetSchemaSlug: target.Schema.Slug,
		AnchorText:       req.AnchorText,
		CanonicalURL:     canonical,
	})
	if err != nil {
		if errors.Is(err, linkingv2.ErrSourceNotFound) {
			writeError(w, http.StatusNotFound, "source entity not found")
			return
		}
		s.logger.Error("linking v2 insert", "error", err,
			"source", req.SourceEntityID, "target", req.TargetEntityID)
		writeError(w, http.StatusInternalServerError, "failed to insert link")
		return
	}
	if res.Inserted {
		s.invalidateRendered(r.Context()) // the entity's content changed outside the entity service
	}

	resp := map[string]any{
		"inserted": res.Inserted,
		"url":      canonical,
	}
	if res.Inserted {
		resp["new_content"] = res.NewContent
		resp["content_size"] = res.ContentSize
	} else {
		resp["skip_reason"] = res.SkipReason
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleLinkingV2Suggest scans the source entity's content for opportunities
// to insert links from the rules catalog and returns them as a non-mutating
// list. The suggestion panel (ADR §4.2) calls this on mount + refresh.
//
// Path: POST /api/v1/linking/suggest/{entity_id}
// Body: optional { "content": "<html>" } — the editor's live document. When
// present, suggestions are computed against it instead of the stored row.
//
// Response: { suggestions: [...] } where each suggestion has status
// (insertable | skipped), the resolved URL, and the metadata the panel needs
// to render the row + drive the subsequent POST /linking/insert call.
func (s *Server) handleLinkingV2Suggest(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("entity_id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid entity_id")
		return
	}

	// limit=0 → scan every active rule. The panel needs the full count to
	// render an honest "N skipped" footer breakdown. Each per-rule dry-run
	// is microseconds; scanning 595 vs 50 doesn't change wall-clock.
	var body struct {
		Content *string `json:"content"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := parseJSON(r, &body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	suggestions, err := linkingv2.SuggestWithContent(database.WithSuperadmin(r.Context()), s.db, s.linkURLs(r.Context()), id, 0, body.Content)
	if err != nil {
		if errors.Is(err, linkingv2.ErrSourceNotFound) {
			writeError(w, http.StatusNotFound, "source entity not found")
			return
		}
		s.logger.Error("linking v2 suggest", "error", err, "source", id)
		writeError(w, http.StatusInternalServerError, "failed to compute suggestions")
		return
	}

	if suggestions == nil {
		suggestions = []linkingv2.Suggestion{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": suggestions})
}

// handleLinkingV2Equity returns the per-target aggregate report used by
// the equity dashboard at /admin/settings/linking/equity. ADR §4.5.
//
// One indexed scan over hatamoto_entity_link_index aggregated by target with
// a per-source-schema breakdown. Plus two
// trivial COUNT queries for the dashboard header. Total cost: ~5ms on
// the current prod data (890 link rows, 51 distinct targets).
func (s *Server) handleLinkingV2Equity(w http.ResponseWriter, r *http.Request) {
	report, err := linkingv2.ComputeEquity(database.WithSuperadmin(r.Context()), s.db)
	if err != nil {
		s.logger.Error("linking v2 equity", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to compute equity")
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// handleLinkingV2InboundLinks returns the drill-down list of inbound
// links for a single target — every source row in the link index that
// points at the target. Used when the dashboard expands a row.
func (s *Server) handleLinkingV2InboundLinks(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid entity_id")
		return
	}
	rows, err := linkingv2.ListInboundLinks(database.WithSuperadmin(r.Context()), s.db, id)
	if err != nil {
		s.logger.Error("linking v2 inbound list", "error", err, "target", id)
		writeError(w, http.StatusInternalServerError, "failed to list inbound links")
		return
	}
	if rows == nil {
		rows = []linkingv2.EquityDetailRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"inbound": rows})
}

// --- Alias rule handlers (hatamoto_internal_link_rules) ---

// handleListLinkRules returns linking rules with optional pagination, search, and sorting.
func (s *Server) handleListLinkRules(w http.ResponseWriter, r *http.Request) {
	filter := &linkingv2.RuleFilter{}

	if ruleType := r.URL.Query().Get("rule_type"); ruleType != "" {
		filter.RuleType = linkingv2.RuleType(ruleType)
	}
	if isActive := r.URL.Query().Get("is_active"); isActive != "" {
		active := isActive == "true"
		filter.IsActive = &active
	}
	if q := r.URL.Query().Get("q"); q != "" {
		filter.Query = q
	}
	if targetID := r.URL.Query().Get("target_entity_id"); targetID != "" {
		id, err := uuid.Parse(targetID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid target_entity_id")
			return
		}
		filter.TargetEntityID = &id
	}
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			filter.Page = p
		}
	}
	if ppStr := r.URL.Query().Get("per_page"); ppStr != "" {
		if pp, err := strconv.Atoi(ppStr); err == nil && pp > 0 {
			filter.PerPage = pp
		}
	}
	if sort := r.URL.Query().Get("sort"); sort != "" {
		filter.Sort = sort
	}
	if order := r.URL.Query().Get("order"); order != "" {
		filter.Order = order
	}
	if source := r.URL.Query().Get("source"); source != "" {
		filter.Source = source
	}

	result, err := linkingv2.ListRulesPaginated(r.Context(), s.db, filter)
	if err != nil {
		s.logger.Error("failed to list link rules", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list rules")
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// handleCreateLinkRule creates a new linking rule.
func (s *Server) handleCreateLinkRule(w http.ResponseWriter, r *http.Request) {

	var input linkingv2.CreateRuleInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	rule, err := linkingv2.CreateRule(r.Context(), s.db, &input)
	if err != nil {
		if errors.Is(err, linkingv2.ErrDuplicateRule) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if strings.Contains(err.Error(), "is required") || strings.Contains(err.Error(), "invalid") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.logger.Error("failed to create link rule", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create rule")
		return
	}

	writeJSON(w, http.StatusCreated, rule)
}

// handleGetLinkRule retrieves a single linking rule.
func (s *Server) handleGetLinkRule(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid rule ID")
		return
	}

	rule, err := linkingv2.GetRule(r.Context(), s.db, id)
	if err != nil {
		if errors.Is(err, linkingv2.ErrRuleNotFound) {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}
		s.logger.Error("failed to get link rule", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get rule")
		return
	}

	writeJSON(w, http.StatusOK, rule)
}

// handleUpdateLinkRule updates an existing linking rule.
func (s *Server) handleUpdateLinkRule(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid rule ID")
		return
	}

	var input linkingv2.UpdateRuleInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	rule, err := linkingv2.UpdateRule(r.Context(), s.db, id, &input)
	if err != nil {
		if errors.Is(err, linkingv2.ErrRuleNotFound) {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}
		if errors.Is(err, linkingv2.ErrDuplicateRule) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if strings.Contains(err.Error(), "is required") || strings.Contains(err.Error(), "invalid") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.logger.Error("failed to update link rule", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update rule")
		return
	}

	writeJSON(w, http.StatusOK, rule)
}

// handlePreviewLinkRule dry-runs a rule phrase against every published
// entity and reports which pages mention it and what a link insert would do
// on each (insertable / already linked / heading-only). Read-only.
//
// Path: POST /api/v1/linking/rules/preview
// Body: { "match_value": "...", "anchor_text": "..."?, "target_entity_id": "..." }
// The anchor text, when set, is the phrase the suggester actually wraps, so
// it takes precedence over match_value — mirroring Suggest.
func (s *Server) handlePreviewLinkRule(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MatchValue     string    `json:"match_value"`
		AnchorText     string    `json:"anchor_text"`
		TargetEntityID uuid.UUID `json:"target_entity_id"`
	}
	if err := parseJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	phrase := strings.TrimSpace(body.AnchorText)
	if phrase == "" {
		phrase = strings.TrimSpace(body.MatchValue)
	}
	if phrase == "" || body.TargetEntityID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "match_value and target_entity_id are required")
		return
	}

	res, err := linkingv2.PreviewRule(database.WithSuperadmin(r.Context()), s.db, phrase, body.TargetEntityID)
	if err != nil {
		s.logger.Error("failed to preview link rule", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to preview rule")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleDeleteLinkRule removes a linking rule.
func (s *Server) handleDeleteLinkRule(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid rule ID")
		return
	}

	if err := linkingv2.DeleteRule(r.Context(), s.db, id); err != nil {
		if errors.Is(err, linkingv2.ErrRuleNotFound) {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}
		s.logger.Error("failed to delete link rule", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete rule")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "rule deleted"})
}

// handleDeleteAllRules wipes every alias rule (clean slate). The v1 links
// table is gone post-migration 093 so there's nothing else to delete here.
func (s *Server) handleDeleteAllRules(w http.ResponseWriter, r *http.Request) {

	rulesDeleted, err := linkingv2.DeleteAllRules(r.Context(), s.db)
	if err != nil {
		s.logger.Error("failed to delete all rules", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete rules")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"rules_deleted": rulesDeleted,
	})
}

// handleBulkDeleteRules removes multiple rules by IDs.
func (s *Server) handleBulkDeleteRules(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := parseJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids array is required")
		return
	}

	ids := make([]uuid.UUID, 0, len(body.IDs))
	for _, idStr := range body.IDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid rule ID: "+idStr)
			return
		}
		ids = append(ids, id)
	}

	deleted, err := linkingv2.DeleteRulesByIDs(r.Context(), s.db, ids)
	if err != nil {
		s.logger.Error("failed to bulk delete rules", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete rules")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"deleted": deleted,
	})
}

// handleDeleteRulesBySource wipes every rule with the given `source` column
// value. Used by the admin "Delete by source" toolbar action — primarily
// for clearing legacy v1-generated cohorts (auto_partial, auto_sibling,
// auto_tag) in one click. Body: { "source": "auto_partial" }.
func (s *Server) handleDeleteRulesBySource(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Source string `json:"source"`
	}
	if err := parseJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Source == "" {
		writeError(w, http.StatusBadRequest, "source is required")
		return
	}

	deleted, err := linkingv2.DeleteRulesBySource(r.Context(), s.db, body.Source)
	if err != nil {
		s.logger.Error("failed to delete rules by source", "error", err, "source", body.Source)
		writeError(w, http.StatusInternalServerError, "failed to delete rules")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"deleted": deleted,
		"source":  body.Source,
	})
}
