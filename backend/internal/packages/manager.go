package packages

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

// DefaultTheme names the built-in theme that ships inside the binary. It is
// always available and is what runs when no theme package is active.
const DefaultTheme = "default"

// Errors the admin and the CLI report to the owner.
var (
	ErrUnsignedNotAllowed = errors.New("this package is not signed; installing unsigned packages is switched off (an owner can allow it in Admin > Plugins)")
	ErrDevDisabled        = errors.New("development packages are disabled in production")
	ErrThemeActive        = errors.New("this theme is active; activate another theme first")
	ErrReservedName       = errors.New("that name is reserved (the built-in theme, the webhooks that moved into core, or a name the admin uses)")
	ErrPermission         = errors.New("permission not requested by the plugin")
	ErrUnknownSource      = errors.New("unknown package source")
	ErrUninstalling       = errors.New("this package is being uninstalled; finish the uninstall first")
	ErrSignerNotAllowed   = errors.New("only first-party packages can be installed here (public demo)")
)

// ReservedPluginName is the name of the former first-party webhooks plugin.
// Webhooks are core now and the boot step retires a plugin by this name, so no
// new plugin may take it.
const ReservedPluginName = "webhooks"

// reservedPluginPaths are plugin names the admin uses as /plugins/<name>
// pages, so a new plugin of that name would be shadowed. One already installed
// still updates and uninstalls: those routes are POST /plugins/<name>/<action>.
var reservedPluginPaths = []string{"browse", "security"}

// ConfirmationRequired is returned by Install when replacing the installed
// package needs the owner's explicit say-so: an older (or same) version, a
// different signer, or an update of the active theme that loses section types
// the content uses. Install again with InstallRequest.Confirm to go ahead.
type ConfirmationRequired struct {
	Kind                   Kind
	Name                   string
	OldVersion, NewVersion string
	// OldSigner and NewSigner are key ids ("" for unsigned).
	OldSigner, NewSigner string
	Downgrade            bool // the new version is not newer than the installed one
	SignerChange         bool // another signer than the installed package's
	// WidenedAccess is set when a plugin update reaches further than the
	// installed one: it lists web addresses the installed version did not
	// (NewHosts) or newly asks for http.fetch.any (NewFetchAny).
	WidenedAccess bool
	NewHosts      []string
	NewFetchAny   bool
	// NewIsland: the update newly asks for builder.island (JavaScript on the
	// site's pages); it counts as widened access too.
	NewIsland bool
	// NewScripts: the update newly asks for site.scripts.
	NewScripts bool
	Missing    []MissingSection
}

func (e *ConfirmationRequired) Error() string {
	var why []string
	if e.Downgrade {
		why = append(why, fmt.Sprintf("v%s is not newer than the installed v%s", e.NewVersion, e.OldVersion))
	}
	if e.SignerChange {
		why = append(why, fmt.Sprintf("it is signed by %s, not %s", orUnsigned(e.NewSigner), orUnsigned(e.OldSigner)))
	}
	if e.WidenedAccess {
		why = append(why, "it can reach further than the installed version (new web addresses, http.fetch.any or JavaScript on your pages, site.scripts)")
	}
	if len(e.Missing) > 0 {
		why = append(why, fmt.Sprintf("the active theme would lose %d section type(s) the content uses", len(e.Missing)))
	}
	return fmt.Sprintf("replacing %s %q needs confirmation: %s", e.Kind, e.Name, strings.Join(why, "; "))
}

func orUnsigned(key string) string {
	if key == "" {
		return "no signature"
	}
	return "key " + key
}

// PluginRuntime is the part of *pluginhost.Runtime the manager drives.
type PluginRuntime interface {
	LoadWith(ctx context.Context, m sdk.Manifest, wasm []byte, a pluginhost.Assets) pluginhost.Status
	Unload(ctx context.Context, name string)
	Statuses() []pluginhost.Status
}

// ThemeEngine is the part of *render.Engine the manager drives.
type ThemeEngine interface {
	SetTheme(layers ...fs.FS) error
	SetThemeName(name string)
	CheckTheme(layers ...fs.FS) error
	SetSettings(map[string]any)
	SectionTypes(layers ...fs.FS) ([]string, error)
}

// DataDropper removes a plugin's own schema, role and stored data.
type DataDropper interface {
	Drop(ctx context.Context, plugin string) error
}

// Options wires a Manager. Plugins, Engine and Dropper are nil in the CLI,
// which only writes the database; a running server applies it within a few
// seconds (see Run).
type Options struct {
	Pool     *pgxpool.Pool
	Trust    TrustSet
	Registry *Registry
	Plugins  PluginRuntime
	Grants   pluginhost.Grants
	Dropper  DataDropper
	Engine   ThemeEngine
	// NewPreview builds a second engine for previewing an installed theme.
	NewPreview func(layers []fs.FS, settings map[string]any) (*render.Engine, error)
	// AllowDev lets packages from `hatamoto theme dev` be installed and loaded.
	// It is the server's decision (never in production); dev rows are ignored
	// at load time when it is off, whatever was written to the database.
	AllowDev bool
	// OnlySigners, when not empty, limits installs to packages signed by one of
	// these key ids (the public demo allows first-party packages only). It
	// applies to every source and refuses unsigned packages whatever the
	// owner's opt-in says.
	OnlySigners []string
	// OnChange runs after what the site shows changed (theme, settings,
	// plugins): the server clears its page cache.
	OnChange func(ctx context.Context)
	Logger   *slog.Logger
}

// Manager installs, activates and removes packages while the server runs. The
// database is the source of truth; Reconcile makes the process match it.
type Manager struct {
	Options
	store *Store

	mu      sync.Mutex // serialises changes and reconciliation
	loaded  map[string]loadedPlugin
	theme   appliedTheme
	preview map[string]previewEntry
	// blocked maps kind/name to why an installed package is not loaded (dev
	// package in production, signer no longer trusted); rebuilt on reconcile.
	blocked map[string]string
	// themeNote is why the active theme is not the one selected ("" when fine).
	themeNote string
	// origins maps plugin name to whose code it is; rebuilt on reconcile.
	origins atomic.Pointer[map[string]Origin]
	// widgets is what reconcile registered from plugin packages' widgets/.
	widgets widgetState
	// widgetCounts is plugin name -> widgets the last reconcile found, for
	// readers that do not hold mu (the admin plugin list).
	widgetCounts atomic.Pointer[map[string]int]
}

type loadedPlugin struct {
	sha    string
	pkg    *Package
	pinned bool // from HATAMOTO_PLUGIN_DIR: reconcile leaves it alone
}

type appliedTheme struct {
	name, sha string
	stamp     string // when the theme's settings last changed
	err       error
	failedSHA string // a (name, sha) that failed to apply: not retried until it changes
}

type previewEntry struct {
	sha string
	eng *render.Engine
}

// NewManager returns a Manager. Call Reconcile once at boot, then Run.
func NewManager(o Options) *Manager {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Trust == nil {
		o.Trust = TrustSet{}
	}
	if o.Registry != nil && o.Registry.Limits == (Limits{}) {
		o.Registry.Limits = DefaultLimits
	}
	return &Manager{Options: o, store: NewStore(o.Pool), loaded: map[string]loadedPlugin{}, preview: map[string]previewEntry{}, blocked: map[string]string{}}
}

// Store gives access to the underlying rows.
func (m *Manager) Store() *Store { return m.store }

// --- install -----------------------------------------------------------------

// InstallRequest says how to install a package.
type InstallRequest struct {
	Source Source
	// Grant lists the permissions the owner approved at the permission prompt
	// (plugins). nil keeps the earlier grant on an update and grants nothing on
	// a new install.
	Grant []string
	// Confirm is the owner's answer to a ConfirmationRequired: replace the
	// installed package anyway.
	Confirm bool
}

// Installed is the outcome of an install.
type Installed struct {
	Kind    Kind
	Name    string
	Version string
	Updated bool               // an older version was replaced
	Status  *pluginhost.Status // plugins, when a runtime is attached
}

// Install parses, verifies and stores a package and, with a runtime or engine
// attached, applies it at once. Signed packages must be signed by a trusted
// key; unsigned ones are installed only when the owner allowed it (or for a
// dev push outside production).
func (m *Manager) Install(ctx context.Context, raw []byte, req InstallRequest) (*Installed, error) {
	switch req.Source {
	case SourceRegistry, SourceUpload, SourceURL:
	case SourceDev:
		if !m.AllowDev {
			return nil, ErrDevDisabled
		}
	default:
		return nil, fmt.Errorf("%w %q", ErrUnknownSource, req.Source)
	}
	p, err := Parse(raw, DefaultLimits)
	if err != nil {
		return nil, err
	}
	if err := m.CheckPolicy(ctx, p, req.Source); err != nil {
		return nil, err
	}
	var perms []sdk.Permission
	switch p.Kind {
	case KindTheme:
		if p.Name == DefaultTheme {
			return nil, ErrReservedName
		}
		if m.Engine != nil {
			if err := m.Engine.CheckTheme(p.FS()); err != nil {
				return nil, &ParseError{Msg: "the theme does not render: " + err.Error()}
			}
		}
	case KindPlugin:
		if p.Name == ReservedPluginName {
			return nil, ErrReservedName
		}
		man, _ := p.PluginManifest()
		if err := pluginhost.SDKRefusal(man); err != nil {
			return nil, &ParseError{Msg: err.Error()}
		}
		if err := validateWidgets(p); err != nil {
			return nil, err
		}
		for _, g := range req.Grant {
			if !slices.Contains(man.Permissions, g) {
				return nil, fmt.Errorf("%w: %q", ErrPermission, g)
			}
			perms = append(perms, g)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	prev, err := m.store.Get(ctx, p.Kind, p.Name)
	updating := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if updating && prev.State == StateUninstalling {
		return nil, ErrUninstalling
	}
	// A plugin installed before these names were reserved keeps updating; only
	// a new install is refused.
	if !updating && p.Kind == KindPlugin && slices.Contains(reservedPluginPaths, p.Name) {
		return nil, ErrReservedName
	}
	var need *ConfirmationRequired
	if updating {
		if need, err = m.needsConfirmation(ctx, p, prev, req.Source); err != nil {
			return nil, err
		}
		if need != nil && !req.Confirm {
			return nil, need
		}
	}
	// Another signer's package must not inherit what the owner granted to the
	// previous one: grants reset until they are given again.
	// An update that widens what the plugin may call is reviewed the same way.
	signerChanged := need != nil && (need.SignerChange || need.WidenedAccess)
	var grants []string // nil: keep what is stored
	if p.Kind == KindPlugin {
		switch {
		case req.Grant != nil:
			grants = normalizePerms(perms)
			if err := checkScriptsGrant(Classify(p.Name, signerOf(p)), grants); err != nil {
				return nil, err
			}
		case signerChanged:
			grants = []string{}
		}
	}
	// The package row and its grants are one transaction: a failed grant write
	// must not leave the new package enabled.
	if err := pgx.BeginFunc(ctx, m.Pool, func(tx pgx.Tx) error {
		if err := m.store.put(ctx, tx, p, req.Source, true); err != nil {
			return err
		}
		if grants != nil {
			if _, err := tx.Exec(ctx, `
				INSERT INTO hatamoto_plugin_grants (plugin, permissions, updated_at) VALUES ($1, $2, now())
				ON CONFLICT (plugin) DO UPDATE SET permissions = EXCLUDED.permissions, updated_at = now()`, p.Name, grants); err != nil {
				return fmt.Errorf("packages: save grants for %q: %w", p.Name, err)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if grants != nil && m.Grants != nil {
		if err := m.Grants.Set(ctx, p.Name, grants); err != nil { // refreshes the in-memory copy
			m.Logger.Error("packages: grants saved but not applied; they apply at the next restart", "plugin", p.Name, "error", err)
		}
	}
	m.reconcile(ctx)
	out := &Installed{Kind: p.Kind, Name: p.Name, Version: p.Version, Updated: updating}
	if p.Kind == KindPlugin && m.Plugins != nil {
		for _, st := range m.Plugins.Statuses() {
			if st.Name == p.Name {
				out.Status = &st
			}
		}
	}
	sum := p.SHA256()
	detail := map[string]any{"version": p.Version, "sha256": hex.EncodeToString(sum[:]), "signer": signerOf(p), "source": string(req.Source)}
	action := "package.install"
	if updating {
		action = "package.update"
		detail["from_version"] = prev.Version
		detail["from_signer"] = prev.SignerKeyID
	}
	if need != nil {
		detail["confirmed_downgrade"], detail["confirmed_signer_change"], detail["confirmed_missing_sections"] = need.Downgrade, need.SignerChange, len(need.Missing)
	}
	if grants != nil {
		detail["grants"] = grants
	}
	if p.Kind == KindPlugin {
		man, _ := p.PluginManifest()
		detail["allowed_hosts"] = man.AllowedHosts
		detail["http_fetch_any"] = slices.Contains(man.Permissions, sdk.PermHTTPFetchAny)
		if ws, err := p.Widgets(); err == nil {
			detail["widgets"] = len(ws.Items)
		}
		if need != nil && need.WidenedAccess {
			detail["confirmed_widened_access"] = true
		}
	}
	m.Audit(ctx, action, string(p.Kind)+":"+p.Name, detail)
	return out, nil
}

func signerOf(p *Package) string {
	if p.Signature == nil {
		return ""
	}
	return p.Signature.Signer
}

func normalizePerms(perms []string) []string {
	out := slices.Clone(perms)
	if out == nil {
		out = []string{}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// NeedsConfirmation reports, without installing, whether replacing the
// installed package of the same name with p needs the owner's confirmation
// (nil when p is new or the replacement is a plain upgrade by the same signer).
func (m *Manager) NeedsConfirmation(ctx context.Context, p *Package, src Source) (*ConfirmationRequired, error) {
	prev, err := m.store.Get(ctx, p.Kind, p.Name)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return m.needsConfirmation(ctx, p, prev, src)
}

func (m *Manager) needsConfirmation(ctx context.Context, p *Package, prev Row, src Source) (*ConfirmationRequired, error) {
	c := &ConfirmationRequired{Kind: p.Kind, Name: p.Name, OldVersion: prev.Version, NewVersion: p.Version, OldSigner: prev.SignerKeyID, NewSigner: signerOf(p)}
	// `theme dev` pushes the same version again and again.
	devLoop := src == SourceDev && prev.Source == SourceDev
	if !devLoop {
		c.Downgrade = !Newer(p.Version, prev.Version)
		c.SignerChange = prev.SignerKeyID != "" && prev.SignerKeyID != c.NewSigner
	}
	if p.Kind == KindTheme && m.Engine != nil {
		if cfg, err := m.store.Config(ctx); err == nil && cfg.ActiveTheme == p.Name {
			missing, err := m.missingFor(ctx, []fs.FS{p.FS()})
			if err != nil {
				return nil, err
			}
			c.Missing = missing
		}
	}
	if p.Kind == KindPlugin && !devLoop {
		c.NewHosts, c.NewFetchAny, c.NewIsland, c.NewScripts = widenedAccess(prev.Manifest, p)
		c.WidenedAccess = len(c.NewHosts) > 0 || c.NewFetchAny || c.NewIsland || c.NewScripts
	}
	if !c.Downgrade && !c.SignerChange && !c.WidenedAccess && len(c.Missing) == 0 {
		return nil, nil
	}
	return c, nil
}

// widenedAccess compares the installed plugin manifest with the update's: the
// allowed_hosts entries the update adds and whether it newly asks for
// http.fetch.any, builder.island or site.scripts. An unreadable installed manifest counts as having nothing.
func widenedAccess(installed json.RawMessage, p *Package) (hosts []string, fetchAny, island, scripts bool) {
	var old sdk.Manifest
	_ = json.Unmarshal(installed, &old)
	next, err := p.PluginManifest()
	if err != nil {
		return nil, false, false, false
	}
	for _, h := range next.AllowedHosts {
		if !slices.Contains(old.AllowedHosts, h) {
			hosts = append(hosts, h)
		}
	}
	newly := func(perm sdk.Permission) bool {
		return slices.Contains(next.Permissions, perm) && !slices.Contains(old.Permissions, perm)
	}
	return hosts, newly(sdk.PermHTTPFetchAny), newly(sdk.PermBuilderIsland), newly(sdk.PermSiteScripts)
}

// CheckPolicy says whether p may be installed from src: signed by a trusted
// key, or unsigned and allowed (the owner's opt-in, or a dev push outside
// production). The error is one of the Err* values.
func (m *Manager) CheckPolicy(ctx context.Context, p *Package, src Source) error {
	if len(m.OnlySigners) > 0 && !slices.Contains(m.OnlySigners, signerOf(p)) {
		return ErrSignerNotAllowed
	}
	err := m.Trust.Verify(p)
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, ErrUnsigned):
		return err
	case src == SourceDev:
		if !m.AllowDev {
			return ErrDevDisabled
		}
		return nil
	}
	cfg, err := m.store.Config(ctx)
	if err != nil {
		return err
	}
	if !cfg.AllowUnsigned {
		return ErrUnsignedNotAllowed
	}
	return nil
}

// FetchFromRegistry downloads kind/name from the registry, checked against the
// hash the signed index lists and against the name it was listed under (a
// registry that serves another package under a listed name is lying).
func (m *Manager) FetchFromRegistry(ctx context.Context, kind Kind, name string) ([]byte, error) {
	idx, err := m.Registry.Fetch(ctx)
	if err != nil {
		return nil, err
	}
	e, ok := idx.Find(kind, name)
	if !ok {
		return nil, fmt.Errorf("the registry has no %s %q", kind, name)
	}
	// With the install limited to some signers (the public demo), only the
	// registry's first-party tier can qualify: say no before downloading
	// anything else, so visitors cannot make the server fetch community archives.
	if len(m.OnlySigners) > 0 && e.Tier != TierFirstParty {
		return nil, ErrSignerNotAllowed
	}
	data, err := m.Registry.Download(ctx, e)
	if err != nil {
		return nil, err
	}
	p, err := Parse(data, DefaultLimits)
	if err != nil {
		return nil, err
	}
	if p.Kind != kind || p.Name != name {
		return nil, fmt.Errorf("the registry served %s %q for %s %q", p.Kind, p.Name, kind, name)
	}
	return data, nil
}

// InstallFromRegistry downloads kind/name from the registry and installs it.
func (m *Manager) InstallFromRegistry(ctx context.Context, kind Kind, name string, req InstallRequest) (*Installed, error) {
	data, err := m.FetchFromRegistry(ctx, kind, name)
	if err != nil {
		return nil, err
	}
	req.Source = SourceRegistry
	return m.Install(ctx, data, req)
}

// --- remove, enable, activate ------------------------------------------------

// Uninstall removes a package. A theme is deleted at once. A plugin goes in a
// recoverable order: its row is marked uninstalling (so it is unloaded and
// cannot be installed over), its permissions are revoked, with dropData its
// schema, role, stored values and settings are dropped, and only then is the row
// deleted. A failure leaves the row uninstalling; call Uninstall again. Core
// content is never touched.
func (m *Manager) Uninstall(ctx context.Context, kind Kind, name string, dropData bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, err := m.store.Get(ctx, kind, name)
	if err != nil {
		return err
	}
	if kind == KindTheme {
		if cfg, err := m.store.Config(ctx); err == nil && cfg.ActiveTheme == name {
			return ErrThemeActive
		}
		if err := m.store.Delete(ctx, kind, name); err != nil {
			return err
		}
		delete(m.preview, name)
		m.reconcile(ctx)
		m.Audit(ctx, "package.uninstall", "theme:"+name, map[string]any{"version": row.Version, "signer": row.SignerKeyID})
		return nil
	}
	nwidgets := m.widgetCount(name)
	if err := m.store.SetState(ctx, kind, name, StateUninstalling); err != nil {
		return err
	}
	m.reconcile(ctx) // unloads it
	if m.Grants != nil {
		if err := m.Grants.Set(ctx, name, []sdk.Permission{}); err != nil {
			return err
		}
	}
	if _, err := m.Pool.Exec(ctx, `DELETE FROM hatamoto_plugin_grants WHERE plugin = $1`, name); err != nil {
		return err
	}
	if dropData {
		if m.Dropper != nil {
			if err := m.Dropper.Drop(ctx, name); err != nil {
				return fmt.Errorf("dropping the data of %q (uninstall is unfinished; try again): %w", name, err)
			}
		}
		if _, err := m.Pool.Exec(ctx, `DELETE FROM hatamoto_plugin_settings WHERE plugin = $1`, name); err != nil {
			return err
		}
	}
	if err := m.store.Delete(ctx, kind, name); err != nil {
		return err
	}
	m.Audit(ctx, "package.uninstall", "plugin:"+name, map[string]any{"version": row.Version, "signer": row.SignerKeyID, "data_dropped": dropData, "widgets": nwidgets})
	return nil
}

// SetEnabled turns an installed plugin on or off without uninstalling it.
func (m *Manager) SetEnabled(ctx context.Context, name string, on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	nwidgets := m.widgetCount(name)
	if err := m.store.SetEnabled(ctx, KindPlugin, name, on); err != nil {
		return err
	}
	m.reconcile(ctx)
	if on {
		nwidgets = m.widgetCount(name)
	}
	m.Audit(ctx, map[bool]string{true: "package.enable", false: "package.disable"}[on], "plugin:"+name, map[string]any{"widgets": nwidgets})
	return nil
}

// MissingSection is a section type content uses that a theme does not render.
type MissingSection struct {
	Type string
	// Uses is how many sections of that type the content holds.
	Uses int
}

// MissingSectionsError is returned by Activate when content uses section
// types the theme lacks and the activation was not forced.
type MissingSectionsError struct{ Missing []MissingSection }

func (e *MissingSectionsError) Error() string {
	return fmt.Sprintf("the theme does not render %d section type(s) the content uses", len(e.Missing))
}

// MissingSections lists the section types used by content that theme (an
// installed theme, or "" / DefaultTheme) cannot render.
func (m *Manager) MissingSections(ctx context.Context, theme string) ([]MissingSection, error) {
	layers, _, err := m.themeLayers(ctx, theme)
	if err != nil {
		return nil, err
	}
	return m.missingFor(ctx, layers)
}

// missingFor lists the section types used by content that the theme layers lack.
func (m *Manager) missingFor(ctx context.Context, layers []fs.FS) ([]MissingSection, error) {
	if m.Engine == nil {
		return nil, errors.New("packages: no render engine attached")
	}
	have, err := m.Engine.SectionTypes(layers...)
	if err != nil {
		return nil, err
	}
	uses, err := m.sectionUses(ctx)
	if err != nil {
		return nil, err
	}
	var out []MissingSection
	for _, ms := range uses {
		if !slices.Contains(have, ms.Type) {
			out = append(out, ms)
		}
	}
	return out, nil
}

// sectionUses counts the section types content uses, sorted by type: from the
// component nodes of page_doc for pages that have one, from page_sections for
// pages that do not (the frozen mirror of a doc page is not read).
func (m *Manager) sectionUses(ctx context.Context) ([]MissingSection, error) {
	counts := map[string]int{}
	rows, err := m.Pool.Query(ctx, `
		SELECT s->>'type', count(*)
		FROM hatamoto_entities e
		CROSS JOIN LATERAL jsonb_array_elements(
		    CASE WHEN jsonb_typeof(e.attributes->'page_sections') = 'array' THEN e.attributes->'page_sections' ELSE '[]'::jsonb END) s
		WHERE NOT (e.attributes ? 'page_doc')
		  AND jsonb_typeof(s) = 'object' AND COALESCE(s->>'type', '') <> ''
		GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			rows.Close()
			return nil, err
		}
		counts[t] += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	docs, err := m.Pool.Query(ctx, `
		SELECT attributes->'page_doc' FROM hatamoto_entities
		WHERE jsonb_typeof(attributes->'page_doc') = 'object'`)
	if err != nil {
		return nil, err
	}
	defer docs.Close()
	for docs.Next() {
		var raw []byte
		if err := docs.Scan(&raw); err != nil {
			return nil, err
		}
		doc, err := builder.Parse(raw)
		if err != nil {
			continue
		}
		for _, c := range builder.Components(doc) {
			if t, _ := c.P["type"].(string); t != "" {
				counts[t]++
			}
		}
	}
	if err := docs.Err(); err != nil {
		return nil, err
	}
	out := make([]MissingSection, 0, len(counts))
	for t, n := range counts {
		out = append(out, MissingSection{Type: t, Uses: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out, nil
}

// Activate makes an installed theme (or the built-in default, for "" or
// DefaultTheme) the site's theme. It first reports section types the theme
// lacks (MissingSectionsError) unless force is set, and refuses a theme that
// does not render.
func (m *Manager) Activate(ctx context.Context, name string, force bool) error {
	if name == DefaultTheme {
		name = ""
	}
	layers, _, err := m.themeLayers(ctx, name)
	if err != nil {
		return err
	}
	if m.Engine != nil {
		if !force {
			missing, err := m.MissingSections(ctx, name)
			if err != nil {
				return err
			}
			if len(missing) > 0 {
				return &MissingSectionsError{Missing: missing}
			}
		}
		if err := m.Engine.CheckTheme(layers...); err != nil {
			return &ParseError{Msg: "the theme does not render: " + err.Error()}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.store.SetActiveTheme(ctx, name); err != nil {
		return err
	}
	m.reconcile(ctx)
	m.Audit(ctx, "package.activate", "theme:"+orDefault(name), map[string]any{"forced": force})
	return m.theme.err
}

// themeLayers is the file systems of an installed theme ("" or DefaultTheme: none).
func (m *Manager) themeLayers(ctx context.Context, name string) ([]fs.FS, Row, error) {
	if name == "" || name == DefaultTheme {
		return nil, Row{}, nil
	}
	p, row, err := m.loadUsable(ctx, KindTheme, name)
	if err != nil {
		return nil, row, err
	}
	return []fs.FS{p.FS()}, row, nil
}

// usable says why an installed package must not run (nil when it may): a dev
// package when this process does not allow them (production), or one signed by
// a key that is no longer trusted. A package marked unsigned stays usable: the
// owner allowed that when installing it.
func (m *Manager) usable(r Row) error {
	if r.Source == SourceDev && !m.AllowDev {
		return ErrDevDisabled
	}
	if r.SignerKeyID != "" {
		if _, ok := m.Trust[r.SignerKeyID]; !ok {
			return fmt.Errorf("%w: it is signed by key %s, which is no longer in HATAMOTO_TRUSTED_KEYS", ErrUntrustedKey, r.SignerKeyID)
		}
	}
	return nil
}

// Usable is usable's exported form: nil when the stored row
// would be loaded by this instance's trust policy, the reason otherwise.
func (m *Manager) Usable(r Row) error { return m.usable(r) }

// loadUsable loads a package whose row is usable and whose signature still
// verifies against the trusted keys.
func (m *Manager) loadUsable(ctx context.Context, kind Kind, name string) (*Package, Row, error) {
	row, err := m.store.Get(ctx, kind, name)
	if err != nil {
		return nil, row, err
	}
	if err := m.usable(row); err != nil {
		return nil, row, err
	}
	p, row, err := m.store.Load(ctx, kind, name)
	if err != nil {
		return nil, row, err
	}
	if row.SignerKeyID != "" {
		if err := m.Trust.Verify(p); err != nil {
			return nil, row, err
		}
	}
	return p, row, nil
}

// Blocked says why an installed package is not loaded because of policy ("" when
// it is not blocked): shown in the admin so a package is not silently dead.
func (m *Manager) Blocked(kind Kind, name string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.blocked[string(kind)+"/"+name]
}

// ScriptsBlocked reports whether an installed plugin ships assets/plugin.js that
// pages do not link because the owner has not granted it site.scripts. A
// package without plugin.wasm never has its plugin.js linked (only a loaded
// plugin does), so it is not flagged.
func (m *Manager) ScriptsBlocked(name string) bool {
	m.mu.Lock()
	l, ok := m.loaded[name]
	m.mu.Unlock()
	return ok && l.pkg != nil && l.pkg.Has(PluginAssetJS) && l.pkg.Has(PluginWasm) && (m.Grants == nil || !m.Grants.Has(name, sdk.PermSiteScripts))
}

// MarkupStopped lists the plugin's sections and partials that are not
// registered because they carry script constructs and it lacks site.scripts.
func (m *Manager) MarkupStopped(name string) []string { return render.PluginMarkupStopped(name) }

// SetAllowUnsigned stores the owner's opt-in for unsigned packages. until ends
// a timed opt-in (nil: until turned off); duration is its label for the audit
// row ("1h", "24h", "forever"; empty when turning it off).
func (m *Manager) SetAllowUnsigned(ctx context.Context, on bool, until *time.Time, duration string) error {
	if err := m.store.SetAllowUnsignedUntil(ctx, on, until); err != nil {
		return err
	}
	detail := map[string]any{"allow": on}
	if on {
		detail["until"] = "never"
		if until != nil {
			detail["until"] = until.UTC().Format(time.RFC3339)
		}
		detail["duration"] = duration
	}
	m.Audit(ctx, "package.allow_unsigned", "package_config", detail)
	return nil
}

// --- theme settings -----------------------------------------------------------

// ThemeSettingsSchema is the settings_schema of an installed theme (nil: none).
func (m *Manager) ThemeSettingsSchema(ctx context.Context, name string) (json.RawMessage, error) {
	if name == "" || name == DefaultTheme {
		return nil, nil
	}
	row, err := m.store.Get(ctx, KindTheme, name)
	if err != nil {
		return nil, err
	}
	var meta ThemeMeta
	if err := json.Unmarshal(row.Manifest, &meta); err != nil {
		return nil, err
	}
	if len(meta.SettingsSchema) == 0 || string(meta.SettingsSchema) == "null" {
		return nil, nil
	}
	return meta.SettingsSchema, nil
}

// ThemeSettings returns a theme's settings with the schema's defaults merged.
func (m *Manager) ThemeSettings(ctx context.Context, name string) (map[string]any, error) {
	schema, err := m.ThemeSettingsSchema(ctx, name)
	if err != nil {
		return nil, err
	}
	stored, err := m.store.ThemeSettings(ctx, name)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	_ = json.Unmarshal(pluginhost.MergeDefaults(schema, stored), &out)
	return out, nil
}

// SaveThemeSettings validates values against the theme's settings_schema and
// stores them. Problems come back as field errors, not as an error. A change to
// the active theme's settings is live at once.
func (m *Manager) SaveThemeSettings(ctx context.Context, name string, values map[string]any) ([]pluginhost.FieldError, error) {
	schema, err := m.ThemeSettingsSchema(ctx, name)
	if err != nil || len(schema) == 0 {
		return nil, errors.Join(err, errors.New("the theme has no settings"))
	}
	body, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	compiled, err := pluginhost.CompileSchema(schema)
	if err != nil {
		return nil, err
	}
	if fields := pluginhost.Validate(compiled, body); len(fields) > 0 {
		return fields, nil
	}
	if err := m.store.PutThemeSettings(ctx, name, body); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.theme.name == name && m.Engine != nil {
		if s, err := m.ThemeSettings(ctx, name); err == nil {
			m.Engine.SetSettings(s)
		}
		if stamp, err := m.store.ThemeSettingsStamp(ctx, name); err == nil {
			m.theme.stamp = stamp.Format(time.RFC3339Nano)
		}
		m.changed(ctx)
	}
	return nil, nil
}

// --- preview ------------------------------------------------------------------

// PreviewEngine returns an engine that renders with any installed theme (or
// the built-in default), whatever is active, cached until the package changes.
func (m *Manager) PreviewEngine(ctx context.Context, name string) (*render.Engine, error) {
	if m.NewPreview == nil {
		return nil, errors.New("packages: preview is not available")
	}
	if name == "" {
		name = DefaultTheme
	}
	layers, row, err := m.themeLayers(ctx, name)
	if err != nil {
		return nil, err
	}
	settings, err := m.ThemeSettings(ctx, name)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.preview[name]; ok && e.sha == row.SHA256 {
		e.eng.SetSettings(settings)
		return e.eng, nil
	}
	eng, err := m.NewPreview(layers, settings)
	if err != nil {
		return nil, err
	}
	m.preview[name] = previewEntry{sha: row.SHA256, eng: eng}
	return eng, nil
}

// --- reconcile ----------------------------------------------------------------

// Reconcile makes the running process match the database: plugins that are
// installed and enabled are loaded (or reloaded when their package changed),
// removed or disabled ones are unloaded, and the active theme is applied.
func (m *Manager) Reconcile(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconcile(ctx)
}

// Run reconciles every interval until ctx ends, so a package installed by the
// CLI (or another replica) goes live without a restart.
func (m *Manager) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Reconcile(ctx)
		}
	}
}

func (m *Manager) changed(ctx context.Context) {
	if m.OnChange != nil {
		m.OnChange(ctx)
	}
}

func (m *Manager) reconcile(ctx context.Context) {
	rows, err := m.store.List(ctx)
	if err != nil {
		m.Logger.Error("packages: list", "error", err)
		return
	}
	m.markBlocked(rows)
	m.refreshOrigins(rows)
	changed := false
	if m.Plugins != nil {
		changed = m.reconcilePlugins(ctx, rows)
	}
	changed = m.reconcileWidgets(ctx, rows) || changed
	if m.Engine != nil {
		changed = m.reconcileTheme(ctx, rows) || changed
	}
	if changed {
		m.changed(ctx)
	}
}

// markBlocked records which installed packages policy keeps from running, and
// logs each one once when it becomes blocked.
func (m *Manager) markBlocked(rows []Row) {
	now := map[string]string{}
	for _, r := range rows {
		if err := m.usable(r); err != nil {
			key := string(r.Kind) + "/" + r.Name
			now[key] = err.Error()
			if m.blocked[key] != now[key] {
				m.Logger.Warn("packages: installed package not loaded", "kind", r.Kind, "name", r.Name, "reason", now[key])
			}
		}
	}
	m.blocked = now
}

// scriptGate says whether plugin's script is linked on pages: policy allows it
// and the owner granted site.scripts.
func (m *Manager) scriptGate(plugin string) bool {
	if m.scriptsPolicyRefuses(plugin) {
		return false
	}
	if m.Grants == nil || !m.Grants.Has(plugin, sdk.PermSiteScripts) {
		return false
	}
	for _, st := range m.Plugins.Statuses() {
		if st.Name == plugin {
			return st.Loaded && slices.Contains(st.Manifest.Permissions, sdk.PermSiteScripts)
		}
	}
	return false
}

func (m *Manager) reconcilePlugins(ctx context.Context, rows []Row) bool {
	// A plugin's script is linked on pages only while the owner grants site.scripts.
	// A grant row outlives an update that stops asking: it counts only while
	// the loaded manifest still requests site.scripts.
	render.SetPluginScriptGate(m.scriptGate)
	want := map[string]Row{}
	for _, r := range rows {
		if r.Kind != KindPlugin || !r.Enabled || r.State != StateInstalled {
			continue
		}
		if _, blocked := m.blocked["plugin/"+r.Name]; blocked {
			continue
		}
		want[r.Name] = r
	}
	changed := false
	for name, l := range m.loaded {
		if _, ok := want[name]; !ok && !l.pinned {
			m.Plugins.Unload(ctx, name)
			delete(m.loaded, name)
			changed = true
		}
	}
	grantsLoaded := false
	for name, row := range want {
		if l, ok := m.loaded[name]; ok && (l.pinned || l.sha == row.SHA256) {
			continue
		}
		if !grantsLoaded {
			// A plugin installed by another process (the CLI, a replica) comes with
			// grants only that process's memory holds: read them before loading, so
			// permission-gated hooks (builder.data) register at once.
			grantsLoaded = true
			if g, ok := m.Grants.(interface{ Load(context.Context) error }); ok {
				if err := g.Load(ctx); err != nil {
					m.Logger.Error("packages: grants not reloaded", "error", err)
				}
			}
		}
		p, _, err := m.loadUsable(ctx, KindPlugin, name)
		if err != nil {
			m.Logger.Error("packages: load plugin package", "plugin", name, "error", err)
			m.loaded[name] = loadedPlugin{sha: row.SHA256} // do not retry until it changes
			continue
		}
		if p.Has(PluginWasm) { // a widget-only package has no code to load
			m.loadPlugin(ctx, p)
		}
		m.loaded[name] = loadedPlugin{sha: row.SHA256, pkg: p}
		changed = true
	}
	return changed
}

// loadPlugin loads a package's plugin into the runtime and publishes its
// public assets.
func (m *Manager) loadPlugin(ctx context.Context, p *Package) pluginhost.Status {
	man, _ := p.PluginManifest()
	wasm, err := p.Wasm()
	var assets pluginhost.Assets
	if err == nil {
		assets, err = pluginhost.AssetsFromFS(p.FS())
	}
	if err != nil {
		m.Logger.Error("packages: plugin files", "plugin", p.Name, "error", err)
		return pluginhost.Status{Name: p.Name, Manifest: man, Error: err.Error()}
	}
	st := m.Plugins.LoadWith(ctx, man, wasm, assets)
	if st.Loaded {
		var css, js string
		if p.Has(PluginAssetCSS) {
			css = "/p/" + p.Name + "/assets/plugin.css"
		}
		if p.Has(PluginAssetJS) {
			js = "/p/" + p.Name + "/assets/plugin.js"
		}
		render.SetPluginAssets(p.Name, css, js)
	}
	return st
}

// Public asset files a plugin may ship; pages link them when present.
const (
	PluginAssetCSS = "assets/plugin.css"
	PluginAssetJS  = "assets/plugin.js"
)

func (m *Manager) reconcileTheme(ctx context.Context, rows []Row) bool {
	cfg, err := m.store.Config(ctx)
	if err != nil {
		m.Logger.Error("packages: config", "error", err)
		return false
	}
	name, sha := cfg.ActiveTheme, ""
	for _, r := range rows {
		if r.Kind == KindTheme && r.Name == name {
			sha = r.SHA256
		}
	}
	m.themeNote = ""
	if reason, blocked := m.blocked["theme/"+name]; blocked && name != "" {
		m.themeNote = fmt.Sprintf("theme %q is not applied (%s); the built-in theme is serving", name, reason)
		sha = ""
	}
	if name != "" && sha == "" { // the row is gone or blocked: fall back to the built-in theme
		name = ""
	}
	stamp := ""
	if name != "" {
		if t, err := m.store.ThemeSettingsStamp(ctx, name); err == nil && !t.IsZero() {
			stamp = t.Format(time.RFC3339Nano)
		}
	}
	if name == m.theme.name && sha == m.theme.sha {
		if stamp == m.theme.stamp {
			return false
		}
		// Only the theme's settings changed (here or on another replica).
		settings, _ := m.ThemeSettings(ctx, name)
		m.Engine.SetSettings(settings)
		m.theme.stamp = stamp
		return true
	}
	key := name + "@" + sha
	if key == m.theme.failedSHA {
		return false
	}
	m.theme.failedSHA = ""
	layers, _, err := m.themeLayers(ctx, name)
	if err == nil {
		m.Engine.SetThemeName(name)
		err = m.Engine.SetTheme(layers...)
	}
	if err != nil {
		m.theme.err, m.theme.failedSHA = err, name+"@"+sha
		m.Logger.Error("packages: theme not applied; the previous theme keeps serving", "theme", name, "error", err)
		// A release that no longer parses the active theme (a template func
		// it calls is gone) leaves the site on the built-in theme with nothing
		// failing at request time; the supervisor's gate has to see it.
		render.NoteBootFault("theme-not-applied:" + name)
		return false
	}
	settings, _ := m.ThemeSettings(ctx, name)
	m.Engine.SetSettings(settings)
	m.theme = appliedTheme{name: name, sha: sha, stamp: stamp}
	m.Logger.Info("packages: theme applied", "theme", orDefault(name))
	return true
}

func orDefault(name string) string {
	if name == "" {
		return DefaultTheme
	}
	return name
}

// ThemeError is why the active theme could not be applied ("" when it was).
func (m *Manager) ThemeError() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.themeNote != "" {
		return m.themeNote
	}
	if m.theme.err != nil && m.theme.failedSHA != "" {
		return m.theme.err.Error()
	}
	return ""
}

// ActiveTheme is the theme the process is serving with (DefaultTheme for the built-in).
func (m *Manager) ActiveTheme() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return orDefault(m.theme.name)
}

// LoadDevDir loads plugins from a directory (HATAMOTO_PLUGIN_DIR, development
// only) as unsigned in-memory packages. They replace an installed package of
// the same name and are not stored.
func (m *Manager) LoadDevDir(ctx context.Context, dir string) {
	if m.Plugins == nil || dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.Logger.Error("packages: read plugin dir", "dir", dir, "error", err)
		}
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range entries {
		pdir := filepath.Join(dir, e.Name())
		if fi, err := os.Stat(filepath.Join(pdir, PluginManifest)); err != nil || fi.IsDir() {
			continue
		}
		raw, err := PackDir(pdir, nil)
		var p *Package
		if err == nil {
			p, err = Parse(raw, DefaultLimits)
		}
		if err != nil {
			m.Logger.Error("packages: plugin dir", "plugin", e.Name(), "error", err)
			continue
		}
		m.loadPlugin(ctx, p)
		m.loaded[p.Name] = loadedPlugin{sha: "dir", pkg: p, pinned: true}
	}
}

// PluginPackage returns the parsed package of a loaded plugin (its public
// assets and ui/ files), nil when it is not loaded.
func (m *Manager) PluginPackage(name string) *Package {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loaded[name].pkg
}

// Rows lists the installed packages, plugins first.
func (m *Manager) Rows(ctx context.Context) ([]Row, error) {
	rows, err := m.store.List(ctx)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Kind > rows[j].Kind })
	return rows, err
}
