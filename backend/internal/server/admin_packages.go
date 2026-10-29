package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

// packageAdmin adapts the package manager to the Plugins and Themes pages.
type packageAdmin struct {
	s *Server

	mu       sync.Mutex
	cached   *packages.Index
	cacheErr error
	cachedAt time.Time
}

const registryCacheTTL = 30 * time.Second

func (p *packageAdmin) mgr() *packages.Manager { return p.s.packages }

// actorCtx tags ctx with the signed-in admin user, so the audit log says who
// made a package change.
func actorCtx(ctx context.Context) context.Context {
	u, ok := admin.UserFromContext(ctx)
	if !ok {
		return ctx
	}
	id, _ := uuid.Parse(u.ID)
	return packages.WithActor(ctx, packages.Actor{ID: id, Email: u.Email, Name: u.Name})
}

// index is the verified registry index, cached briefly so one page view reads
// the registry once however many lists it builds.
func (p *packageAdmin) index(ctx context.Context) (*packages.Index, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Since(p.cachedAt) < registryCacheTTL {
		return p.cached, p.cacheErr
	}
	if p.mgr().Registry == nil {
		return nil, packages.ErrRegistryOff
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	p.cached, p.cacheErr = p.mgr().Registry.Fetch(ctx)
	p.cachedAt = time.Now()
	return p.cached, p.cacheErr
}

func (p *packageAdmin) installedVersions(ctx context.Context) map[string]string {
	out := map[string]string{}
	rows, _ := p.mgr().Rows(ctx)
	for _, r := range rows {
		out[string(r.Kind)+"/"+r.Name] = r.Version
	}
	return out
}

// updateFor is the registry's version when it is newer than the installed one.
func (p *packageAdmin) updateFor(ctx context.Context, kind packages.Kind, name, installed string) string {
	idx, err := p.index(ctx)
	if err != nil || idx == nil {
		return ""
	}
	if e, ok := idx.Find(kind, name); ok && packages.Newer(e.Version, installed) {
		return e.Version
	}
	return ""
}

// newPermissions are the registry entry's permissions that the installed
// manifest does not list: what an update would ask for on top of today.
func newPermissions(offered, installed []string) []string {
	var out []string
	for _, perm := range offered {
		if !slices.Contains(installed, perm) && !slices.Contains(out, perm) {
			out = append(out, perm)
		}
	}
	return out
}

// updateNewFor lists the permissions the registry's newer version of a plugin
// asks for beyond the installed manifest's. It reads the cached index only.
func (p *packageAdmin) updateNewFor(ctx context.Context, name, installed string, have []string) []string {
	idx, err := p.index(ctx)
	if err != nil || idx == nil {
		return nil
	}
	if e, ok := idx.Find(packages.KindPlugin, name); ok && packages.Newer(e.Version, installed) {
		return newPermissions(e.Permissions, have)
	}
	return nil
}

func (p *packageAdmin) Registry(ctx context.Context) admin.RegistryView {
	idx, err := p.index(ctx)
	switch {
	case errors.Is(err, packages.ErrRegistryOff):
		return admin.RegistryView{Off: true}
	case err != nil:
		host := ""
		if u, perr := url.Parse(p.s.cfg.Packages.RegistryURL); perr == nil {
			host = u.Host
		}
		return admin.RegistryView{Err: err.Error(), Host: host}
	}
	have := p.installedVersions(ctx)
	var v admin.RegistryView
	for _, e := range idx.Packages {
		cur, installed := have[string(e.Kind)+"/"+e.Name]
		v.Entries = append(v.Entries, admin.RegistryEntry{
			Kind: string(e.Kind), Name: e.Name, Version: e.Version, Description: e.Description, Homepage: e.Homepage,
			Permissions: e.Permissions, Installed: installed, InstalledVersion: cur, Update: installed && packages.Newer(e.Version, cur),
		})
	}
	return v
}

func (p *packageAdmin) Fetch(ctx context.Context, kind, name string) ([]byte, error) {
	if p.mgr().Registry == nil {
		return nil, &admin.Refusal{Msg: "No registry is set up."}
	}
	data, err := p.mgr().FetchFromRegistry(ctx, packages.Kind(kind), name)
	return data, refusal(err)
}

// refusal turns what the owner should read into an admin.Refusal; anything
// else (database, internal) passes through to be logged.
func refusal(err error) error {
	var pe *packages.ParseError
	var need *packages.ConfirmationRequired
	switch {
	case err == nil:
		return nil
	case errors.As(err, &need):
		return &admin.Refusal{Msg: need.Error() + ". Start the install again and confirm it."}
	case errors.As(err, &pe), errors.Is(err, packages.ErrUninstalling), errors.Is(err, packages.ErrIndexExpired), errors.Is(err, packages.ErrIndexNotFresh), errors.Is(err, packages.ErrInsecureURL),
		errors.Is(err, packages.ErrUnsigned), errors.Is(err, packages.ErrUntrustedKey), errors.Is(err, packages.ErrBadSignature),
		errors.Is(err, packages.ErrUnsignedNotAllowed), errors.Is(err, packages.ErrSignerNotAllowed), errors.Is(err, packages.ErrDevDisabled),
		errors.Is(err, packages.ErrReservedName), errors.Is(err, packages.ErrPermission), errors.Is(err, packages.ErrRegistryOff), errors.Is(err, packages.ErrCommunityScripts):
		return &admin.Refusal{Msg: strings.TrimPrefix(err.Error(), "package: ")}
	}
	if strings.Contains(err.Error(), "registry") || strings.Contains(err.Error(), "download") {
		return &admin.Refusal{Msg: err.Error()}
	}
	return err
}

func (p *packageAdmin) Inspect(ctx context.Context, data []byte) (admin.PackageSummary, error) {
	pkg, err := packages.Parse(data, packages.DefaultLimits)
	if err != nil {
		return admin.PackageSummary{}, refusal(err)
	}
	sum := admin.PackageSummary{Kind: string(pkg.Kind), Name: pkg.Name, Version: pkg.Version}
	switch pkg.Kind {
	case packages.KindPlugin:
		m, _ := pkg.PluginManifest()
		sum.Description, sum.Permissions, sum.AllowedHosts = m.Description, m.Permissions, m.AllowedHosts
	case packages.KindTheme:
		m, _ := pkg.ThemeMeta()
		sum.Description = m.Description
	}
	if pkg.Signature != nil {
		sum.Signed, sum.Signer = true, pkg.Signature.Signer
	}
	if err := p.mgr().CheckPolicy(ctx, pkg, packages.SourceUpload); err != nil {
		sum.Problem = err.Error()
	}
	if row, err := p.mgr().Store().Get(ctx, pkg.Kind, pkg.Name); err == nil {
		sum.Updating, sum.InstalledVersion = true, row.Version
		if p.s.plugins != nil {
			sum.Granted = p.s.plugins.Grants().Granted(pkg.Name)
		}
		if row.State == packages.StateUninstalling {
			sum.Problem = packages.ErrUninstalling.Error()
		}
	}
	if need, err := p.mgr().NeedsConfirmation(ctx, pkg, packages.SourceUpload); err == nil && need != nil {
		c := &admin.ConfirmInfo{Name: need.Name, Downgrade: need.Downgrade, SignerChange: need.SignerChange,
			WidenedAccess: need.WidenedAccess, NewHosts: need.NewHosts, NewFetchAny: need.NewFetchAny, NewIsland: need.NewIsland, NewScripts: need.NewScripts,
			OldVersion: need.OldVersion, NewVersion: need.NewVersion, OldSigner: need.OldSigner, NewSigner: need.NewSigner}
		for _, m := range need.Missing {
			c.Missing = append(c.Missing, admin.MissingSection{Type: m.Type, Uses: m.Uses})
		}
		sum.Confirm = c
	}
	return sum, nil
}

func (p *packageAdmin) Install(ctx context.Context, data []byte, source string, grant []string, confirm bool) error {
	_, err := p.mgr().Install(actorCtx(ctx), data, packages.InstallRequest{Source: packages.Source(source), Grant: grant, Confirm: confirm})
	return refusal(err)
}

func (p *packageAdmin) SetEnabled(ctx context.Context, name string, on bool) error {
	return p.mgr().SetEnabled(actorCtx(ctx), name, on)
}

func (p *packageAdmin) Uninstall(ctx context.Context, kind, name string, dropData bool) error {
	err := p.mgr().Uninstall(actorCtx(ctx), packages.Kind(kind), name, dropData)
	if errors.Is(err, packages.ErrThemeActive) {
		return admin.ErrThemeActive
	}
	return err
}

func (p *packageAdmin) AllowUnsigned(ctx context.Context) bool {
	cfg, err := p.mgr().Store().Config(ctx)
	return err == nil && cfg.AllowUnsigned
}

func (p *packageAdmin) SetAllowUnsigned(ctx context.Context, on bool) error {
	return p.mgr().SetAllowUnsigned(actorCtx(ctx), on, nil, "")
}

func (p *packageAdmin) Themes(ctx context.Context) ([]admin.ThemeInfo, error) {
	rows, err := p.mgr().Rows(ctx)
	if err != nil {
		return nil, err
	}
	active := p.mgr().ActiveTheme()
	out := []admin.ThemeInfo{{Name: packages.DefaultTheme, Builtin: true, Active: active == packages.DefaultTheme}}
	for _, r := range rows {
		if r.Kind != packages.KindTheme {
			continue
		}
		var meta packages.ThemeMeta
		_ = json.Unmarshal(r.Manifest, &meta)
		out = append(out, admin.ThemeInfo{
			Name: r.Name, Version: r.Version, Description: meta.Description, Active: r.Name == active,
			Signed: r.Signed(), Signer: r.SignerKeyID, Source: string(r.Source),
			HasSettings: len(meta.SettingsSchema) > 0 && string(meta.SettingsSchema) != "null",
			Update:      p.updateFor(ctx, packages.KindTheme, r.Name, r.Version),
			Blocked:     p.mgr().Blocked(packages.KindTheme, r.Name),
		})
	}
	return out, nil
}

func (p *packageAdmin) MissingSections(ctx context.Context, theme string) ([]admin.MissingSection, error) {
	ms, err := p.mgr().MissingSections(ctx, theme)
	out := make([]admin.MissingSection, 0, len(ms))
	for _, m := range ms {
		out = append(out, admin.MissingSection{Type: m.Type, Uses: m.Uses})
	}
	return out, err
}

// Activate switches the theme. The admin showed the owner the missing section
// types on the confirmation page, so the check is not repeated.
func (p *packageAdmin) Activate(ctx context.Context, theme string) error {
	return refusal(p.mgr().Activate(actorCtx(ctx), theme, true))
}

func (p *packageAdmin) ThemeSettingsSchema(ctx context.Context, theme string) []byte {
	b, _ := p.mgr().ThemeSettingsSchema(ctx, theme)
	return b
}

func (p *packageAdmin) ThemeSettings(ctx context.Context, theme string) (map[string]any, error) {
	return p.mgr().ThemeSettings(ctx, theme)
}

func (p *packageAdmin) SaveThemeSettings(ctx context.Context, theme string, values map[string]any) (schemaform.Errors, error) {
	fields, err := p.mgr().SaveThemeSettings(ctx, theme, values)
	if err != nil || len(fields) == 0 {
		return nil, err
	}
	errs := schemaform.Errors{}
	for _, f := range fields {
		path := strings.ReplaceAll(strings.Trim(f.Path, "/"), "/", ".")
		errs[path] = append(errs[path], f.Message)
	}
	return errs, nil
}

func (p *packageAdmin) PreviewURL(theme string) string {
	if p.s.preview == nil {
		return "/"
	}
	return "/?" + previewParam + "=" + url.QueryEscape(p.s.preview.Token(theme, time.Now()))
}

func (p *packageAdmin) UIFile(w http.ResponseWriter, r *http.Request, plugin, file, csp, cache string) bool {
	pkg := p.mgr().PluginPackage(plugin)
	if pkg == nil || !pkg.Has(file) {
		return false
	}
	packages.ServeFile(w, r, pkg, file, cache, csp)
	return true
}

// bufferedResponse captures a plugin route's response.
type bufferedResponse struct {
	h      http.Header
	status int
	body   []byte
}

func (b *bufferedResponse) Header() http.Header { return b.h }
func (b *bufferedResponse) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}
func (b *bufferedResponse) Write(p []byte) (int, error) {
	b.WriteHeader(http.StatusOK)
	b.body = append(b.body, p...)
	return len(p), nil
}

func (p *packageAdmin) CallRoute(ctx context.Context, plugin, method, path string, query url.Values, body []byte, user admin.CurrentUser) admin.RouteResult {
	if p.s.plugins == nil {
		return admin.RouteResult{Status: http.StatusNotFound, ContentType: "application/json", Body: []byte(`{"error":"not found"}`)}
	}
	u := url.URL{Path: "/api/v1/p/" + plugin + "/" + path, RawQuery: query.Encode()}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), strings.NewReader(string(body)))
	if err != nil {
		return admin.RouteResult{Status: http.StatusBadRequest, ContentType: "application/json", Body: []byte(`{"error":"bad request"}`)}
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := &bufferedResponse{h: http.Header{}}
	p.s.plugins.AdminRoutes().Serve(rec, req, plugin, path, &sdk.HTTPUser{ID: user.ID, Role: user.Role})
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return admin.RouteResult{Status: rec.status, ContentType: rec.h.Get("Content-Type"), Body: rec.body}
}

func (p *packageAdmin) CallHost(ctx context.Context, plugin, fn string, args json.RawMessage) json.RawMessage {
	if p.s.plugins == nil {
		return json.RawMessage(`{"ok":false,"error":{"code":"unavailable","message":"plugins are not available"}}`)
	}
	return p.s.plugins.CallHost(ctx, plugin, sdk.CallRequest{Fn: fn, Args: args})
}

// decoratePlugins adds what packages know to the plugin list: the package
// metadata, the admin page of a plugin that ships one, and installed plugins
// that are switched off (the runtime has no status for those).
func (p *packageAdmin) decoratePlugins(infos []admin.PluginInfo) []admin.PluginInfo {
	ctx := context.Background()
	rows, err := p.mgr().Rows(ctx)
	if err != nil {
		return infos
	}
	uis := map[string]*sdk.AdminUI{}
	for _, st := range p.s.pluginStatuses() {
		if st.Manifest.UI != nil {
			uis[st.Name] = st.Manifest.UI
		}
	}
	byName := map[string]packages.Row{}
	for _, r := range rows {
		if r.Kind == packages.KindPlugin {
			byName[r.Name] = r
		}
	}
	seen := map[string]bool{}
	for i := range infos {
		seen[infos[i].Name] = true
		if r, ok := byName[infos[i].Name]; ok {
			infos[i].Package = p.meta(ctx, r)
		}
		if ui := uis[infos[i].Name]; ui != nil {
			infos[i].UI = p.uiInfo(ui, infos[i].Granted)
		}
	}
	for _, r := range rows {
		if r.Kind != packages.KindPlugin || seen[r.Name] {
			continue
		}
		var m sdk.Manifest
		_ = json.Unmarshal(r.Manifest, &m)
		meta := p.meta(ctx, r)
		meta.WidgetOnly = p.mgr().WidgetCount(r.Name) > 0 // no runtime status: a pack without wasm
		infos = append(infos, admin.PluginInfo{
			Name: r.Name, Version: m.Version, SDK: m.SDK, Description: m.Description, Permissions: m.Permissions,
			Granted: p.grantedOf(r.Name), AllowedHosts: m.AllowedHosts, Package: meta,
			Refused: p.s.refusedPermissions(r.Name, m.Permissions),
		})
	}
	return infos
}

func (p *packageAdmin) grantedOf(name string) []string {
	if p.s.plugins == nil {
		return nil
	}
	return p.s.plugins.Grants().Granted(name)
}

func (p *packageAdmin) meta(ctx context.Context, r packages.Row) *admin.PackageMeta {
	var man sdk.Manifest
	_ = json.Unmarshal(r.Manifest, &man)
	scripts := p.mgr().ScriptsBlocked(r.Name)
	if !scripts && p.s.plugins != nil && p.s.plugins.ScriptsDropped(r.Name) {
		scripts = !p.s.plugins.Grants().Has(r.Name, sdk.PermSiteScripts)
	}
	m := &admin.PackageMeta{Enabled: r.Enabled, Signed: r.Signed(), Signer: r.SignerKeyID, FirstParty: firstPartySigned(r), Source: string(r.Source),
		Update: p.updateFor(ctx, packages.KindPlugin, r.Name, r.Version), UpdateNew: p.updateNewFor(ctx, r.Name, r.Version, man.Permissions),
		Blocked: p.mgr().Blocked(packages.KindPlugin, r.Name), ScriptsBlocked: scripts, MarkupStopped: p.mgr().MarkupStopped(r.Name), AsksScripts: slices.Contains(man.Permissions, sdk.PermSiteScripts), Uninstalling: r.State == packages.StateUninstalling}
	if r.Name == legacyWebhooksPlugin {
		m.WebhooksImported = p.s.webhooksImportRecorded(ctx)
	}
	return m
}

func (p *packageAdmin) uiInfo(ui *sdk.AdminUI, granted []string) *admin.PluginUIInfo {
	entry := ui.Entry
	if entry == "" {
		entry = sdk.DefaultUIEntry
	}
	return &admin.PluginUIInfo{Title: ui.Title, Entry: entry, Roles: ui.Roles, Functions: pluginhost.HostFunctionsFor(slices.Clone(granted))}
}
