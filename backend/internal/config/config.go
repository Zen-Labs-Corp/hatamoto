// Package config provides application configuration loading from environment variables.
package config

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/adminpath"
)

// Config holds all application configuration.
type Config struct {
	// Server configuration
	Server ServerConfig

	// Database configuration
	Database DatabaseConfig

	// Secret is the server secret (HATAMOTO_SECRET, or JWT_SECRET as older
	// deployments name it). Flash cookies, theme-preview links and stored
	// import credentials are keyed from it.
	Secret string

	// PublicSite configuration (canonical public origin)
	PublicSite PublicSiteConfig

	// PageCacheTTL is how long a rendered public page stays in the HTML cache
	// (HATAMOTO_PAGE_CACHE_TTL, default 5m, 30s..30d).
	PageCacheTTL time.Duration
	// PageCacheTTLFromEnv is true when PageCacheTTL came from a valid
	// HATAMOTO_PAGE_CACHE_TTL, false when it is the built-in default.
	PageCacheTTLFromEnv bool

	// Warnings are non-fatal configuration problems found by Load (an invalid
	// value fell back to its default); the caller logs them at boot.
	Warnings []string

	// MCPAllowedOrigins are extra browser origins allowed to call /mcp
	// (HATAMOTO_MCP_ALLOWED_ORIGINS, comma-separated). The public origin is
	// always allowed; requests without an Origin header are not affected.
	MCPAllowedOrigins []string

	// AgentAnalytics configures the optional access-log ingest (default off)
	AgentAnalytics AgentAnalyticsConfig

	// SMTP configuration for email notifications
	SMTP SMTPConfig

	// Feature flags
	Features FeaturesConfig

	// Hatamoto-specific brand / identity config (single-tenant)
	Hatamoto HatamotoConfig

	// Security header toggles (CSP enforce mode, disable switch)
	Security SecurityConfig

	// Packages controls plugin and theme packages (registry, trusted keys).
	Packages PackageConfig

	// Plugins controls the WASM plugin runtime.
	Plugins PluginConfig

	// Admin controls the Go admin UI.
	Admin AdminConfig

	// Demo is the public demo mode (HATAMOTO_DEMO=1); see demo.go.
	Demo DemoConfig

	// Environment
	Environment string

	// AdminCookieSecure controls the Secure attribute on the admin session
	// cookie. Defaults to true in production; set false for local HTTP dev.
	AdminCookieSecure bool

	// EnableCookieAuth lets the /api/v1 session also travel as an HttpOnly
	// cookie (with CSRF enforcement). Default FALSE: /api/v1 clients send the
	// session token as a bearer. The Go admin always uses its own cookie.
	EnableCookieAuth bool
	LogLevel         string
}

// HatamotoConfig holds brand-bearing identifiers that vary per deployment.
// Kept in its own struct so OSS forks see one place to override their
// site identity instead of hunting through SMTP / public-site configs.
type HatamotoConfig struct {
	// CanonicalSlug is the stable site slug stamped into API responses. Each
	// deployment sets its own. Required — validated in Validate().
	CanonicalSlug string

	// SiteName is the human-readable brand name rendered into outbound
	// transactional emails (the shared base.html <title>, header, and
	// footer attribution + form-submission footer line). Config-time
	// stable on purpose — siteconfig is mutable runtime state and the
	// email brand identity should not silently drift if an admin edits
	// the company name in the UI. Required —
	// validated in Validate().
	SiteName string
}

// SecurityConfig holds security-header toggles. CSPEnforce defaults to false
// so every deploy ships Content-Security-Policy-Report-Only until an operator
// explicitly opts into enforcement after verifying the admin editor + public
// pages under the policy. HeadersDisabled is an escape hatch for local
// debugging only — never set true in production.
type SecurityConfig struct {
	CSPEnforce      bool
	HeadersDisabled bool
}

// FeaturesConfig holds reversible feature toggles. Empty for now — the
// rollout flags this struct carried (LINKING_V2_ACTIVE and one other)
// have been retired post-cutover. Kept as a struct so adding a new
// flag in the future doesn't require restructuring the config root.
type FeaturesConfig struct{}

// SMTPConfig holds SMTP email settings.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	FromName string
	// FromNameFromSite is true when SMTP_FROM_NAME is unset: the admin's own
	// emails then use the site name as it is in the database at send time.
	FromNameFromSite bool
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Host            string
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ReportOutputDir string
	EnableWorker    bool
	// Media settings
	MediaUploadDir string
	MediaBaseURL   string
	MediaMaxSizeMB int
}

// DatabaseConfig holds PostgreSQL connection settings.
type DatabaseConfig struct {
	URL             string
	MaxConnections  int
	MinConnections  int
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// PublicSiteConfig holds the canonical public origin for the tenant. When set
// it is the origin of every absolute public URL the API emits (canonicals,
// breadcrumbs, JSON-LD, sitemaps, llms.txt)
// because the request's Host header may be an internal address.
type PublicSiteConfig struct {
	BaseURL string
	// APIPrefix (PUBLIC_API_PREFIX) is an extra mount point for the public API
	// (forms and track), served in addition to /api/v1/public. Empty means only
	// the default. Pages written by the theme call this prefix.
	APIPrefix string
}

// extraReservedRoots are first segments the server owns that
// adminpath.ReservedPrefixes leaves out ("admin" is a weak but allowed admin
// address there, yet a public API under it would still collide).
var extraReservedRoots = []string{"admin", "mcp", "uploads", "sitemap.xsl"}

func reservedPrefixRoot(seg string) bool {
	return slices.Contains(adminpath.ReservedPrefixes, seg) || slices.Contains(extraReservedRoots, seg)
}

// ValidatePublicAPIPrefix checks PUBLIC_API_PREFIX: empty, or a lower-case path
// of at least two segments that does not start in a path the server owns.
// A CMS page at the same path would be shadowed by the API.
func ValidatePublicAPIPrefix(p string) error {
	if p == "" {
		return nil
	}
	bad := func(why string) error { return fmt.Errorf("PUBLIC_API_PREFIX %q %s", p, why) }
	if !strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") || strings.Contains(p, "//") {
		return bad("must start with / and have no trailing or empty segment")
	}
	segs := strings.Split(p[1:], "/")
	if len(segs) < 2 {
		return bad("needs at least two segments, for example /x/v1")
	}
	for _, s := range segs {
		if s == "" {
			return bad("has an empty segment")
		}
		for _, r := range s {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return bad("may only use a-z, 0-9, - and _ in each segment")
			}
		}
	}
	if reservedPrefixRoot(segs[0]) {
		return bad("starts with a path the server already uses")
	}
	return nil
}

// Validate checks the public site settings.
func (c PublicSiteConfig) Validate() error { return ValidatePublicAPIPrefix(c.APIPrefix) }

// CheckPrefixAgainstAdmin refuses a PUBLIC_API_PREFIX that lives under the
// admin address (the first segments match), where the admin intercept and the
// public routes would shadow each other. An empty prefix or admin address is
// fine.
func CheckPrefixAgainstAdmin(prefix, adminAddr string) error {
	prefix, adminAddr = strings.ToLower(prefix), strings.ToLower(strings.Trim(adminAddr, "/"))
	if prefix == "" || adminAddr == "" {
		return nil
	}
	first, _, _ := strings.Cut(strings.TrimPrefix(prefix, "/"), "/")
	if first == adminAddr {
		return fmt.Errorf("PUBLIC_API_PREFIX %q is under the admin address /%s; change PUBLIC_API_PREFIX, or run `hatamoto admin-url set <path>` to choose another address", prefix, adminAddr)
	}
	return nil
}

// AgentAnalyticsConfig holds the opt-in access-log ingest settings. Everything
// is off by default; nothing here is derived from other secrets.
type AgentAnalyticsConfig struct {
	// LogIngestEnabled (AGENT_LOG_INGEST) turns on log ingest. It only takes
	// effect together with LogIngestSecret (see LogIngestActive).
	LogIngestEnabled bool
	// LogIngestSecret (AGENT_LOG_INGEST_SECRET) authenticates POST
	// /api/v1/public/agent-logs via X-Agent-Log-Secret. No dev default.
	LogIngestSecret string
	// CaddyLogPath (AGENT_LOG_CADDY_PATH), when set with the flag on, makes
	// the backend tail that Caddy JSON access log (read-only).
	CaddyLogPath string
}

// LogIngestActive reports whether the log ingest endpoint should be mounted:
// the flag is on and a secret is configured.
func (c AgentAnalyticsConfig) LogIngestActive() bool {
	return c.LogIngestEnabled && c.LogIngestSecret != ""
}

func loadAgentAnalyticsConfig() AgentAnalyticsConfig {
	return AgentAnalyticsConfig{
		LogIngestEnabled: getEnvBool("AGENT_LOG_INGEST", false),
		LogIngestSecret:  getEnv("AGENT_LOG_INGEST_SECRET", ""),
		CaddyLogPath:     getEnv("AGENT_LOG_CADDY_PATH", ""),
	}
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	cfg := &Config{
		Environment: getEnv("ENVIRONMENT", "development"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
	}

	// Server config
	port, err := strconv.Atoi(getEnv("API_PORT", "8090"))
	if err != nil {
		return nil, fmt.Errorf("invalid API_PORT: %w", err)
	}
	cfg.Server = ServerConfig{
		Host:            getEnv("API_HOST", "0.0.0.0"),
		Port:            port,
		ReadTimeout:     getDuration("SERVER_READ_TIMEOUT", 30*time.Second),
		WriteTimeout:    getDuration("SERVER_WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:     getDuration("SERVER_IDLE_TIMEOUT", 120*time.Second),
		ReportOutputDir: getEnv("REPORT_OUTPUT_DIR", "./reports"),
		EnableWorker:    !strings.EqualFold(strings.TrimSpace(os.Getenv("HATAMOTO_WORKER")), "off"), // on unless HATAMOTO_WORKER=off
		MediaUploadDir:  getEnv("MEDIA_UPLOAD_DIR", "./uploads"),
		MediaBaseURL:    getEnv("MEDIA_BASE_URL", "/media"),
		MediaMaxSizeMB:  getEnvInt("MEDIA_MAX_SIZE_MB", 10),
	}

	cfg.Packages = LoadPackageConfig()
	cfg.Plugins = LoadPluginConfig()
	cfg.Admin = loadAdminConfig()
	if cfg.Demo, err = LoadDemoConfig(); err != nil {
		return nil, err
	}

	cfg.Security = SecurityConfig{
		CSPEnforce:      getEnvBool("CSP_ENFORCE", false),
		HeadersDisabled: getEnvBool("SECURITY_HEADERS_DISABLED", false),
	}

	// Database config
	cfg.Database = DatabaseConfig{
		URL:             getEnvRequired("DATABASE_URL"),
		MaxConnections:  getEnvInt("DATABASE_MAX_CONNECTIONS", 25),
		MinConnections:  getEnvInt("DATABASE_MIN_CONNECTIONS", 5),
		MaxConnLifetime: getDuration("DATABASE_MAX_CONN_LIFETIME", 1*time.Hour),
		MaxConnIdleTime: getDuration("DATABASE_MAX_CONN_IDLE_TIME", 30*time.Minute),
	}

	cfg.Secret = getEnv("HATAMOTO_SECRET", getEnv("JWT_SECRET", ""))

	cfg.AdminCookieSecure = getEnvBool("ADMIN_COOKIE_SECURE", cfg.Environment == "production")
	cfg.EnableCookieAuth = getEnvBool("ENABLE_COOKIE_AUTH", false)

	// Public site config — public URLs default to the request host but are
	// pinned via PUBLIC_BASE_URL (canonicals, sitemaps, JSON-LD, llms.txt and
	// emails) for origins fronted by a proxy, CDN or server-side fetch.
	cfg.PublicSite = PublicSiteConfig{
		BaseURL:   getEnv("PUBLIC_BASE_URL", ""),
		APIPrefix: getEnv("PUBLIC_API_PREFIX", ""),
	}

	warned := len(cfg.Warnings)
	cfg.PageCacheTTL, cfg.Warnings = loadPageCacheTTL(cfg.Warnings)
	// An invalid value adds a warning and falls back to the default.
	cfg.PageCacheTTLFromEnv = strings.TrimSpace(os.Getenv("HATAMOTO_PAGE_CACHE_TTL")) != "" && len(cfg.Warnings) == warned

	for part := range strings.SplitSeq(os.Getenv("HATAMOTO_MCP_ALLOWED_ORIGINS"), ",") {
		if o := strings.TrimSpace(part); o != "" {
			cfg.MCPAllowedOrigins = append(cfg.MCPAllowedOrigins, o)
		}
	}

	cfg.AgentAnalytics = loadAgentAnalyticsConfig()

	// SMTP config for email notifications. SMTP_FROM and SMTP_FROM_NAME are
	// brand-bearing strings (the From header recipients see) — no default,
	// fail-fast in Validate(). SMTP_HOST stays default-able so dev runs can
	// boot without SMTP plumbing; production rejects "localhost".
	cfg.SMTP = SMTPConfig{
		Host:     getEnv("SMTP_HOST", "localhost"),
		Port:     getEnvInt("SMTP_PORT", 587),
		Username: getEnv("SMTP_USERNAME", ""),
		Password: getEnv("SMTP_PASSWORD", ""),
		From:     getEnv("SMTP_FROM", DefaultSMTPFrom),
		FromName: getEnv("SMTP_FROM_NAME", ""),
	}

	// Feature flags. Currently no toggles; the struct is initialised empty
	// so a future flag addition only needs a field append.
	cfg.Features = FeaturesConfig{}

	// Hatamoto identity (single-tenant). Neutral defaults, so `serve` boots with
	// only DATABASE_URL; the site name the owner sets in the admin (stored in
	// the database) wins over HATAMOTO_SITE_NAME at runtime.
	cfg.Hatamoto = HatamotoConfig{
		CanonicalSlug: getEnv("HATAMOTO_CANONICAL_SLUG", DefaultCanonicalSlug),
		SiteName:      getEnv("HATAMOTO_SITE_NAME", DefaultSiteName),
	}
	// The sender name follows the site name unless set explicitly.
	if cfg.SMTP.FromName == "" {
		cfg.SMTP.FromName = cfg.Hatamoto.SiteName
		cfg.SMTP.FromNameFromSite = true
	}

	return cfg, nil
}

// getEnv returns environment variable value or default.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvRequired returns environment variable or panics if not set.
func getEnvRequired(key string) string {
	value := os.Getenv(key)
	if value == "" {
		// During initialization, we return empty string and let validation handle it
		return ""
	}
	return value
}

// getEnvInt returns environment variable as int or default.
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return defaultValue
}

// Page cache TTL bounds and default. The floor keeps a typo from turning the
// cache off in effect; the ceiling bounds how long a missed invalidation can
// show stale markup.
const (
	DefaultPageCacheTTL = 5 * time.Minute
	MinPageCacheTTL     = 30 * time.Second
	MaxPageCacheTTL     = 30 * 24 * time.Hour
)

// loadPageCacheTTL reads HATAMOTO_PAGE_CACHE_TTL. An unparsable or out-of-range
// value yields the default and a warning appended to warnings.
func loadPageCacheTTL(warnings []string) (time.Duration, []string) {
	raw := strings.TrimSpace(os.Getenv("HATAMOTO_PAGE_CACHE_TTL"))
	if raw == "" {
		return DefaultPageCacheTTL, warnings
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < MinPageCacheTTL || d > MaxPageCacheTTL {
		return DefaultPageCacheTTL, append(warnings, fmt.Sprintf(
			"HATAMOTO_PAGE_CACHE_TTL=%q is not a duration between %s and %s; using the default %s",
			raw, MinPageCacheTTL, MaxPageCacheTTL, DefaultPageCacheTTL))
	}
	return d, warnings
}

// getDuration returns environment variable as duration or default.
func getDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return defaultValue
}

// getEnvBool returns environment variable as bool or default.
func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if b, err := strconv.ParseBool(value); err == nil {
			return b
		}
	}
	return defaultValue
}

// Defaults that let `hatamoto serve` boot with only DATABASE_URL. Email stays
// off until SMTP_HOST is a real host (IsConfigured); the site name is a seed for
// the first-boot row and is edited in the admin afterwards.
const (
	DefaultSMTPFrom      = "noreply@localhost"
	DefaultCanonicalSlug = "site"
	DefaultSiteName      = "My Hatamoto site"
)

// Validate checks that the configuration can boot: DATABASE_URL, a server secret
// (generated on first boot by `serve`) and valid render/admin settings. Nothing
// else is required.
func (c *Config) Validate() error {
	if c.Database.URL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if err := c.Admin.Validate(); err != nil {
		return err
	}
	if err := c.Packages.Validate(); err != nil {
		return err
	}
	if err := c.PublicSite.Validate(); err != nil {
		return err
	}
	// The pinned admin address is known here; a stored one is checked at boot.
	if err := CheckPrefixAgainstAdmin(c.PublicSite.APIPrefix, c.Admin.Path); err != nil {
		return err
	}
	// Mail from noreply@localhost is rejected by real relays (and fails SPF and
	// DMARC), so turning SMTP on without saying who the mail is from is an error.
	if h := c.SMTP.Host; h != "" && h != "localhost" && c.SMTP.From == DefaultSMTPFrom {
		return fmt.Errorf("SMTP_FROM is required when SMTP_HOST is set (the default %s cannot send)", DefaultSMTPFrom)
	}
	if c.Secret == "" {
		return fmt.Errorf("HATAMOTO_SECRET is required (JWT_SECRET is accepted as its old name)")
	}
	if len(c.Secret) < 32 {
		return fmt.Errorf("HATAMOTO_SECRET must be at least 32 characters")
	}
	return nil
}

// Address returns the server address in host:port format.
func (c *ServerConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}
