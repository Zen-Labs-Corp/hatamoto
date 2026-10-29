package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/siteconfig"
)

// reservedLocaleSegments are first URL segments the site already routes, so a
// locale on one of them would be unreachable or would shadow a built-in route.
// Segments are lowercase words without dots, so file roots (robots.txt,
// favicon.ico, feed.xml) cannot collide; "sitemap" and "auth" also reserve
// their prefixes.
var reservedLocaleSegments = []string{
	"api", "media", "theme", "admin", "search", "category", "tag", "author", "feed", "health", "ready",
	"static", "mcp", "robots", "llms", "preview", "page", "forms", "p",
}

// localeSettingsRequest is the part of a site settings change the locale
// checks look at: the default locale plus the list, prefix-all and x-default.
type localeSettingsRequest struct {
	Default   string
	Entries   []siteconfig.LocaleEntry
	PrefixAll bool
	XDefault  string
}

// localeSettingsError is a rejected locale setting. Field names the JSON field
// at fault ("locales", "language", "x_default_locale").
type localeSettingsError struct {
	Field, Message string
}

func (e *localeSettingsError) Error() string { return e.Message }

// localeMessage is err without the generic validation prefix.
func localeMessage(err error) string {
	return strings.TrimPrefix(err.Error(), entity.ErrValidationFailed.Error()+": ")
}

// validateLocaleSettings checks the locale list the way the settings form and
// the REST API both need: the pure rules of entity.NewLocaleSet, then check
// (the checks that need the running site: reserved and shadowed segments, see
// Server.checkLocaleSegments; nil skips them). It returns the normalized
// entries and x-default to store.
func validateLocaleSettings(ctx context.Context, in localeSettingsRequest, check func(context.Context, []string) error) ([]siteconfig.LocaleEntry, string, error) {
	entries := make([]entity.LocaleEntry, len(in.Entries))
	for i, e := range in.Entries {
		entries[i] = entity.LocaleEntry{Tag: e.Tag, Segment: e.Segment}
	}
	set, err := entity.NewLocaleSet(in.Default, entries, in.PrefixAll, in.XDefault)
	if err != nil {
		field := "locales"
		if strings.Contains(err.Error(), "x-default") {
			field = "x_default_locale"
		}
		return nil, "", &localeSettingsError{Field: field, Message: localeMessage(err)}
	}
	out := make([]siteconfig.LocaleEntry, 0, len(set.Entries()))
	var segments []string
	for _, e := range set.Entries() {
		out = append(out, siteconfig.LocaleEntry{Tag: e.Tag, Segment: e.Segment})
		if e.Segment != "" {
			segments = append(segments, e.Segment)
		}
	}
	if check != nil && len(segments) > 0 {
		if err := check(ctx, segments); err != nil {
			return nil, "", &localeSettingsError{Field: "locales", Message: err.Error()}
		}
	}
	return out, set.XDefault(), nil
}

// checkLocaleSegments rejects segments the site cannot give to a locale: a
// reserved route, a section or archive prefix, a term kind, a published
// top-level slug, or a segment an active redirect rule already claims. It
// never deletes or changes the rule or page it finds.
func (s *Server) checkLocaleSegments(ctx context.Context, segments []string) error {
	snap := s.urlSnapshot(ctx)
	taken := map[string]string{}
	for p := range snap.SectionPrefixes() {
		taken[p] = "a section of your site"
	}
	for _, c := range snap.Configs() {
		if first, _, _ := strings.Cut(strings.Trim(c.ArchivePath, "/"), "/"); first != "" {
			taken[first] = "an archive of your site"
		}
	}
	if first, _, _ := strings.Cut(strings.Trim(snap.PostArchivePath(), "/"), "/"); first != "" {
		taken[first] = "an archive of your site"
	}
	for _, seg := range segments {
		if reason := reservedSegmentReason(seg); reason != "" {
			return fmt.Errorf("segment %q is %s", seg, reason)
		}
		if why, ok := taken[seg]; ok {
			return fmt.Errorf("segment %q is already %s", seg, why)
		}
		if s.db == nil {
			continue
		}
		var n int
		err := s.db.Pool().QueryRow(ctx, `
			SELECT (SELECT count(*) FROM hatamoto_entities WHERE slug = $1 AND parent_id IS NULL AND status = 'published' AND variant_key IS NULL)
			     + (SELECT count(*) FROM hatamoto_terms WHERE kind = $1)`, seg).Scan(&n)
		if err != nil {
			return fmt.Errorf("check segment %q: %w", seg, err)
		}
		if n > 0 {
			return fmt.Errorf("segment %q is already used by a published page or a term kind", seg)
		}
		var rule string
		err = s.db.Pool().QueryRow(ctx, `
			SELECT source_path FROM hatamoto_redirects
			WHERE is_active AND (source_path = $1 OR source_path LIKE $2) LIMIT 1`, "/"+seg, "/"+seg+"/%").Scan(&rule)
		if err == nil {
			return fmt.Errorf("segment %q is claimed by the redirect rule %s; change or remove the rule first", seg, rule)
		}
		if !isNoRows(err) {
			return fmt.Errorf("check segment %q: %w", seg, err)
		}
	}
	return nil
}

// reservedSegmentReason says why seg is a route of its own, or "".
func reservedSegmentReason(seg string) string {
	for _, r := range reservedLocaleSegments {
		if seg == r {
			return "a route of its own"
		}
	}
	if strings.HasPrefix(seg, "sitemap") || strings.HasPrefix(seg, "auth") {
		return "reserved for a route of its own"
	}
	return ""
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// applyLocales validates the locale fields of a PATCH against the stored
// settings (a change of the language alone must keep the default locale in the
// list) and copies the normalized values into update. It writes the 400 and
// returns false when they are rejected.
func (h *siteSettingsHandlers) applyLocales(w http.ResponseWriter, r *http.Request, in *siteSettingsPatch, update *siteconfig.UpdateInput) bool {
	cfg, err := h.store.Get(r.Context())
	if err != nil {
		h.logger.Error("site settings: load", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update site settings")
		return false
	}
	req := localeSettingsRequest{Default: siteLanguage(cfg), Entries: cfg.Locales, PrefixAll: cfg.PrefixAll, XDefault: cfg.XDefaultLocale}
	if update.Language != nil {
		req.Default = *update.Language
	}
	if in.Locales != nil {
		req.Entries = *in.Locales
	}
	if in.PrefixAll != nil {
		req.PrefixAll = *in.PrefixAll
	}
	if in.XDefaultLocale != nil {
		req.XDefault = *in.XDefaultLocale
	}
	if len(req.Entries) == 0 {
		// A single-locale site: the list is cleared and the flags mean nothing.
		if in.Locales != nil || in.PrefixAll != nil || in.XDefaultLocale != nil {
			empty, off, none := []siteconfig.LocaleEntry{}, false, ""
			update.Locales, update.PrefixAll, update.XDefaultLocale = &empty, &off, &none
		}
		return true
	}
	entries, xdef, err := validateLocaleSettings(r.Context(), req, h.checkLocales)
	if err != nil {
		var le *localeSettingsError
		if errors.As(err, &le) {
			writeError(w, http.StatusBadRequest, "invalid "+le.Field+": "+le.Message)
			return false
		}
		h.logger.Error("site settings: check locales", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update site settings")
		return false
	}
	update.Locales, update.PrefixAll, update.XDefaultLocale = &entries, &req.PrefixAll, &xdef
	return true
}
