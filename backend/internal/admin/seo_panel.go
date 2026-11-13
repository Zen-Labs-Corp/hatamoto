package admin

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/seopanel"
)

// The shared SEO panel (blog editor, entity form) is analysed by seopanel; this
// file adds what only the admin pages need: the schema-type select and the
// stores that make the preview match the live head.

type (
	seoInput  = seopanel.Input
	seoCheck  = seopanel.Check
	PanelMode = seopanel.PanelMode
)

const (
	checkPass = seopanel.CheckPass
	checkWarn = seopanel.CheckWarn
	checkFail = seopanel.CheckFail

	ModeBody     = seopanel.ModeBody
	ModeMetaOnly = seopanel.ModeMetaOnly
)

// seoPanel is the analysis plus the Schema tab's form fields.
type seoPanel struct {
	seopanel.Panel
	// ShowSchemaFields asks for the entry's schema type select and custom
	// JSON-LD box above the preview; SchemaTypes are the select's options.
	ShowSchemaFields bool
	SchemaTypes      []ui.Option
	// WebPageOptions and BreadcrumbOptions are the node switch selects: the
	// first option follows the schema, then On and Off.
	WebPageOptions, BreadcrumbOptions []ui.Option
}

func buildPanel(in seoInput) seoPanel { return seoPanel{Panel: seopanel.Build(in)} }

// withSchemaFields turns the Schema tab's fields on for the form that shows
// them. The select's first option names the @type the entry gets without an
// override.
func (p *seoPanel) withSchemaFields(sc *entity.EntitySchema, in seoInput) {
	st, _ := sc.URLSettings()
	def := "the type's schema"
	if !entity.SchemaFixesJSONLDType(sc) {
		if def = st.SchemaOrgType; def == "" {
			if def = legacySchemaTypes[sc.Slug]; def == "" {
				def = "Thing"
			}
		}
	}
	p.ShowSchemaFields, p.CustomJSONLD = true, in.CustomJSONLD
	p.SchemaTypes = []ui.Option{{Value: "", Label: "Use type default (" + def + ")", Selected: in.SchemaType == ""}}
	for _, t := range entity.AllowedSchemaOrgTypes {
		p.SchemaTypes = append(p.SchemaTypes, ui.Option{Value: t, Label: t, Selected: in.SchemaType == t})
	}
	p.WebPageOptions = entrySwitchOptions(st.EmitWebPage, in.JSONLDWebPage)
	p.BreadcrumbOptions = entrySwitchOptions(st.EmitBreadcrumb, in.JSONLDBreadcrumb)
}

// entrySwitchOptions are the choices of an entry's node switch. The first one
// names what the schema does without an override.
func entrySwitchOptions(schema entity.NodeSwitch, current string) []ui.Option {
	def := "Auto"
	switch schema {
	case entity.NodeSwitchOn:
		def = "On"
	case entity.NodeSwitchOff:
		def = "Off"
	}
	return []ui.Option{
		{Value: "", Label: "Use schema default (" + def + ")", Selected: current == ""},
		{Value: string(entity.NodeSwitchOn), Label: "On", Selected: current == string(entity.NodeSwitchOn)},
		{Value: string(entity.NodeSwitchOff), Label: "Off", Selected: current == string(entity.NodeSwitchOff)},
	}
}

// withSEOSettings fills in what the preview needs to match the live head: the
// stored SEO settings, the type and the site name. A missing store or a read
// error leaves the preview on the entry's own values.
func (a *Admin) withSEOSettings(r *http.Request, in *seoInput, schemaSlug string) {
	if a.deps.SEO == nil {
		return
	}
	set, err := a.deps.SEO.Get(r.Context())
	if err != nil {
		return
	}
	in.SEO, in.SchemaSlug = &set, schemaSlug
	if a.deps.Site != nil {
		if cfg, err := a.deps.Site.Get(r.Context()); err == nil && cfg != nil {
			in.SiteName, in.Tagline = cfg.CompanyName, cfg.Tagline
		}
	}
}

// markHome flags the preview as the homepage's when id is the entity the site
// serves at "/": the chosen one, or while none is chosen the page the public
// site falls back to. Without the stores it leaves the preview alone.
func (a *Admin) markHome(ctx context.Context, in *seoInput, id uuid.UUID) {
	if id == uuid.Nil {
		return
	}
	if a.deps.Site != nil {
		if cfg, err := a.deps.Site.Get(ctx); err == nil && cfg != nil && cfg.HomepageEntityID != uuid.Nil {
			in.Home = cfg.HomepageEntityID == id
			return
		}
	}
	if a.deps.Growth.Explain == nil {
		return
	}
	if ex, err := a.deps.Growth.Explain.ExplainURL(ctx, "/"); err == nil {
		in.Home = ex.Page.EntityID == id.String()
	}
}
