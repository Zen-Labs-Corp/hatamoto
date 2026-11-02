package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/seo"
	"hatamoto.dev/core/internal/seopanel"
	"hatamoto.dev/core/internal/seosvc"
)

// Top-level keys of the SEO document that are not seo.Settings sections.
const (
	seoTypeSettingsKey = "type_settings"
	seoAICrawlersKey   = "ai_crawlers"
)

// seoSections are the seo.Settings sections the API exposes, under their JSON
// names. A patch may name any of them plus the two keys above.
var seoSections = []string{
	"separator", "home", "types", "taxonomy", "archives", "variables", "social",
	"index", "robots", "verification", "sitemap", "breadcrumbs", "indexnow", "monitor404",
}

// seoBackend is the settings store and the one transaction that writes schema
// rows and settings together (seoAdmin).
type seoBackend interface {
	Get(ctx context.Context) (seo.Settings, error)
	saveWithSettings(ctx context.Context, patches []schemaPatch, fn func(*seo.Settings)) error
}

// seoSchemas lists the content types.
type seoSchemas interface {
	ListSchemas(ctx context.Context) ([]*entity.EntitySchema, error)
}

// seoService is the seosvc.Service the REST handler and the MCP tools call. It
// wraps the writers the admin tabs use, so the API gets the same validation,
// cache purge and audit.
type seoService struct {
	backend    seoBackend
	schemas    seoSchemas
	robots     agents.RobotsStore
	adminPath  func() string
	audit      func(context.Context, admin.AuditEvent)
	invalidate func(context.Context)
	// srv builds the page views behind Preview and Analyze; nil in unit tests
	// of Get and Update.
	srv *Server
}

var _ seosvc.Service = (*seoService)(nil)

// newSEOService is the service on the server's stores, nil when one is missing.
func (s *Server) newSEOService() *seoService {
	if s.seoStore == nil || s.entityService == nil || s.db == nil || s.robotsPolicy == nil {
		return nil
	}
	return &seoService{
		backend:    seoAdmin{db: s.db, store: s.seoStore, entities: s.entityService},
		schemas:    s.entityService,
		robots:     s.robotsPolicy,
		adminPath:  s.adminPathValue,
		audit:      s.adminAudit,
		invalidate: s.invalidateRendered,
		srv:        s,
	}
}

// seoTypeSettings is the per-type part of the document and of a patch: the
// stored schema settings the SEO tabs edit.
type seoTypeSettings struct {
	ArchiveTitle       *string         `json:"archive_title,omitempty"`
	ArchiveDescription *string         `json:"archive_description,omitempty"`
	SchemaOrgType      *string         `json:"schema_org_type,omitempty"`
	ArticleType        *string         `json:"article_type,omitempty"`
	FAQFromContent     *bool           `json:"faq_from_content,omitempty"`
	Sitemap            *seoTypeSitemap `json:"sitemap,omitempty"`
}

// seoTypeSitemap is the sitemap part of seoTypeSettings. Priority stays raw so
// a patch can tell null (remove) from absent (keep).
type seoTypeSitemap struct {
	Exclude    *bool           `json:"exclude,omitempty"`
	Priority   json.RawMessage `json:"priority,omitempty"`
	ChangeFreq *string         `json:"change_freq,omitempty"`
}

type seoAICrawlers struct {
	DeniedPurposes []string `json:"denied_purposes"`
}

// seoTypes lists the content types that have SEO settings: every type except
// the builder's blocks and theme parts, which are never public.
func (v *seoService) seoTypes(ctx context.Context) ([]*entity.EntitySchema, error) {
	all, err := v.schemas.ListSchemas(ctx)
	if err != nil {
		return nil, fmt.Errorf("list content types: %w", err)
	}
	out := make([]*entity.EntitySchema, 0, len(all))
	for _, sc := range all {
		if !entity.IsReuseSlug(sc.Slug) {
			out = append(out, sc)
		}
	}
	return out, nil
}

// Get implements seosvc.Service.
func (v *seoService) Get(ctx context.Context) (seosvc.Doc, error) {
	set, err := v.backend.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("load seo settings: %w", err)
	}
	types, err := v.seoTypes(ctx)
	if err != nil {
		return nil, err
	}
	denied, err := v.robots.DeniedPurposes(ctx)
	if err != nil {
		return nil, fmt.Errorf("load crawler policy: %w", err)
	}
	return seoDoc(set, types, denied)
}

// seoDoc renders the document. Sections keep their stored polarity (the JSON
// of seo.Settings); the IndexNow key is left out.
func seoDoc(set seo.Settings, types []*entity.EntitySchema, denied []agents.Purpose) (seosvc.Doc, error) {
	raw, err := json.Marshal(set)
	if err != nil {
		return nil, err
	}
	doc := seosvc.Doc{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	for _, name := range seoSections {
		if _, ok := doc[name]; !ok {
			doc[name] = map[string]any{}
		}
	}
	doc["indexnow"] = map[string]any{"enabled": set.IndexNow.Enabled, "endpoint": set.IndexNow.Endpoint}

	// The document has the shape of a patch, so Get then Update with an edited
	// copy round-trips. Every field is written, empty ones included.
	ts := make(map[string]any, len(types))
	for _, sc := range types {
		st, _ := sc.URLSettings()
		var priority any
		if st.Sitemap.Priority != nil {
			priority = *st.Sitemap.Priority
		}
		ts[sc.Slug] = map[string]any{
			"archive_title": st.ArchiveTitle, "archive_description": st.ArchiveDescription,
			"schema_org_type": st.SchemaOrgType, "article_type": st.ArticleType,
			"faq_from_content": st.FAQEnabled(),
			"sitemap":          map[string]any{"exclude": st.Sitemap.Exclude, "priority": priority, "change_freq": st.Sitemap.ChangeFreq},
		}
	}
	doc[seoTypeSettingsKey] = ts

	names := make([]string, 0, len(denied))
	for _, p := range denied {
		names = append(names, string(p))
	}
	sort.Strings(names)
	doc[seoAICrawlersKey] = seoAICrawlers{DeniedPurposes: names}
	return doc, nil
}

// seoFieldErrors collects refusals by field, first message wins.
type seoFieldErrors map[string]string

func (f seoFieldErrors) add(field, msg string) {
	if _, ok := f[field]; !ok {
		f[field] = msg
	}
}

// decodeStrict decodes raw into dst and refuses keys dst has no field for.
func decodeStrict(raw json.RawMessage, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the value")
	}
	return nil
}

// mergeMap merges a JSON object into m: a null value deletes the key, any other
// value is decoded over the current entry, so a patch of one field of an entry
// keeps its siblings.
func mergeMap[V any](m map[string]V, raw json.RawMessage) error {
	var p map[string]json.RawMessage
	if err := json.Unmarshal(raw, &p); err != nil || p == nil {
		return errors.New("must be an object")
	}
	for k, v := range p {
		if string(bytes.TrimSpace(v)) == "null" {
			delete(m, k)
			continue
		}
		cur := m[k]
		if err := decodeStrict(v, &cur); err != nil {
			return fmt.Errorf("%s: %v", k, err)
		}
		m[k] = cur
	}
	return nil
}

// splitMaps removes the named keys of the object raw and returns them, plus the
// object without them. A struct decode would replace a map, not merge it.
func splitMaps(raw json.RawMessage, keys ...string) (rest json.RawMessage, maps map[string]json.RawMessage, err error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, nil, errors.New("must be an object")
	}
	maps = map[string]json.RawMessage{}
	for _, k := range keys {
		if v, ok := obj[k]; ok {
			maps[k] = v
			delete(obj, k)
		}
	}
	rest, err = json.Marshal(obj)
	return rest, maps, err
}

// seoApplySection applies one section of a patch onto set. It never touches
// the IndexNow key.
func seoApplySection(set *seo.Settings, name string, raw json.RawMessage) error {
	switch name {
	case "separator":
		return decodeStrict(raw, &set.Separator)
	case "home":
		if err := decodeStrict(raw, &set.Home); err != nil {
			return err
		}
		set.Home = seo.TemplateOrDefault(set.Home)
	case "types":
		if err := mergeMap(set.Types, raw); err != nil {
			return err
		}
		for k, t := range set.Types {
			if t.Title == "" && t.Description == "" {
				delete(set.Types, k) // an empty entry stores nothing, like the admin
			}
		}
	case "taxonomy":
		return mergeMap(set.Taxonomy, raw)
	case "variables":
		return mergeMap(set.Variables, raw)
	case "archives":
		if err := decodeStrict(raw, &set.Archives); err != nil {
			return err
		}
		a := &set.Archives
		for _, p := range []*string{&a.Archive, &a.Author, &a.Date, &a.Search, &a.NotFound} {
			*p = seo.TemplateOrDefault(*p)
		}
	case "social":
		rest, maps, err := splitMaps(raw, "types", "taxonomy")
		if err != nil {
			return err
		}
		if err := decodeStrict(rest, &set.Social); err != nil {
			return err
		}
		if m, ok := maps["types"]; ok {
			if set.Social.Types == nil {
				set.Social.Types = map[string]seo.SocialType{}
			}
			if err := mergeMap(set.Social.Types, m); err != nil {
				return fmt.Errorf("types: %w", err)
			}
		}
		if m, ok := maps["taxonomy"]; ok {
			if set.Social.Taxonomy == nil {
				set.Social.Taxonomy = map[string]string{}
			}
			if err := mergeMap(set.Social.Taxonomy, m); err != nil {
				return fmt.Errorf("taxonomy: %w", err)
			}
		}
		if len(set.Social.Types) == 0 {
			set.Social.Types = nil
		}
		if len(set.Social.Taxonomy) == 0 {
			set.Social.Taxonomy = nil
		}
	case "index":
		rest, maps, err := splitMaps(raw, "noindex_types", "noindex_taxonomy")
		if err != nil {
			return err
		}
		if err := decodeStrict(rest, &set.Index); err != nil {
			return err
		}
		for key, dst := range map[string]*map[string]bool{"noindex_types": &set.Index.NoIndexTypes, "noindex_taxonomy": &set.Index.NoIndexTaxonomy} {
			m, ok := maps[key]
			if !ok {
				continue
			}
			if *dst == nil {
				*dst = map[string]bool{}
			}
			if err := mergeMap(*dst, m); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			for k, on := range *dst {
				if !on {
					delete(*dst, k) // only noindex entries are stored
				}
			}
		}
		seo.NormalizeIndex(&set.Index)
	case "robots":
		return decodeStrict(raw, &set.Robots)
	case "verification":
		return decodeStrict(raw, &set.Verification)
	case "sitemap":
		return decodeStrict(raw, &set.Sitemap)
	case "breadcrumbs":
		return decodeStrict(raw, &set.Breadcrumbs)
	case "monitor404":
		return decodeStrict(raw, &set.Monitor404)
	case "indexnow":
		var p struct {
			Enabled  *bool   `json:"enabled"`
			Endpoint *string `json:"endpoint"`
		}
		if err := decodeStrict(raw, &p); err != nil {
			if strings.Contains(err.Error(), `"key"`) {
				return errors.New("the key is generated by the site and cannot be set")
			}
			return err
		}
		if p.Enabled != nil {
			set.IndexNow.Enabled = *p.Enabled
		}
		if p.Endpoint != nil {
			set.IndexNow.Endpoint = strings.TrimSpace(*p.Endpoint)
		}
		if set.IndexNow.Enabled && set.IndexNow.Key == "" {
			set.IndexNow.Key = seo.NewIndexNowKey()
		}
	}
	return nil
}

// seoPlan is a validated patch, ready to write.
type seoPlan struct {
	sections  []string      // settings sections to apply, sorted
	schemas   []schemaPatch // per-type schema writes
	slugOf    map[uuid.UUID]string
	denied    []agents.Purpose // nil: leave the policy alone
	setDenied bool
	touched   []string // every top-level key of the patch, sorted, for the audit
}

// seoPlanPatch validates a patch against the stored state without writing
// anything: every refusal is reported before the first write.
func (v *seoService) seoPlanPatch(ctx context.Context, patch map[string]json.RawMessage) (*seoPlan, *seosvc.ValidationError, error) {
	cur, err := v.backend.Get(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("load seo settings: %w", err)
	}
	fe := seoFieldErrors{}
	plan := &seoPlan{slugOf: map[uuid.UUID]string{}}
	next := cur.Clone()
	for key := range patch {
		plan.touched = append(plan.touched, key)
	}
	sort.Strings(plan.touched)
	for _, key := range plan.touched {
		raw := patch[key]
		switch {
		case key == seoTypeSettingsKey:
			if err := v.planTypes(ctx, plan, fe, raw); err != nil {
				return nil, nil, err
			}
		case key == seoAICrawlersKey:
			planAICrawlers(plan, fe, raw)
		case slices.Contains(seoSections, key):
			if err := seoApplySection(&next, key, raw); err != nil {
				fe.add(key, err.Error())
				continue
			}
			plan.sections = append(plan.sections, key)
		default:
			fe.add(key, "unknown setting")
		}
	}
	if len(plan.sections) > 0 && len(fe) == 0 {
		for _, e := range next.ValidateWith(v.adminPath()) {
			fe.add(e.Field, e.Message)
		}
	}
	if len(fe) > 0 {
		return nil, &seosvc.ValidationError{Fields: fe}, nil
	}
	return plan, nil, nil
}

// planTypes resolves type_settings: slugs to schemas, values to schema patches.
func (v *seoService) planTypes(ctx context.Context, plan *seoPlan, fe seoFieldErrors, raw json.RawMessage) error {
	var byslug map[string]json.RawMessage
	if err := json.Unmarshal(raw, &byslug); err != nil || byslug == nil {
		fe.add(seoTypeSettingsKey, "must be an object keyed by content type")
		return nil
	}
	types, err := v.seoTypes(ctx)
	if err != nil {
		return err
	}
	slugs := make([]string, 0, len(byslug))
	for s := range byslug {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		field := seoTypeSettingsKey + "[" + slug + "]"
		i := slices.IndexFunc(types, func(sc *entity.EntitySchema) bool { return sc.Slug == slug })
		if i < 0 {
			fe.add(field, "unknown content type")
			continue
		}
		sc := types[i]
		var p seoTypeSettings
		if err := decodeStrict(byslug[slug], &p); err != nil {
			fe.add(field, err.Error())
			continue
		}
		patch, ok := seoSchemaPatch(sc, p, field, fe)
		if !ok {
			continue
		}
		plan.schemas = append(plan.schemas, schemaPatch{id: sc.ID, patch: patch})
		plan.slugOf[sc.ID] = slug
	}
	return nil
}

// seoSchemaPatch turns one type's patch into the entity patch. A sitemap patch
// replaces the three keys the entity layer owns, so the current values are
// read first and only the named ones change. ok is false when a field was
// refused. Value rules (allowed schema types, lengths, priority range) are the
// entity layer's, enforced in the same transaction before anything commits.
func seoSchemaPatch(sc *entity.EntitySchema, p seoTypeSettings, field string, fe seoFieldErrors) (entity.SchemaSEOPatch, bool) {
	ok := true
	out := entity.SchemaSEOPatch{
		ArchiveTitle: p.ArchiveTitle, ArchiveDescription: p.ArchiveDescription,
		SchemaOrgType: p.SchemaOrgType, ArticleType: p.ArticleType, FAQFromContent: p.FAQFromContent,
	}
	// A value the type cannot take is refused only when it differs from the
	// stored one: seo_get writes every field, so sending it back unchanged is
	// a no-op.
	cur, _ := sc.URLSettings()
	if sc.IsBuiltin && sc.Slug != entity.PersonSchemaSlug {
		if p.ArchiveTitle != nil && *p.ArchiveTitle != cur.ArchiveTitle {
			fe.add(field+"[archive_title]", "The archive text of a built-in type cannot be changed.")
			ok = false
		}
		if p.ArchiveDescription != nil && *p.ArchiveDescription != cur.ArchiveDescription {
			fe.add(field+"[archive_description]", "The archive text of a built-in type cannot be changed.")
			ok = false
		}
		out.ArchiveTitle, out.ArchiveDescription = nil, nil
	}
	if sc.Slug == entity.PersonSchemaSlug || entity.SchemaFixesJSONLDType(sc) {
		if p.SchemaOrgType != nil && *p.SchemaOrgType != cur.SchemaOrgType {
			fe.add(field+"[schema_org_type]", "This type's schema type is fixed by the type itself.")
			ok = false
		}
		out.SchemaOrgType = nil
	}
	if p.Sitemap != nil {
		st, _ := sc.URLSettings()
		sp := entity.SchemaSitemapPatch{Exclude: st.Sitemap.Exclude, Priority: st.Sitemap.Priority, ChangeFreq: st.Sitemap.ChangeFreq}
		if p.Sitemap.Exclude != nil {
			sp.Exclude = *p.Sitemap.Exclude
		}
		if p.Sitemap.ChangeFreq != nil {
			sp.ChangeFreq = *p.Sitemap.ChangeFreq
		}
		if raw := bytes.TrimSpace(p.Sitemap.Priority); len(raw) > 0 {
			sp.Priority = nil
			if string(raw) != "null" {
				var f float64
				if err := json.Unmarshal(raw, &f); err != nil {
					fe.add(field+"[sitemap][priority]", "must be a number from 0 to 1, or null")
					ok = false
				} else {
					sp.Priority = &f
				}
			}
		}
		out.Sitemap = &sp
	}
	return out, ok
}

// planAICrawlers validates the crawler policy: the full list of denied purposes.
func planAICrawlers(plan *seoPlan, fe seoFieldErrors, raw json.RawMessage) {
	var p seoAICrawlers
	if err := decodeStrict(raw, &p); err != nil {
		fe.add(seoAICrawlersKey, err.Error())
		return
	}
	seen := map[agents.Purpose]bool{}
	denied := []agents.Purpose{}
	for _, name := range p.DeniedPurposes {
		purpose, ok := agents.ParsePurpose(name)
		if !ok {
			fe.add(seoAICrawlersKey+"[denied_purposes]", fmt.Sprintf("Unknown purpose %q; use one of %s.", name, strings.Join(agents.PurposeNames(), ", ")))
			return
		}
		if !seen[purpose] {
			seen[purpose] = true
			denied = append(denied, purpose)
		}
	}
	plan.denied, plan.setDenied = denied, true
}

// Update implements seosvc.Service. Every check runs before the first write;
// the settings and the per-type rows are then written in one transaction, and
// the crawler policy (a separate store) after it commits.
func (v *seoService) Update(ctx context.Context, actor seosvc.Actor, patch map[string]json.RawMessage) (seosvc.Doc, error) {
	plan, verr, err := v.seoPlanPatch(ctx, patch)
	if err != nil {
		return nil, err
	}
	if verr != nil {
		return nil, verr
	}
	if len(plan.touched) == 0 {
		return v.Get(ctx)
	}
	apply := func(set *seo.Settings) {
		for _, name := range plan.sections {
			// Validated against the stored copy above; the fresh row differs
			// only if someone saved in between, and Merge validates it again.
			_ = seoApplySection(set, name, patch[name])
		}
	}
	if len(plan.sections) > 0 || len(plan.schemas) > 0 {
		if err := v.backend.saveWithSettings(ctx, plan.schemas, apply); err != nil {
			if verr := seoWriteError(err, plan.slugOf); verr != nil {
				return nil, verr
			}
			return nil, fmt.Errorf("save seo settings: %w", err)
		}
	}
	if plan.setDenied {
		if err := v.robots.SetDenied(ctx, plan.denied); err != nil {
			v.invalidate(ctx)
			// The settings are committed, so they are audited even though
			// the policy write failed.
			saved := slices.DeleteFunc(slices.Clone(plan.touched), func(n string) bool { return n == seoAICrawlersKey })
			if len(saved) > 0 {
				v.auditSaved(ctx, actor, saved)
			}
			return nil, fmt.Errorf("save crawler policy (the other settings were saved): %w", err)
		}
	}
	v.invalidate(ctx)
	v.auditSaved(ctx, actor, plan.touched)
	return v.Get(ctx)
}

// auditSaved records a write of the named sections.
func (v *seoService) auditSaved(ctx context.Context, actor seosvc.Actor, sections []string) {
	v.audit(ctx, admin.AuditEvent{
		ActorID: actor.UserID, Actor: actor.Label(), Action: "seo.settings", Target: "seo",
		Detail: seoAuditDetail(actor, sections),
	})
}

// seoAuditDetail names who changed which sections. Values stay out: a section
// may hold the 404 ignore patterns, and the IndexNow key is never written here.
func seoAuditDetail(actor seosvc.Actor, sections []string) map[string]any {
	d := map[string]any{"via": actor.Via, "sections": sections}
	if actor.KeyName != "" {
		d["key"] = actor.KeyName
	}
	return d
}

// seoWriteError maps a refused write to a validation error; nil for any other.
func seoWriteError(err error, slugOf map[uuid.UUID]string) *seosvc.ValidationError {
	var ve *seo.ValidationError
	if errors.As(err, &ve) {
		fe := seoFieldErrors{}
		for _, e := range ve.Errors {
			fe.add(e.Field, e.Message)
		}
		return &seosvc.ValidationError{Fields: fe}
	}
	var pe *admin.SEOPatchError
	if !errors.As(err, &pe) {
		return nil
	}
	field := seoTypeSettingsKey + "[" + slugOf[pe.ID] + "]"
	var se *entity.SettingsError
	switch {
	case errors.As(pe.Err, &se):
		if se.Field != "" {
			field += "[" + se.Field + "]"
		}
		return &seosvc.ValidationError{Fields: map[string]string{field: se.Msg}}
	case errors.Is(pe.Err, entity.ErrCannotModifyBuiltin):
		return &seosvc.ValidationError{Fields: map[string]string{field: "The archive text of a built-in type cannot be changed."}}
	}
	return nil
}

// internalRequest is a GET for path on the site's public host, for the
// functions that need a request to resolve a page without serving one.
func (s *Server) internalRequest(ctx context.Context, path string) (*http.Request, error) {
	host := "localhost"
	if s.cfg != nil {
		if u, err := url.Parse(strings.TrimSpace(s.cfg.PublicSite.BaseURL)); err == nil && u.Host != "" {
			host = u.Host
		}
	}
	return http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host+path, nil)
}

// seoEntity loads an entity with its schema.
func (s *Server) seoEntity(ctx context.Context, id uuid.UUID) (*entity.Entity, error) {
	e, err := s.entityService.GetEntity(ctx, id)
	if err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			return nil, seosvc.ErrNotFound
		}
		return nil, err
	}
	if e.Schema == nil {
		if e.Schema, err = s.entityService.GetSchema(ctx, e.SchemaID); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// Preview implements seosvc.Service: the page is resolved and its view built
// as for a visitor, then render.SEOPreview reads the head values from it.
func (v *seoService) Preview(ctx context.Context, id uuid.UUID) (seosvc.Preview, error) {
	s := v.srv
	e, err := s.seoEntity(ctx, id)
	if err != nil {
		return seosvc.Preview{}, err
	}
	path := s.entityPublicPath(ctx, e)
	home := s.isHomepage(ctx, e.ID)
	if home {
		path = "/" // the homepage is served there, not at its own slug
	}
	req, err := s.internalRequest(ctx, path)
	if err != nil {
		return seosvc.Preview{}, err
	}
	var rr *routeResult
	if home {
		rr, _, err = homeRoute(s.loadHomepage(withoutAccept(req)))
	} else {
		rr, _, err = s.resolveRouteForRender(ctx, path, req)
	}
	if err != nil {
		return seosvc.Preview{}, err
	}
	if rr == nil || rr.Page == nil || rr.Page.Entity == nil || rr.Page.Entity.ID != e.ID {
		return seosvc.Preview{}, fmt.Errorf("%w: %s has no public page (not published?)", seosvc.ErrNotFound, e.Slug)
	}
	view := pageViewFromRoute(rr, s.publicBaseURL(req), path)
	site, err := s.buildSiteView(ctx)
	if err != nil {
		return seosvc.Preview{}, err
	}
	view.Site = site
	view.SEO = s.seoSettings(ctx)
	if err := s.fillView(ctx, req, rr, view); err != nil {
		return seosvc.Preview{}, err
	}
	s.applyLocale(ctx, rr, view, view.BaseURL)
	return render.SEOPreview(ctx, view), nil
}

// Analyze implements seosvc.Service: the entity as stored, through the same
// seopanel.Build the admin panel runs.
func (v *seoService) Analyze(ctx context.Context, id uuid.UUID) (seosvc.Panel, error) {
	s := v.srv
	e, err := s.seoEntity(ctx, id)
	if err != nil {
		return seosvc.Panel{}, err
	}
	attrs := map[string]any{}
	if len(e.Attributes) > 0 {
		_ = json.Unmarshal(e.Attributes, &attrs)
	}
	f := seopanel.FromAttrs(attrs)
	excerpt, _ := attrs["excerpt"].(string)
	in := seopanel.Input{
		Title: e.Name, MetaTitle: f.Title, MetaDescription: f.Description, Slug: e.Slug, Excerpt: excerpt,
		Canonical: f.Canonical, Published: e.Status == entity.StatusPublished, Mode: seopanel.ModeMetaOnly,
		NoKeyword: true, NoLLMs: true,
		OGTitle: f.OGTitle, OGDescription: f.OGDescription, OGImage: f.OGImage, Social: true,
		SchemaType: f.SchemaType, CustomJSONLD: f.CustomJSONLD,
	}
	if prop := seopanel.BodyProperty(e.Schema); prop != "" {
		in.Mode = seopanel.ModeBody
		in.Content, _ = attrs[prop].(string)
	}
	in.Path = s.entityPublicPath(ctx, e)
	if in.Path == "" {
		in.Path = "/" + e.Slug
	}
	req, err := s.internalRequest(ctx, in.Path)
	if err != nil {
		return seosvc.Panel{}, err
	}
	in.BaseURL = s.publicBaseURL(req)
	if set, err := v.backend.Get(ctx); err == nil {
		in.SEO, in.SchemaSlug = &set, e.Schema.Slug
		if cfg, err := s.siteConfig.Get(ctx); err == nil && cfg != nil {
			in.SiteName, in.Tagline = cfg.CompanyName, cfg.Tagline
		}
	}
	in.Home = s.isHomepage(ctx, e.ID)
	return seopanel.Build(in), nil
}

// isHomepage is whether id is the entry the site serves at "/": the chosen
// one, or while none is chosen the page the public site falls back to.
func (s *Server) isHomepage(ctx context.Context, id uuid.UUID) bool {
	if cfg, err := s.siteConfig.Get(ctx); err == nil && cfg != nil && cfg.HomepageEntityID != uuid.Nil {
		return cfg.HomepageEntityID == id
	}
	req, err := s.internalRequest(ctx, "/")
	if err != nil {
		return false
	}
	rr, _, err := homeRoute(s.loadHomepage(req))
	return err == nil && rr != nil && rr.Page != nil && rr.Page.Entity != nil && rr.Page.Entity.ID == id
}
