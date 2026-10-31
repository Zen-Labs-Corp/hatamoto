package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/analytics"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/httpx"
	linkingv2 "hatamoto.dev/core/internal/linking/v2"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/searchperf"
	"hatamoto.dev/core/internal/sectiontemplate"
)

// The interfaces below are the slice of each service the "site and growth"
// screens (menus, forms, redirects, section templates, dashboards, internal
// linking, plugins) use. They are declared here, not in the services, so
// handler tests run on fakes; server/admin_growth_mount.go supplies the real
// ones. A nil member means the screen answers 503.

// MenuStore is menu.Service as the menu screens use it.
type MenuStore interface {
	ListMenus(ctx context.Context) ([]*menu.Menu, error)
	GetMenuWithItems(ctx context.Context, id uuid.UUID) (*menu.Menu, error)
	CreateMenu(ctx context.Context, in *menu.CreateMenuInput) (*menu.Menu, error)
	ReplaceMenu(ctx context.Context, id uuid.UUID, fields *menu.UpdateMenuInput, items []menu.ReplaceItem) (*menu.Menu, error)
	DeleteMenu(ctx context.Context, id uuid.UUID) error
}

// FormStore is form.Service as the forms screens use it.
type FormStore interface {
	ListForms(ctx context.Context, status string) ([]*form.Form, error)
	GetForm(ctx context.Context, id uuid.UUID) (*form.Form, error)
	CreateForm(ctx context.Context, in *form.CreateFormInput) (*form.Form, error)
	UpdateForm(ctx context.Context, id uuid.UUID, in *form.UpdateFormInput) (*form.Form, error)
	DeleteForm(ctx context.Context, id uuid.UUID) error
	ListSubmissions(ctx context.Context, formID uuid.UUID, p *form.ListSubmissionsParams) ([]*form.FormSubmission, int, error)
	GetSubmission(ctx context.Context, id uuid.UUID) (*form.FormSubmission, error)
	UpdateSubmission(ctx context.Context, id uuid.UUID, in *form.UpdateSubmissionInput) (*form.FormSubmission, error)
	DeleteSubmission(ctx context.Context, id uuid.UUID) error
}

// RedirectStore is redirect.Service as the redirect screens use it.
type RedirectStore interface {
	ListRedirects(ctx context.Context, p redirect.RedirectListParams) ([]*redirect.Redirect, int, error)
	GetRedirect(ctx context.Context, id uuid.UUID) (*redirect.Redirect, error)
	CreateRedirect(ctx context.Context, in *redirect.CreateRedirectInput) (*redirect.Redirect, error)
	UpdateRedirect(ctx context.Context, id uuid.UUID, in *redirect.UpdateRedirectInput) (*redirect.Redirect, error)
	DeleteRedirect(ctx context.Context, id uuid.UUID) error
	List404Entries(ctx context.Context, p redirect.NotFoundListParams) ([]*redirect.NotFoundEntry, int, error)
	ListRecent404Entries(ctx context.Context, since time.Time, limit int) ([]*redirect.NotFoundEntry, error)
	Ignore404Entry(ctx context.Context, id uuid.UUID) error
	Delete404Entry(ctx context.Context, id uuid.UUID) error
}

// URLExplanation answers "what happens to this URL": the redirect providers in
// order (the first match wins) and, when none redirects, the page that serves it.
type URLExplanation struct {
	Redirects *redirect.Answer
	// Page is what serves the path when no redirect fires; Kind is "" when
	// nothing does (a 404).
	Page PageAnswer
}

// PageAnswer names what serves a public path.
type PageAnswer struct {
	Kind     string // entity | home | archive | ""
	Name     string
	Schema   string
	Path     string // canonical path
	EntityID string
}

// URLExplainer resolves a path the way the public site does.
type URLExplainer interface {
	ExplainURL(ctx context.Context, path string) (URLExplanation, error)
}

// PickOption is one result of an entity or term search.
type PickOption struct {
	Value string // entity id, or a term's archive path
	Label string
	Hint  string // schema, slug, kind
	Slug  string // an entity's slug (empty for terms)
}

// Pickers search the things a menu item or a rule can point at. This is the
// minimal search-select backend; the entity and media pickers of v1-11 replace
// it.
type Pickers interface {
	SearchEntities(ctx context.Context, q string, limit int) ([]PickOption, error)
	SearchTerms(ctx context.Context, q string, limit int) ([]PickOption, error)
	// EntityName is the display name of an entity, "" when it does not exist.
	EntityName(ctx context.Context, id uuid.UUID) string
}

// SectionStore is sectiontemplate.Service as the section template screens use it.
type SectionStore interface {
	List(ctx context.Context, f *sectiontemplate.ListFilter, limit, offset int) (*sectiontemplate.ListResult, error)
	Get(ctx context.Context, id uuid.UUID) (*sectiontemplate.SectionTemplate, error)
	Update(ctx context.Context, id uuid.UUID, in *sectiontemplate.UpdateInput) (*sectiontemplate.SectionTemplate, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// AgentStats is the read side of AI-crawler analytics (agents.StatsStore plus
// first crawls) and the robots policy.
type AgentStats interface {
	agents.StatsStore
	FirstCrawls(ctx context.Context, rg agents.Range, p agents.Page) ([]agents.FirstCrawlRow, int, error)
	FirstCrawlAggregates(ctx context.Context, rg agents.Range) (agents.FirstCrawlAgg, error)
	PublishedPages(ctx context.Context) (int, error)
	FetchesBySchema(ctx context.Context, rg agents.Range) ([]agents.SeriesPoint, error)
	HealthSeries(ctx context.Context, rg agents.Range) ([]agents.HealthDay, error)
	NeverFetchedBySchema(ctx context.Context) ([]agents.SchemaCount, error)
	agents.RobotsStore
}

// Referrals reads the daily AI-referral and pageview rollup.
type Referrals interface {
	AIReferrals(ctx context.Context, from, to time.Time) ([]analytics.ReferralDay, error)
	Pageviews(ctx context.Context, from, to time.Time) ([]analytics.DayCount, error)
}

// LinkingStore is internal linking v2 as the linking screens use it.
type LinkingStore interface {
	ListRules(ctx context.Context, f *linkingv2.RuleFilter) (*linkingv2.PaginatedRulesResult, error)
	GetRule(ctx context.Context, id uuid.UUID) (*linkingv2.LinkRule, error)
	CreateRule(ctx context.Context, in *linkingv2.CreateRuleInput) (*linkingv2.LinkRule, error)
	UpdateRule(ctx context.Context, id uuid.UUID, in *linkingv2.UpdateRuleInput) (*linkingv2.LinkRule, error)
	DeleteRules(ctx context.Context, ids []uuid.UUID) (int, error)
	DeleteRulesBySource(ctx context.Context, source string) (int, error)
	DeleteAllRules(ctx context.Context) (int, error)
	PreviewRule(ctx context.Context, phrase string, target uuid.UUID) (linkingv2.RulePreviewResult, error)
	Suggest(ctx context.Context, source uuid.UUID) ([]linkingv2.Suggestion, error)
	Equity(ctx context.Context) (*linkingv2.EquityReport, error)
	Inbound(ctx context.Context, target uuid.UUID) ([]linkingv2.EquityDetailRow, error)
	// EntityName is the display name of an entity, "" when it does not exist.
	EntityName(ctx context.Context, id uuid.UUID) string
	// Insert wraps the first usable mention of anchor in source's content with a
	// link to target (the suggestion panel's "Insert").
	Insert(ctx context.Context, source, target uuid.UUID, anchor string) (InsertOutcome, error)
	// SuggestIn is Suggest against the editor's live (unsaved) content html.
	SuggestIn(ctx context.Context, source uuid.UUID, html string) ([]linkingv2.Suggestion, error)
	// WrapIn links the first usable mention of anchor in html to target and
	// returns the new html. Nothing is stored: the editor saves it as usual.
	WrapIn(ctx context.Context, target uuid.UUID, anchor, html string) (string, InsertOutcome, error)
}

// InsertOutcome reports what Insert did.
type InsertOutcome struct {
	Inserted   bool
	SkipReason string
}

// PluginInfo is one plugin as the plugins page shows it.
type PluginInfo struct {
	Name, Version, SDK, Description, Error, DataError string
	Loaded                                            bool
	Hooks, Permissions, Granted, AllowedHosts         []string
	// Refused is the part of Permissions this plugin may never be given (a
	// community plugin and its scripts); the page shows them unticked.
	Refused                      []string
	Routes, Cron, Cookies, Funcs []string // one human line each
	SettingsSchema               []byte
	// Package is set for a plugin installed from a package (nil for one loaded
	// from HATAMOTO_PLUGIN_DIR); UI for one that ships an admin page.
	Package *PackageMeta
	UI      *PluginUIInfo
}

// PluginAdmin is the plugin runtime as the plugins page uses it.
type PluginAdmin interface {
	Plugins() []PluginInfo
	Grant(ctx context.Context, name string, permissions []string) error
	// Settings is the stored settings with schema defaults merged and write-only
	// values masked.
	Settings(ctx context.Context, name string) (map[string]any, error)
	// StoredSecrets names the writeOnly settings that hold a value; the form
	// says "stored" for them and never shows the value.
	StoredSecrets(ctx context.Context, name string) (map[string]bool, error)
	// SaveSettings validates against the plugin's schema; problems come back as
	// field errors (dotted paths), not as an error.
	SaveSettings(ctx context.Context, name string, values map[string]any) (schemaform.Errors, error)
}

// GrowthDeps groups the services of the site and growth screens.
type GrowthDeps struct {
	Menus     MenuStore
	Forms     FormStore
	Redirects RedirectStore
	Explain   URLExplainer
	Pickers   Pickers
	Sections  SectionStore
	Agents    AgentStats
	Referrals Referrals
	// SearchPerf supplies search performance data (nil = none connected).
	SearchPerf searchperf.Provider
	Linking    LinkingStore
	Plugins    PluginAdmin
	Packages   PackageAdmin
	FormMail   FormMailer
	// AgentRegistry names the crawlers (agents.Default() when nil).
	AgentRegistry *agents.Registry
}

// mountGrowth registers the site and growth screens.
func (a *Admin) mountGrowth(authed *httpx.Group) {
	a.mountThemeFrame(authed)
	a.mountMenus(authed)
	a.mountForms(authed)
	a.mountRedirects(authed)
	a.mountSections(authed)
	a.mountAnalytics(authed)
	a.mountLinking(authed)
	a.mountPlugins(authed)
	a.mountThemes(authed)
}

// unavailable answers a screen whose service the server did not wire.
func (a *Admin) unavailable(w http.ResponseWriter, r *http.Request, what string) {
	a.Error(w, r, http.StatusServiceUnavailable, "Not available", what+" is not available on this server.")
}

// writeJSON answers the JSON endpoints behind the admin pages (pickers, chart
// data). They are behind the session, so they are never cached.
func (a *Admin) writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		a.logger().Error("admin: write json failed", "err", err)
	}
}

// bulkDelete drives pages/bulkdelete: the confirmation shown before a bulk
// delete (also the no-JS path).
type bulkDelete struct {
	Count      int
	Post, Back string
	IDs        []string
	Noun       string
	Action     string
}
