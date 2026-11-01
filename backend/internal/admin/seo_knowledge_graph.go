package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/bizprofile"
)

const seoKGPath = "/settings/seo/knowledge-graph"

type seoKGData struct {
	Table ui.Table
	// Preview is the site-wide graph as the public head prints it, indented;
	// PreviewError says why there is none.
	Preview      string
	PreviewError bool
}

// seoKGTable lists the organization details the graph is built from, each with
// the page where it is edited. Nothing here is edited on this tab.
func (a *Admin) seoKGTable(r *http.Request) ui.Table {
	t := ui.Table{
		ID: "kg-inputs", Caption: "What the graph is built from",
		Columns: []ui.Col{{Key: "detail", Label: "Detail"}, {Key: "value", Label: "Saved"}, {Key: "edit", Label: "Edit in"}},
	}
	general := a.url("settings", "general")
	locales := a.url("settings", "locales", "business")

	var name, logo string
	var profileLinks, localeCards int
	if a.deps.Site != nil {
		if cfg, err := a.deps.Site.Get(r.Context()); err == nil && cfg != nil {
			name, logo = cfg.CompanyName, cfg.LogoURL
			if p, err := siteProfile(cfg); err == nil {
				profileLinks = len(p.SameAs)
			}
			stored, _ := bizprofile.ParseLocales(cfg.LocaleProfiles)
			for _, p := range stored {
				if label, _ := localeBadge(p); label != "Defaults" {
					localeCards++
				}
			}
		}
	}
	state := func(set bool) ui.Cell {
		if set {
			return ui.Cell{Badge: &ui.Badge{Label: "Set", Kind: "success"}}
		}
		return ui.Cell{Badge: &ui.Badge{Label: "Not set", Kind: "neutral"}}
	}
	link := func(text, href string) ui.Cell { return ui.Cell{Text: text, Link: href} }
	nameCell := state(name != "")
	if name != "" {
		nameCell = ui.Cell{Text: name}
	}
	rows := []struct {
		id, label string
		value     ui.Cell
		edit      ui.Cell
	}{
		{"name", "Organization name", nameCell, link("General", general)},
		{"logo", "Logo", state(logo != ""), link("General", general)},
		{"profiles", "Profile links (sameAs)", ui.Cell{Text: strconv.Itoa(profileLinks)}, link("Locales > Business details", locales)},
		{"locales", "Locales with their own business details", ui.Cell{Text: strconv.Itoa(localeCards)}, link("Locales > Business details", locales)},
	}
	for _, row := range rows {
		t.Rows = append(t.Rows, ui.Row{ID: row.id, Label: row.label, Cells: []ui.Cell{{Text: row.label}, row.value, row.edit}})
	}
	return t
}

// handleSEOKnowledgeGraph is GET /admin/settings/seo/knowledge-graph: the
// organization details and the exact site graph the public head prints.
func (a *Admin) handleSEOKnowledgeGraph(w http.ResponseWriter, r *http.Request) {
	d := seoKGData{Table: a.seoKGTable(r)}
	if a.deps.SiteGraph != nil {
		graph, err := a.deps.SiteGraph(r)
		var pretty []byte
		if err == nil {
			pretty, err = json.MarshalIndent(graph, "", "  ")
		}
		if err != nil {
			a.logger().Error("seo knowledge graph preview", "err", err)
			d.PreviewError = true
		} else {
			d.Preview = string(pretty)
		}
	}
	v := a.NewView(w, r, "SEO")
	v.Section, v.Tab = "seo", "seo-kg"
	v.Width = "narrow"
	v.Data = d
	a.Render(w, r, http.StatusOK, "settings/seo-knowledge-graph", v)
}
