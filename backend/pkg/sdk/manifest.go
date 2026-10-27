package sdk

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

// NamePattern restricts plugin names: they become URL segments, schema
// suffixes and settings keys.
var NamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)

// Limits bounds a plugin's resource use. Zero fields take the Default* values.
type Limits struct {
	// MemoryPages is the cap on each wasm linear memory, in 64 KiB pages.
	// A standard Go guest needs at least MinMemoryPages.
	MemoryPages uint32 `json:"memory_pages,omitempty"`
	// HookTimeoutsMS overrides the time one call of a hook may run, in
	// milliseconds. Keys are hook names ("render.page"), or "http_handle",
	// "on_cron_<job>" and "func_<name>" for the other exports. A hook not
	// listed gets DefaultFilterTimeoutMS (filters and template funcs) or
	// DefaultActionTimeoutMS (actions, cron, routes). Zero is refused: a
	// guest without a deadline cannot be stopped.
	HookTimeoutsMS map[string]uint64 `json:"hook_timeouts_ms,omitempty"`
	// MaxInstances caps concurrent calls (and so live instances) of the plugin.
	MaxInstances int `json:"max_instances,omitempty"`
	// MaxOutputBytes caps what one call may return. Larger output fails the
	// call (fail open); it never reaches the JSON decoder.
	MaxOutputBytes uint32 `json:"max_output_bytes,omitempty"`
}

// Limit defaults and bounds enforced by Manifest.Validate.
const (
	DefaultMemoryPages = 256  // 16 MiB
	MinMemoryPages     = 64   // 4 MiB: a standard Go guest does not start below this
	MaxMemoryPages     = 1024 // 64 MiB

	// Per-hook timeouts (Limits.HookTimeoutsMS).
	DefaultFilterTimeoutMS = 250
	DefaultActionTimeoutMS = 10000
	MaxTimeoutMS           = 10000

	DefaultMaxInstances = 4
	MaxMaxInstances     = 16
	DefaultOutputBytes  = 1 << 20 // 1 MiB
	MaxOutputBytes      = 4 << 20 // 4 MiB
)

// Manifest is the content of a plugin's plugin.json.
type Manifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"` // the plugin's own semver
	SDK         string `json:"sdk"`     // SDK range the plugin was built against, e.g. "^0.5"
	Description string `json:"description,omitempty"`

	// Config is handed to the guest (pdk.GetConfig).
	Config map[string]string `json:"config,omitempty"`

	// AllowedHosts is the plugin's outbound HTTP allowlist for http.fetch
	// (exact hosts, "*.example.com", "host:port"). "*" means any public
	// address on ports 80 and 443 and needs the http.fetch.any permission,
	// which the owner grants separately. Without the http.fetch permission
	// granted it has no effect.
	AllowedHosts []string `json:"allowed_hosts,omitempty"`

	// Permissions are the capabilities the plugin asks for (see Permissions).
	// The owner grants them; nothing is granted by default.
	Permissions []Permission `json:"permissions,omitempty"`

	// Routes are served under /api/v1/p/<name>/... (signed in, CSRF, roles)
	// or, with Public set, /p/<name>/... (anonymous, rate limited), by the
	// plugin's `http_handle` export.
	Routes []Route `json:"routes,omitempty"`

	// Cron lists scheduled calls of the plugin's `on_cron_<name>` exports.
	Cron []CronJob `json:"cron,omitempty"`

	// Cookies are the cookie names a render.page filter may read and set.
	Cookies []string `json:"cookies,omitempty"`

	// RenderPage tunes the render.page filter (see PageOptions).
	RenderPage *PageOptions `json:"render_page,omitempty"`

	// Funcs are template functions: the theme calls plugin_<name>_<func>
	// and the host calls the plugin's `func_<func>` export with JSON args.
	Funcs []string `json:"funcs,omitempty"`

	// UI is an optional admin page the plugin ships in its package's ui/
	// directory. It runs in a sandboxed iframe and reaches the host only
	// through a bridge limited to the plugin's own routes and granted host
	// functions.
	UI *AdminUI `json:"ui,omitempty"`

	// BuilderSources declares the data sources the plugin answers through the
	// builder.data filter (at most MaxBuilderSources). A source's id in the
	// builder is ext.<plugin>.<name>. Needs the builder.data permission.
	BuilderSources []BuilderSource `json:"builder_sources,omitempty"`

	Limits Limits `json:"limits,omitempty"`

	// SettingsSchema is an optional JSON Schema (draft 2020-12) object
	// describing the plugin's settings.
	SettingsSchema json.RawMessage `json:"settings_schema,omitempty"`
}

// BuilderSource is one data source a plugin offers to the page builder.
type BuilderSource struct {
	// Name is the source's short name, unique within the plugin.
	Name string `json:"name"`
	// Label is shown in the editor (1 to 60 characters).
	Label string `json:"label"`
	// Kind is BuilderSourceValue (one string for a prop) or
	// BuilderSourceItems (a list for a loop).
	Kind string `json:"kind"`
	// Class is the prop class a value source fits: text, url or image
	// (default text). Only for value sources.
	Class string `json:"class,omitempty"`
}

// Builder source kinds and value classes.
const (
	BuilderSourceValue = "value"
	BuilderSourceItems = "items"

	MaxBuilderSources = 8
)

// builderSourceNamePattern is a builder source name.
var builderSourceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func (m Manifest) validateBuilderSources() error {
	if len(m.BuilderSources) == 0 {
		return nil
	}
	if len(m.BuilderSources) > MaxBuilderSources {
		return fmt.Errorf("sdk: plugin %q: more than %d builder_sources", m.Name, MaxBuilderSources)
	}
	if !slices.Contains(m.Permissions, PermBuilderData) {
		return fmt.Errorf("sdk: plugin %q: builder_sources needs the %q permission", m.Name, PermBuilderData)
	}
	seen := map[string]bool{}
	for _, s := range m.BuilderSources {
		if !builderSourceNamePattern.MatchString(s.Name) {
			return fmt.Errorf("sdk: plugin %q: builder source name %q (want %s)", m.Name, s.Name, builderSourceNamePattern)
		}
		if seen[s.Name] {
			return fmt.Errorf("sdk: plugin %q: duplicate builder source %q", m.Name, s.Name)
		}
		seen[s.Name] = true
		if n := len([]rune(s.Label)); n < 1 || n > 60 {
			return fmt.Errorf("sdk: plugin %q: builder source %q: label must be 1 to 60 characters", m.Name, s.Name)
		}
		switch s.Kind {
		case BuilderSourceValue:
			if s.Class != "" && s.Class != "text" && s.Class != "url" && s.Class != "image" {
				return fmt.Errorf("sdk: plugin %q: builder source %q: class %q (want text, url or image)", m.Name, s.Name, s.Class)
			}
		case BuilderSourceItems:
			if s.Class != "" {
				return fmt.Errorf("sdk: plugin %q: builder source %q: class applies to value sources only", m.Name, s.Name)
			}
		default:
			return fmt.Errorf("sdk: plugin %q: builder source %q: kind %q (want value or items)", m.Name, s.Name, s.Kind)
		}
	}
	return nil
}

// PageOptions are the manifest options of the render.page filter.
type PageOptions struct {
	// FullBlocks sends every block with its whole content (the pre-0.9
	// payload). It is slower: the cost of a call grows with the page's size.
	FullBlocks bool `json:"full_blocks,omitempty"`
}

// slimPageSDK is the first SDK version whose render.page payload is slim.
const slimPageSDK = "0.9.0"

// FullPageBlocks reports whether the host sends this plugin render.page blocks
// with their whole content: when it asks for it, or when it was built for an
// SDK range that includes neither 0.9 nor the current SDK (those plugins
// expect the old payload).
func (m Manifest) FullPageBlocks() bool {
	if m.RenderPage != nil && m.RenderPage.FullBlocks {
		return true
	}
	ok, err := Satisfies(slimPageSDK, m.SDK)
	// A range such as ^0.10 does not contain 0.9.0 but is newer than it:
	// any version from 0.9.0 on (the compatible ones and the current) counts.
	for _, v := range append(slices.Clone(CompatibleWith), Version) {
		if err != nil || ok {
			break
		}
		if later, _ := Satisfies(v, ">"+slimPageSDK); later {
			ok, err = Satisfies(v, m.SDK)
		}
	}
	return err != nil || !ok
}

// Route is one HTTP route of a plugin. Path is relative to the plugin's
// mount and uses net/http patterns ("/leads", "/leads/{id}", "/files/{rest...}").
type Route struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	// Public routes are mounted at /p/<name> and need no sign-in.
	Public bool `json:"public,omitempty"`
	// Roles may call a non-public route; empty means owner only.
	Roles []string `json:"roles,omitempty"`
}

// AdminUI declares a plugin's admin page.
type AdminUI struct {
	// Title is the sidebar label and the page heading.
	Title string `json:"title"`
	// Entry is the HTML file under ui/ (default "index.html").
	Entry string `json:"entry,omitempty"`
	// Roles may open the page; empty means owner only.
	Roles []string `json:"roles,omitempty"`
}

// DefaultUIEntry is the AdminUI entry when none is declared.
const DefaultUIEntry = "index.html"

// uiEntryPattern is a relative file path made of safe characters.
var uiEntryPattern = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_./-]{0,99}\.html$`)

func (u AdminUI) validate() error {
	if n := len([]rune(u.Title)); n < 1 || n > 40 {
		return fmt.Errorf("ui.title must be 1 to 40 characters")
	}
	if e := u.Entry; e != "" && (!uiEntryPattern.MatchString(e) || strings.Contains(e, "..") || strings.Contains(e, "//")) {
		return fmt.Errorf("ui.entry %q must be a relative .html path under ui/", e)
	}
	for _, role := range u.Roles {
		if role != "owner" && role != "editor" {
			return fmt.Errorf("ui.roles: %q (want owner or editor)", role)
		}
	}
	return nil
}

// CronJob is a scheduled call. Every is a Go duration of at least one minute.
type CronJob struct {
	Name  string `json:"name"`
	Every string `json:"every"`
}

// Limits on the manifest's lists.
const (
	MaxRoutes    = 64
	MaxCronJobs  = 16
	MaxFuncs     = 64
	MaxCookies   = 8
	MinCronEvery = time.Minute
)

// identPattern is a cron job, template func or section name.
var identPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// cookieNamePattern restricts declared cookie names to a safe token.
var cookieNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// WithDefaults returns m with zero limits replaced by the defaults.
func (m Manifest) WithDefaults() Manifest {
	if m.Limits.MemoryPages == 0 {
		m.Limits.MemoryPages = DefaultMemoryPages
	}
	if m.Limits.MaxInstances == 0 {
		m.Limits.MaxInstances = DefaultMaxInstances
	}
	if m.Limits.MaxOutputBytes == 0 {
		m.Limits.MaxOutputBytes = DefaultOutputBytes
	}
	return m
}

// Validate checks the manifest is well formed and its limits are in range.
// It does not check SDK compatibility with the running host (see Compatible).
func (m Manifest) Validate() error {
	if !NamePattern.MatchString(m.Name) {
		return fmt.Errorf("sdk: invalid plugin name %q (want %s)", m.Name, NamePattern)
	}
	if _, _, err := parseSemver(m.Version); err != nil {
		return fmt.Errorf("sdk: plugin %q: version: %w", m.Name, err)
	}
	if _, err := parseRange(m.SDK); err != nil {
		return fmt.Errorf("sdk: plugin %q: sdk range: %w", m.Name, err)
	}
	l := m.WithDefaults().Limits
	switch {
	case l.MemoryPages < MinMemoryPages || l.MemoryPages > MaxMemoryPages:
		return fmt.Errorf("sdk: plugin %q: limits.memory_pages %d outside %d..%d", m.Name, l.MemoryPages, MinMemoryPages, MaxMemoryPages)
	case l.MaxInstances < 1 || l.MaxInstances > MaxMaxInstances:
		return fmt.Errorf("sdk: plugin %q: limits.max_instances %d outside 1..%d", m.Name, l.MaxInstances, MaxMaxInstances)
	case l.MaxOutputBytes > MaxOutputBytes:
		return fmt.Errorf("sdk: plugin %q: limits.max_output_bytes %d above %d", m.Name, l.MaxOutputBytes, MaxOutputBytes)
	}
	if err := m.validateHookTimeouts(); err != nil {
		return err
	}
	for _, h := range m.AllowedHosts {
		if h == "" {
			return fmt.Errorf("sdk: plugin %q: empty allowed_hosts entry", m.Name)
		}
	}
	if err := m.validateCapabilities(); err != nil {
		return err
	}
	if err := m.validateBuilderSources(); err != nil {
		return err
	}
	if slices.Contains(m.AllowedHosts, "*") && !slices.Contains(m.Permissions, PermHTTPFetchAny) {
		return fmt.Errorf("sdk: plugin %q: allowed_hosts \"*\" needs the %q permission", m.Name, PermHTTPFetchAny)
	}
	if slices.Contains(m.Permissions, PermHTTPFetchAny) && !slices.Contains(m.Permissions, PermHTTPFetch) {
		return fmt.Errorf("sdk: plugin %q: %q needs %q as well", m.Name, PermHTTPFetchAny, PermHTTPFetch)
	}
	return nil
}

// validateHookTimeouts checks Limits.HookTimeoutsMS: every key names a hook
// or export the plugin can have, every value is within 1..MaxTimeoutMS.
func (m Manifest) validateHookTimeouts() error {
	for key, ms := range m.Limits.HookTimeoutsMS {
		switch {
		case ms == 0:
			return fmt.Errorf("sdk: plugin %q: limits.hook_timeouts_ms[%q] is 0; a timeout must be at least 1 ms (omit the key for the default)", m.Name, key)
		case ms > MaxTimeoutMS:
			return fmt.Errorf("sdk: plugin %q: limits.hook_timeouts_ms[%q] %d above %d", m.Name, key, ms, MaxTimeoutMS)
		case !m.knownTimeoutKey(key):
			return fmt.Errorf("sdk: plugin %q: limits.hook_timeouts_ms: unknown hook %q", m.Name, key)
		}
	}
	return nil
}

func (m Manifest) knownTimeoutKey(key string) bool {
	if key == "http_handle" || slices.Contains(ActionNames, key) || slices.Contains(FilterNames, key) {
		return true
	}
	if job, ok := strings.CutPrefix(key, "on_cron_"); ok {
		return slices.ContainsFunc(m.Cron, func(c CronJob) bool { return c.Name == job })
	}
	if fn, ok := strings.CutPrefix(key, "func_"); ok {
		return slices.Contains(m.Funcs, fn)
	}
	return false
}

// HookTimeout is how long one call of the plugin's export may run: the
// manifest's override (keyed by hook name or export name), else the default
// for the export's kind: DefaultFilterTimeoutMS for filter_* and func_*,
// DefaultActionTimeoutMS for everything else.
func (m Manifest) HookTimeout(export string) time.Duration {
	ms := uint64(DefaultActionTimeoutMS)
	if strings.HasPrefix(export, "filter_") || strings.HasPrefix(export, "func_") {
		ms = DefaultFilterTimeoutMS
	}
	if v := m.Limits.HookTimeoutsMS[export]; v > 0 {
		ms = v
	} else if v := m.Limits.HookTimeoutsMS[hookOfExport(export)]; v > 0 {
		ms = v
	}
	return time.Duration(ms) * time.Millisecond
}

// hookOfExport is the built-in hook name a on_/filter_ export serves, or "".
func hookOfExport(export string) string {
	for _, pair := range []struct {
		prefix string
		names  []string
	}{{"on_", ActionNames}, {"filter_", FilterNames}} {
		for _, hook := range pair.names {
			if ExportName(pair.prefix, hook) == export {
				return hook
			}
		}
	}
	return ""
}

func (m Manifest) validateCapabilities() error {
	seen := map[string]bool{}
	for _, p := range m.Permissions {
		if !ValidPermission(p) {
			return fmt.Errorf("sdk: plugin %q: unknown permission %q", m.Name, p)
		}
		if seen[p] {
			return fmt.Errorf("sdk: plugin %q: duplicate permission %q", m.Name, p)
		}
		seen[p] = true
	}
	if len(m.Routes) > MaxRoutes {
		return fmt.Errorf("sdk: plugin %q: more than %d routes", m.Name, MaxRoutes)
	}
	if err := m.validateRoutes(); err != nil {
		return fmt.Errorf("sdk: plugin %q: routes: %w", m.Name, err)
	}
	if len(m.Cron) > MaxCronJobs {
		return fmt.Errorf("sdk: plugin %q: more than %d cron jobs", m.Name, MaxCronJobs)
	}
	names := map[string]bool{}
	for _, c := range m.Cron {
		every, err := time.ParseDuration(c.Every)
		switch {
		case !identPattern.MatchString(c.Name):
			return fmt.Errorf("sdk: plugin %q: cron name %q (want %s)", m.Name, c.Name, identPattern)
		case names[c.Name]:
			return fmt.Errorf("sdk: plugin %q: duplicate cron job %q", m.Name, c.Name)
		case err != nil:
			return fmt.Errorf("sdk: plugin %q: cron %q: every: %w", m.Name, c.Name, err)
		case every < MinCronEvery:
			return fmt.Errorf("sdk: plugin %q: cron %q: every %s is below the %s minimum", m.Name, c.Name, c.Every, MinCronEvery)
		}
		names[c.Name] = true
	}
	if len(m.Funcs) > MaxFuncs {
		return fmt.Errorf("sdk: plugin %q: more than %d funcs", m.Name, MaxFuncs)
	}
	funcs := map[string]bool{}
	for _, f := range m.Funcs {
		if !identPattern.MatchString(f) || funcs[f] {
			return fmt.Errorf("sdk: plugin %q: bad or duplicate func %q (want %s)", m.Name, f, identPattern)
		}
		funcs[f] = true
	}
	if m.UI != nil {
		if err := m.UI.validate(); err != nil {
			return fmt.Errorf("sdk: plugin %q: %w", m.Name, err)
		}
	}
	if len(m.Cookies) > MaxCookies {
		return fmt.Errorf("sdk: plugin %q: more than %d cookies", m.Name, MaxCookies)
	}
	for _, c := range m.Cookies {
		if !cookieNamePattern.MatchString(c) {
			return fmt.Errorf("sdk: plugin %q: cookie name %q", m.Name, c)
		}
		// The host stores plugin cookies as hmp_<plugin>_<name>, so a plugin
		// cannot reach a core cookie anyway; refuse core-looking names so a
		// manifest never suggests it can.
		if lc := strings.ToLower(c); strings.HasPrefix(lc, "hatamoto_") || strings.HasPrefix(lc, "__host-") || strings.HasPrefix(lc, "__secure-") || strings.HasPrefix(lc, "hmp_") {
			return fmt.Errorf("sdk: plugin %q: cookie name %q is reserved", m.Name, c)
		}
	}
	return nil
}

// validateRoutes checks methods, patterns and roles, and that no two routes
// overlap, by registering them on a scratch ServeMux (which panics on a bad
// or conflicting pattern).
func (m Manifest) validateRoutes() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	admin, public := http.NewServeMux(), http.NewServeMux()
	for _, r := range m.Routes {
		switch r.Method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			return fmt.Errorf("method %q (want GET, POST, PUT, PATCH or DELETE)", r.Method)
		}
		if !strings.HasPrefix(r.Path, "/") {
			return fmt.Errorf("path %q must start with /", r.Path)
		}
		for _, role := range r.Roles {
			if role != "owner" && role != "editor" && role != "viewer" {
				return fmt.Errorf("role %q", role)
			}
		}
		if r.Public && len(r.Roles) > 0 {
			return fmt.Errorf("%s %s: a public route has no roles", r.Method, r.Path)
		}
		mux := admin
		if r.Public {
			mux = public
		}
		mux.HandleFunc(r.Method+" "+r.Path, func(http.ResponseWriter, *http.Request) {})
	}
	return nil
}

// Compatible reports whether this SDK satisfies the manifest's SDK range.
func (m Manifest) Compatible() (bool, error) {
	return compatibleWith(Version, CompatibleWith, m.SDK)
}

// compatibleWith is Compatible with the host values passed in, so tests can
// model another host.
func compatibleWith(version string, list []string, rng string) (bool, error) {
	if ok, err := Satisfies(version, rng); err != nil || ok {
		return ok, err
	}
	for _, v := range list {
		if ok, _ := Satisfies(v, rng); ok {
			return true, nil
		}
	}
	return false, nil
}
