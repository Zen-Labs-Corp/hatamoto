package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/siteconfig"
)

// siteSettingsStore is the slice of siteconfig.Store the admin settings
// handlers use.
type siteSettingsStore interface {
	Get(ctx context.Context) (*siteconfig.Config, error)
	Update(ctx context.Context, in siteconfig.UpdateInput) (*siteconfig.Config, error)
}

// siteSettingsHandlers serves the owner-only site settings admin API: site
// name, branding, homepage, notifications and theme tokens, all stored in the
// singleton hatamoto_site_config row the public renderer reads.
type siteSettingsHandlers struct {
	store      siteSettingsStore
	logger     *slog.Logger
	invalidate func(context.Context)
	// checkLocales validates the locale list against the running site (see
	// Server.checkLocaleSegments); nil runs the pure rules only.
	checkLocales func(context.Context, []string) error
}

// siteSettingsResponse is the wire shape of GET/PATCH /settings/site.
type siteSettingsResponse struct {
	Name              string `json:"name"`
	LogoURL           string `json:"logo_url"`
	PrimaryColor      string `json:"primary_color"`
	CustomDomain      string `json:"custom_domain"`
	NotificationEmail string `json:"notification_email"`
	Timezone          string `json:"timezone"`
	Language          string `json:"language"`
	// Locales is the ordered public locale list (empty: a single-locale site);
	// the default locale is Language. PrefixAll puts the default locale under
	// its own segment too, XDefaultLocale is the hreflang x-default ("" = the
	// first live copy).
	Locales           []siteconfig.LocaleEntry `json:"locales"`
	PrefixAll         bool                     `json:"prefix_all"`
	XDefaultLocale    string                   `json:"x_default_locale"`
	Phone             string                   `json:"phone"`
	BusinessHours     string                   `json:"business_hours"`
	Address           siteAddress              `json:"address"`
	LegacyWPRedirects bool                     `json:"legacy_wp_redirects"`
	HomepageEntityID  *uuid.UUID               `json:"homepage_entity_id,omitempty"`
	ThemeConfig       siteconfig.ThemeConfig   `json:"theme_config"`
	UpdatedAt         time.Time                `json:"updated_at"`
}

// siteSettingsPatch is the PATCH body; omitted (null) fields are unchanged.
type siteSettingsPatch struct {
	Name              *string                   `json:"name"`
	LogoURL           *string                   `json:"logo_url"`
	PrimaryColor      *string                   `json:"primary_color"`
	CustomDomain      *string                   `json:"custom_domain"`
	NotificationEmail *string                   `json:"notification_email"`
	Timezone          *string                   `json:"timezone"`
	Language          *string                   `json:"language"`
	Locales           *[]siteconfig.LocaleEntry `json:"locales"`
	PrefixAll         *bool                     `json:"prefix_all"`
	XDefaultLocale    *string                   `json:"x_default_locale"`
	Phone             *string                   `json:"phone"`
	BusinessHours     *string                   `json:"business_hours"`
	Address           *siteAddress              `json:"address"`
	LegacyWPRedirects *bool                     `json:"legacy_wp_redirects"`
	HomepageEntityID  *uuid.UUID                `json:"homepage_entity_id"`
}

// siteAddress is the business postal address (stored as JSON in
// hatamoto_site_config.postal_address, used by the Organization JSON-LD).
type siteAddress struct {
	Street     string `json:"street"`
	City       string `json:"city"`
	State      string `json:"state"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
}

const (
	maxContactFieldLen = 200
	maxPhoneLen        = 50
)

// valid reports whether every address field fits.
func (a siteAddress) valid() bool {
	for _, f := range []string{a.Street, a.City, a.State, a.PostalCode, a.Country} {
		if utf8.RuneCountInString(f) > maxContactFieldLen {
			return false
		}
	}
	return true
}

// canonicalLanguage validates a BCP 47 tag and returns it in canonical case
// ("en-au" -> "en-AU").
func canonicalLanguage(v string) (string, bool) {
	tag, err := entity.NormalizeLocale(v)
	return tag, err == nil
}

func mountSiteSettings(r *httpx.Group, h *siteSettingsHandlers) {
	{
		r := r.Group("/settings/site", middleware.RequireRole("owner"))
		r.HandleFunc("GET /", h.handleGet)
		r.HandleFunc("PATCH /", h.handlePatch)
		r.HandleFunc("GET /theme", h.handleGetTheme)
		r.HandleFunc("PUT /theme", h.handlePutTheme)
	}
}

func (s *Server) mountSiteSettingsRoutes(r *httpx.Group) {
	mountSiteSettings(r, &siteSettingsHandlers{
		store:      s.siteConfig,
		logger:     s.logger,
		invalidate: s.invalidateSiteSettings,

		checkLocales: s.checkLocaleSegments,
	})
}

// invalidateSiteSettings runs after site settings changed. The URL registry
// reads the time zone for {month} and {day}, so it is rebuilt first: pages
// rendered after the purge must not cache URLs in the old zone.
func (s *Server) invalidateSiteSettings(ctx context.Context) {
	s.entityService.URLRegistry().Invalidate()
	s.invalidateRendered(ctx)
}

func siteSettingsFrom(cfg *siteconfig.Config) siteSettingsResponse {
	resp := siteSettingsResponse{
		Name:              cfg.CompanyName,
		LogoURL:           cfg.LogoURL,
		PrimaryColor:      cfg.PrimaryColor,
		CustomDomain:      cfg.CustomDomain,
		NotificationEmail: cfg.NotificationEmail,
		Timezone:          cfg.Timezone,
		Language:          siteLanguage(cfg),
		Locales:           append([]siteconfig.LocaleEntry{}, cfg.Locales...),
		PrefixAll:         cfg.PrefixAll,
		XDefaultLocale:    cfg.XDefaultLocale,
		Phone:             cfg.Phone,
		BusinessHours:     cfg.BusinessHours,
		Address:           siteAddress(postalAddress(cfg.PostalAddress)),
		LegacyWPRedirects: cfg.LegacyWPRedirects,
		ThemeConfig:       effectiveTheme(cfg),
		UpdatedAt:         cfg.UpdatedAt,
	}
	if cfg.HomepageEntityID != uuid.Nil {
		id := cfg.HomepageEntityID
		resp.HomepageEntityID = &id
	}
	return resp
}

func (h *siteSettingsHandlers) handleGet(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.store.Get(r.Context())
	if err != nil {
		h.logger.Error("site settings: load", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load site settings")
		return
	}
	writeJSON(w, http.StatusOK, siteSettingsFrom(cfg))
}

func (h *siteSettingsHandlers) handlePatch(w http.ResponseWriter, r *http.Request) {
	var in siteSettingsPatch
	if err := parseJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if in.LogoURL != nil && !validSiteURL(*in.LogoURL) {
		writeError(w, http.StatusBadRequest, "invalid logo_url: must be an http(s), relative or protocol-relative URL")
		return
	}
	if in.PrimaryColor != nil && !validColor(*in.PrimaryColor) {
		writeError(w, http.StatusBadRequest, "invalid primary_color")
		return
	}
	update := siteconfig.UpdateInput{
		CompanyName:       in.Name,
		LogoURL:           in.LogoURL,
		PrimaryColor:      in.PrimaryColor,
		CustomDomain:      in.CustomDomain,
		NotificationEmail: in.NotificationEmail,
		Timezone:          in.Timezone,
		HomepageEntityID:  in.HomepageEntityID,
		Phone:             in.Phone,
		BusinessHours:     in.BusinessHours,
		LegacyWPRedirects: in.LegacyWPRedirects,
	}
	if in.Language != nil {
		lang, ok := canonicalLanguage(*in.Language)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid language: use a BCP 47 tag such as en or en-AU")
			return
		}
		update.Language = &lang
	}
	if in.Locales != nil || in.PrefixAll != nil || in.XDefaultLocale != nil || in.Language != nil {
		if !h.applyLocales(w, r, &in, &update) {
			return
		}
	}
	if in.Phone != nil && utf8.RuneCountInString(*in.Phone) > maxPhoneLen ||
		in.BusinessHours != nil && utf8.RuneCountInString(*in.BusinessHours) > maxContactFieldLen {
		writeError(w, http.StatusBadRequest, "phone or business_hours too long")
		return
	}
	if in.Address != nil {
		if !in.Address.valid() {
			writeError(w, http.StatusBadRequest, "address field too long")
			return
		}
		raw, err := json.Marshal(in.Address)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid address")
			return
		}
		update.PostalAddress = raw
	}
	cfg, err := h.store.Update(r.Context(), update)
	if err != nil {
		h.logger.Error("site settings: update", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update site settings")
		return
	}
	h.invalidate(r.Context())
	writeJSON(w, http.StatusOK, siteSettingsFrom(cfg))
}

func (h *siteSettingsHandlers) handleGetTheme(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.store.Get(r.Context())
	if err != nil {
		h.logger.Error("site settings: load theme", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load theme")
		return
	}
	writeJSON(w, http.StatusOK, effectiveTheme(cfg))
}

// handlePutTheme merges the sent top-level theme keys on the stored
// theme_extra object. Keys that are not sent (headerCta, socialLinks and any
// other extras) are preserved.
func (h *siteSettingsHandlers) handlePutTheme(w http.ResponseWriter, r *http.Request) {
	var sent map[string]json.RawMessage
	if err := parseJSON(r, &sent); err != nil || sent == nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateThemePayload(sent); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg, err := h.store.Get(r.Context())
	if err != nil {
		h.logger.Error("site settings: load theme", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update theme")
		return
	}
	merged := map[string]json.RawMessage{}
	if len(cfg.ThemeExtra) > 0 {
		_ = json.Unmarshal(cfg.ThemeExtra, &merged)
	}
	for k, v := range sent {
		merged[k] = v
	}
	// colors.primary lives only in the primary_color column (the Branding
	// field), never in the blob, so there is a single source of truth.
	primary := extractPrimary(merged)
	raw, err := json.Marshal(merged)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid theme config")
		return
	}
	updated, err := h.store.Update(r.Context(), siteconfig.UpdateInput{ThemeExtra: raw, PrimaryColor: primary})
	if err != nil {
		h.logger.Error("site settings: update theme", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update theme")
		return
	}
	h.invalidate(r.Context())
	writeJSON(w, http.StatusOK, effectiveTheme(updated))
}

// extractPrimary removes colors.primary from merged["colors"] and returns it
// for the primary_color column. A primary sent in this request replaces the
// stored colors object; a legacy primary left in the blob is migrated. Nil when
// there is none.
func extractPrimary(merged map[string]json.RawMessage) *string {
	raw, ok := merged["colors"]
	if !ok {
		return nil
	}
	var colors map[string]json.RawMessage
	if json.Unmarshal(raw, &colors) != nil || colors == nil {
		return nil
	}
	var primary *string
	if p, ok := colors["primary"]; ok {
		var v string
		if json.Unmarshal(p, &v) == nil && v != "" {
			primary = &v
		}
		delete(colors, "primary")
	}
	if b, err := json.Marshal(colors); err == nil {
		merged["colors"] = b
	}
	return primary
}
