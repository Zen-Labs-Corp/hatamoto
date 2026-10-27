package packages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"slices"
	"sort"
	"strings"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

// A plugin package may ship builder widgets: widgets/<name>/ with schema.json
// and widget.html (required), widget.css and island.js (optional). Each is
// registered as the builder kind <plugin>_<name>.
const (
	widgetsDir     = "widgets"
	maxPackWidgets = 16 // widgets in one package
	widgetSchema   = "schema.json"
	widgetTemplate = "widget.html"
	widgetStylesF  = "widget.css"
	widgetIslandF  = "island.js"
)

var (
	widgetDirRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	widgetFileSet = map[string]bool{widgetSchema: true, widgetTemplate: true, widgetStylesF: true, widgetIslandF: true}
)

// checkWidgetEntry refuses a path under widgets/ that is not widgets/ itself,
// widgets/<name>/ or one of the four files of a widget.
func checkWidgetEntry(clean string, isDir bool) error {
	parts := strings.Split(clean, "/")
	switch {
	case len(parts) == 1: // widgets (a directory; a file was refused as a root file)
		return nil
	case !widgetDirRe.MatchString(parts[1]):
		return refuse("%s: widget directory names are lowercase letters, digits and \"-\", at most 32, starting with a letter", clean)
	case len(parts) == 2 && isDir:
		return nil
	case len(parts) == 3 && !isDir && widgetFileSet[parts[2]]:
		return nil
	}
	return refuse("%s: a widget holds only %s, %s, %s and %s", clean, widgetSchema, widgetTemplate, widgetStylesF, widgetIslandF)
}

// Widgets are the widgets a plugin package ships, in name order.
type Widgets struct {
	Plugin string
	Items  []Widget
}

// Widget is one widgets/<name>/ directory. Kind is the builder kind.
type Widget struct {
	Name, Kind                string
	Schema, HTML, CSS, Island []byte
}

// Widgets reads the widgets of a plugin package (none for a theme or a
// package without widgets/).
func (p *Package) Widgets() (Widgets, error) {
	out := Widgets{Plugin: p.Name}
	if p.Kind != KindPlugin {
		return out, nil
	}
	dirs, err := fs.ReadDir(p.FS(), widgetsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		kind, err := builder.PackKindName(p.Name, d.Name())
		if err != nil {
			return out, fmt.Errorf("%s/%s: %w", widgetsDir, d.Name(), err)
		}
		w := Widget{Name: d.Name(), Kind: kind}
		for _, f := range []struct {
			file     string
			dst      *[]byte
			required bool
		}{
			{widgetSchema, &w.Schema, true}, {widgetTemplate, &w.HTML, true},
			{widgetStylesF, &w.CSS, false}, {widgetIslandF, &w.Island, false},
		} {
			b, err := p.ReadFile(widgetsDir + "/" + d.Name() + "/" + f.file)
			switch {
			case errors.Is(err, fs.ErrNotExist) && !f.required:
			case err != nil:
				return out, fmt.Errorf("%s/%s/%s: %w", widgetsDir, d.Name(), f.file, err)
			default:
				*f.dst = b
			}
		}
		out.Items = append(out.Items, w)
	}
	return out, nil
}

// validateWidgets applies every install-time rule to the widgets of p: the
// builder schema rules and the render template/CSS/island rules, at most 16
// widgets. The error is a ParseError naming widgets/<name>/<file> and the rule.
// The same rules run again when stored packages are reconciled.
func validateWidgets(p *Package) error {
	schemas, _, err := buildWidgets(p)
	if err != nil {
		return err
	}
	// A package already installed before the element settings existed keeps its
	// own anchor/aria/tip props (reconcile only warns); a new one must pick
	// other names.
	for _, s := range schemas {
		if owned := s.Owned(); len(owned) > 0 {
			return refuse("widget %s: schema property %q is reserved for the builder's element settings; rename it", s.Name, owned[0])
		}
	}
	return nil
}

// buildWidgets validates the widgets of p and returns them in the forms the
// builder and render registries take.
func buildWidgets(p *Package) ([]builder.PackWidgetSchema, []render.PackWidget, error) {
	ws, err := p.Widgets()
	if err != nil {
		return nil, nil, refuse("%v", err)
	}
	if len(ws.Items) > maxPackWidgets {
		return nil, nil, refuse("%d widgets, at most %d per package", len(ws.Items), maxPackWidgets)
	}
	own := map[string]bool{}
	for _, w := range ws.Items {
		own[w.Kind] = true
	}
	var schemas []builder.PackWidgetSchema
	var views []render.PackWidget
	for _, w := range ws.Items {
		at := widgetsDir + "/" + w.Name + "/"
		if len(w.Schema) == 0 {
			return nil, nil, refuse("%s%s: missing", at, widgetSchema)
		}
		if len(w.HTML) == 0 {
			return nil, nil, refuse("%s%s: missing", at, widgetTemplate)
		}
		sc, err := builder.ParsePackWidget(ws.Plugin, w.Name, w.Schema, own)
		if err != nil {
			return nil, nil, refuse("%s%s: %v", at, widgetSchema, err)
		}
		if err := render.ValidatePackWidget(w.Kind, render.PackFiles{HTML: w.HTML, CSS: w.CSS, Island: w.Island}); err != nil {
			return nil, nil, refuse("%s%v", at, err)
		}
		if len(w.Island) > 0 {
			if man, err := p.PluginManifest(); err != nil || !slices.Contains(man.Permissions, sdk.PermBuilderIsland) {
				return nil, nil, refuse("%sisland.js: the plugin must list the %q permission in plugin.json (an island runs JavaScript on your site's pages)", at, sdk.PermBuilderIsland)
			}
		}
		schemas = append(schemas, sc)
		views = append(views, render.PackWidget{Kind: w.Kind, HTML: w.HTML, CSS: w.CSS, Island: w.Island})
	}
	return schemas, views, nil
}

// widgetEntry is what reconcile knows of one stored plugin package's widgets,
// cached by package hash so unchanged packages are not read again.
type widgetEntry struct {
	sha     string
	schemas []builder.PackWidgetSchema
	views   []render.PackWidget
}

// widgetState is the registration reconcile applied last.
type widgetState struct {
	key     string // sorted name@sha of the packages applied
	schemas []builder.PackWidgetSchema
	views   []render.PackWidget
	cache   map[string]widgetEntry // plugin name -> entry
}

// widgetCount is how many widgets the stored package of a plugin ships.
func (m *Manager) widgetCount(name string) int { return len(m.widgets.cache[name].schemas) }

// WidgetCount is widgetCount for callers outside the manager's lock: the
// widgets the last reconcile found in the plugin's package.
func (m *Manager) WidgetCount(name string) int {
	if c := m.widgetCounts.Load(); c != nil {
		return (*c)[name]
	}
	return 0
}

// reconcileWidgets makes the builder and render registries match the enabled,
// installed, unblocked plugin packages that ship widgets. One swap per call,
// only when the wanted set changed. A package that no longer validates is
// skipped and logged, never partially registered. It reports whether the
// registries changed.
func (m *Manager) reconcileWidgets(ctx context.Context, rows []Row) bool {
	var wanted []Row
	for _, r := range rows {
		if r.Kind != KindPlugin || !r.Enabled || r.State != StateInstalled {
			continue
		}
		if _, blocked := m.blocked["plugin/"+r.Name]; blocked {
			continue
		}
		wanted = append(wanted, r)
	}
	sort.Slice(wanted, func(i, j int) bool { return wanted[i].Name < wanted[j].Name })

	// Grants changed by another process (the CLI, a replica) reach this one
	// here: read them when an island's grant decides what is served.
	if m.islandPlugins(wanted) {
		if g, ok := m.Grants.(interface{ Load(context.Context) error }); ok {
			if err := g.Load(ctx); err != nil {
				m.Logger.Error("packages: grants not reloaded", "error", err)
			}
		}
	}
	cache := map[string]widgetEntry{}
	var (
		schemas []builder.PackWidgetSchema
		views   []render.PackWidget
		keys    []string
	)
	for _, r := range wanted {
		e, ok := m.widgets.cache[r.Name]
		if !ok || e.sha != r.SHA256 {
			e = m.readWidgets(ctx, r)
		}
		cache[r.Name] = e
		if len(e.schemas) == 0 {
			continue
		}
		if len(schemas)+len(e.schemas) > builder.MaxPackWidgets {
			m.Logger.Warn("packages: widgets skipped, the site is at its limit", "plugin", r.Name, "widgets", len(e.schemas), "limit", builder.MaxPackWidgets)
			continue
		}
		// An island is served only while the owner grants builder.island: the
		// widget renders either way.
		vs, key := e.views, r.Name+"@"+r.SHA256
		if islandOf(vs) {
			// A community island is same-origin script: never served, even
			// with a grant stored before the policy.
			if m.Grants != nil && m.Grants.Has(r.Name, sdk.PermBuilderIsland) && !m.scriptsPolicyRefuses(r.Name) {
				key += "+island"
			} else {
				vs = withoutIslands(vs)
			}
		}
		schemas, views = append(schemas, e.schemas...), append(views, vs...)
		keys = append(keys, key)
	}
	m.widgets.cache = cache
	counts := make(map[string]int, len(cache))
	for name, e := range cache {
		counts[name] = len(e.schemas)
	}
	m.widgetCounts.Store(&counts)
	key := strings.Join(keys, ",")
	if key == m.widgets.key {
		return false
	}
	if err := builder.SetPackKinds(schemas); err != nil {
		m.Logger.Error("packages: register widgets", "error", err)
		return false
	}
	if err := render.SetPackWidgets(views); err != nil {
		m.Logger.Error("packages: register widget templates", "error", err)
		if rerr := builder.SetPackKinds(m.widgets.schemas); rerr != nil { // keep the two registries in step
			m.Logger.Error("packages: restore widgets", "error", rerr)
		}
		return false
	}
	m.widgets.key, m.widgets.schemas, m.widgets.views = key, schemas, views
	return true
}

// readWidgets loads and validates the widgets of one stored plugin package.
// A failure is logged and cached as "no widgets" until the package changes.
func (m *Manager) readWidgets(ctx context.Context, r Row) widgetEntry {
	e := widgetEntry{sha: r.SHA256}
	p := m.loaded[r.Name].pkg
	if p == nil || m.loaded[r.Name].sha != r.SHA256 {
		var err error
		if p, _, err = m.loadUsable(ctx, KindPlugin, r.Name); err != nil {
			m.Logger.Error("packages: read widgets", "plugin", r.Name, "error", err)
			return widgetEntry{} // sha unset: tried again at the next reconcile
		}
	}
	if !hasWidgetFiles(p) {
		return e
	}
	schemas, views, err := buildWidgets(p)
	if err != nil {
		m.Logger.Error("packages: plugin widgets skipped", "plugin", r.Name, "error", err)
		return e
	}
	warnOwned(m.Logger, r.Name, schemas)
	e.schemas, e.views = schemas, views
	return e
}

// warnOwned logs the widgets that declare anchor/aria/tip themselves: they
// stay registered and the builder skips that setting for them.
func warnOwned(log *slog.Logger, plugin string, schemas []builder.PackWidgetSchema) {
	for _, s := range schemas {
		if owned := s.Owned(); len(owned) > 0 {
			log.Warn("packages: plugin widget declares props the builder now uses for element settings; the builder skips those settings for it",
				"plugin", plugin, "widget", s.Name, "props", owned)
		}
	}
}

func hasWidgetFiles(p *Package) bool {
	ents, err := fs.ReadDir(p.FS(), widgetsDir)
	return err == nil && len(ents) > 0
}

func islandOf(vs []render.PackWidget) bool {
	return slices.ContainsFunc(vs, func(v render.PackWidget) bool { return len(v.Island) > 0 })
}

// withoutIslands copies vs with every island dropped.
func withoutIslands(vs []render.PackWidget) []render.PackWidget {
	out := slices.Clone(vs)
	for i := range out {
		out[i].Island = nil
	}
	return out
}

// islandPlugins reports whether any of rows asks for builder.island.
func (m *Manager) islandPlugins(rows []Row) bool {
	if m.Grants == nil {
		return false
	}
	for _, r := range rows {
		var man sdk.Manifest
		if json.Unmarshal(r.Manifest, &man) == nil && slices.Contains(man.Permissions, sdk.PermBuilderIsland) {
			return true
		}
	}
	return false
}

// SetGrants replaces the grants of an installed plugin the runtime does not
// know (a widget-only package has no wasm to load): perms must be listed in
// its manifest. The registries are reconciled so an island grant applies at
// once. ErrNotFound is returned for an unknown plugin.
func (m *Manager) SetGrants(ctx context.Context, name string, perms []sdk.Permission) error {
	row, err := m.store.Get(ctx, KindPlugin, name)
	if err != nil {
		return ErrNotFound
	}
	var man sdk.Manifest
	if err := json.Unmarshal(row.Manifest, &man); err != nil {
		return err
	}
	for _, p := range perms {
		if !slices.Contains(man.Permissions, p) {
			return fmt.Errorf("%w: %q", ErrPermNotRequested, p)
		}
	}
	if err := checkScriptsGrant(ClassifyRow(row), perms); err != nil {
		return err
	}
	if m.Grants == nil {
		return errors.New("packages: no grant store")
	}
	if perms == nil {
		perms = []sdk.Permission{}
	}
	if err := m.Grants.Set(ctx, name, perms); err != nil {
		return err
	}
	m.Reconcile(ctx)
	return nil
}

// ErrPermNotRequested is what SetGrants answers for a permission the manifest lacks.
var ErrPermNotRequested = errors.New("packages: permission not requested by the plugin")
