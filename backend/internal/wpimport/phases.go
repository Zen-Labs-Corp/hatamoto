package wpimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/author"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/taxonomy"
)

// importSettings copies the title, tagline, language and time zone.
func (r *Runner) importSettings(ctx context.Context) error {
	s := r.dump.Site
	if r.state.Mode == ModeExisting {
		return r.reportSettingDiffs(ctx)
	}
	// A database that never booted has no site row to update.
	if _, err := r.d.DB.Pool().Exec(ctx, `INSERT INTO hatamoto_site_config (company_name) VALUES ($1) ON CONFLICT (id) DO NOTHING`, firstNonEmpty(s.Title, "Site")); err != nil {
		return fmt.Errorf("site config row: %w", err)
	}
	in := siteconfig.UpdateInput{}
	if s.Title != "" {
		in.CompanyName = &s.Title
	}
	if s.Tagline != "" {
		in.Tagline = &s.Tagline
	}
	if s.Timezone != "" {
		in.Timezone = &s.Timezone
	}
	if lang, err := entity.NormalizeLocale(s.Language); err == nil {
		in.Language = &lang
	}
	if _, err := r.d.Site.Update(ctx, in); err != nil {
		// A site setting must not stop the content import.
		r.res.Warnings = append(r.res.Warnings, "site settings not fully imported: "+err.Error())
	}
	return nil
}

// reportSettingDiffs leaves the settings of a site that has content alone and
// lists where WordPress differs.
func (r *Runner) reportSettingDiffs(ctx context.Context) error {
	cur, err := r.d.Site.Get(ctx)
	if err != nil {
		return nil // no site row yet: nothing to differ from
	}
	s := r.dump.Site
	for _, d := range []struct{ name, wp, have string }{
		{"site title", s.Title, cur.CompanyName}, {"tagline", s.Tagline, cur.Tagline},
		{"time zone", s.Timezone, cur.Timezone}, {"language", s.Language, cur.Language},
	} {
		if d.wp != "" && d.wp != d.have {
			r.res.Warnings = append(r.res.Warnings, fmt.Sprintf("site settings kept: %s is %q here, %q in WordPress", d.name, d.have, d.wp))
		}
	}
	return nil
}

// settingsPatch merges keys into a schema's settings object.
func settingsPatch(cur json.RawMessage, set map[string]any) (json.RawMessage, error) {
	m := map[string]any{}
	if len(cur) > 0 {
		if err := json.Unmarshal(cur, &m); err != nil {
			return nil, err
		}
	}
	for k, v := range set {
		m[k] = v
	}
	return json.Marshal(m)
}

// prepareSchemas sets the URL patterns of the post and page schemas to the
// WordPress permalink structure (an empty target only), and creates a schema
// for each custom post type. A target with content of its own keeps every
// existing pattern: imported items take them, and the redirect phase maps the
// old WordPress URLs.
func (r *Runner) prepareSchemas(ctx context.Context) error {
	patch := func(slug string, set map[string]any) error {
		sc, err := r.schema(ctx, slug)
		if err != nil {
			return fmt.Errorf("schema %q: %w", slug, err)
		}
		// Each key is checked on its own with the rule of the schema service:
		// the values come from the remote site, and one bad value must not
		// take the others (or, at load time, the whole schema) down with it.
		good := map[string]any{}
		for _, k := range sortedKeys(set) {
			cand := map[string]any{k: set[k]}
			for gk, gv := range good {
				cand[gk] = gv
			}
			raw, err := settingsPatch(sc.Settings, cand)
			if err == nil {
				err = entity.ValidateSchemaSettingsForWrite(raw)
			}
			if err != nil {
				r.res.Warnings = append(r.res.Warnings, fmt.Sprintf("schema %q: setting %s=%v from WordPress not applied: %v", slug, k, set[k], err))
				continue
			}
			good = cand
		}
		if len(good) == 0 {
			return nil
		}
		raw, err := settingsPatch(sc.Settings, good)
		if err != nil {
			return err
		}
		// Built-in schemas cannot be edited through entity.Service (it refuses
		// them); their URL settings are exactly what a migration sets, so the
		// repository is written after the same validation the service applies.
		if _, err := r.d.SchemaRepo.Update(ctx, sc.ID, &entity.UpdateSchemaInput{Settings: &raw}); err != nil {
			return err
		}
		delete(r.schemas, slug)
		return nil
	}
	r.schemaOf["post"], r.schemaOf["page"] = "post", "page"
	switch {
	case r.opts.NoPermalinks:
	case r.state.Mode == ModeExisting:
		r.res.Warnings = append(r.res.Warnings, "the target site has content: its URL patterns were not changed; imported posts and pages take the existing patterns and old WordPress URLs are redirected")
	default:
		set := map[string]any{}
		if p, ok := PatternFromPermalink(r.dump.Site.Permalink); ok {
			set["url_pattern"] = p
		}
		if pp := r.postsPage(); pp != nil {
			if p := normPath(pathOf(pp.Link)); p != "/" {
				set["archive_path"] = p
				if pp.Title != "" {
					set["archive_title"] = truncateRunes(pp.Title, entity.MaxArchiveTitleLen)
				}
			}
		}
		if len(set) > 0 {
			if err := patch("post", set); err != nil {
				return err
			}
		}
		if err := patch("page", map[string]any{"url_pattern": "/{path}"}); err != nil {
			return err
		}
	}
	for _, ct := range r.dump.Detect.CustomTypes {
		slug, err := r.customSchema(ctx, ct)
		if err != nil {
			r.res.Warnings = append(r.res.Warnings, fmt.Sprintf("custom post type %q not imported: %v", ct.Slug, err))
			continue
		}
		r.schemaOf[ct.Slug] = slug
	}
	r.d.Entities.URLRegistry().Invalidate()
	return nil
}

func truncateRunes(s string, n int) string {
	if r := []rune(strings.TrimSpace(s)); len(r) > n {
		return string(r[:n])
	}
	return strings.TrimSpace(s)
}

func (r *Runner) postsPage() *Item {
	if r.dump.Site.PostsPageID == 0 {
		return nil
	}
	id := fmt.Sprint(r.dump.Site.PostsPageID)
	for i := range r.dump.Items {
		if r.dump.Items[i].ID == id {
			return &r.dump.Items[i]
		}
	}
	return nil
}

var cptSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// customSchema creates (or finds) the schema of a custom post type and aims
// its URL pattern at the type's WordPress URLs.
func (r *Runner) customSchema(ctx context.Context, ct CustomType) (string, error) {
	slug := strings.Trim(cptSlugRe.ReplaceAllString(strings.ToLower(ct.Slug), "-"), "-")
	if slug == "" {
		return "", errors.New("no usable slug")
	}
	if slug == "post" || slug == "page" {
		slug += "-type"
	}
	settings := map[string]any{}
	var sample *Item
	for i := range r.dump.Items {
		if it := &r.dump.Items[i]; it.Type == ct.Slug && it.Status == wpPublish && it.Link != "" {
			sample = it
			break
		}
	}
	if sample != nil {
		old := normPath(pathOf(sample.Link))
		if prefix, ok := strings.CutSuffix(old, "/"+sample.Slug); ok && sample.Slug != "" && sample.ParentID == "" {
			if ct.Hierarchical {
				settings["url_pattern"] = prefix + "/{path}"
			} else {
				settings["url_pattern"] = prefix + "/{slug}"
			}
			if prefix != "" && ct.HasArchive {
				settings["archive_path"] = prefix
			}
		} else if ct.Hierarchical {
			settings["url_pattern"] = "/" + slug + "/{path}"
		}
	}
	if _, ok := settings["url_pattern"]; !ok {
		settings["url_pattern"] = "/" + slug + "/{slug}"
		if ct.HasArchive {
			settings["archive_path"] = "/" + slug
		}
	}
	rawSettings, _ := json.Marshal(settings)
	if existing, err := r.d.Entities.GetSchemaBySlug(ctx, slug); err == nil {
		if existing.IsBuiltin || r.state.Mode == ModeExisting {
			return slug, nil // never change the patterns of a schema that exists on a site with content
		}
		merged, err := settingsPatch(existing.Settings, settings)
		if err != nil {
			return "", err
		}
		if _, err := r.d.Entities.UpdateSchema(ctx, existing.ID, &entity.UpdateSchemaInput{Settings: &merged}); err != nil {
			return "", err
		}
		return slug, nil
	} else if !isBuiltinGone(err) {
		return "", err
	}
	name := ct.Name
	if name == "" {
		name = slug
	}
	body := `{"type":"object","properties":{
		"content":{"type":"string","title":"Content"},
		"excerpt":{"type":"string","title":"Excerpt"},
		"featured_image":{"type":"string","title":"Featured Image"},
		"meta_title":{"type":"string","title":"SEO title"},
		"meta_description":{"type":"string","title":"SEO description"},
		"og_image":{"type":"string","title":"Social image"},
		"no_index":{"type":"boolean","title":"Hide from search engines"}}}`
	desc := "Imported from WordPress"
	if _, err := r.d.Entities.CreateSchema(ctx, &entity.CreateSchemaInput{Name: name, Slug: slug, Description: &desc, Schema: json.RawMessage(body), Settings: rawSettings}); err != nil {
		return "", err
	}
	return slug, nil
}

// importAuthors creates a person for every WordPress author, keeping the WP
// login as the slug. A person that already has the slug (an earlier run, or
// one the owner made) is used as is, never overwritten. New people are not
// public: the owner opts them in. Their old author URL is only redirected
// once the person has a page.
func (r *Runner) importAuthors(ctx context.Context) error {
	for _, a := range r.dump.Authors {
		slug := strings.ToLower(a.Slug)
		if !author.ValidSlug(slug) {
			slug = author.Slugify(a.Name)
		}
		p, err := r.d.People.GetBySlug(ctx, slug)
		switch {
		case err == nil:
		case errors.Is(err, person.ErrNotFound):
			p, err = r.createPerson(ctx, a, slug)
		}
		if err != nil {
			r.res.Errors = append(r.res.Errors, ItemError{Kind: "author", ID: a.Key, Title: a.Name, Err: err.Error()})
			continue
		}
		r.authorIDs[a.Key] = p.ID
		r.res.Authors++
		if a.Link != "" && p.Public {
			if ent, err := r.d.Entities.GetEntity(ctx, p.ID); err == nil {
				if snap, err := r.d.Entities.URLRegistry().Snapshot(ctx); err == nil {
					r.termPaths[normPath(pathOf(a.Link))] = snap.EntityPath(ent, nil)
				}
			}
		} else if a.Link != "" {
			// Kept 404 on purpose until the owner makes the person public.
			r.hiddenAuthors[normPath(pathOf(a.Link))] = p.Name
		}
	}
	return nil
}

// createPerson imports one author as a non-public person keyed by the WP
// login, so a re-run updates the same entity.
func (r *Runner) createPerson(ctx context.Context, a Author, slug string) (*person.Person, error) {
	attrs := map[string]any{"public": false}
	if bio := strings.TrimSpace(a.Bio); bio != "" {
		attrs["bio"] = "<p>" + html.EscapeString(bio) + "</p>"
	}
	if a.AvatarURL != "" {
		if m := r.mediaFor(ctx, a.AvatarURL); m != nil && m.PublicURL != nil {
			attrs["photo"] = *m.PublicURL
		}
	}
	raw, err := json.Marshal(attrs)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(a.Name)
	if name == "" {
		name = slug
	}
	for try := 1; ; try++ {
		s := slug
		if try > 1 {
			s = fmt.Sprintf("%s-%d", slug, try)
		}
		res, err := r.d.Entities.Import(ctx, r.opts.Actor, entity.ImportBatch{Source: SourceName, Items: []entity.ImportItem{{
			Key: r.key("author:" + a.Key), SchemaSlug: person.SchemaSlug, Name: name, Slug: s,
			Status: entity.StatusDraft, Attributes: raw,
		}}})
		if err != nil {
			return nil, err
		}
		ir := res.Items[0]
		if errors.Is(ir.Err, entity.ErrSlugAlreadyExists) && try < maxSlugTries {
			continue
		}
		if ir.Err != nil {
			return nil, ir.Err
		}
		return r.d.People.Get(ctx, ir.ID)
	}
}

// mediaFor returns the imported media for an old-site URL.
func (r *Runner) mediaFor(ctx context.Context, raw string) *media.Media {
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	if _, ok := r.resolveMedia(ctx)(u); !ok {
		return nil
	}
	if m, ok := r.media.get(mediaKey(u)); ok {
		return m
	}
	return nil
}

func termKind(tax string) string {
	switch tax {
	case "post_tag":
		return "tag"
	}
	return strings.ToLower(tax)
}

var nonSlug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func termSlug(t Term) string {
	slug := t.Slug
	if un, err := url.PathUnescape(slug); err == nil {
		slug = un
	}
	slug = strings.ToLower(slug)
	if nonSlug.MatchString(slug) {
		return slug
	}
	if s := taxonomy.Slugify(t.Name); s != "" {
		return s
	}
	return author.Slugify(t.Name) // hash form for names without ASCII letters
}

// importTerms creates categories, tags and custom terms, parents first.
func (r *Runner) importTerms(ctx context.Context) error {
	pending := append([]Term(nil), r.dump.Terms...)
	done := map[string]bool{}
	key := func(t Term) string { return t.Taxonomy + ":" + t.Slug }
	for len(pending) > 0 {
		var next []Term
		progressed := false
		for _, t := range pending {
			if t.ParentSlug != "" && !done[t.Taxonomy+":"+t.ParentSlug] {
				next = append(next, t)
				continue
			}
			progressed = true
			done[key(t)] = true
			if !taxonomy.ValidKind(termKind(t.Taxonomy)) {
				r.res.Warnings = append(r.res.Warnings, fmt.Sprintf("taxonomy %q cannot be a Hatamoto term kind: its terms are not imported", t.Taxonomy))
				continue
			}
			if err := r.importTerm(ctx, t, key(t)); err != nil {
				r.res.Errors = append(r.res.Errors, ItemError{Kind: "term", ID: key(t), Title: t.Name, Err: err.Error()})
			}
		}
		if !progressed { // a parent that does not exist: import the rest as roots
			for i := range next {
				next[i].ParentSlug = ""
			}
		}
		pending = next
	}
	return nil
}

func (r *Runner) importTerm(ctx context.Context, t Term, k string) error {
	kind := termKind(t.Taxonomy)
	var parent *uuid.UUID
	if t.ParentSlug != "" {
		if id, ok := r.termIDs[t.Taxonomy+":"+t.ParentSlug]; ok {
			parent = &id
		}
	}
	slug := termSlug(t)
	id, ok, err := r.st.Lookup(ctx, KindTerm, k)
	if err != nil {
		return err
	}
	var term *taxonomy.Term
	if ok {
		desc := t.Description
		in := taxonomy.UpdateTermInput{ID: id, Name: t.Name, Slug: slug, Description: &desc, ParentID: parent, ClearParent: parent == nil}
		term, err = r.d.Taxonomy.UpdateTerm(ctx, in)
	} else {
		term, err = r.d.Taxonomy.CreateTerm(ctx, taxonomy.CreateTermInput{Kind: kind, Name: t.Name, Slug: slug, Description: t.Description, ParentID: parent})
		if errors.Is(err, taxonomy.ErrTermSlugConflict) {
			// A term of that name exists already (a re-run without the map): adopt it.
			if existing, gerr := r.d.Taxonomy.GetTermBySlug(ctx, kind, slug); gerr == nil {
				if r.state.Mode == ModeExisting {
					term, err = existing, nil // the site's own term: used as is
				} else {
					desc := t.Description
					term, err = r.d.Taxonomy.UpdateTerm(ctx, taxonomy.UpdateTermInput{ID: existing.ID, Name: t.Name, Slug: slug, Description: &desc, ParentID: parent, ClearParent: parent == nil})
				}
			}
		}
	}
	if err != nil {
		return err
	}
	r.termIDs[k] = term.ID
	r.res.Terms++
	if err := r.st.Put(ctx, r.run.ID, KindTerm, k, term.ID, ""); err != nil {
		return err
	}
	if t.Link != "" {
		if err := taxonomy.FillPaths(ctx, r.d.TermRepo, term); err == nil {
			r.termPaths[normPath(pathOf(t.Link))] = taxonomy.ArchivePrefix(term.Kind) + term.Path
		}
	}
	return nil
}

// importMenus replaces each menu's items with the WordPress menu.
func (r *Runner) importMenus(ctx context.Context) error {
	for _, m := range r.dump.Menus {
		if err := r.importMenu(ctx, m); err != nil {
			r.res.Errors = append(r.res.Errors, ItemError{Kind: "menu", ID: m.ID, Title: m.Name, Err: err.Error()})
			continue
		}
		r.res.Menus++
	}
	return nil
}

func menuLocation(m Menu) string {
	probe := strings.ToLower(m.Location + " " + m.Slug + " " + m.Name)
	switch {
	case strings.Contains(probe, "footer"):
		return menu.LocationFooter
	case strings.Contains(probe, "sidebar"):
		return menu.LocationSidebar
	case strings.Contains(probe, "primary"), strings.Contains(probe, "main"), strings.Contains(probe, "header"), strings.Contains(probe, "top"):
		return menu.LocationHeader
	}
	return ""
}

func (r *Runner) importMenu(ctx context.Context, m Menu) error {
	id, ok, err := r.st.Lookup(ctx, KindMenu, m.ID)
	if err != nil {
		return err
	}
	if !ok {
		slug := strings.Trim(cptSlugRe.ReplaceAllString(strings.ToLower(m.Slug), "-"), "-")
		if slug == "" {
			slug = "menu-" + m.ID
		}
		var mm *menu.Menu
		for _, s := range []string{slug, "wp-" + slug} {
			if mm, err = r.d.Menus.CreateMenu(ctx, &menu.CreateMenuInput{Name: m.Name, Slug: s}); err == nil {
				break
			}
		}
		if err != nil {
			return err
		}
		id = mm.ID
		if err := r.st.Put(ctx, r.run.ID, KindMenu, m.ID, id, ""); err != nil {
			return err
		}
	}
	items, err := r.menuItems(ctx, m)
	if err != nil {
		return err
	}
	name := m.Name
	fields := &menu.UpdateMenuInput{Name: &name}
	if loc := menuLocation(m); loc != "" {
		fields.Location = &loc
	}
	if _, err := r.d.Menus.ReplaceMenu(ctx, id, fields, items); err != nil && fields.Location != nil {
		// The location may belong to another menu already: keep the items without it.
		r.res.Warnings = append(r.res.Warnings, fmt.Sprintf("menu %q: location %q not set: %v", m.Name, *fields.Location, err))
		fields.Location = nil
		_, err = r.d.Menus.ReplaceMenu(ctx, id, fields, items)
		return err
	} else if err != nil {
		return err
	}
	return nil
}

// menuItems converts the WordPress items, each parent before its children.
func (r *Runner) menuItems(ctx context.Context, m Menu) ([]menu.ReplaceItem, error) {
	byID := map[string]MenuItem{}
	for _, it := range m.Items {
		byID[it.ID] = it
	}
	index := map[string]int{}
	var out []menu.ReplaceItem
	dropped := map[string]bool{} // items left out for an unsafe URL (already reported)
	var add func(it MenuItem, depth int) bool
	add = func(it MenuItem, depth int) bool {
		if _, done := index[it.ID]; done {
			return true
		}
		parent := -1
		if it.ParentID != "" {
			p, ok := byID[it.ParentID]
			if !ok || depth > 8 {
				return false
			}
			if !add(p, depth+1) {
				return false
			}
			parent = index[p.ID]
		}
		ri, ok := r.menuItem(ctx, it)
		if !ok {
			dropped[it.ID] = true
			return false
		}
		ri.Parent = parent
		index[it.ID] = len(out)
		out = append(out, ri)
		return true
	}
	for _, it := range m.Items {
		if !add(it, 0) && !dropped[it.ID] {
			r.res.Warnings = append(r.res.Warnings, fmt.Sprintf("menu %q: item %q has a broken parent and was left out", m.Name, it.Title))
		}
	}
	return out, nil
}

func (r *Runner) menuItem(ctx context.Context, it MenuItem) (menu.ReplaceItem, bool) {
	ri := menu.ReplaceItem{Label: it.Title, OpenInNewTab: it.NewTab}
	if it.Type == "post_type" {
		if info, ok := r.infos[it.ObjectID]; ok && info.ent != nil {
			id := info.ent.ID
			ri.LinkType, ri.EntityID = menu.LinkTypeEntity, &id
			return ri, true
		}
	}
	target := it.URL
	if it.Type == "taxonomy" {
		if parts := strings.SplitN(it.ObjectID, ":", 2); len(parts) == 2 {
			if id, ok := r.termIDs[it.ObjectID]; ok {
				if t, err := r.d.Taxonomy.GetTerm(ctx, id); err == nil {
					if taxonomy.FillPaths(ctx, r.d.TermRepo, t) == nil {
						target = taxonomy.ArchivePrefix(t.Kind) + t.Path
					}
				}
			}
		}
	}
	if u, err := url.Parse(target); err == nil && u.Host != "" && r.hosts[strings.ToLower(u.Hostname())] {
		p := u.Path
		if np, ok := r.termPaths[normPath(p)]; ok {
			p = np
		}
		if u.RawQuery != "" {
			p += "?" + u.RawQuery
		}
		target = p
	}
	if target == "" {
		target = "/"
	}
	if !safeMenuURL(target) {
		r.res.Warnings = append(r.res.Warnings, fmt.Sprintf("menu item %q: URL %.60q is not http, https, mailto, tel or relative: left out", it.Title, target))
		return menu.ReplaceItem{}, false
	}
	ri.URL = &target
	if strings.HasPrefix(target, "/") {
		ri.LinkType = menu.LinkTypeCustom
	} else {
		ri.LinkType = menu.LinkTypeExternal
	}
	return ri, true
}

// safeMenuURL is the allowlist of the theme's safeURL: http, https, mailto,
// tel, sms and relative references.
func safeMenuURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, `/\`) {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto", "tel", "sms":
		return true
	case "":
		return u.Host == ""
	}
	return false
}

// errOwnerRedirect marks a source path that a redirect of the owner (not of an
// import) already handles.
var errOwnerRedirect = errors.New("a redirect that is not from an import already handles this path")

// upsertRedirect makes old answer 301 to target, once. A redirect is only ever
// overwritten when this source's id map owns it.
func (r *Runner) upsertRedirect(ctx context.Context, from, to string) error {
	if id, ok, err := r.st.Lookup(ctx, KindRedirect, from); err != nil {
		return err
	} else if ok {
		status := 301
		if _, err := r.d.Redirects.UpdateRedirect(ctx, id, &redirect.UpdateRedirectInput{TargetURL: &to, StatusCode: &status}); err == nil {
			return nil
		}
		// The owner deleted it meanwhile: create it again below.
	}
	rd, err := r.d.Redirects.CreateRedirect(ctx, &redirect.CreateRedirectInput{SourcePath: from, TargetURL: to, StatusCode: 301})
	if errors.Is(err, redirect.ErrDuplicateSourcePath) {
		return errOwnerRedirect
	}
	if err != nil {
		return err
	}
	r.res.Redirects++
	return r.st.Put(ctx, r.run.ID, KindRedirect, from, rd.ID, "")
}

// livePaths maps the public path of every published entity to its id.
func (r *Runner) livePaths(ctx context.Context) (map[string]uuid.UUID, error) {
	snap, err := r.d.Entities.URLRegistry().Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := entity.NewPostgresPublicRepository(r.d.DB).ListAllPublishedWithAttrs(ctx, snap.OverrideAttributes())
	if err != nil {
		return nil, err
	}
	live := make(map[string]uuid.UUID, len(entries))
	for _, e := range entries {
		if loc, found, err := snap.Locate(e.SchemaSlug, e.Attrs, snap.VarsForSitemap(e)); found && err == nil {
			live[normPath(loc.Path)] = e.ID
		}
	}
	return live, nil
}

// importRedirects writes a 301 for every old URL whose new URL differs, unless
// a live page answers at the old URL (live pages always win: the clash is
// reported instead). Redirects of an earlier run that are no longer wanted are
// removed, so a re-sync cannot leave one in front of a live page.
func (r *Runner) importRedirects(ctx context.Context) error {
	live, err := r.livePaths(ctx)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	redirectTo := func(old, to, title string, self uuid.UUID) {
		if owner, ok := live[old]; ok && owner != self {
			r.res.URLConflicts = append(r.res.URLConflicts, URLConflict{URL: old, Target: to, Reason: "an existing page answers at this URL, so no redirect was made; the imported content lives at " + to})
			return
		}
		want[old] = true
		switch err := r.upsertRedirect(ctx, old, to); {
		case errors.Is(err, errOwnerRedirect):
			r.res.URLConflicts = append(r.res.URLConflicts, URLConflict{URL: old, Target: to, Reason: "a redirect of your own already handles this URL; the imported content lives at " + to})
		case err != nil:
			r.res.Errors = append(r.res.Errors, ItemError{Kind: "redirect", ID: old, Title: title, Err: err.Error()})
		}
	}
	site, err := r.siteLanguage(ctx)
	if err != nil {
		return err
	}
	frontID := fmt.Sprint(r.dump.Site.FrontPageID)
	otherLang := 0
	for id, info := range r.infos {
		it := info.item
		if info.ent != nil && info.ent.Locale != site {
			// Not public: its URL is the site language page's, so no redirect.
			if it.Status == wpPublish {
				otherLang++
			}
			continue
		}
		if info.ent == nil || it.Status != wpPublish || it.Link == "" || (r.dump.Site.ShowOnFront == "page" && id == frontID) {
			continue
		}
		old := normPath(pathOf(it.Link))
		if old == "/" || info.newPath == "" || old == normPath(info.newPath) {
			continue
		}
		redirectTo(old, info.newPath, it.Title, info.ent.ID)
	}
	if otherLang > 0 {
		r.res.Warnings = append(r.res.Warnings, fmt.Sprintf("%d items in other languages are stored but not public, so they got no redirects", otherLang))
	}
	for old, to := range r.termPaths {
		if old != normPath(to) && old != "/" {
			redirectTo(old, to, "", uuid.Nil)
		}
	}
	return r.removeStaleRedirects(ctx, want)
}

func (r *Runner) removeStaleRedirects(ctx context.Context, want map[string]bool) error {
	owned, err := r.st.All(ctx, KindRedirect)
	if err != nil {
		return err
	}
	for from, id := range owned {
		if want[from] {
			continue
		}
		if err := r.d.Redirects.DeleteRedirect(ctx, id); err != nil && !errors.Is(err, redirect.ErrRedirectNotFound) {
			r.res.Errors = append(r.res.Errors, ItemError{Kind: "redirect", ID: from, Err: "stale redirect not removed: " + err.Error()})
			continue
		}
		if err := r.st.Delete(ctx, KindRedirect, from); err != nil {
			return err
		}
		r.res.RedirectsRemoved++
	}
	return nil
}

// finish sets the home page, drops caches and checks the promise: no
// wp-content URL left in imported content.
func (r *Runner) finish(ctx context.Context) error {
	if r.state.Mode == ModeExisting {
		if r.dump.Site.ShowOnFront == "page" && r.dump.Site.FrontPageID != 0 {
			r.res.Warnings = append(r.res.Warnings, "home page kept: WordPress shows a static page on the front, the site keeps its own home page")
		}
	} else if r.dump.Site.ShowOnFront == "page" && r.dump.Site.FrontPageID != 0 {
		if info, ok := r.infos[fmt.Sprint(r.dump.Site.FrontPageID)]; ok && info.ent != nil {
			id := info.ent.ID
			if _, err := r.d.Site.Update(ctx, siteconfig.UpdateInput{HomepageEntityID: &id}); err != nil {
				r.res.Warnings = append(r.res.Warnings, "home page not set: "+err.Error())
			}
		}
	}
	r.d.Entities.URLRegistry().Invalidate()
	if r.opts.Purge != nil {
		r.opts.Purge(ctx)
	}
	left, err := r.wpContentLeft(ctx)
	if err != nil {
		return err
	}
	r.res.WPContentLeft = left
	return nil
}

func (r *Runner) wpContentLeft(ctx context.Context) ([]string, error) {
	rows, err := r.d.DB.Pool().Query(ctx, `SELECT id, attributes::text FROM hatamoto_entities WHERE metadata->>'import_key' LIKE $1`, r.keyPrefix()+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id uuid.UUID
		var attrs string
		if err := rows.Scan(&id, &attrs); err != nil {
			return nil, err
		}
		for _, u := range WPContentURLs(attrs) {
			out = append(out, id.String()+": "+u)
		}
	}
	return out, rows.Err()
}
