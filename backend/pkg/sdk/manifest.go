package sdk

import (
	"encoding/json/jsontext"
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
	MemoryPages uint32 `json:"memory_pages,omitzero"`
	// HookTimeoutsMS overrides the time one call of a hook may run, in
	// milliseconds. Keys are hook names ("render.page"), or "http_handle",
	// "on_cron_<job>" and "func_<name>" for the other exports. A hook not
	// listed gets DefaultFilterTimeoutMS (filters and template funcs) or
	// DefaultActionTimeoutMS (actions, cron, routes). Zero is refused: a
	// guest without a deadline cannot be stopped.
	HookTimeoutsMS map[string]uint64 `json:"hook_timeouts_ms,omitempty"`
	// MaxInstances caps concurrent calls (and so live instances) of the plugin.
	MaxInstances int `json:"max_instances,omitzero"`
	// MaxOutputBytes caps what one call may return. Larger output fails the
	// call (fail open); it never reaches the JSON decoder.
	MaxOutputBytes uint32 `json:"max_output_bytes,omitzero"`
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

	// RequestHeaders are extra request header names (for example a webhook
	// signature) passed to this plugin's routes, on top of the fixed list.
	// Credentials and the visitor's address can never be listed
	// (see forbiddenRequestHeaders). At most MaxRequestHeaders.
	RequestHeaders []string `json:"request_headers,omitempty"`

	// CSP lists the https hosts the plugin needs added to the public pages'
	// Content-Security-Policy. The owner sees them in the permission prompt;
	// they apply only while the plugin is enabled. See CSP.
	CSP *CSP `json:"csp,omitzero"`

	// RenderPage tunes the render.page filter (see PageOptions).
	RenderPage *PageOptions `json:"render_page,omitzero"`

	// Funcs are template functions: the theme calls plugin_<name>_<func>
	// and the host calls the plugin's `func_<func>` export with JSON args.
	Funcs []string `json:"funcs,omitempty"`

	// UI is an optional admin page the plugin ships in its package's ui/
	// directory. It runs in a sandboxed iframe and reaches the host only
	// through a bridge limited to the plugin's own routes and granted host
	// functions.
	UI *AdminUI `json:"ui,omitzero"`

	// BuilderSources declares the data sources the plugin answers through the
	// builder.data filter (at most MaxBuilderSources). A source's id in the
	// builder is ext.<plugin>.<name>. Needs the builder.data permission.
	BuilderSources []BuilderSource `json:"builder_sources,omitempty"`

	// Runs declares the runs the owner can start (fetch phase, then transform
	// phase). A plugin with runs must export on_run_step. Runs need no
	// permission.
	Runs []Run `json:"runs,omitempty"`

	Limits Limits `json:"limits"`

	// SettingsSchema is an optional JSON Schema (draft 2020-12) object
	// describing the plugin's settings.
	SettingsSchema jsontext.Value `json:"settings_schema,omitempty"`
}

// CSP is the set of hosts a plugin asks to have added to the public pages'
// Content-Security-Policy, per directive. Each entry is an https origin
// ("https://api.example.com") or one with a leading wildcard label
// ("https://*.example.com"): no keywords, no "*", no path or port, at most
// MaxCSPHosts per list. Only script-src and connect-src change what a page may
// do today (the base policy already allows https images and frames); community
// plugins may list image hosts only.
type CSP struct {
	FrameSrc   []string `json:"frame_src,omitempty"`
	ImgSrc     []string `json:"img_src,omitempty"`
	ScriptSrc  []string `json:"script_src,omitempty"`
	ConnectSrc []string `json:"connect_src,omitempty"`
}

// MaxCSPHosts caps each list of CSP.
const MaxCSPHosts = 8

// cspHostPattern is an https origin, optionally with one leading wildcard
// label: scheme, DNS labels, no port, path or query.
var cspHostPattern = regexp.MustCompile(`^https://(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Empty reports whether c lists no host.
func (c *CSP) Empty() bool {
	return c == nil || len(c.FrameSrc)+len(c.ImgSrc)+len(c.ScriptSrc)+len(c.ConnectSrc) == 0
}

func (c *CSP) validate() error {
	for name, list := range map[string][]string{"frame_src": c.FrameSrc, "img_src": c.ImgSrc, "script_src": c.ScriptSrc, "connect_src": c.ConnectSrc} {
		if len(list) > MaxCSPHosts {
			return fmt.Errorf("csp.%s: more than %d hosts", name, MaxCSPHosts)
		}
		seen := map[string]bool{}
		for _, h := range list {
			if !cspHostPattern.MatchString(h) {
				return fmt.Errorf("csp.%s: %q is not an https host (want https://host or https://*.host)", name, h)
			}
			if seen[h] {
				return fmt.Errorf("csp.%s: duplicate host %q", name, h)
			}
			if err := checkCSPHost(name, h); err != nil {
				return err
			}
			seen[h] = true
		}
	}
	return nil
}

// sharedHostSuffixes are public suffixes and multi-tenant platforms where
// anyone can register a subdomain, so a wildcard over them trusts strangers.
var sharedHostSuffixes = []string{"github.io", "githubusercontent.com", "herokuapp.com", "amazonaws.com", "cloudfront.net", "netlify.app", "vercel.app", "pages.dev", "workers.dev", "web.app", "firebaseapp.com", "blogspot.com", "azurewebsites.net", "appspot.com", "co.uk", "com.au", "co.jp", "co.nz", "org.uk"}

// checkCSPHost refuses hosts a well-formed pattern still makes unsafe: a
// wildcard in script-src, a wildcard over a shared suffix or with fewer than
// two labels after it, and IP literals.
func checkCSPHost(list, h string) error {
	host := strings.TrimPrefix(h, "https://")
	wild := strings.HasPrefix(host, "*.")
	if wild && list == "script_src" {
		return fmt.Errorf("csp.%s: wildcard host %q is not allowed in script_src", list, h)
	}
	if wild && strings.Count(host, ".") < 2 {
		return fmt.Errorf("csp.%s: wildcard host %q needs at least two labels after the *", list, h)
	}
	if wild && slices.Contains(sharedHostSuffixes, strings.TrimPrefix(host, "*.")) {
		return fmt.Errorf("csp.%s: wildcard host %q covers hosts anyone can register", list, h)
	}
	if last := host[strings.LastIndex(host, ".")+1:]; last[0] >= '0' && last[0] <= '9' {
		return fmt.Errorf("csp.%s: %q is an IP address, not a host name", list, h)
	}
	return nil
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
	// Params are the filters an items source accepts (at most
	// MaxBuilderParams). The editor stores a value per loop and the host sends
	// the sanitized values on the builder.data request.
	Params []BuilderParam `json:"params,omitempty"`
}

// BuilderParam is one typed filter of an items source.
type BuilderParam struct {
	// Name is the key in the request's params (same shape as a source name).
	Name string `json:"name"`
	// Label is shown in the editor (1 to 60 characters).
	Label string `json:"label"`
	// Type is BuilderParamText, BuilderParamSelect, BuilderParamBool or
	// BuilderParamNumber.
	Type string `json:"type"`
	// Options are the choices of a select param (1 to MaxBuilderParamOptions);
	// no other type takes options.
	Options []BuilderOption `json:"options,omitempty"`
}

// BuilderOption is one choice of a select param. Label defaults to Value.
type BuilderOption struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// Builder source kinds and value classes.
const (
	BuilderSourceValue = "value"
	BuilderSourceItems = "items"

	MaxBuilderSources = 8
)

// Builder param types and limits.
const (
	BuilderParamText   = "text"
	BuilderParamSelect = "select"
	BuilderParamBool   = "bool"
	BuilderParamNumber = "number"

	MaxBuilderParams       = 8
	MaxBuilderParamOptions = 50
	// MaxBuilderParamValue is the longest text or option value, in bytes.
	MaxBuilderParamValue = 200
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
			if len(s.Params) > 0 {
				return fmt.Errorf("sdk: plugin %q: builder source %q: params apply to items sources only", m.Name, s.Name)
			}
		case BuilderSourceItems:
			if s.Class != "" {
				return fmt.Errorf("sdk: plugin %q: builder source %q: class applies to value sources only", m.Name, s.Name)
			}
			if err := validateBuilderParams(m.Name, s); err != nil {
				return err
			}
		default:
			return fmt.Errorf("sdk: plugin %q: builder source %q: kind %q (want value or items)", m.Name, s.Name, s.Kind)
		}
	}
	return nil
}

func validateBuilderParams(plugin string, s BuilderSource) error {
	if len(s.Params) > MaxBuilderParams {
		return fmt.Errorf("sdk: plugin %q: builder source %q: more than %d params", plugin, s.Name, MaxBuilderParams)
	}
	seen := map[string]bool{}
	for _, p := range s.Params {
		fail := func(format string, a ...any) error {
			return fmt.Errorf("sdk: plugin %q: builder source %q: param %q: %s", plugin, s.Name, p.Name, fmt.Sprintf(format, a...))
		}
		if !builderSourceNamePattern.MatchString(p.Name) {
			return fail("name (want %s)", builderSourceNamePattern)
		}
		if seen[p.Name] {
			return fail("duplicate param")
		}
		seen[p.Name] = true
		if n := len([]rune(p.Label)); n < 1 || n > 60 {
			return fail("label must be 1 to 60 characters")
		}
		switch p.Type {
		case BuilderParamSelect:
			if len(p.Options) < 1 || len(p.Options) > MaxBuilderParamOptions {
				return fail("a select needs 1 to %d options", MaxBuilderParamOptions)
			}
			vals := map[string]bool{}
			for _, o := range p.Options {
				if o.Value == "" || len(o.Value) > MaxBuilderParamValue {
					return fail("option value must be 1 to %d bytes", MaxBuilderParamValue)
				}
				if vals[o.Value] {
					return fail("duplicate option %q", o.Value)
				}
				vals[o.Value] = true
			}
		case BuilderParamText, BuilderParamBool, BuilderParamNumber:
			if len(p.Options) > 0 {
				return fail("options apply to select params only")
			}
		default:
			return fail("type %q (want text, select, bool or number)", p.Type)
		}
	}
	return nil
}

// PageOptions are the manifest options of the render.page filter.
type PageOptions struct {
	// FullBlocks sends every block with its whole content (the pre-0.9
	// payload). It is slower: the cost of a call grows with the page's size.
	FullBlocks bool `json:"full_blocks,omitzero"`
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
	Public bool `json:"public,omitzero"`
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
	MaxRoutes   = 64
	MaxCronJobs = 16
	MaxFuncs    = 64
	MaxCookies  = 8
	// MaxRequestHeaders caps Manifest.RequestHeaders.
	MaxRequestHeaders = 8
	MinCronEvery      = time.Minute
)

// identPattern is a cron job, template func or section name.
var identPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// cookieNamePattern restricts declared cookie names to a safe token.
var cookieNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// headerNamePattern is an HTTP header field name (a token), as declared in
// request_headers.
var headerNamePattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// forbiddenRequestHeaders never reach a plugin: credentials and every header
// that carries the visitor's address.
var forbiddenRequestHeaders = []string{"authorization", "cookie", "proxy-authorization", "x-forwarded-for", "x-real-ip", "forwarded", "cf-connecting-ip",
	"connection", "te", "upgrade", "keep-alive", "transfer-encoding", "host", "content-length", "via", "trailer"}

// forbiddenHeaderTokens are dash-separated words that mark a header as one a
// proxy or CDN uses to carry the visitor's address (True-Client-IP,
// Fastly-Client-IP, X-Envoy-External-Address, ...). Refusing by shape covers
// the vendor headers the explicit list cannot name.
var forbiddenHeaderTokens = []string{"ip", "ips", "ipv4", "ipv6", "addr", "address", "forwarded", "client", "real", "for", "connecting", "remote", "via"}

// forbiddenRequestHeader reports whether the lower-case header name is never
// passed to plugins.
func forbiddenRequestHeader(lc string) bool {
	if slices.Contains(forbiddenRequestHeaders, lc) || strings.HasPrefix(lc, "proxy-") || strings.Contains(lc, "forwarded") {
		return true
	}
	return slices.ContainsFunc(strings.Split(lc, "-"), func(t string) bool { return slices.Contains(forbiddenHeaderTokens, t) })
}

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
	if err := m.validateRuns(); err != nil {
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
	if key == "http_handle" || key == RunStepExport || slices.Contains(ActionNames, key) || slices.Contains(FilterNames, key) {
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
		if repl, gone := removedPermissions[p]; gone {
			return fmt.Errorf("plugin %q not loaded: permission %q was removed in Hatamoto 1.1.\n"+
				"  Replace it with a run: declare a \"runs\" block in plugin.json and call %s\n"+
				"  from on_run_step. See docs/PLUGIN_API.md \"Runs and changesets\"", m.Name, p, repl)
		}
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
	if err := m.validateRequestHeaders(); err != nil {
		return err
	}
	if m.CSP != nil {
		if err := m.CSP.validate(); err != nil {
			return fmt.Errorf("sdk: plugin %q: %w", m.Name, err)
		}
	}
	return nil
}

// validateRequestHeaders checks the declared request header names.
func (m Manifest) validateRequestHeaders() error {
	if len(m.RequestHeaders) > MaxRequestHeaders {
		return fmt.Errorf("sdk: plugin %q: more than %d request headers", m.Name, MaxRequestHeaders)
	}
	seen := map[string]bool{}
	for _, h := range m.RequestHeaders {
		lc := strings.ToLower(h)
		if !headerNamePattern.MatchString(h) {
			return fmt.Errorf("sdk: plugin %q: request header name %q", m.Name, h)
		}
		if forbiddenRequestHeader(lc) {
			return fmt.Errorf("sdk: plugin %q: request header %q is never passed to plugins", m.Name, h)
		}
		if seen[lc] {
			return fmt.Errorf("sdk: plugin %q: duplicate request header %q", m.Name, h)
		}
		seen[lc] = true
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
