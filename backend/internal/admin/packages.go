package admin

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/permtext"
	"hatamoto.dev/core/internal/themestrings"
)

// The types and interface below are what the Plugins and Themes pages need of
// the package manager (internal/packages). The server adapts the manager to
// them (server/admin_packages.go), so handler tests run on fakes.

// ErrThemeActive is what Uninstall answers for the active theme.
var ErrThemeActive = errors.New("theme is active")

// Refusal is a package that was refused for a reason the owner should read
// (bad signature, unsigned, unknown permission, a theme that does not render).
type Refusal struct{ Msg string }

// ContentTypesLine is the prompt sentence for ContentTypes ("" when none).
func (s PackageSummary) ContentTypesLine() string { return permtext.ContentTypesLine(s.ContentTypes) }

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
	// Screenshot is an absolute https URL or "".
	Screenshot string
	// SiteType and Flags describe a theme; both may be empty.
	SiteType string
	Flags    []string
	// Active marks the live theme. Registry never sets it: the theme Browse
	// handler sets it from ThemeInfo.
	Active bool
	// Tags are the tags of a tag pack, as the index lists them.
	Tags []TagInfo
	// Developer is set for an entry of the developer index: published by a
	// registered developer, signed by SignerKeyID and not reviewed by Hatamoto.
	Developer bool
	// SignerKeyID is the id (16 hex characters) of the key the index says signed
	// the entry. It is the entry's claim; installing checks the package itself.
	SignerKeyID string
}

// TagInfo is one tag of a tag pack as the owner is shown it: what it is called,
// the id it needs, its consent category and the hosts its script comes from.
type TagInfo struct {
	Name, IDLabel, Category string
	Hosts                   []string
	Websockets              bool
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
	// DeveloperAvail is set when the developer index was read and its entries
	// are in Entries. It is false when developer packages are not offered here
	// (no index configured, the public demo) or the index could not be read.
	DeveloperAvail bool
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
	// TagPack is set for a package that only declares tracking tags: it has no
	// code and no runtime status, and that is healthy.
	TagPack bool
	Signed  bool
	Signer  string
	// FirstParty is set when the package is signed by one of the project's own keys
	// (whether that key is still trusted is Blocked's business).
	FirstParty bool
	// LegacyFirstParty (webhooks row only) is set when the row is signed by a
	// first-party key. The plugin is no longer on the first-party list, so
	// FirstParty cannot say whether it is the real one.
	LegacyFirstParty bool
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
	// Revoked is the reason the registry revoked the package ("" when it was
	// not). A revoked package is disabled and cannot be turned back on.
	Revoked string
	// LoadError is why the package was refused at load (a permission the host
	// removed, an unreadable archive); "" when it was not.
	LoadError string
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
	NewCSPHosts            []string
	NewRequestHeaders      []string
	NewTagHosts            []string
	TagCategoryChanged     bool
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
	// RequestHeaders and CSP are what the plugin asks to receive and to add to
	// the public pages' security policy.
	RequestHeaders []string
	// ContentTypes are the names of the entity types the plugin installs.
	ContentTypes []string
	CSP          []permtext.CSPLine
	// Tags are the tags of a tag pack; the prompt shows them instead of
	// permissions.
	Tags []TagInfo
	// Refused is the part of Permissions that policy will not grant this
	// package; the install prompt says so and does not post them.
	Refused []string
	// DeclaresRuns: the plugin declares runs that stage changes for review;
	// Proposes: one of them may suggest site-wide changes.
	DeclaresRuns, Proposes bool
	Signed                 bool
	Signer                 string
	// FirstParty is set when the signer is one of the project's own keys.
	FirstParty bool
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
	// Revoked is the reason the registry revoked the theme ("" when it was not).
	Revoked string
}

// defaultRevocation is the reason stored when the registry gives none.
const defaultRevocation = "revoked by the registry"

// revokedText is the line a revoked package carries; "" when reason is "".
func revokedText(reason string) string {
	switch reason {
	case "":
		return ""
	case defaultRevocation:
		return "Disabled: " + defaultRevocation + "."
	}
	return "Disabled: " + defaultRevocation + " (" + reason + ")"
}

// RevokedText is the owner-facing line for a revoked plugin; "" when not revoked.
func (m PackageMeta) RevokedText() string { return revokedText(m.Revoked) }

// RevokedText is the owner-facing line for a revoked theme; "" when not revoked.
func (t ThemeInfo) RevokedText() string { return revokedText(t.Revoked) }

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

// ContentTypeUse is a content type a plugin installed and its entry count.
type ContentTypeUse struct {
	Slug, Name string
	Entries    int
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
	Uninstall(ctx context.Context, kind, name string, dropData, dropContentTypes bool) error
	// ContentTypes lists the content types the plugin installed, with their
	// entry counts (what "remove content types" would delete).
	ContentTypes(ctx context.Context, name string) []ContentTypeUse
	// IsTagPack reports whether the stored package holds tags, in any state
	// (off, blocked, no longer valid).
	IsTagPack(ctx context.Context, name string) bool
	AllowUnsigned(ctx context.Context) bool
	// UnsignedState is AllowUnsigned plus the end of a timed opt-in.
	UnsignedState(ctx context.Context) UnsignedState
	// SetAllowUnsigned turns the opt-in on until the given time (nil: until
	// turned off) or off; duration labels it in the audit log.
	SetAllowUnsigned(ctx context.Context, on bool, until *time.Time, duration string) error
	// AllowDeveloper reports whether the owner allows developer packages.
	AllowDeveloper(ctx context.Context) bool
	// SetAllowDeveloper turns developer packages on or off. Off never stops an
	// installed package.
	SetAllowDeveloper(ctx context.Context, on bool) error
	// Signers lists the trusted keys.
	Signers(ctx context.Context) []SignerInfo

	Themes(ctx context.Context) ([]ThemeInfo, error)
	MissingSections(ctx context.Context, theme string) ([]MissingSection, error)
	// Activate switches the site to the theme ("default" is the built-in one).
	Activate(ctx context.Context, theme string) error
	ThemeSettingsSchema(ctx context.Context, theme string) []byte
	// ThemeStrings is the UI strings the theme declares (ok false: not installed).
	ThemeStrings(ctx context.Context, theme string) (themestrings.Decl, bool)
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
	CallHost(ctx context.Context, plugin, fn string, args jsontext.Value) jsontext.Value
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
