// Package config provides application configuration loading from environment variables.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/adminpath"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/runenv"
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

	// GeoIPDB is HATAMOTO_GEOIP_DB: path to a country .mmdb used to derive
	// the visitor country of a pageview. The file is reloaded when it is
	// replaced, without a restart. Empty disables country.
	GeoIPDB string

	// GeoIPMmap is HATAMOTO_GEOIP_MMAP: map the GeoIP file read-only instead
	// of copying it into memory. Off by default. The file must then be
	// replaced by rename, never overwritten in place.
	GeoIPMmap bool

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

	// Purge is the optional cache purge webhook (HATAMOTO_PURGE_*).
	Purge PurgeConfig

	// Demo is the public demo mode (HATAMOTO_DEMO=1); see demo.go.
	Demo DemoConfig

	// RunMode is the HATAMOTO_ENV run mode (production or staging). It is
	// separate from Environment.
	RunMode runenv.Mode

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

// SecurityConfig holds security-header toggles. CSPEnforce defaults to true:
// public pages send an enforcing Content-Security-Policy. CSPEnforceSet is true
// when CSP_ENFORCE was given in the environment; it then overrides the choice
// stored in the site settings (which applies only while the variable is unset).
// HeadersDisabled is an escape hatch for local debugging only — never set true
// in production.
type SecurityConfig struct {
	CSPEnforce      bool // env value, or true when CSP_ENFORCE is unset
	CSPEnforceSet   bool // CSP_ENFORCE was set to a valid bool
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
	// Security is SMTP_SECURITY, lower-cased: "" (means starttls), "starttls",
	// "tls" or "none".
	Security string
	From     string
	FromName string
	// FromNameFromSite is true when SMTP_FROM_NAME is unset: the admin's own
	// emails then use the site name as it is in the database at send time.
	FromNameFromSite bool
}

// Enabled reports whether SMTP_* turns email on: a host is set, and it is not
// the default "localhost" unless SMTP_SECURITY=none (a local mail catcher).
func (s SMTPConfig) Enabled() bool {
	return s.Host != "" && (s.Host != "localhost" || s.Security == string(email.SecurityNone))
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
	// FormUploadMaxTotalMB caps the total size of files kept for public form
	// submissions. Zero or negative means the default (see FormUploadMaxTotalBytes).
	FormUploadMaxTotalMB int
	// MediaStorage is where media bytes live: "local" (default) or "s3".
	// The S3 target itself comes from HATAMOTO_MEDIA_S3_* and is never held here.
	MediaStorage string
}

// DatabaseConfig holds PostgreSQL connection settings.
type DatabaseConfig struct {
	URL string
	// DirectURL (DATABASE_DIRECT_URL) is an optional connection string that
	// bypasses a transaction-mode pooler. Migrations, backups, session locks
	// and plugin databases use it. Empty means DATABASE_URL.
	DirectURL       string
	MaxConnections  int
	MinConnections  int
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	// PublicAcquireTimeout bounds how long a public page request waits for a
	// pool connection before answering 503. 0 disables the bound.
	PublicAcquireTimeout time.Duration
	// publicAcquireInvalid holds an unparsable DATABASE_PUBLIC_ACQUIRE_TIMEOUT
	// so Validate can reject it instead of silently using the default.
	publicAcquireInvalid string
	// UnixSocketOff (DATABASE_UNIX_SOCKET=off) stops a loopback URL from being
	// tried over the local unix socket first. Zero value: enabled.
	UnixSocketOff bool
	// SocketDirs (DATABASE_SOCKET_DIRS, colon- or comma-separated) replaces the
	// default socket directories. Nil: the defaults.
	SocketDirs []string
	// unixSocketInvalid holds an unparsable DATABASE_UNIX_SOCKET so Validate
	// can reject it.
	unixSocketInvalid string
}

// parseUnixSocket reads DATABASE_UNIX_SOCKET: on/true/1/yes or unset keep the
// socket preference, off/false/0/no turn it off, anything else is invalid.
func parseUnixSocket(v string) (off bool, invalid string) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "on", "true", "1", "yes":
		return false, ""
	case "off", "false", "0", "no":
		return true, ""
	}
	return false, v
}

// splitSocketDirs splits DATABASE_SOCKET_DIRS on colons and commas. Nil when
// no directory is given.
func splitSocketDirs(v string) []string {
	var dirs []string
	for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == ':' || r == ',' }) {
		if f = strings.TrimSpace(f); f != "" {
			dirs = append(dirs, f)
		}
	}
	return dirs
}

// EffectiveDirectURL is the URL for work that needs real server sessions:
// DirectURL when set, else URL.
func (d DatabaseConfig) EffectiveDirectURL() string {
	if d.DirectURL != "" {
		return d.DirectURL
	}
	return d.URL
}

// Validate rejects settings that cannot work.
func (d DatabaseConfig) Validate() error {
	if d.DirectURL != "" {
		if _, err := pgxpool.ParseConfig(d.DirectURL); err != nil {
			return fmt.Errorf("DATABASE_DIRECT_URL is not a valid PostgreSQL connection string")
		}
	}
	if d.publicAcquireInvalid != "" {
		return fmt.Errorf("DATABASE_PUBLIC_ACQUIRE_TIMEOUT %q is not a duration (use e.g. 3s or 500ms)", d.publicAcquireInvalid)
	}
	if d.PublicAcquireTimeout < 0 {
		return fmt.Errorf("DATABASE_PUBLIC_ACQUIRE_TIMEOUT must not be negative, got %s", d.PublicAcquireTimeout)
	}
	if d.unixSocketInvalid != "" {
		return fmt.Errorf("DATABASE_UNIX_SOCKET %q is not on or off", d.unixSocketInvalid)
	}
	for _, dir := range d.SocketDirs {
		if !filepath.IsAbs(dir) {
			return fmt.Errorf("DATABASE_SOCKET_DIRS entry %q is not an absolute path", dir)
		}
	}
	return nil
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
	// "f" is reserved for the hosted form page /f/{slug}, but an install that
	// already serves its API under /f/... must keep booting: routes deeper than
	// /f/{slug} do not collide with it.
	if reservedPrefixRoot(segs[0]) && segs[0] != "f" {
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
		Host:                 getEnv("API_HOST", "0.0.0.0"),
		Port:                 port,
		ReadTimeout:          getDuration("SERVER_READ_TIMEOUT", 30*time.Second),
		WriteTimeout:         getDuration("SERVER_WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:          getDuration("SERVER_IDLE_TIMEOUT", 120*time.Second),
		ReportOutputDir:      getEnv("REPORT_OUTPUT_DIR", "./reports"),
		EnableWorker:         !strings.EqualFold(strings.TrimSpace(os.Getenv("HATAMOTO_WORKER")), "off"), // on unless HATAMOTO_WORKER=off
		MediaUploadDir:       getEnv("MEDIA_UPLOAD_DIR", "./uploads"),
		MediaBaseURL:         getEnv("MEDIA_BASE_URL", "/media"),
		MediaMaxSizeMB:       getEnvInt("MEDIA_MAX_SIZE_MB", 10),
		FormUploadMaxTotalMB: getEnvInt("FORM_UPLOAD_MAX_TOTAL_MB", defaultFormUploadMaxTotalMB),
		MediaStorage:         strings.ToLower(strings.TrimSpace(getEnv("MEDIA_STORAGE", "local"))),
	}

	cfg.Packages = LoadPackageConfig()
	cfg.Plugins = LoadPluginConfig()
	cfg.Admin = loadAdminConfig()
	cfg.Purge = loadPurgeConfig()
	if cfg.Demo, err = LoadDemoConfig(); err != nil {
		return nil, err
	}
	if cfg.RunMode, err = runenv.Parse(os.Getenv(runenv.EnvVar)); err != nil {
		return nil, err
	}

	cspOn, cspSet := lookupEnvBool("CSP_ENFORCE", true)
	cfg.Security = SecurityConfig{
		CSPEnforce:      cspOn,
		CSPEnforceSet:   cspSet,
		HeadersDisabled: getEnvBool("SECURITY_HEADERS_DISABLED", false),
	}

	// Database config
	cfg.Database = DatabaseConfig{
		URL:             getEnvRequired("DATABASE_URL"),
		DirectURL:       strings.TrimSpace(getEnv("DATABASE_DIRECT_URL", "")),
		MaxConnections:  getEnvInt("DATABASE_MAX_CONNECTIONS", 25),
		MinConnections:  getEnvInt("DATABASE_MIN_CONNECTIONS", 5),
		MaxConnLifetime: getDuration("DATABASE_MAX_CONN_LIFETIME", 1*time.Hour),
		MaxConnIdleTime: getDuration("DATABASE_MAX_CONN_IDLE_TIME", 30*time.Minute),

		PublicAcquireTimeout: getDuration("DATABASE_PUBLIC_ACQUIRE_TIMEOUT", 3*time.Second),
	}

	if v := os.Getenv("DATABASE_PUBLIC_ACQUIRE_TIMEOUT"); v != "" {
		if _, err := time.ParseDuration(v); err != nil {
			cfg.Database.publicAcquireInvalid = v
		}
	}

	cfg.Database.UnixSocketOff, cfg.Database.unixSocketInvalid = parseUnixSocket(os.Getenv("DATABASE_UNIX_SOCKET"))
	cfg.Database.SocketDirs = splitSocketDirs(os.Getenv("DATABASE_SOCKET_DIRS"))

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

	cfg.GeoIPDB = strings.TrimSpace(os.Getenv("HATAMOTO_GEOIP_DB"))
	cfg.GeoIPMmap = getEnvBool("HATAMOTO_GEOIP_MMAP", false)

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
		Security: strings.ToLower(strings.TrimSpace(getEnv("SMTP_SECURITY", ""))),
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

// defaultFormUploadMaxTotalMB is the form upload store cap when the knob is unset.
const defaultFormUploadMaxTotalMB = 1024

// FormUploadMaxTotalBytes is FormUploadMaxTotalMB in bytes, with the default
// for a zero or negative value.
func (c ServerConfig) FormUploadMaxTotalBytes() int64 {
	mb := c.FormUploadMaxTotalMB
	if mb <= 0 {
		mb = defaultFormUploadMaxTotalMB
	}
	return int64(mb) << 20
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

// lookupEnvBool returns the parsed value and true when key is set to a valid
// bool, else def and false (unset, empty or unparseable all fall back).
func lookupEnvBool(key string, def bool) (bool, bool) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b, true
		}
	}
	return def, false
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
	if err := c.Database.Validate(); err != nil {
		return err
	}
	if err := c.Admin.Validate(); err != nil {
		return err
	}
	if err := c.Purge.Validate(); err != nil {
		return err
	}
	if err := c.Packages.Validate(); err != nil {
		return err
	}
	if err := c.PublicSite.Validate(); err != nil {
		return err
	}
	// The admin lives on the public host, so one comparison covers both.
	if h := c.Admin.PreviewHost; h != "" && c.PublicSite.BaseURL != "" {
		if u, err := url.Parse(c.PublicSite.BaseURL); err == nil && sameOriginHost(h, u.Host) {
			return fmt.Errorf("HATAMOTO_PREVIEW_HOST: %q is the public and admin host; it must be a separate host", h)
		}
	}
	// The pinned admin address is known here; a stored one is checked at boot.
	if err := CheckPrefixAgainstAdmin(c.PublicSite.APIPrefix, c.Admin.Path); err != nil {
		return err
	}
	// Mail from noreply@localhost is rejected by real relays (and fails SPF and
	// DMARC), so turning SMTP on without saying who the mail is from is an error.
	switch email.Security(c.SMTP.Security) {
	case "", email.SecurityStarttls, email.SecurityTLS:
	case email.SecurityNone:
		if c.SMTP.Enabled() {
			if err := email.CheckNone(c.SMTP.Host, c.SMTP.Username != "" || c.SMTP.Password != ""); err != nil {
				return fmt.Errorf("SMTP_SECURITY=none: %w", err)
			}
		}
	default:
		return fmt.Errorf("SMTP_SECURITY: %q is not supported, use starttls, tls or none", c.SMTP.Security)
	}
	if c.SMTP.Enabled() && c.SMTP.From == DefaultSMTPFrom {
		return fmt.Errorf("SMTP_FROM is required when SMTP_HOST is set (the default %s cannot send)", DefaultSMTPFrom)
	}
	if m := c.Server.MediaStorage; m != "" && m != "local" && m != "s3" {
		return fmt.Errorf("MEDIA_STORAGE: %q is not supported, use local or s3", m)
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
