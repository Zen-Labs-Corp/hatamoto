// Package admin is the server-rendered admin UI: html/template pages from
// backend/web, htmx for interactivity, the same session cookie and stores as
// the JSON API. It never imports internal/server; the server builds Deps from
// its services (server/admin_mount.go) and calls Mount.
package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/starter"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/session"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/user"
	"hatamoto.dev/core/internal/webhook"
	"hatamoto.dev/core/web"
)

// SessionStore is the slice of session.Store the admin uses.
type SessionStore interface {
	Validate(ctx context.Context, token string) (*session.Session, error)
	Revoke(ctx context.Context, id uuid.UUID) error
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
}

// UserReader loads the signed-in user.
type UserReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (*user.User, error)
}

// SiteStore reads and writes the singleton site config.
type SiteStore interface {
	Get(ctx context.Context) (*siteconfig.Config, error)
	Update(ctx context.Context, in siteconfig.UpdateInput) (*siteconfig.Config, error)
	// SetLocaleProfile stores one locale's business details; empty raw clears it.
	SetLocaleProfile(ctx context.Context, tag string, raw json.RawMessage) error
}

// OwnerCreator creates the first owner (auth.Bootstrap).
type OwnerCreator interface {
	CreateFirstOwner(ctx context.Context, name, email, password string) (*user.User, error)
}

// StarterInstaller installs the optional starter content.
type StarterInstaller interface {
	Install(ctx context.Context, o starter.Options) (starter.Result, error)
}

// Authenticator signs people in (auth.Service).
type Authenticator interface {
	LoginSession(ctx context.Context, email, password, userAgent, ip string) (*user.User, string, error)
	IssueSession(ctx context.Context, userID uuid.UUID, userAgent, ip string) (string, error)
}

// TokenService is the reset/invite token flow (auth.UserTokens).
type TokenService interface {
	IssueReset(ctx context.Context, userID uuid.UUID) (string, error)
	ResetValid(ctx context.Context, token string) bool
	ConsumeReset(ctx context.Context, token, newPassword string) (*user.User, error)
	PeekInvite(ctx context.Context, token string) (*auth.Invite, error)
	ConsumeInvite(ctx context.Context, token, name, password string) (*user.User, error)
	IssueInvite(ctx context.Context, email string, role user.Role, invitedBy uuid.UUID) (string, *auth.Invite, error)
	ListInvites(ctx context.Context) ([]auth.Invite, error)
	RevokeInvite(ctx context.Context, id uuid.UUID) error
}

// UserManager is the user repository the Users screens use.
type UserManager interface {
	GetByID(ctx context.Context, id uuid.UUID) (*user.User, error)
	List(ctx context.Context, limit, offset int) ([]*user.User, error)
	Create(ctx context.Context, in *user.CreateUserInput, passwordHash string) (*user.User, error)
	Update(ctx context.Context, id uuid.UUID, in *user.UpdateUserInput) (*user.User, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// PasswordChanger changes a user's own password (auth.UserTokens).
type PasswordChanger interface {
	ChangePassword(ctx context.Context, userID uuid.UUID, current, newPassword string, keepSession *uuid.UUID) error
}

// WebhookStore is the webhook service as the Settings > Webhooks screens use
// it. A list never carries a signing secret.
type WebhookStore interface {
	List(ctx context.Context) ([]webhook.ListedSubscription, error)
	// Create stores the subscription and returns it with its new signing secret.
	Create(ctx context.Context, in webhook.Subscription) (webhook.Subscription, string, error)
	Get(ctx context.Context, id string) (*webhook.Subscription, error)
	Update(ctx context.Context, id string, p webhook.SubscriptionPatch) error
	SetActive(ctx context.Context, id string, active bool) error
	// RegenerateSecret replaces the signing secret and returns the new one.
	RegenerateSecret(ctx context.Context, id string) (string, error)
	Delete(ctx context.Context, id string) error
	// SendTest sends a test delivery now and returns its recorded outcome.
	SendTest(ctx context.Context, id string) (*webhook.DeliveryRow, error)
	// ListDeliveries is one page of the delivery log (subscriptionID "" = all)
	// and the total row count. Rows never carry the payload.
	ListDeliveries(ctx context.Context, subscriptionID string, limit, offset int) ([]webhook.DeliveryRow, int, error)
	// RetryDelivery re-queues a failed delivery; false means it had not failed.
	RetryDelivery(ctx context.Context, id string) (bool, error)
	// CountFailedSince is how many deliveries queued since the time have failed.
	CountFailedSince(ctx context.Context, since time.Time) (int, error)
}

// APIKeyStore is auth.APIKeyService as the API-keys screen uses it.
type APIKeyStore interface {
	List(ctx context.Context) ([]*auth.APIKey, error)
	Create(ctx context.Context, createdBy *uuid.UUID, in *auth.CreateAPIKeyInput) (*auth.APIKeyWithSecret, error)
	Revoke(ctx context.Context, id uuid.UUID, revokedBy *uuid.UUID) error
	Regenerate(ctx context.Context, id uuid.UUID) (string, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// UserFinder looks users up by address (forgot-password) and counts them.
type UserFinder interface {
	GetByEmail(ctx context.Context, email string) (*user.User, error)
	Count(ctx context.Context) (int, error)
}

// Mailer sends the auth emails (email.Service).
type Mailer interface {
	IsConfigured() bool
	SendPasswordReset(ctx context.Context, to string, data email.ResetMailData) error
	SendInvite(ctx context.Context, to string, data email.InviteMailData) error
	SendTest(ctx context.Context, to, siteName string) error
	SendAdminAddressChanged(ctx context.Context, to string, data email.AdminAddressChangedData) error
	SendAdminAddressSetup(ctx context.Context, to string, data email.AdminAddressSetupData) error
}

// Deps is everything the admin needs. Interfaces are declared here (not in the
// services) so handler tests run on fakes without a database.
type Deps struct {
	Logger *slog.Logger
	// Sessions, Users and Site may be nil only in route-table builds: the
	// handlers are never invoked there.
	Sessions SessionStore
	Users    UserReader
	Site     SiteStore
	Nav      *Nav
	Auth     Authenticator
	Tokens   TokenService
	Finder   UserFinder
	Mailer   Mailer
	// BaseURL returns the site address the owner or operator configured
	// (PUBLIC_BASE_URL, else the Site address setting); "" when none is set.
	// Emailed links use only this, never the request's headers.
	BaseURL func(*http.Request) string
	// CookieSecure is ADMIN_COOKIE_SECURE: a floor for the Secure cookie
	// attribute (TLS seen on the request also turns it on).
	CookieSecure bool
	Manager      UserManager
	Passwords    PasswordChanger
	APIKeys      APIKeyStore
	// Webhooks is the outgoing-webhook service behind Settings > Webhooks; nil
	// answers that section with 404. Set it only when non-nil: a typed nil
	// would defeat the check.
	Webhooks WebhookStore
	// WebhookDeliveryOn is true when a job sends queued webhooks; false shows
	// the "delivery is switched off" notice.
	WebhookDeliveryOn bool
	// WebhooksPluginLeft says a plugin named webhooks is still installed (one
	// that was not moved into core); true shows the notice on the webhooks page.
	WebhooksPluginLeft func(context.Context) bool
	// ValidateLocales checks the Locales settings against the running site and
	// returns the normalized list and x-default to store; nil rejects a locale
	// list in the form.
	ValidateLocales func(ctx context.Context, def string, entries []siteconfig.LocaleEntry, prefixAll bool, xDefault string) ([]siteconfig.LocaleEntry, string, error)
	// Invalidate clears the rendered-page cache after settings change.
	Invalidate func(context.Context)
	// BuilderTokens reports whether the live theme opted in to the design
	// tokens (theme.json builder.tokens); nil means no.
	BuilderTokens func() bool
	// BuilderParts lists the layout slots (header, footer, single, archive) the
	// live theme renders parts for (theme.json builder.parts); nil means none.
	BuilderParts func() []string
	// PageCache is the rendered-page cache behind Settings > Performance;
	// nil answers that page with 503.
	PageCache PageCacheService
	// Spam is the spam protection settings behind Settings > Forms > Spam
	// protection; nil answers that page with 503.
	Spam SpamService
	// SEO is the SEO settings store behind Settings > SEO; nil answers those
	// pages with 503. Set it only when non-nil.
	SEO SEOService
	// EmailSettings is the SMTP configuration behind Settings > Email and the
	// setup wizard's Email step; nil answers those pages with 503.
	EmailSettings EmailSettingsService
	// Updates is the automatic-update service behind Settings > Updates and
	// the owner banner; nil renders that page as unavailable and no banner.
	// Set it only when non-nil: a typed nil would defeat the check.
	Updates UpdatesService
	// Stats is the opt-in install stats service behind the Install stats card
	// on Settings > Updates, the setup wizard step and the owner banner; nil
	// hides all three. Set it only when non-nil.
	Stats StatsService
	// UpdatesUnavailable is why Updates is nil, shown on the page.
	UpdatesUnavailable string
	// Backups is the backup service behind Settings > Backups; nil answers
	// that page with 503. Set it only when non-nil.
	Backups BackupsService
	// Audit records an owner action in the admin audit log; nil skips it.
	Audit func(ctx context.Context, e AuditEvent)
	// AuditLog reads the audit log back; nil leaves the Audit log page
	// unmounted. Set it only when non-nil.
	AuditLog AuditReader
	Owners   OwnerCreator
	Starter  StarterInstaller
	// SetupCode gates the first owner; nil disables the wizard's first step.
	SetupCode *SetupCode
	// NeedsSetup reports a fresh install (no users yet); /admin/login then
	// sends visitors to the setup wizard. Nil means never.
	NeedsSetup func(context.Context) bool
	// FlashKey signs the flash cookie (derive it from the server secret).
	FlashKey []byte
	// SiteNameFallback is shown until the site config has a name.
	SiteNameFallback string
	// RateLimit wraps the unauthenticated auth screens; nil means none.
	RateLimit func(http.Handler) http.Handler
	// Growth holds the services of the menus, forms, redirects, dashboards,
	// linking and plugins screens (see growth.go).
	Growth GrowthDeps
	// Content holds the services behind the entity, schema, taxonomy and media
	// screens (v1-11).
	Content ContentDeps
	// People is the person behind each user account: the profile pages and
	// the sign-in profile gate. Nil skips the gate.
	People PeopleStore
	// PersonPath is the public path of a person by slug (the person content
	// type's URL pattern); nil or "" falls back to /author/{slug}.
	PersonPath func(ctx context.Context, slug string) string
	// Import runs the WordPress import wizard (importjob.Manager); nil answers 503.
	Import ImportService
	// Dir, when set, replaces the embedded templates/CSS/JS with files from
	// disk and rebuilds them per request (dev loop, HATAMOTO_ADMIN_DIR).
	Dir string
	// PublicAPIPrefix is the extra public API prefix (PUBLIC_API_PREFIX), empty
	// when only /api/v1/public is served. Form addresses shown in the admin use it.
	PublicAPIPrefix string
	// AppVersion is the running build's version (app.Version, stamped by the
	// Makefile); empty or a development build shows as "dev build".
	AppVersion string
	// Dev turns on the development-only pages (the styleguide, HATAMOTO_DEV=1).
	Dev bool
	// Demo is the public demo mode (HATAMOTO_DEMO=1): a fixed banner, the
	// public login on the sign-in page and the refusals of config.DemoConfig.
	Demo config.DemoConfig

	// AdminPath is the URL prefix every admin route is served under. Empty
	// means DefaultAdminPath; otherwise it is "/" plus one lowercase [a-z0-9-]
	// segment of 1 to 64 characters, not starting or ending with "-". Reserved
	// names and the spec's length rules come in PR 2. Demo mode requires the
	// default.
	AdminPath string
	// Address is the live admin address: its state, the picker's checks and
	// the change itself. Nil hides the address screens (route-table builds).
	Address AddressService
	// Sudo, when set, holds the sudo grants. The server builds one and passes
	// it to every Admin it swaps in, so an owner keeps sudo across a change.
	// Nil means this Admin keeps its own.
	Sudo *SudoStore
}

// DefaultAdminPath is where the admin lives unless Deps.AdminPath says
// otherwise. It is the one place the prefix is spelled out.
const DefaultAdminPath = "/admin"

// Admin holds the parsed templates and assets and the HTTP handlers.
type Admin struct {
	deps    Deps
	nav     *Nav
	adminFS fs.FS
	// adminPath is the normalised URL prefix. It lives on Admin, never on
	// built state, so dev mode rebuilds keep it.
	adminPath string
	// legacy is the old-URL table under adminPath; robotsReturns matches the
	// analytics pages that may carry the robots policy form.
	legacy        []legacyRule
	robotsReturns *regexp.Regexp
	// csrfKey keys the synchronizer tokens (see csrf.go). csrfTokenFn is a
	// test seam that replaces the derivation; nil in production.
	csrfKey     []byte
	csrfTokenFn func(kind, id string) string
	// noFetchMetaLogged is the unix time of the last "no fetch metadata" warning.
	noFetchMetaLogged atomic.Int64
	// speculation is the shell's speculation rules JSON under adminPath, built once.
	speculation template.HTML
	// appVersion is Deps.AppVersion as the shell's colophon shows it.
	appVersion string

	mu     sync.RWMutex
	assets *Assets
	pages  map[string]*pageSet
	loaded bool

	// secrets holds a freshly minted API key between the POST that created it
	// and the one GET that shows it.
	secrets secretStore
	// addrMailed remembers which users were already emailed their admin
	// address on the Done step, so a reload does not send it again.
	addrMailed sync.Map
	// notifyWG tracks the owner emails sent in the background after an address change.
	notifyWG sync.WaitGroup
	// staged holds packages between the permission prompt and its confirmation.
	staged staged
	// bridgeLimit throttles plugin page bridge calls per user and plugin.
	bridgeLimit bridgeLimiter
	// tickets issues the one-time, session-bound frame tickets, and holds the
	// drafts staged for the preview frame (frameticket.go).
	tickets *ticketStore
	// sudo holds password re-confirmation (sudo mode) state.
	sudo sudoState
}

// New parses every template and bundles the assets. A parse failure is
// returned so `serve` refuses to boot rather than 500 on every page.
func New(d Deps) (*Admin, error) {
	adminPath, err := normalizeAdminPath(d.AdminPath)
	if err != nil {
		return nil, err
	}
	d.AdminPath = adminPath
	// The demo refusals in config/demo.go match /admin only; fail closed until
	// they are prefix-aware.
	if d.Demo.Enabled && adminPath != DefaultAdminPath {
		return nil, fmt.Errorf("admin: demo mode needs the default admin path %s, got %q", DefaultAdminPath, adminPath)
	}
	if d.Nav == nil {
		d.Nav = defaultNav(adminPath)
	}
	if d.Dir == "" {
		d.Dir = os.Getenv("HATAMOTO_ADMIN_DIR")
	}
	if d.Sudo == nil {
		d.Sudo = NewSudoStore()
	}
	a := &Admin{deps: d, nav: d.Nav, adminFS: web.Admin(), adminPath: adminPath,
		legacy:        legacyRulesFor(adminPath),
		robotsReturns: robotsReturnsFor(adminPath),
		speculation:   speculationRulesFor(adminPath),
		appVersion:    displayAppVersion(d.AppVersion),
		csrfKey:       deriveCSRFKey(d.FlashKey),
	}
	a.sudo.store = &d.Sudo.s
	a.tickets = &ticketStore{key: deriveTicketKey(a.csrfKey)}
	// Plugins is the owner's, and an editor's when a plugin UI admits them.
	d.Nav.update(pluginsID, func(it *NavItem) {
		it.Roles = nil
		it.Visible = func(role string) bool { return role == "owner" || a.firstUIPlugin(role) != "" }
		it.HrefFor = func(role string) string {
			if role == "owner" {
				return a.url("plugins")
			}
			return a.url("plugins", url.PathEscape(a.firstUIPlugin(role)), "settings")
		}
	})
	if d.Dir != "" {
		a.adminFS = os.DirFS(d.Dir)
	}
	if err := a.build(); err != nil {
		return nil, err
	}
	return a, nil
}

// Nav returns the sidebar registry (plugins register their items on it).
func (a *Admin) Nav() *Nav { return a.nav }

func (a *Admin) logger() *slog.Logger {
	if a.deps.Logger != nil {
		return a.deps.Logger
	}
	return slog.Default()
}

// Mount registers every /admin route on r. Registration order is irrelevant:
// ServeMux picks the most specific pattern.
func (a *Admin) Mount(r *httpx.Group) {
	g := r.Group(a.adminPath, limitImportBody(a.adminPath), a.limitUploadBody, a.guardFor(scopeAdmin))
	g.HandleFunc("GET /_/{path...}", a.serveAssets)
	root := r.Group("", a.guardFor(scopeRoot)) // /auth and /plugin-ui live outside the prefix
	// The previous admin signed in at /auth/login (and /auth/register); old
	// bookmarks and links land on the Go sign-in page.
	root.HandleFunc("GET /auth/{rest...}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, a.url("login"), http.StatusFound)
	})
	root.HandleFunc("GET /-/edit", a.handleEditLink)
	a.mountPluginUIRoot(root)

	own := g.Group("", secureHeaders, a.demoGuard)
	own.HandleFunc("POST /theme", a.handleTheme)

	authed := own.Group("", a.RequireSession, a.RequireProfile)
	authed.HandleFunc("GET /", a.handleHome)

	a.mountScreens(own, authed)
	if a.deps.Dev {
		authed.Group("", a.RequireRole("owner")).HandleFunc("GET /_styleguide", a.handleStyleguide)
	}

	// Lowest precedence: everything else under /admin.
	fallback := a.fallback()
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		g.Handle(m+" /{rest...}", fallback)
	}
}

// mountScreens is where each admin area registers its routes.
func (a *Admin) mountScreens(public, authed *httpx.Group) {
	limited := public
	if a.deps.RateLimit != nil {
		limited = public.Group("", a.deps.RateLimit)
	}
	a.mountLogin(limited, authed)
	a.mountRecovery(limited)
	a.mountAccept(limited)
	a.mountSetup(limited, authed)
	a.mountUsers(authed)
	a.mountProfile(authed)
	a.mountSudo(authed)
	a.mountPeople(authed)
	a.mountSettings(authed)
	a.mountGrowth(authed)
	a.mountContent(authed)
	a.mountImport(authed)
	a.mountPageEditor(authed, public)
}

// fallback serves unowned /admin paths: a session-gated 404 page.
func (a *Admin) fallback() http.Handler {
	return secureHeaders(legacyRedirectFor(a.legacy, a.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.Error(w, r, http.StatusNotFound, "Page not found", "That address does not exist in the admin.")
	}))))
}

func (a *Admin) serveAssets(w http.ResponseWriter, r *http.Request) {
	as, _ := a.state()
	as.Handler().ServeHTTP(w, r)
}

func (a *Admin) handleHome(w http.ResponseWriter, r *http.Request) { a.handleDashboard(w, r) }
