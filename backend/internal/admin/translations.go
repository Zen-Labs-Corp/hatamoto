package admin

import (
	"context"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
)

// translationsData feeds entities/translations.
type translationsData struct {
	Name, ID   string
	Tabs       []ui.Tab
	Actions    []ui.Button
	Empty      bool // the page has nothing to compare: EmptyState says why
	EmptyState ui.Empty
	Intro      string
	Copies     ui.Table
	Grid       ui.Table
	Hreflang   ui.Table
	// Link is the "Link an existing page" dialog; nil hides it (no edit rights).
	Link *linkDialog
}

type linkDialog struct{ Action, Src string }

// handleTranslations is the Translations tab of an entry: one row per
// configured locale, a field by locale grid and what search engines see.
func (a *Admin) handleTranslations(w http.ResponseWriter, r *http.Request) {
	e, sc, ok := a.loadEntity(w, r)
	if !ok {
		return
	}
	e.Schema = sc
	ctx := r.Context()
	id := e.ID.String()
	cfg, err := a.loadSite(ctx)
	if err != nil {
		a.serverError(w, r, "The site settings could not be loaded", err)
		return
	}
	set := localeSetOf(cfg)
	d := translationsData{Name: e.Name, ID: id, Tabs: a.entityTabsFor(ctx, e, "translations")}
	g := a.deps.Content.Groups
	switch {
	case !set.Multi() || g == nil:
		d.Empty = true
		d.EmptyState = ui.Empty{Icon: "globe", Title: "This site has one language", Body: "Add a second language to keep translated copies of this page side by side.",
			Actions: []ui.Button{{Label: "Add a locale", Href: a.url("settings", "locales")}}}
	case e.Locale == entity.LocaleShared:
		d.Empty = true
		d.EmptyState = ui.Empty{Icon: "globe", Title: "Shared by every locale", Body: "This page is not tied to one language, so it has no translated copies."}
	default:
		members, err := g.Members(ctx, e.ID)
		if err != nil {
			a.serverError(w, r, "The translations could not be loaded", err)
			return
		}
		if canEditTranslations(r) {
			base := a.translationsURL(e.ID)
			d.Link = &linkDialog{Action: base + "/link", Src: base + "/candidates"}
			d.Actions = []ui.Button{{Label: "Link an existing page", Icon: "link", Href: "#link-copy", Attrs: map[string]string{"data-hm-open": "link-copy"}}}
		}
		a.fillTranslations(ctx, &d, e, members, set.Default(), set)
	}
	v := a.NewView(w, r, "Translations of "+e.Name)
	v.Data = d
	v.Crumbs = a.entityCrumbs(e.Name, id, Crumb{Label: "Translations"})
	a.Render(w, r, http.StatusOK, "entities/translations", v)
}

// copyView is a member with the entity behind it (for the address and the
// attributes) and whether it is older than the original.
type copyView struct {
	m     entity.Member
	e     *entity.Entity
	stale bool
}

// canEditTranslations reports whether the person may change links: the same
// roles the write routes allow.
func canEditTranslations(r *http.Request) bool {
	u := userFrom(r.Context())
	return u != nil && (u.Role == "owner" || u.Role == "editor")
}

func (a *Admin) fillTranslations(ctx context.Context, d *translationsData, e *entity.Entity, members []entity.Member, defaultLocale string, set *entity.LocaleSet) {
	grouped := len(members) > 1
	if !grouped {
		members = []entity.Member{{ID: e.ID, Locale: e.Locale, Slug: e.Slug, Name: e.Name, Status: string(e.Status),
			CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt, ContentChangedAt: e.UpdatedAt}}
	}
	orig := entity.OriginalOf(members, defaultLocale)
	loc := a.siteLocation(ctx)
	byLocale := map[string]*copyView{}
	for _, m := range members {
		cv := &copyView{m: m, e: e, stale: grouped && entity.OutOfDate(m, *orig)}
		if m.ID != e.ID && a.deps.Content.Entities != nil {
			// A copy that cannot be loaded shows no address or attributes,
			// not the data of the page being viewed.
			cv.e = nil
			if other, err := a.deps.Content.Entities.GetEntity(ctx, m.ID); err == nil {
				other.Schema = e.Schema
				cv.e = other
			}
		}
		byLocale[m.Locale] = cv
	}

	d.Intro = "Each language keeps its own copy of this page. The original is the copy the others are compared with."
	d.Copies = ui.Table{
		ID: "translations-copies", Caption: "Copies of " + e.Name + " by language", CSRF: csrfToken(ctx),
		Columns: []ui.Col{{Key: "locale", Label: "Language"}, {Key: "address", Label: "Address"}, {Key: "status", Label: "Status"}},
		Empty:   ui.Empty{Icon: "globe", Title: "No languages", Body: "Add languages in the site settings."},
	}
	tags := set.Tags()
	seen := map[string]bool{}
	for _, tag := range tags {
		seen[tag] = true
	}
	for _, m := range members { // a copy whose locale was removed from the settings still shows
		if !seen[m.Locale] {
			tags = append(tags, m.Locale)
		}
	}
	for _, tag := range tags {
		cv := byLocale[tag]
		name := set.Name(tag)
		if name == "" {
			name = localeDisplayName(tag)
		}
		lang := ui.Cell{Text: name, Sub: tag}
		if set.IsHidden(tag) {
			lang.Suffix = "(hidden)"
		}
		row := ui.Row{ID: tag, Label: name + " copy"}
		if cv == nil {
			text := "no copy"
			if set.IsHidden(tag) {
				text = "no copy, hidden"
			}
			row.Cells = []ui.Cell{lang, {Text: "None"}, {Text: text}}
			d.Copies.Rows = append(d.Copies.Rows, row)
			continue
		}
		if grouped && cv.m.ID == orig.ID {
			lang.Tag = &ui.Badge{Label: "Original", Kind: "info"}
		}
		row.Cells = []ui.Cell{lang, {Text: a.copyAddress(ctx, cv.e), Clip: true}, a.statusCell(cv)}
		row.Actions = a.copyActions(e, cv.m, grouped, cv.m.ID == orig.ID, d.Link != nil)
		d.Copies.Rows = append(d.Copies.Rows, row)
	}

	d.Grid = ui.Table{ID: "translations-grid", Caption: "Fields of " + e.Name + " by language", Columns: []ui.Col{{Key: "field", Label: "Field"}}}
	var titleRow, addrRow, seoRow, statusRow, updatedRow, editRow []ui.Cell
	titleRow, addrRow, seoRow, statusRow, updatedRow, editRow = []ui.Cell{{Text: "Title"}}, []ui.Cell{{Text: "Address"}}, []ui.Cell{{Text: "Search title"}}, []ui.Cell{{Text: "Status"}}, []ui.Cell{{Text: "Updated"}}, []ui.Cell{{Text: "Open"}}
	for _, tag := range tags { // settings order, as in the copies table
		cv := byLocale[tag]
		if cv == nil {
			continue
		}
		d.Grid.Columns = append(d.Grid.Columns, ui.Col{Key: cv.m.Locale, Label: cv.m.Locale})
		titleRow = append(titleRow, ui.Cell{Text: cv.m.Name})
		addrRow = append(addrRow, ui.Cell{Text: a.copyAddress(ctx, cv.e), Clip: true})
		seo := ""
		if cv.e != nil {
			seo = seoFrom(attrsMap(cv.e.Attributes)).Title
		}
		if seo == "" {
			seo = "None"
		}
		seoRow = append(seoRow, ui.Cell{Text: seo})
		statusRow = append(statusRow, ui.Cell{Text: statusLabel(cv.m.Status)})
		upd := fmtDateTime(cv.m.UpdatedAt.In(loc))
		if cv.stale {
			upd += " (older than original)"
		}
		updatedRow = append(updatedRow, ui.Cell{Text: upd})
		editRow = append(editRow, ui.Cell{Text: "Edit", Link: a.adminPath + "/entities/" + cv.m.ID.String()})
	}
	for i, cells := range [][]ui.Cell{titleRow, addrRow, seoRow, statusRow, updatedRow, editRow} {
		d.Grid.Rows = append(d.Grid.Rows, ui.Row{ID: []string{"title", "address", "seo", "status", "updated", "edit"}[i], Label: cells[0].Text, Cells: cells})
	}

	d.Hreflang = ui.Table{
		ID: "translations-hreflang", Caption: "What search engines see for " + e.Name,
		Columns: []ui.Col{{Key: "hreflang", Label: "Search engines see"}, {Key: "address", Label: "Address"}},
		Empty:   ui.Empty{Icon: "globe", Title: "Nothing yet", Body: "Search engines see a language once its copy is live and the language is public."},
	}
	// Same order and x-default rule as the renderer (server.xDefaultHref):
	// settings order; the x-default locale, else (when none is set) the
	// default locale, else the first live copy.
	xdef, first, def := "", "", ""
	for _, tag := range set.PublicTags() {
		cv := byLocale[tag]
		if cv == nil || cv.e == nil || cv.m.Status != string(entity.StatusPublished) {
			continue
		}
		addr := a.copyAddress(ctx, cv.e)
		d.Hreflang.Rows = append(d.Hreflang.Rows, ui.Row{ID: "hl-" + tag, Label: tag, Cells: []ui.Cell{{Text: "hreflang " + tag}, {Text: addr, Clip: true}}})
		if first == "" {
			first = addr
		}
		if tag == set.Default() {
			def = addr
		}
		if tag == set.XDefault() {
			xdef = addr
		}
	}
	if xdef == "" && set.XDefault() == "" {
		xdef = def
	}
	if xdef == "" {
		xdef = first
	}
	if xdef != "" {
		d.Hreflang.Rows = append(d.Hreflang.Rows, ui.Row{ID: "hl-x-default", Label: "x-default", Cells: []ui.Cell{{Text: "hreflang x-default"}, {Text: xdef, Clip: true}}})
	}
}

// copyActions are the row actions of one copy: Edit, and for those who may
// change links, Make original (not on the original) and Unlink (not on a page
// without translations).
func (a *Admin) copyActions(page *entity.Entity, m entity.Member, grouped, isOriginal, canEdit bool) []ui.Action {
	id := m.ID.String()
	actions := []ui.Action{{Label: "Edit", Href: a.adminPath + "/entities/" + id}}
	if !canEdit || !grouped {
		return actions
	}
	base := a.translationsURL(page.ID)
	if !isOriginal {
		actions = append(actions, ui.Action{Label: "Make original", Post: base + "/original", Fields: map[string]string{"member": id}})
	}
	return append(actions, ui.Action{Label: "Unlink", Danger: true, Href: base + "/unlink?member=" + id, Post: base + "/unlink?member=" + id,
		Confirm: "The page stays as it is, but it is no longer listed as a translation.", ConfirmLabel: "Unlink"})
}

func (a *Admin) copyAddress(ctx context.Context, e *entity.Entity) string {
	if e != nil && a.deps.Content.PublicPath != nil {
		if p := a.deps.Content.PublicPath(ctx, e); p != "" {
			return p
		}
	}
	return "None"
}

func statusLabel(s string) string {
	switch s {
	case "published":
		return "Live"
	case "":
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// statusCell is the status of a copy: Live, or its other status, with an
// "Out of date" mark when it is older than the original.
func (a *Admin) statusCell(cv *copyView) ui.Cell {
	kind := "neutral"
	if cv.m.Status == string(entity.StatusPublished) {
		kind = "success"
	}
	label := statusLabel(cv.m.Status)
	if cv.stale && cv.m.Status == string(entity.StatusPublished) {
		return ui.Cell{Badge: &ui.Badge{Label: "Out of date", Kind: "warning"}}
	}
	c := ui.Cell{Badge: &ui.Badge{Label: label, Kind: kind}}
	if cv.stale {
		c.Tag = &ui.Badge{Label: "Out of date", Kind: "warning"}
	}
	return c
}
