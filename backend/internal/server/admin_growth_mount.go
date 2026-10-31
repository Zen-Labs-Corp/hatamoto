package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/analytics"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
	linkingv2 "hatamoto.dev/core/internal/linking/v2"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/taxonomy"
	"hatamoto.dev/sdk"
)

// adminGrowth connects the site and growth screens of the Go admin (menus,
// forms, redirects, dashboards, internal linking, plugins) to the server's
// services. Members stay nil when their service is not set up, so the screen
// answers 503 instead of crashing (the route-table build has no services).
func (s *Server) adminGrowth() admin.GrowthDeps {
	var g admin.GrowthDeps
	if s.menuService != nil {
		g.Menus = s.menuService
	}
	if s.redirectService != nil {
		g.Redirects = s.redirectService
		if s.db != nil && s.entityService != nil {
			g.Explain = urlExplainer{s}
		}
	}
	if s.db != nil {
		g.Agents = agentStats{agents.NewStatsRepository(s.db), agents.NewRobotsRepository(s.db)}
		g.Referrals = analytics.NewReader(s.db)
		g.Traffic = analytics.NewReader(s.db)
		g.EngagementSetting = engagementSetting{store: analytics.NewSettingsStore(s.db), svc: s.analyticsService}
		g.GeoStatus = s.geoStatus
	}
	if s.db != nil && s.entityService != nil {
		g.Linking = linkingAdmin{s}
	}
	g.Plugins = pluginAdmin{s: s}
	if s.packages != nil {
		g.Packages = &packageAdmin{s: s}
		g.Plugins = pluginAdmin{s: s, pkgs: g.Packages.(*packageAdmin)}
	}
	if s.sectionTemplateService != nil {
		g.Sections = s.sectionTemplateService
	}
	if s.formService != nil {
		g.Forms = s.formService
		if s.emailService != nil {
			g.FormMail = formMailer{s}
		}
	}
	if s.entityService != nil && s.taxonomyService != nil {
		g.Pickers = pickers{s}
	}
	return g
}

// geoStatus is whether a country database is configured and whether it opened.
func (s *Server) geoStatus() (configured, loaded bool) { return s.geoConfigured, s.geoLoaded }

// engagementSetting is the engagement switch of the Traffic page: the store is the truth, the
// service its cached copy that the request path reads.
type engagementSetting struct {
	store *analytics.SettingsStore
	svc   *analytics.Service
}

// Enabled is what the beacon is counting now; without a service it reads the store (default on).
func (e engagementSetting) Enabled(ctx context.Context) bool {
	if e.svc != nil {
		return e.svc.EngagementEnabled()
	}
	on, err := e.store.EngagementEnabled(ctx)
	return err != nil || on
}

// Set writes the store, then switches the running service.
func (e engagementSetting) Set(ctx context.Context, on bool) error {
	if err := e.store.SetEngagement(ctx, on); err != nil {
		return err
	}
	if e.svc != nil {
		e.svc.SetEngagement(on)
	}
	return nil
}

// pickers is the minimal entity and term search behind the admin's
// search-select partial (menu items, linking rules).
type pickers struct{ s *Server }

func (p pickers) SearchEntities(ctx context.Context, q string, limit int) ([]admin.PickOption, error) {
	f := &entity.ListEntitiesFilter{Search: q}
	if schemas, err := p.s.entityService.ListSchemas(ctx); err == nil {
		for _, sc := range schemas {
			if entity.IsReuseSchema(sc) { // blocks and layout parts are not pages to link to
				f.ExcludeSchemaIDs = append(f.ExcludeSchemaIDs, sc.ID)
			}
		}
	}
	res, err := p.s.entityService.ListEntitiesWithOffset(ctx, f, &entity.OffsetPage{Page: 1, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]admin.PickOption, 0, len(res.Items))
	for _, e := range res.Items {
		out = append(out, admin.PickOption{Value: e.ID.String(), Label: e.Name, Slug: e.Slug, Hint: "/" + e.Slug + " · " + string(e.Status)})
	}
	return out, nil
}

func (p pickers) EntityName(ctx context.Context, id uuid.UUID) string {
	e, err := p.s.entityService.GetEntity(ctx, id)
	if err != nil {
		return ""
	}
	return e.Name
}

// EntityStatus reports an entity's status and whether it exists (the homepage
// setting only accepts published pages).
func (p pickers) EntityStatus(ctx context.Context, id uuid.UUID) (string, bool) {
	e, err := p.s.entityService.GetEntity(ctx, id)
	if err != nil {
		return "", false
	}
	return string(e.Status), true
}

func (p pickers) SearchTerms(ctx context.Context, q string, limit int) ([]admin.PickOption, error) {
	res, err := p.s.taxonomyService.ListTerms(ctx, "", q, 1, limit)
	if err != nil {
		return nil, err
	}
	out := make([]admin.PickOption, 0, len(res.Items))
	for _, t := range res.Items {
		path := t.Path
		if path == "" {
			path = t.Slug
		}
		out = append(out, admin.PickOption{Value: taxonomy.ArchivePrefix(t.Kind) + strings.TrimPrefix(path, "/"), Label: t.Name, Hint: t.Kind})
	}
	return out, nil
}

// formMailer forwards a stored submission by email (the submission screen's
// "Email this" action), the way the JSON endpoint does.
type formMailer struct{ s *Server }

func (m formMailer) Configured() bool { return m.s.emailService.IsConfigured() }

func (m formMailer) Forward(ctx context.Context, f *form.Form, sub *form.FormSubmission, to []string, by string) error {
	fields := make([]email.FormFieldData, 0, len(f.Fields))
	for _, ff := range f.Fields {
		if val, ok := sub.Data[ff.Name]; ok {
			fields = append(fields, email.FormFieldData{Label: ff.Label, Value: formatFieldValue(val)})
		}
	}
	return m.s.emailService.SendFormSubmissionForward(ctx, to, email.FormSubmissionData{
		FormName: f.Name, FormSlug: f.Slug, SubmissionID: sub.ID.String(), SubmittedAt: sub.CreatedAt, Fields: fields,
		SubmitterIP: sub.Metadata.IPAddress, SubmitterPage: m.s.redactPageURL(sub.Metadata.PageURL), SiteName: m.s.siteName(ctx),
	}, by)
}

// urlExplainer answers the redirects screen's "explain a URL": the redirect
// providers in order and, when none redirects, the page that serves the path.
type urlExplainer struct{ s *Server }

func (e urlExplainer) ExplainURL(ctx context.Context, path string) (admin.URLExplanation, error) {
	out := admin.URLExplanation{Redirects: e.s.buildRedirectChain().Resolve(ctx, path)}
	if out.Redirects.Found() {
		return out, nil
	}
	repo := entity.NewPostgresPublicRepository(e.s.db)
	if p := "/" + strings.Trim(path, "/"); p == "/" {
		home, err := repo.GetHomepage(ctx)
		if err == nil && home != nil {
			out.Page = admin.PageAnswer{Kind: resolveKindHome, Name: home.Name, Schema: schemaSlug(home), Path: "/", EntityID: home.ID.String()}
		}
		return out, nil
	}
	res, err := resolveEntity(ctx, repo, e.s.urlSnapshot(ctx), path)
	if err != nil || res == nil {
		return out, err
	}
	out.Page = admin.PageAnswer{Kind: res.Kind, Path: res.URL}
	switch {
	case res.Entity != nil:
		out.Page.Name, out.Page.Schema, out.Page.EntityID = res.Entity.Name, schemaSlug(res.Entity), res.Entity.ID.String()
	case res.Archive != nil:
		out.Page.Name, out.Page.Schema = res.Archive.Title, res.Archive.SchemaSlug
	}
	return out, nil
}

func schemaSlug(e *entity.Entity) string {
	if e.Schema != nil {
		return e.Schema.Slug
	}
	return ""
}

// agentStats joins the crawler stats and the robots policy stores for the
// dashboards.
type agentStats struct {
	*agents.StatsRepository
	*agents.RobotsRepository
}

// linkingAdmin is internal linking v2 for the admin screens. Reads and writes
// that cross entities run as superadmin like the JSON handlers do.
type linkingAdmin struct{ s *Server }

func (l linkingAdmin) ListRules(ctx context.Context, f *linkingv2.RuleFilter) (*linkingv2.PaginatedRulesResult, error) {
	return linkingv2.ListRulesPaginated(ctx, l.s.db, f)
}
func (l linkingAdmin) GetRule(ctx context.Context, id uuid.UUID) (*linkingv2.LinkRule, error) {
	return linkingv2.GetRule(ctx, l.s.db, id)
}
func (l linkingAdmin) CreateRule(ctx context.Context, in *linkingv2.CreateRuleInput) (*linkingv2.LinkRule, error) {
	return linkingv2.CreateRule(ctx, l.s.db, in)
}
func (l linkingAdmin) UpdateRule(ctx context.Context, id uuid.UUID, in *linkingv2.UpdateRuleInput) (*linkingv2.LinkRule, error) {
	return linkingv2.UpdateRule(ctx, l.s.db, id, in)
}
func (l linkingAdmin) DeleteRules(ctx context.Context, ids []uuid.UUID) (int, error) {
	return linkingv2.DeleteRulesByIDs(ctx, l.s.db, ids)
}
func (l linkingAdmin) DeleteRulesBySource(ctx context.Context, source string) (int, error) {
	return linkingv2.DeleteRulesBySource(ctx, l.s.db, source)
}
func (l linkingAdmin) DeleteAllRules(ctx context.Context) (int, error) {
	return linkingv2.DeleteAllRules(ctx, l.s.db)
}
func (l linkingAdmin) PreviewRule(ctx context.Context, phrase string, target uuid.UUID) (linkingv2.RulePreviewResult, error) {
	return linkingv2.PreviewRule(database.WithSuperadmin(ctx), l.s.db, phrase, target)
}
func (l linkingAdmin) Suggest(ctx context.Context, source uuid.UUID) ([]linkingv2.Suggestion, error) {
	return linkingv2.Suggest(database.WithSuperadmin(ctx), l.s.db, l.s.linkURLs(ctx), source, 0)
}
func (l linkingAdmin) Equity(ctx context.Context) (*linkingv2.EquityReport, error) {
	return linkingv2.ComputeEquity(database.WithSuperadmin(ctx), l.s.db)
}
func (l linkingAdmin) Inbound(ctx context.Context, target uuid.UUID) ([]linkingv2.EquityDetailRow, error) {
	return linkingv2.ListInboundLinks(database.WithSuperadmin(ctx), l.s.db, target)
}
func (l linkingAdmin) EntityName(ctx context.Context, id uuid.UUID) string {
	e, err := l.s.entityService.GetEntity(ctx, id)
	if err != nil {
		return ""
	}
	return e.Name
}

// linkTarget loads target and resolves its canonical address now (never
// trusted from a form). ok is false when its type cannot be linked to.
func (l linkingAdmin) linkTarget(ctx context.Context, target uuid.UUID) (*entity.Entity, string, bool, error) {
	tgt, err := l.s.entityService.GetEntity(ctx, target)
	if err != nil {
		return nil, "", false, err
	}
	if tgt.Schema == nil {
		return nil, "", false, errors.New("target entity schema missing")
	}
	snap := l.s.urlSnapshot(ctx)
	canonical, err := linkingv2.ResolveWithAttrs(snap, tgt.Schema.Slug, snap.VarsFor(tgt, ancestorSlugs(ctx, snap, entity.NewPostgresPublicRepository(l.s.db), tgt, tgt.Schema.Slug)), entity.FlattenAttributes(tgt.Attributes))
	if err != nil {
		if errors.Is(err, linkingv2.ErrSchemaNotLinkable) || errors.Is(err, linkingv2.ErrUnknownSchema) {
			return tgt, "", false, nil
		}
		return nil, "", false, err
	}
	return tgt, canonical, true, nil
}

// SuggestIn is Suggest against the editor's live content.
func (l linkingAdmin) SuggestIn(ctx context.Context, source uuid.UUID, html string) ([]linkingv2.Suggestion, error) {
	return linkingv2.SuggestWithContent(database.WithSuperadmin(ctx), l.s.db, l.s.linkURLs(ctx), source, 0, &html)
}

// WrapIn is the editor's insert: the same wrap as Insert, on the live html, stored nowhere.
func (l linkingAdmin) WrapIn(ctx context.Context, target uuid.UUID, anchor, html string) (string, admin.InsertOutcome, error) {
	tgt, canonical, ok, err := l.linkTarget(ctx, target)
	if err != nil {
		return "", admin.InsertOutcome{}, err
	}
	if !ok {
		return html, admin.InsertOutcome{SkipReason: "not_linkable"}, nil
	}
	res, err := linkingv2.WrapAll(html, []linkingv2.LinkSpec{{TargetEntityID: target, TargetSlug: tgt.Slug, TargetSchemaSlug: tgt.Schema.Slug, AnchorText: anchor, CanonicalURL: canonical}})
	if err != nil {
		return "", admin.InsertOutcome{}, err
	}
	if res.Inserted == 0 {
		skip := string(linkingv2.SkipNoMatch)
		for k := range res.Skips {
			skip = string(k)
			break
		}
		return html, admin.InsertOutcome{SkipReason: skip}, nil
	}
	return res.NewHTML, admin.InsertOutcome{Inserted: true}, nil
}

// Insert is the suggestion panel's insert: the target's canonical address is
// resolved now (never trusted from the form), then the first usable mention of
// the phrase in the source's saved content becomes the link.
func (l linkingAdmin) Insert(ctx context.Context, source, target uuid.UUID, anchor string) (admin.InsertOutcome, error) {
	tgt, canonical, ok, err := l.linkTarget(ctx, target)
	if err != nil {
		return admin.InsertOutcome{}, err
	}
	if !ok {
		return admin.InsertOutcome{SkipReason: "not_linkable"}, nil
	}
	res, err := linkingv2.Insert(database.WithSuperadmin(ctx), l.s.db, linkingv2.InsertInput{
		SourceEntityID: source, TargetEntityID: target, TargetSlug: tgt.Slug, TargetSchemaSlug: tgt.Schema.Slug, AnchorText: anchor, CanonicalURL: canonical,
	})
	if err != nil {
		return admin.InsertOutcome{}, err
	}
	if res.Inserted {
		l.s.invalidateRendered(ctx) // the entity's content changed outside the entity service
	}
	return admin.InsertOutcome{Inserted: res.Inserted, SkipReason: res.SkipReason}, nil
}

// pluginAdmin is the plugin runtime for the plugins page. It reads the runtime
// at call time: plugins are set up after the admin is mounted.
type pluginAdmin struct {
	s    *Server
	pkgs *packageAdmin // nil without packages
}

func (p pluginAdmin) Plugins() []admin.PluginInfo {
	var out []admin.PluginInfo
	for _, v := range p.s.pluginViews() {
		info := admin.PluginInfo{
			Name: v.Name, Version: v.Version, SDK: v.SDK, Description: v.Description, Error: v.Error, DataError: v.DataError, Loaded: v.Loaded,
			Hooks: v.Hooks, Permissions: v.Permissions, Granted: v.Granted, AllowedHosts: v.AllowedHosts, Cookies: v.Cookies, Funcs: v.Funcs,
			Refused: p.s.refusedPermissions(v.Name, v.Permissions),
		}
		for _, r := range v.Routes {
			line := r.Method + " " + r.Path
			if r.Public {
				line += " (public, no sign-in)"
			} else if len(r.Roles) > 0 {
				line += " (" + strings.Join(r.Roles, ", ") + ")"
			}
			info.Routes = append(info.Routes, line)
		}
		for _, c := range v.Cron {
			info.Cron = append(info.Cron, c.Name+" every "+c.Every)
		}
		if json.Valid(v.SettingsSchema) {
			info.SettingsSchema = v.SettingsSchema
		}
		out = append(out, info)
	}
	if p.pkgs != nil {
		out = p.pkgs.decoratePlugins(out)
	}
	return out
}

// refusedPermissions is the part of perms that policy never lets plugin hold.
func (s *Server) refusedPermissions(plugin string, perms []string) []string {
	if s.packages == nil || !s.packages.ScriptsRefused(plugin) {
		return nil
	}
	var out []string
	for _, perm := range perms {
		if perm == string(sdk.PermSiteScripts) || perm == string(sdk.PermBuilderIsland) {
			out = append(out, perm)
		}
	}
	return out
}

func (p pluginAdmin) Grant(ctx context.Context, name string, perms []string) error {
	if p.s.plugins == nil {
		return admin.ErrPluginNotFound
	}
	before := p.s.plugins.Grants().Granted(name)
	_, err := p.s.plugins.Grant(ctx, name, perms)
	if errors.Is(err, pluginhost.ErrPluginNotFound) && p.s.packages != nil {
		// A widget-only package has no wasm in the runtime: its grants
		// (builder.island) go through the package manager.
		err = p.s.packages.SetGrants(ctx, name, perms)
		switch {
		case errors.Is(err, packages.ErrPermNotRequested):
			err = pluginhost.ErrNotRequested
		case errors.Is(err, packages.ErrNotFound):
			err = pluginhost.ErrPluginNotFound
		}
	}
	if err == nil && p.s.packages != nil {
		p.s.packages.Audit(actorCtx(ctx), "package.grants", "plugin:"+name, map[string]any{"from": before, "to": perms})
		// Islands follow builder.island; reload the registries and drop the
		// pages rendered under the old grants (a builder.data source that was
		// empty without its grant, an island that was not served).
		p.s.packages.Reconcile(ctx)
	}
	if err == nil {
		p.s.invalidateRendered(ctx)
	}
	switch {
	case errors.Is(err, pluginhost.ErrCommunityScripts):
		p.s.logger.Warn("grant refused for a community package", "plugin", name, "error", err)
		return admin.ErrCommunityGrant
	case errors.Is(err, pluginhost.ErrNotRequested), errors.Is(err, pluginhost.ErrUnknownPermission):
		return admin.ErrBadGrant
	case errors.Is(err, pluginhost.ErrPluginNotFound):
		return admin.ErrPluginNotFound
	}
	return err
}

func (p pluginAdmin) schema(name string) []byte {
	for _, st := range p.s.pluginStatuses() {
		if st.Name == name {
			return st.Manifest.SettingsSchema
		}
	}
	return nil
}

func (p pluginAdmin) Settings(ctx context.Context, name string) (map[string]any, error) {
	schema := p.schema(name)
	if p.s.pluginSettingsStore == nil || len(schema) == 0 {
		return nil, errors.New("plugin has no settings")
	}
	stored, err := p.s.pluginSettingsStore.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(pluginSettingsView(schema, stored), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (p pluginAdmin) StoredSecrets(ctx context.Context, name string) (map[string]bool, error) {
	schema := p.schema(name)
	if p.s.pluginSettingsStore == nil || len(schema) == 0 {
		return nil, errors.New("plugin has no settings")
	}
	stored, err := p.s.pluginSettingsStore.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	return pluginhost.WriteOnlySet(schema, stored), nil
}

// SaveSettings validates against the plugin's schema and stores the result,
// keeping write-only values the form never shows (the JSON endpoint's rules).
func (p pluginAdmin) SaveSettings(ctx context.Context, name string, values map[string]any) (schemaform.Errors, error) {
	schema := p.schema(name)
	if p.s.pluginSettingsStore == nil || len(schema) == 0 {
		return nil, errors.New("plugin has no settings")
	}
	body, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	compiled, err := pluginhost.CompileSchema(schema)
	if err != nil {
		return nil, err
	}
	old, err := p.s.pluginSettingsStore.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	merged := pluginhost.PreserveWriteOnly(schema, old, body)
	if fields := pluginhost.Validate(compiled, merged); len(fields) > 0 {
		errs := schemaform.Errors{}
		for _, f := range fields {
			path := strings.ReplaceAll(strings.Trim(f.Path, "/"), "/", ".")
			errs[path] = append(errs[path], f.Message)
		}
		return errs, nil
	}
	if err := p.s.pluginSettingsStore.Put(ctx, name, merged); err != nil {
		return nil, err
	}
	p.s.invalidateRendered(ctx) // plugin settings feed template funcs, filters and sitemaps
	return nil, nil
}
