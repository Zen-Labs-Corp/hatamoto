package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"hatamoto.dev/core/internal/admin/schemaform"
)

// The types and interface below are what the Plugins and Themes pages need of
// the package manager (internal/packages). The server adapts the manager to
// them (server/admin_packages.go), so handler tests run on fakes.

// ErrThemeActive is what Uninstall answers for the active theme.
var ErrThemeActive = errors.New("theme is active")

// Refusal is a package that was refused for a reason the owner should read
// (bad signature, unsigned, unknown permission, a theme that does not render).
type Refusal struct{ Msg string }

func (e *Refusal) Error() string { return e.Msg }

// RegistryEntry is one package the registry lists.
type RegistryEntry struct {
	Kind, Name, Version, Description, Homepage string
	Permissions                                []string
	Installed                                  bool
	InstalledVersion                           string
	// Update is set when the registry's version is newer than the installed one.
	Update bool
	// Descriptive fields the registry may leave empty.
	Title, Author, Tier, Category, About, Source, Commit string
	Features                                             []string
	Size                                                 int64
	Featured                                             bool
}

// RegistryView is what browsing the registry returned.
type RegistryView struct {
	Entries []RegistryEntry
	// Off means no registry is configured; Err says why a configured one failed.
	Off bool
	Err string
	// Host is the registry's host name; set whenever a registry is configured.
	Host string
	// FetchedAt is when the index was last read from the registry (zero if it
	// never was or the read failed before the cache was filled).
	FetchedAt time.Time
}

// UnsignedState is the owner's opt-in for unsigned packages, already
// evaluated against the clock: On is false once Until has passed.
type UnsignedState struct {
	On bool
	// Until is when a timed opt-in ends; nil is "until it is turned off".
	Until *time.Time
}

// SignerInfo is one key whose packages and registry indexes are trusted.
type SignerInfo struct {
	Name, Algorithm, Fingerprint string
	// Source says where the key comes from: "Built in" or "Environment".
	Source string
}

// PackageMeta is what the packages table knows about an installed plugin.
type PackageMeta struct {
	Enabled bool
	// WidgetOnly is set for a package that ships widgets and no plugin code:
	// the runtime has no status for it, and that is healthy.
	WidgetOnly bool
	Signed     bool
	Signer     string
	// FirstParty is set when the package is signed by one of the project's own keys
	// (whether that key is still trusted is Blocked's business).
	FirstParty bool
	// WebhooksImported is set (webhooks row only) when the boot step has recorded that
	// the plugin's subscriptions were copied into core.
	WebhooksImported bool
	Source           string
	// Update is the newer version the registry offers ("" when none).
	Update string
	// UpdateNew lists the permissions that update asks for and the installed
	// version does not; empty when Update is, or when it asks for nothing new.
	UpdateNew []string
	// Blocked says why policy keeps the package from loading (a signer that is
	// no longer trusted, a dev package in production); "" when it is not.
	Blocked string
	// ScriptsBlocked is set when the plugin ships browser JavaScript (an
	// assets/plugin.js, or script tags for the page head) that is not loaded
	// because the owner has not granted site.scripts.
	ScriptsBlocked bool
	// MarkupStopped lists the sections and partials that are not registered
	// because they carry scripts and the plugin lacks site.scripts.
	MarkupStopped []string
	// AsksScripts: its manifest lists site.scripts, so the owner can grant it.
	AsksScripts bool
	// Uninstalling is set while an uninstall is unfinished (retry it).
	Uninstalling bool
}

// ConfirmInfo is what replacing an installed package puts at stake: the owner
// must confirm it. SignerChange and WidenedAccess also clear the plugin's grants.
type ConfirmInfo struct {
	Name                    string
	Downgrade, SignerChange bool
	// WidenedAccess is set when an update can call web addresses the installed
	// version could not; it also clears the plugin's grants.
	WidenedAccess          bool
	NewHosts               []string
	NewFetchAny            bool
	NewIsland              bool
	NewScripts             bool
	OldVersion, NewVersion string
	OldSigner, NewSigner   string
	// Missing are section types the active theme would lose.
	Missing []MissingSection
}

// PluginUIInfo is a plugin's declared admin page.
type PluginUIInfo struct {
	Title string
	// Entry is the HTML file under the package's ui/ directory.
	Entry string
	Roles []string
	// Functions are the host functions its page may call: what its granted
	// permissions allow.
	Functions []string
}

// PackageSummary is a package as shown on the permission prompt.
type PackageSummary struct {
	Kind, Name, Version, Description string
	Permissions, AllowedHosts        []string
	Signed                           bool
	Signer                           string
	// Problem says why installing would be refused (untrusted signer, bad
	// signature, unsigned while unsigned packages are off); "" when it would go ahead.
	Problem string
	// Updating is set when a package of that name is installed already.
	Updating         bool
	InstalledVersion string
	Granted          []string
	// Confirm is set when replacing the installed package needs the owner's
	// confirmation (older version, other signer, lost sections).
	Confirm *ConfirmInfo
}

// ThemeInfo is one theme on the Themes page.
type ThemeInfo struct {
	Name, Version, Description string
	Builtin, Active            bool
	Signed                     bool
	Signer, Source             string
	HasSettings                bool
	Update                     string
	// Blocked says why policy keeps an installed theme from running ("" when it is not).
	Blocked string
}

// MissingSection is a section type the content uses that a theme lacks.
type MissingSection struct {
	Type string
	Uses int
}

// RouteResult is a plugin route's response, relayed by the bridge.
type RouteResult struct {
	Status      int
	ContentType string
	Body        []byte
}

// PackageAdmin is the package manager as the Plugins and Themes pages use it.
type PackageAdmin interface {
	Registry(ctx context.Context) RegistryView
	// RefreshRegistry is Registry without the short cache, for "Try again"
	// and "Check for updates".
	RefreshRegistry(ctx context.Context) RegistryView
	// Fetch downloads kind/name from the registry (hash-checked).
	Fetch(ctx context.Context, kind, name string) ([]byte, error)
	// Inspect parses a package for the permission prompt.
	Inspect(ctx context.Context, data []byte) (PackageSummary, error)
	// Install stores and applies the package; grant is the approved permission
	// list (nil keeps the earlier grant); confirm is the owner's yes to a
	// Summary.Confirm. Reasons the owner should read come back as *Refusal.
	Install(ctx context.Context, data []byte, source string, grant []string, confirm bool) error
	SetEnabled(ctx context.Context, name string, on bool) error
	Uninstall(ctx context.Context, kind, name string, dropData bool) error
	AllowUnsigned(ctx context.Context) bool
	// UnsignedState is AllowUnsigned plus the end of a timed opt-in.
	UnsignedState(ctx context.Context) UnsignedState
	// SetAllowUnsigned turns the opt-in on until the given time (nil: until
	// turned off) or off; duration labels it in the audit log.
	SetAllowUnsigned(ctx context.Context, on bool, until *time.Time, duration string) error
	// Signers lists the trusted keys.
	Signers(ctx context.Context) []SignerInfo

	Themes(ctx context.Context) ([]ThemeInfo, error)
	MissingSections(ctx context.Context, theme string) ([]MissingSection, error)
	// Activate switches the site to the theme ("default" is the built-in one).
	Activate(ctx context.Context, theme string) error
	ThemeSettingsSchema(ctx context.Context, theme string) []byte
	ThemeSettings(ctx context.Context, theme string) (map[string]any, error)
	SaveThemeSettings(ctx context.Context, theme string, values map[string]any) (schemaform.Errors, error)
	// PreviewURL is the public-site URL that starts a preview of the theme.
	PreviewURL(theme string) string

	// UIFile serves a file of the plugin package's ui/ directory; it reports
	// false when the plugin is not loaded or has no such file.
	UIFile(w http.ResponseWriter, r *http.Request, plugin, file, csp, cache string) bool
	// CallRoute runs one of the plugin's own routes as the signed-in user.
	CallRoute(ctx context.Context, plugin, method, path string, query url.Values, body []byte, user CurrentUser) RouteResult
	// CallHost runs one host function of the plugin (its permissions apply);
	// the result is the sdk.CallResponse JSON.
	CallHost(ctx context.Context, plugin, fn string, args json.RawMessage) json.RawMessage
}

// staged holds a downloaded or uploaded package between the permission prompt
// and the confirmation, in memory for ten minutes. Only the user who staged it
// can take it.
type staged struct {
	mu sync.Mutex
	m  map[string]stagedPkg
}

type stagedPkg struct {
	user, source string
	data         []byte
	expires      time.Time
}

const (
	stagedTTL = 10 * time.Minute
	stagedMax = 8
)

func (s *staged) put(user, source string, data []byte) (string, bool) {
	id := newNonce()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]stagedPkg{}
	}
	now := time.Now()
	for k, e := range s.m {
		if now.After(e.expires) {
			delete(s.m, k)
		}
	}
	if len(s.m) >= stagedMax {
		return "", false
	}
	s.m[id] = stagedPkg{user: user, source: source, data: data, expires: now.Add(stagedTTL)}
	return id, true
}

func (s *staged) take(user, id string) (stagedPkg, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[id]
	if !ok || e.user != user || time.Now().After(e.expires) {
		return stagedPkg{}, false
	}
	delete(s.m, id)
	return e, true
}
