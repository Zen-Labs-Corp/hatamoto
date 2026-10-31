package admin

import (
	"cmp"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"slices"

	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
)

// docState is what a page's stored page_doc is to the editor choice.
type docState = entity.DocState

const (
	docNone   = entity.DocNone
	docOK     = entity.DocOK
	docBroken = entity.DocBroken
	docLegacy = entity.DocLegacy
)

// builderDoc reads the page_doc stored on e.
func builderDoc(e *entity.Entity) (*builder.Doc, docState) { return entity.BuilderDoc(e) }

// handleBuilderScreen renders the builder for a page that has a page_doc. The
// no-JS outline sits inside <noscript>; ?outline=1 shows it as the page itself.
func (a *Admin) handleBuilderScreen(w http.ResponseWriter, r *http.Request, e *entity.Entity, sc *entity.EntitySchema, doc *builder.Doc) {
	plain := r.URL.Query().Get("outline") == "1"
	d, err := a.outlineData(e, doc, newOutlineState(), plain, e.Name)
	if err != nil {
		a.serverError(w, r, "The page outline could not be built", err)
		return
	}
	d.Outline = plain
	if !plain {
		if d.Shell, err = a.builderShellData(r, e, sc, doc); err != nil {
			a.serverError(w, r, "The builder could not be opened", err)
			return
		}
	}
	a.renderOutline(w, r, http.StatusOK, d)
}

// builderShell is what the shell's markup needs beyond the outline's data.
type builderShell struct {
	SchemaName, ListHref, VersionsHref, ViewHref, DeleteHref string
	StatusBadge                                              *ui.Badge
	// Boot is the whole <script type="application/json"> element the screen
	// module starts from. html/template would JS-escape a value placed in a
	// script element, so the element is built here: json.Marshal escapes <, > and
	// &, so the data cannot close it. It is data, not script: the CSP does not apply.
	Boot template.HTML
	// Role is "block" or "part" when the builder edits one (empty for a page):
	// the bar then offers Save block / Save part and no Publish.
	Role, SaveLabel, TitleLabel string
	// Usage is where a block is used, listed server-side in its own builder.
	Usage       []usageItem
	UsageLoaded bool
	UsageLabel  string
	// Translations is the drawer's URL on a page of a multi-locale site ("" otherwise:
	// no opener, no drawer).
	Translations string
}

type bootBindSource struct {
	Source string `json:"source"`
	Label  string `json:"label"`
	Group  string `json:"group"`
	Class  string `json:"class"`
}

type bootSection struct {
	Type   string                `json:"type"`
	Title  string                `json:"title"`
	Fields []schemaform.TopField `json:"fields,omitempty"`
}

func (a *Admin) builderShellData(r *http.Request, e *entity.Entity, sc *entity.EntitySchema, doc *builder.Doc) (*builderShell, error) {
	id := e.ID.String()
	kinds := builder.BootKinds(false)
	bindSources := []bootBindSource{}
	for _, b := range builder.BindSources() {
		bindSources = append(bindSources, bootBindSource{Source: b.Source, Label: b.Label, Group: b.Group, Class: b.Class})
	}
	sections := []bootSection{}
	_, types := a.themeTypes()
	for _, t := range types {
		sections = append(sections, bootSection{Type: t.Type, Title: t.Title, Fields: schemaform.TopFields(t.Schema)})
	}
	sh := &builderShell{
		SchemaName: sc.Name + "s", ListHref: a.adminPath + "/entities?type=" + url.QueryEscape(sc.Slug),
		VersionsHref: a.adminPath + "/entities/" + id + "/versions", DeleteHref: a.adminPath + "/entities/" + id + "/delete", StatusBadge: statusBadge(e.Status),
	}
	if c := a.deps.Content; c.PublicPath != nil && e.Status == entity.StatusPublished {
		e.Schema = sc
		sh.ViewHref = c.PublicPath(r.Context(), e)
	}
	base := a.adminPath + "/entities/" + id
	user := ""
	if u := userFrom(r.Context()); u != nil {
		user = u.ID
	}
	a.shellReuse(r, sh, e, sc)
	if sh.Role == "" {
		if _, ok := a.multiLocale(r.Context()); ok && e.Locale != entity.LocaleShared && a.deps.Content.Copier != nil {
			sh.Translations = base + "/builder/translations"
		}
	}
	// Site styles is owner only (the Theme routes are): editors get neither the flag nor the values.
	canStyle := false
	if u := userFrom(r.Context()); u != nil {
		canStyle = u.Role == "owner"
	}
	var siteStyles *bootSiteStyles
	if canStyle {
		if cfg, err := a.loadSite(r.Context()); err == nil {
			siteStyles = siteStylesBoot(cfg)
		} else {
			canStyle = false
		}
	}
	bootMap := map[string]any{
		"doc": doc, "version": e.Version, "status": string(e.Status), "name": e.Name, "user": user, "entity": id,
		"kinds": kinds, "bindSources": bindSources, "sections": sections, "options": a.builderOptions(r), "design": a.builderDesign(r), "reuse": a.builderReuse(r, e, sc),
		"urls": map[string]string{
			"stage": base + "/builder/stage", "render": base + "/edit/render", "component": base + "/builder/component",
			"save": base + "/builder/save", "seo": base + "/builder/seo", "analyze": base + "/builder/analyze", "nodeVersions": base + "/builder/node-versions", "paste": base + "/builder/paste", "stylespec": a.adminPath + "/builder/stylespec", "presets": a.adminPath + "/builder/presets", "details": base, "versions": sh.VersionsHref, "view": sh.ViewHref, "theme": a.adminPath + "/theme/colours",
			"recovery": base + "/builder/recovery", "recoveryDiscard": base + "/builder/recovery/discard",
		},
		"canStyle": canStyle,
	}
	if sh.Translations != "" {
		bootMap["urls"].(map[string]string)["translations"] = sh.Translations
	}
	if c := a.recoveryFor(r, e.ID); c != nil {
		bootMap["recovery"] = recoveryView(c, true)
	}
	if siteStyles != nil {
		bootMap["siteStyles"] = siteStyles
	}
	boot, err := json.Marshal(bootMap)
	if err != nil {
		return nil, err
	}
	sh.Boot = template.HTML(`<script type="application/json" id="hm-builder-boot">` + string(boot) + `</script>`) //nolint:gosec // json.Marshal output, <>& escaped
	return sh, nil
}

// maxBuilderTerms caps the term choices of the loop inspector; maxBuilderPages
// the published page paths the link field offers.
const (
	maxBuilderTerms = 200
	maxBuilderPages = 200
)

// builderOptions are the choices the datalist props offer: form slugs, menu
// locations, public schema slugs, "<kind>:<slug>" terms and the plugin item
// sources a loop can read, from the services
// that are wired (an empty list otherwise: the inspector then shows a plain
// text field).
func (a *Admin) builderOptions(r *http.Request) map[string][]string {
	out := map[string][]string{"forms": {}, "menus": {}, "schemas": {}, "terms": {}, "sources": {}, "pages": a.builderPages(r), "reserved": builder.ReservedAnchors}
	for _, src := range builder.ExtSources() {
		if src.Kind == builder.ExtItems {
			out["sources"] = append(out["sources"], src.ID())
		}
	}
	if es := a.deps.Content.Entities; es != nil {
		if list, err := es.ListSchemas(r.Context()); err == nil {
			for _, sc := range list {
				if !entity.IsReuseSlug(sc.Slug) {
					out["schemas"] = append(out["schemas"], sc.Slug)
				}
			}
		}
	}
	if ts := a.deps.Content.Terms; ts != nil {
		if res, err := ts.ListTerms(r.Context(), "", "", 1, maxBuilderTerms); err == nil {
			for _, t := range res.Items {
				out["terms"] = append(out["terms"], t.Kind+":"+t.Slug)
			}
		}
	}
	if fs := a.deps.Growth.Forms; fs != nil {
		if list, err := fs.ListForms(r.Context(), ""); err == nil {
			for _, f := range list {
				out["forms"] = append(out["forms"], f.Slug)
			}
		}
	}
	if ms := a.deps.Growth.Menus; ms != nil {
		if list, err := ms.ListMenus(r.Context()); err == nil {
			seen := map[string]bool{}
			for _, m := range list {
				if m.Location != nil && *m.Location != "" && !seen[*m.Location] {
					seen[*m.Location] = true
					out["menus"] = append(out["menus"], *m.Location)
				}
			}
		}
	}
	return out
}

// builderPages lists the public paths of published entities (blocks and theme
// parts excluded), type "page" first, capped at maxBuilderPages, for the link
// field's datalist. Only schemas with a public URL are listed, and a page of
// entities is resolved in one batch rather than one query per entity. Empty
// when the entity or path services are not wired.
func (a *Admin) builderPages(r *http.Request) []string {
	out := []string{}
	c := a.deps.Content
	if c.Entities == nil || (c.PublicPath == nil && c.PublicPaths == nil) {
		return out
	}
	schemas, err := c.Entities.ListSchemas(r.Context())
	if err != nil {
		return out
	}
	slices.SortStableFunc(schemas, func(x, y *entity.EntitySchema) int {
		return cmp.Compare(btoi(x.Slug != "page"), btoi(y.Slug != "page")) // pages first
	})
	pub := entity.StatusPublished
	seen := map[string]bool{}
	for _, sc := range schemas {
		if entity.IsReuseSchema(sc) {
			continue
		}
		if c.PublicPaths != nil {
			if _, routable := c.PublicPaths(r.Context(), sc, nil); !routable {
				continue
			}
		}
		for page := 1; len(out) < maxBuilderPages; page++ {
			// the repository serves at most 100 a page (a larger limit falls back to 20)
			res, err := c.Entities.ListEntitiesWithOffset(r.Context(), &entity.ListEntitiesFilter{SchemaID: &sc.ID, Status: &pub}, &entity.OffsetPage{Page: page, Limit: 100})
			if err != nil {
				return out
			}
			for _, e := range res.Items {
				e.Schema = sc
			}
			var paths []string
			if c.PublicPaths != nil {
				paths, _ = c.PublicPaths(r.Context(), sc, res.Items)
			} else {
				for _, e := range res.Items {
					paths = append(paths, c.PublicPath(r.Context(), e))
				}
			}
			for _, p := range paths {
				if p != "" && !seen[p] && len(out) < maxBuilderPages {
					seen[p] = true
					out = append(out, p)
				}
			}
			if page >= res.TotalPages {
				break
			}
		}
	}
	return out
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
