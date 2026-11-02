// Package siteconfig reads the singleton site configuration row
// (hatamoto_site_config).
//
// The table has exactly one row (BOOLEAN primary key with CHECK (id = TRUE)).
// Reads are extremely hot (every public page render), so we cache the value
// in-process with a 60-second TTL — same pattern as redirect.Service.
package siteconfig

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
)

// cacheTTL is the read-cache lifetime. Site config changes rarely; 60s is
// the same TTL the redirect service uses, keeping observability uniform.
const cacheTTL = 60 * time.Second

// LocaleEntry is one configured locale: a BCP 47 tag, its URL segment, an
// optional display name and a Hidden flag. Hidden is the inverse of "public" so
// a list stored before the flag existed decodes as all public.
type LocaleEntry struct {
	Tag     string `json:"tag"`
	Segment string `json:"segment"`
	Name    string `json:"name,omitempty"`   // shown to visitors, at most 80 runes
	Hidden  bool   `json:"hidden,omitempty"` // not public: prefix 404s, left out of sitemaps and hreflang
}

// SitemapFileSegment returns the first locale segment that is also the name of
// a sitemap file of the legacy naming ("index" or "misc"), or "". Such a
// segment can stay in group naming but not in legacy naming, where its locale
// file would share a name with the sitemap index or the misc file.
func SitemapFileSegment(entries []LocaleEntry) string {
	for _, e := range entries {
		if e.Segment == "index" || e.Segment == "misc" {
			return e.Segment
		}
	}
	return ""
}

// Config is the in-memory shape of the singleton row.
type Config struct {
	CompanyName       string
	LogoURL           string
	CustomDomain      string
	PrimaryColor      string
	ThemeExtra        json.RawMessage // raw JSONB, callers parse if they need it
	Tagline           string
	Timezone          string
	HomepageEntityID  uuid.UUID // uuid.Nil if unset
	NotificationEmail string
	// Language is the BCP 47 site language ("en" by default): <html lang>,
	// JSON-LD inLanguage and feed language.
	Language string
	// Locales is the full list of public locales in display order, the
	// default included (nil = a single-locale site). The default locale is
	// Language.
	Locales []LocaleEntry
	// PrefixAll puts the default locale under its own URL segment too.
	PrefixAll bool
	// XDefaultLocale is the locale tag used for hreflang x-default ("" = unset).
	XDefaultLocale string
	// SitemapNaming is "group" (default, one sitemap file per locale and
	// group) or "legacy" (one file per locale); "" is read as group.
	SitemapNaming string
	// Phone and BusinessHours are the public contact details, free text.
	Phone         string
	BusinessHours string
	// PostalAddress is the business address, JSONB {street, city, state,
	// postal_code, country}. Nil-or-empty means "unset".
	PostalAddress json.RawMessage
	// BusinessProfile is the site-wide business details (JSONB), holding only
	// the fields that have no column of their own. Never nil after a load.
	BusinessProfile json.RawMessage
	// LocaleProfiles is the per-locale business details (JSONB object keyed by
	// BCP 47 tag). Never nil after a load.
	LocaleProfiles json.RawMessage
	// LegacyWPRedirects turns on the built-in WordPress URL redirects
	// (dated permalinks, feeds, taxonomy archives, wp-* paths). Off by default.
	LegacyWPRedirects bool
	// PublicBaseURL is the site's public origin set in the admin ("" = derive
	// from the request); the PUBLIC_BASE_URL env var overrides it.
	PublicBaseURL string
	// SetupCompletedAt is when the first-run wizard finished (nil = still open).
	SetupCompletedAt *time.Time
	// MenusSeededAt is when the one-time menu seeding finished (nil = not yet).
	// Set by MarkMenusSeeded only, never by Update.
	MenusSeededAt *time.Time
	UpdatedAt     time.Time

	// Page cache controls (Settings > Performance). Zero values are
	// sentinels: TTL 0 = server default, MaxMB 0 = no limit, preload interval
	// 0 = same as the cache lifetime.
	PageCacheEnabled           bool
	PageCacheTTLSeconds        int
	PageCacheMaxMB             int
	PagePreloadEnabled         bool
	PagePreloadIntervalSeconds int
	// PagePreloadMode is PreloadModeMissing or PreloadModeAll.
	PagePreloadMode string
	// PagePreloadAfterChanges schedules a preload shortly after a content
	// change empties the cache.
	PagePreloadAfterChanges bool
}

// Preload modes: skip pages that are already cached, or rebuild everything.
const (
	PreloadModeMissing = "missing"
	PreloadModeAll     = "all"
)

// Page cache lifetime bounds in seconds (0 = server default).
const (
	MinPageCacheTTLSeconds = 30
	MaxPageCacheTTLSeconds = 30 * 24 * 3600
	// legacyMaxTTLSeconds is the old column's CHECK ceiling; the previous
	// binary must keep reading a valid value there.
	legacyMaxTTLSeconds = 86400
)

// ErrInvalidSitemapNaming is returned by Update for a sitemap naming other
// than group or legacy (the column has no CHECK, an expand-only migration).
var ErrInvalidSitemapNaming = errors.New("invalid sitemap naming")

// ErrInvalidPageCache is returned by Update for out-of-range page cache values.
var ErrInvalidPageCache = errors.New("invalid page cache setting")

// ErrNotConfigured is returned when the singleton row has not been seeded.
// Code paths that hit this should fall back to safe defaults (the
// pre-Path-B WhiteLabelConfig was JSONB-nullable too — same blast radius).
var ErrNotConfigured = errors.New("site config row missing — run migration 075")

// Store reads (and eventually writes) hatamoto_site_config. Holds a small TTL'd
// cache to absorb high-frequency reads from public page renders.
type Store struct {
	db     *database.DB
	logger *slog.Logger

	mu       sync.RWMutex
	cached   *Config
	cachedAt time.Time
}

// NewStore constructs a site-config store backed by the given database.
func NewStore(db *database.DB, logger *slog.Logger) *Store {
	return &Store{db: db, logger: logger}
}

// Get returns the singleton site config, refreshing from the DB if the
// cached copy is older than cacheTTL. Runs under superadmin context — the
// table has no RLS post-Migration-A but the GUC set is harmless.
//
// On DB error, returns the stale cached copy if available; otherwise the
// error. Callers should treat "stale" as acceptable since the singleton
// changes manually via admin UI (PR 2b will add the write path), never
// via automated traffic.
func (s *Store) Get(ctx context.Context) (*Config, error) {
	s.mu.RLock()
	if s.cached != nil && time.Since(s.cachedAt) < cacheTTL {
		c := s.cached
		s.mu.RUnlock()
		return c, nil
	}
	s.mu.RUnlock()

	cfg, err := s.load(ctx)
	if err != nil {
		// Stale-on-error: surface the failure but keep serving.
		s.mu.RLock()
		stale := s.cached
		s.mu.RUnlock()
		if stale != nil {
			s.logger.Warn("site config refresh failed, serving stale", slog.String("error", err.Error()))
			return stale, nil
		}
		return nil, err
	}

	s.mu.Lock()
	s.cached = cfg
	s.cachedAt = time.Now()
	s.mu.Unlock()
	return cfg, nil
}

// Invalidate forces the next Get to hit the DB. Call from any write path
// that mutates hatamoto_site_config so admin UI changes are immediately visible.
func (s *Store) Invalidate() {
	s.mu.Lock()
	s.cached = nil
	s.cachedAt = time.Time{}
	s.mu.Unlock()
}

// UpdateInput is the patch shape for Update. Any pointer-nil field is
// left unchanged. theme_extra is JSONB — pass the raw bytes you want
// stored (validate upstream).
type UpdateInput struct {
	CompanyName       *string
	LogoURL           *string
	CustomDomain      *string
	PrimaryColor      *string
	ThemeExtra        json.RawMessage
	Tagline           *string
	Timezone          *string
	HomepageEntityID  *uuid.UUID
	NotificationEmail *string
	PostalAddress     json.RawMessage
	// BusinessProfile replaces the site-wide profile when non-empty; pass
	// {} to clear it. Empty leaves it unchanged.
	BusinessProfile json.RawMessage
	Language        *string
	// Locales replaces the whole list when non-nil (an empty slice clears it).
	Locales           *[]LocaleEntry
	PrefixAll         *bool
	XDefaultLocale    *string
	SitemapNaming     *string // nil = unchanged
	Phone             *string
	BusinessHours     *string
	LegacyWPRedirects *bool
	PublicBaseURL     *string
	// Page cache controls; see Config for the zero-value sentinels.
	PageCacheEnabled           *bool
	PageCacheTTLSeconds        *int
	PageCacheMaxMB             *int
	PagePreloadEnabled         *bool
	PagePreloadIntervalSeconds *int
	PagePreloadMode            *string
	PagePreloadAfterChanges    *bool
	// SetupCompleted true stamps setup_completed_at once (never cleared).
	SetupCompleted bool
}

// validatePageCache checks the page cache fields that carry no DB constraint.
func (in UpdateInput) validatePageCache() error {
	if in.PageCacheTTLSeconds != nil {
		if v := *in.PageCacheTTLSeconds; v != 0 && (v < MinPageCacheTTLSeconds || v > MaxPageCacheTTLSeconds) {
			return fmt.Errorf("%w: lifetime %d s outside %d..%d", ErrInvalidPageCache, v, MinPageCacheTTLSeconds, MaxPageCacheTTLSeconds)
		}
	}
	if in.PagePreloadMode != nil && *in.PagePreloadMode != PreloadModeMissing && *in.PagePreloadMode != PreloadModeAll {
		return fmt.Errorf("%w: preload mode %q", ErrInvalidPageCache, *in.PagePreloadMode)
	}
	return nil
}

// Update applies a patch to the singleton row and invalidates the cache.
// Concurrency-safe via single UPDATE statement + COALESCE for nil fields.
// Returns the refreshed Config on success.
func (s *Store) Update(ctx context.Context, in UpdateInput) (*Config, error) {
	if err := in.validatePageCache(); err != nil {
		return nil, err
	}
	conn, err := s.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return nil, fmt.Errorf("acquire db conn: %w", err)
	}
	defer conn.Release()

	// Convert pointers to sql.Null* for COALESCE semantics in the UPDATE.
	var (
		companyName       any = nil
		logoURL           any = nil
		customDomain      any = nil
		primaryColor      any = nil
		tagline           any = nil
		timezone          any = nil
		homepageEntityID  any = nil
		notificationEmail any = nil
		themeExtra        any = nil
		postalAddress     any = nil
		language          any = nil
		phone             any = nil
		businessHours     any = nil
		legacyWP          any = nil
		locales           any = nil
		prefixAll         any = nil
		xDefaultLocale    any = nil
		businessProfile   any = nil
	)
	if in.CompanyName != nil {
		companyName = *in.CompanyName
	}
	if in.LogoURL != nil {
		logoURL = *in.LogoURL
	}
	if in.CustomDomain != nil {
		customDomain = *in.CustomDomain
	}
	if in.PrimaryColor != nil {
		primaryColor = *in.PrimaryColor
	}
	if in.Tagline != nil {
		tagline = *in.Tagline
	}
	if in.Timezone != nil {
		timezone = *in.Timezone
	}
	if in.HomepageEntityID != nil {
		homepageEntityID = *in.HomepageEntityID
	}
	if in.NotificationEmail != nil {
		notificationEmail = *in.NotificationEmail
	}
	if len(in.ThemeExtra) > 0 {
		themeExtra = []byte(in.ThemeExtra)
	}
	if len(in.PostalAddress) > 0 {
		postalAddress = []byte(in.PostalAddress)
	}
	if len(in.BusinessProfile) > 0 {
		businessProfile = []byte(in.BusinessProfile)
	}
	if in.Language != nil {
		language = *in.Language
	}
	if in.Phone != nil {
		phone = *in.Phone
	}
	if in.BusinessHours != nil {
		businessHours = *in.BusinessHours
	}
	if in.LegacyWPRedirects != nil {
		legacyWP = *in.LegacyWPRedirects
	}
	if in.Locales != nil {
		list := *in.Locales
		if list == nil {
			list = []LocaleEntry{}
		}
		raw, err := json.Marshal(list)
		if err != nil {
			return nil, fmt.Errorf("encode locales: %w", err)
		}
		locales = raw
	}
	if in.PrefixAll != nil {
		prefixAll = *in.PrefixAll
	}
	if in.XDefaultLocale != nil {
		xDefaultLocale = *in.XDefaultLocale
	}
	var sitemapNaming any
	if in.SitemapNaming != nil {
		if *in.SitemapNaming != "group" && *in.SitemapNaming != "legacy" {
			return nil, fmt.Errorf("%w: sitemap naming %q", ErrInvalidSitemapNaming, *in.SitemapNaming)
		}
		sitemapNaming = *in.SitemapNaming
	}
	var publicBaseURL any
	if in.PublicBaseURL != nil {
		publicBaseURL = *in.PublicBaseURL
	}
	var cacheEnabled, cacheTTL, cacheTTLLegacy, preloadMode, preloadAfter, cacheMaxMB, preloadEnabled, preloadInterval any
	if in.PageCacheEnabled != nil {
		cacheEnabled = *in.PageCacheEnabled
	}
	if in.PageCacheTTLSeconds != nil {
		cacheTTL = *in.PageCacheTTLSeconds
		cacheTTLLegacy = min(*in.PageCacheTTLSeconds, legacyMaxTTLSeconds)
	}
	if in.PagePreloadMode != nil {
		preloadMode = *in.PagePreloadMode
	}
	if in.PagePreloadAfterChanges != nil {
		preloadAfter = *in.PagePreloadAfterChanges
	}
	if in.PageCacheMaxMB != nil {
		cacheMaxMB = *in.PageCacheMaxMB
	}
	if in.PagePreloadEnabled != nil {
		preloadEnabled = *in.PagePreloadEnabled
	}
	if in.PagePreloadIntervalSeconds != nil {
		preloadInterval = *in.PagePreloadIntervalSeconds
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin site config update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var oldLanguage string
	if in.Language != nil {
		// The row lock keeps a concurrent language change from folding twice.
		if err := tx.QueryRow(ctx, `SELECT language FROM hatamoto_site_config WHERE id = TRUE FOR UPDATE`).Scan(&oldLanguage); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("read site language: %w", err)
		}
	}

	_, err = tx.Exec(ctx, `
		UPDATE hatamoto_site_config SET
		  company_name       = COALESCE($1, company_name),
		  logo_url           = COALESCE($2, logo_url),
		  custom_domain      = COALESCE($3, custom_domain),
		  primary_color      = COALESCE($4, primary_color),
		  theme_extra        = COALESCE($5, theme_extra),
		  tagline            = COALESCE($6, tagline),
		  timezone           = COALESCE($7, timezone),
		  homepage_entity_id = COALESCE($8, homepage_entity_id),
		  notification_email = COALESCE($9, notification_email),
		  postal_address     = COALESCE($10, postal_address),
		  language           = COALESCE($11, language),
		  phone              = COALESCE($12, phone),
		  business_hours     = COALESCE($13, business_hours),
		  legacy_wp_redirects = COALESCE($14, legacy_wp_redirects),
		  public_base_url    = COALESCE($15, public_base_url),
		  setup_completed_at = CASE WHEN $16 THEN COALESCE(setup_completed_at, NOW()) ELSE setup_completed_at END,
		  page_cache_enabled = COALESCE($17, page_cache_enabled),
		  page_cache_ttl_seconds = COALESCE($22, page_cache_ttl_seconds),
		  page_cache_ttl_secs = COALESCE($18, page_cache_ttl_secs),
		  page_preload_mode = COALESCE($23, page_preload_mode),
		  page_preload_after_changes = COALESCE($24, page_preload_after_changes),
		  page_cache_max_mb = COALESCE($19, page_cache_max_mb),
		  page_preload_enabled = COALESCE($20, page_preload_enabled),
		  page_preload_interval_seconds = COALESCE($21, page_preload_interval_seconds),
		  locales            = COALESCE($25, locales),
		  prefix_all         = COALESCE($26, prefix_all),
		  x_default_locale   = COALESCE($27, x_default_locale),
		  business_profile   = COALESCE($28, business_profile),
		  sitemap_naming     = COALESCE($29, sitemap_naming),
		  updated_at         = NOW()
		WHERE id = TRUE
	`, companyName, logoURL, customDomain, primaryColor, themeExtra,
		tagline, timezone, homepageEntityID, notificationEmail, postalAddress,
		language, phone, businessHours, legacyWP, publicBaseURL, in.SetupCompleted,
		cacheEnabled, cacheTTL, cacheMaxMB, preloadEnabled, preloadInterval,
		cacheTTLLegacy, preloadMode, preloadAfter, locales, prefixAll, xDefaultLocale, businessProfile, sitemapNaming)
	if err != nil {
		return nil, fmt.Errorf("update site config: %w", err)
	}
	if in.Language != nil {
		if err := foldDefaultBusiness(ctx, tx, oldLanguage, *in.Language); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit site config update: %w", err)
	}

	// Invalidate cache before re-load so callers don't see stale data.
	s.Invalidate()
	return s.Get(ctx)
}

// foldDefaultBusiness runs when the default locale changes. The old default's
// business becomes that locale's card (bizprofile.SwapDefault), so its pages keep
// their LocalBusiness, and the card the new default locale had is folded into
// the default business (bizprofile.Fold): the column keeps meaning "the default
// locale's business". The swap only happens on a site with several locales that
// still lists the old default; on a single-language site there is no page left
// for a card. It changes nothing when the language is the same, or when there
// is neither a swap nor a card under the new default. It runs in the caller's
// transaction, after the UPDATE, so a patch that also sends business_profile
// folds on top of what it stored.
func foldDefaultBusiness(ctx context.Context, tx pgx.Tx, oldLanguage, newLanguage string) error {
	oldTag, errOld := entity.NormalizeLocale(oldLanguage)
	newTag, errNew := entity.NormalizeLocale(newLanguage)
	if errNew != nil || (errOld == nil && oldTag == newTag) {
		return nil
	}
	var (
		profile, cards, address, locales []byte
		phone, hours                     sql.NullString
	)
	err := tx.QueryRow(ctx, `
		SELECT business_profile, locale_profiles, phone, business_hours, postal_address, locales
		FROM hatamoto_site_config WHERE id = TRUE`).Scan(&profile, &cards, &phone, &hours, &address, &locales)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read business profile: %w", err)
	}
	cards, err = normaliseCardKeys(cards)
	if err != nil {
		return fmt.Errorf("fold default business: %w", err)
	}
	out, changed, err := bizprofile.FoldRow(bizprofile.Row{
		Default: newTag, BusinessProfile: profile, LocaleProfiles: cards,
		Phone: phone.String, BusinessHours: hours.String, PostalAddress: address,
		Previous: swappedDefault(oldTag, errOld, locales),
	})
	if err != nil {
		return fmt.Errorf("fold default business: %w", err)
	}
	if !changed {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE hatamoto_site_config SET business_profile = $1, locale_profiles = $2,
		  phone = $3, business_hours = $4, postal_address = $5, updated_at = NOW()
		WHERE id = TRUE`, out.BusinessProfile, out.LocaleProfiles, out.Phone, out.BusinessHours, []byte(out.PostalAddress)); err != nil {
		return fmt.Errorf("write folded business profile: %w", err)
	}
	return nil
}

// swappedDefault is the old default tag when the old default business must be
// kept as a card: the site has several locales and one of them is the old
// default. Otherwise it is "".
func swappedDefault(oldTag string, errOld error, rawLocales []byte) string {
	var list []LocaleEntry
	if errOld != nil || len(rawLocales) == 0 || json.Unmarshal(rawLocales, &list) != nil || len(list) < 2 {
		return ""
	}
	for _, e := range list {
		if tag, err := entity.NormalizeLocale(e.Tag); err == nil && tag == oldTag {
			return oldTag
		}
	}
	return ""
}

// normaliseCardKeys re-keys the stored cards with entity.NormalizeLocale (older
// rows can hold un-normalised tags such as en-UK). A key that is not a valid tag
// stays as it is.
func normaliseCardKeys(raw []byte) ([]byte, error) {
	var in map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &in) != nil {
		return raw, nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		if tag, err := entity.NormalizeLocale(k); err == nil {
			k = tag
		}
		out[k] = v
	}
	return json.Marshal(out)
}

// SetLocaleProfile stores the business details of one locale, keyed by its
// BCP 47 tag, and leaves every other locale as it is. The merge happens in SQL,
// so two saves of different locales cannot overwrite each other. Empty raw
// removes the locale's entry.
func (s *Store) SetLocaleProfile(ctx context.Context, tag string, raw json.RawMessage) error {
	conn, err := s.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return fmt.Errorf("acquire db conn: %w", err)
	}
	defer conn.Release()

	if len(raw) == 0 {
		_, err = conn.Exec(ctx, `
			UPDATE hatamoto_site_config
			SET locale_profiles = locale_profiles - $1::text, updated_at = NOW()
			WHERE id = TRUE`, tag)
	} else {
		_, err = conn.Exec(ctx, `
			UPDATE hatamoto_site_config
			SET locale_profiles = locale_profiles || jsonb_build_object($1::text, $2::jsonb), updated_at = NOW()
			WHERE id = TRUE`, tag, []byte(raw))
	}
	if err != nil {
		return fmt.Errorf("set locale profile: %w", err)
	}
	s.Invalidate()
	return nil
}

// MarkMenusSeeded stamps the menu-seeding marker once and never clears it;
// safe to call repeatedly.
func (s *Store) MarkMenusSeeded(ctx context.Context) error {
	conn, err := s.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return fmt.Errorf("acquire db conn: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `
		UPDATE hatamoto_site_config
		SET menus_seeded_at = COALESCE(menus_seeded_at, NOW())
		WHERE id = TRUE`); err != nil {
		return fmt.Errorf("mark menus seeded: %w", err)
	}
	s.Invalidate()
	return nil
}

// legacyTTLCap is the largest value the old binary's CHECK accepts for
// page_cache_ttl_seconds; the new binary mirrors min(ttl, cap) into it.
const legacyTTLCap = 86400

// effectivePageCacheTTL picks the stored lifetime. The new column wins only
// while the legacy column still mirrors it. Any write by an older binary (after
// a rollback) breaks that mirror, so the legacy value is the newer choice.
func effectivePageCacheTTL(secs, legacy int) int {
	if secs > 0 && legacy == min(secs, legacyTTLCap) {
		return secs
	}
	return legacy
}

func (s *Store) load(ctx context.Context) (*Config, error) {
	conn, err := s.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return nil, fmt.Errorf("acquire db conn: %w", err)
	}
	defer conn.Release()

	cfg := &Config{}
	var logoURL, customDomain, primaryColor, tagline, timezone, notificationEmail, phone, businessHours sql.NullString
	var homepageEntityID *uuid.UUID
	var themeExtra, postalAddress, locales, businessProfile, localeProfiles []byte
	var ttlSecs, ttlLegacy int

	err = conn.QueryRow(ctx, `
		SELECT company_name, logo_url, custom_domain, primary_color,
		       theme_extra, tagline, timezone, homepage_entity_id,
		       notification_email, postal_address, language, phone, business_hours,
		       legacy_wp_redirects, public_base_url, setup_completed_at, updated_at,
		       page_cache_enabled,
		       page_cache_ttl_secs, page_cache_ttl_seconds,
		       page_cache_max_mb, page_preload_enabled, page_preload_interval_seconds,
		       page_preload_mode, page_preload_after_changes,
		       locales, prefix_all, x_default_locale,
		       business_profile, locale_profiles, sitemap_naming, menus_seeded_at
		FROM hatamoto_site_config
		WHERE id = TRUE
		LIMIT 1
	`).Scan(
		&cfg.CompanyName, &logoURL, &customDomain, &primaryColor,
		&themeExtra, &tagline, &timezone, &homepageEntityID,
		&notificationEmail, &postalAddress, &cfg.Language, &phone, &businessHours,
		&cfg.LegacyWPRedirects, &cfg.PublicBaseURL, &cfg.SetupCompletedAt, &cfg.UpdatedAt,
		&cfg.PageCacheEnabled, &ttlSecs, &ttlLegacy, &cfg.PageCacheMaxMB,
		&cfg.PagePreloadEnabled, &cfg.PagePreloadIntervalSeconds,
		&cfg.PagePreloadMode, &cfg.PagePreloadAfterChanges,
		&locales, &cfg.PrefixAll, &cfg.XDefaultLocale,
		&businessProfile, &localeProfiles, &cfg.SitemapNaming, &cfg.MenusSeededAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("query site config: %w", err)
	}

	cfg.PageCacheTTLSeconds = effectivePageCacheTTL(ttlSecs, ttlLegacy)

	if len(locales) > 0 {
		if err := json.Unmarshal(locales, &cfg.Locales); err != nil {
			return nil, fmt.Errorf("decode locales: %w", err)
		}
		if len(cfg.Locales) == 0 {
			cfg.Locales = nil
		}
	}

	if logoURL.Valid {
		cfg.LogoURL = logoURL.String
	}
	if customDomain.Valid {
		cfg.CustomDomain = customDomain.String
	}
	if primaryColor.Valid {
		cfg.PrimaryColor = primaryColor.String
	}
	if tagline.Valid {
		cfg.Tagline = tagline.String
	}
	if timezone.Valid {
		cfg.Timezone = timezone.String
	}
	if notificationEmail.Valid {
		cfg.NotificationEmail = notificationEmail.String
	}
	if homepageEntityID != nil {
		cfg.HomepageEntityID = *homepageEntityID
	}
	if len(themeExtra) > 0 {
		cfg.ThemeExtra = json.RawMessage(themeExtra)
	}
	if len(postalAddress) > 0 {
		cfg.PostalAddress = json.RawMessage(postalAddress)
	}
	if cfg.SitemapNaming != "legacy" {
		cfg.SitemapNaming = "group"
	}
	cfg.BusinessProfile = json.RawMessage(businessProfile)
	cfg.LocaleProfiles = json.RawMessage(localeProfiles)
	if phone.Valid {
		cfg.Phone = phone.String
	}
	if businessHours.Valid {
		cfg.BusinessHours = businessHours.String
	}

	return cfg, nil
}
