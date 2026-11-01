package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/taxonomy"
)

// extraReservedLocaleSegments are first URL segments the site routes that the
// term-kind reservation (taxonomy.ReservedSegment) does not cover: the built-in
// kinds' own archives and the root plugin UI mount. Segments are lowercase
// words without dots, so file roots (robots.txt, favicon.ico, feed.xml) cannot
// collide; "sitemap" also reserves its prefix (sitemap-*.xml). "index" and "misc"
// would clash with the sitemap index and the misc file of the legacy naming.
var extraReservedLocaleSegments = []string{"category", "tag", "plugin-ui", "index", "misc"}

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

// LocaleField reports the field at fault, for the admin form to place the message.
func (e *localeSettingsError) LocaleField() string { return e.Field }

// localeCheckInfraError is a failure of the segment check itself (database
// error, cancelled request), as opposed to a segment the rules reject. The
// cause is logged and never shown: Error is a fixed, human message.
type localeCheckInfraError struct {
	segment string // "" when the failure is not tied to one segment
	err     error
}

func (e *localeCheckInfraError) Error() string {
	if e.segment == "" {
		return "could not verify the locale segments right now, try again"
	}
	return fmt.Sprintf("could not verify segment %q right now, try again", e.segment)
}

func (e *localeCheckInfraError) Unwrap() error { return e.err }

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
		entries[i] = entity.LocaleEntry{Tag: e.Tag, Segment: e.Segment, Name: e.Name, Hidden: e.Hidden}
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
		out = append(out, siteconfig.LocaleEntry{Tag: e.Tag, Segment: e.Segment, Name: e.Name, Hidden: e.Hidden})
		if e.Segment != "" {
			segments = append(segments, e.Segment)
		}
	}
	if check != nil && len(segments) > 0 {
		if err := check(ctx, segments); err != nil {
			var infra *localeCheckInfraError
			if errors.As(err, &infra) {
				slog.ErrorContext(ctx, "site settings: locale segment check failed", "segment", infra.segment, "error", infra.err)
			}
			return nil, "", &localeSettingsError{Field: "locales", Message: err.Error()}
		}
	}
	return out, set.XDefault(), nil
}

// checkLocaleSegments rejects segments the site cannot give to a locale: a
// reserved route, a section or archive prefix, a term kind, a published
// top-level slug, or a segment an active redirect rule already claims. It
// never deletes or changes the rule or page it finds.
//
// Only a segment that is not yet one of the site's stored locales is checked
// against the site's own content: a stored segment is already live, and a
// redirect or page added under it later must not block unrelated settings
// saves. A redirect only shadows a locale when it matches the locale root (exact or
// wildcard).
func (s *Server) checkLocaleSegments(ctx context.Context, segments []string) error {
	stored := map[string]bool{}
	if s.siteConfig != nil {
		cfg, err := s.siteConfig.Get(ctx)
		if err != nil {
			return &localeCheckInfraError{err: fmt.Errorf("load the stored locales: %w", err)}
		}
		for _, e := range cfg.Locales {
			stored[e.Segment] = true
		}
	}
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
		// A stored index or misc segment is live in group naming: only a new one is refused.
		if reason := reservedSegmentReason(seg); reason != "" && !(stored[seg] && (seg == "index" || seg == "misc")) {
			return fmt.Errorf("segment %q is %s", seg, reason)
		}
		if stored[seg] {
			continue
		}
		if why, ok := taken[seg]; ok {
			return fmt.Errorf("segment %q is already %s", seg, why)
		}
		if s.db == nil {
			continue
		}
		var id, name, slug string
		err := s.db.Pool().QueryRow(ctx, `
			SELECT id::text, name, slug FROM hatamoto_entities
			WHERE slug = $1 AND parent_id IS NULL AND status = 'published' AND variant_key IS NULL
			ORDER BY created_at LIMIT 1`, seg).Scan(&id, &name, &slug)
		switch {
		case err == nil:
			return fmt.Errorf("segment %q is already used by the published page \"%s\" (/%s, id %s). "+
				"Pick another segment, or move or unpublish that page first, or convert region pages to locales in Settings > General > Convert existing region pages",
				seg, name, slug, id)
		case !errors.Is(err, pgx.ErrNoRows):
			return &localeCheckInfraError{segment: seg, err: err}
		}
		var isKind bool
		if err := s.db.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_terms WHERE kind = $1)`, seg).Scan(&isKind); err != nil {
			return &localeCheckInfraError{segment: seg, err: err}
		}
		if isKind {
			return fmt.Errorf("segment %q is already used as a term kind", seg)
		}
		rule, err := s.redirectClaiming(ctx, seg)
		if err != nil {
			return &localeCheckInfraError{segment: seg, err: err}
		}
		if rule != "" {
			return fmt.Errorf("segment %q is claimed by the redirect rule %s; change or remove the rule first", seg, rule)
		}
	}
	return nil
}

// redirectClaiming is the source of an active redirect rule that matches the
// locale root "/seg" or "/seg/" (an exact rule or a wildcard such as "/seg/**"),
// or "". A rule that only matches deeper paths ("/seg/old") does not shadow the
// locale and is not reported.
func (s *Server) redirectClaiming(ctx context.Context, seg string) (string, error) {
	rows, err := s.db.Pool().Query(ctx, `
		SELECT source_path FROM hatamoto_redirects
		WHERE is_active AND (source_path IN ($1, $2) OR source_path LIKE '%*%')`, "/"+seg, "/"+seg+"/")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var src string
		if err := rows.Scan(&src); err != nil {
			return "", err
		}
		if src == "/"+seg || src == "/"+seg+"/" {
			return src, nil
		}
		if redirect.IsPatternSource(src) {
			for _, root := range []string{"/" + seg, "/" + seg + "/"} {
				if _, ok := redirect.MatchPattern(src, root); ok {
					return src, nil
				}
			}
		}
	}
	return "", rows.Err()
}

// reservedSegmentReason says why seg is a route of its own, or "".
func reservedSegmentReason(seg string) string {
	if taxonomy.ReservedSegment(seg) || slices.Contains(extraReservedLocaleSegments, seg) {
		return "a route of its own"
	}
	if strings.HasPrefix(seg, "sitemap") {
		return "reserved for a route of its own"
	}
	return ""
}

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
