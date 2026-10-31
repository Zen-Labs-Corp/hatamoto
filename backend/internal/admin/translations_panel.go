package admin

import (
	"context"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
)

// translationsPanelData feeds hm/translations: the Translations tab's content
// trimmed for the blog editor aside and the builder drawer.
type translationsPanelData struct {
	// Err replaces the panel with an error callout when the copies could not be loaded.
	Err   string
	Intro string
	// Copies has an Open or Create row per language, and Unlink for those who
	// may change links. No POST forms: the panel sits inside the editor form.
	Copies ui.Table
	// Hreflang is what search engines see, as "en-US, en-GB, x-default".
	Hreflang string
	// Actions go to the Translations tab, where pages are linked and compared.
	Actions []ui.Button
	// CopyForms are the Create copy dialogs the Create rows open.
	CopyForms []copyForm
}

// translationsPanel builds the panel of e for a multi-locale site; nil when e
// has no copies to show (shared by every locale).
func (a *Admin) translationsPanel(ctx context.Context, r *http.Request, e *entity.Entity, set *entity.LocaleSet) (*translationsPanelData, error) {
	if e.Locale == entity.LocaleShared {
		return nil, nil
	}
	members, err := a.deps.Content.Groups.Members(ctx, e.ID)
	if err != nil {
		return nil, err
	}
	base := a.translationsURL(e.ID)
	d := translationsData{}
	canEdit := canEditTranslations(r)
	if canEdit {
		d.Link = &linkDialog{}
	}
	a.fillTranslations(ctx, &d, e, members, set.Default(), set)
	p := &translationsPanelData{Intro: d.Intro, Copies: d.Copies, CopyForms: d.CopyForms}
	if len(members) > 1 {
		if orig := entity.OriginalOf(members, set.Default()); orig != nil && orig.ID != e.ID {
			p.Intro = "You are editing the " + e.Locale + " copy of the " + orig.Locale + " original."
		}
	}
	// The panel shows no POST action: Make original lives on the tab.
	for i := range p.Copies.Rows {
		acts := p.Copies.Rows[i].Actions[:0:0]
		for _, act := range p.Copies.Rows[i].Actions {
			if act.Post == "" || act.Confirm != "" {
				acts = append(acts, act)
			}
		}
		p.Copies.Rows[i].Actions = acts
	}
	var seen []string
	for _, row := range d.Hreflang.Rows {
		seen = append(seen, row.Label)
	}
	p.Hreflang = strings.Join(seen, ", ")
	if canEdit {
		p.Actions = append(p.Actions, ui.Button{Label: "Link an existing page", Icon: "link", Href: base})
	}
	p.Actions = append(p.Actions, ui.Button{Label: "Compare copies", Href: base})
	return p, nil
}
