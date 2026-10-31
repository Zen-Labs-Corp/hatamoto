package server

import (
	"context"
	"net/http"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/recovery"
	"hatamoto.dev/core/internal/render"
)

// contentDeps hands the entity, schema, taxonomy and media services to the Go
// admin. Each is set only when its service exists (a typed nil would pass a
// nil check later); the route-table build has none and never serves.
func (s *Server) contentDeps() admin.ContentDeps {
	var d admin.ContentDeps
	if s.entityService != nil {
		d.Entities = s.entityService
		if s.db != nil {
			repo := entity.NewPostgresPublicRepository(s.db)
			d.PublicPath = func(ctx context.Context, e *entity.Entity) string {
				return locateEntity(ctx, s.urlSnapshot(ctx), repo, e).Path
			}
			d.PublicPaths = func(ctx context.Context, sc *entity.EntitySchema, rows []*entity.Entity) ([]string, bool) {
				return publicPaths(ctx, s.urlSnapshot(ctx), repo, sc, rows)
			}
		}
	}
	if s.taxonomyService != nil {
		d.Terms = s.taxonomyService
	}
	if s.mediaService != nil {
		d.Media = s.mediaService
	}
	if s.db != nil {
		d.Groups = entity.NewGroupStore(s.db.Pool())
		d.Authors = authorPicker{Store: s.people(), Byline: person.NewByline(s.db.Pool())}
	}
	if s.db != nil {
		d.Crawlers = agents.NewStatsRepository(s.db)
		d.Recovery = recovery.New(s.db)
	}
	if s.sectionTemplateService != nil {
		d.Templates = s.sectionTemplateService
	}
	// The page editor reads the theme and renders previews through the
	// engine, which may be set after the admin is mounted: look it up per call.
	d.Theme = func() []admin.ThemeSection {
		if s.renderEngine == nil {
			return nil
		}
		info := s.renderEngine.ThemeInfo()
		out := make([]admin.ThemeSection, 0, len(info.Sections))
		for _, sec := range info.Sections {
			out = append(out, admin.ThemeSection{Type: sec.Type, Title: sec.Title, Description: sec.Description, Schema: sec.Schema})
		}
		return out
	}
	d.MenuTheme = func() render.MenuDecl {
		if s.renderEngine == nil {
			return render.MenuDecl{}
		}
		return s.renderEngine.MenuDecl()
	}
	d.MenuPreview = s.renderMenuPreview
	d.Preview = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.renderEngine == nil {
			http.Error(w, "page rendering is off", http.StatusServiceUnavailable)
			return
		}
		s.newRenderAdminHandlers(s.renderEngine).handleRenderPreview(w, r)
	})
	d.RenderNodes = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.renderEngine == nil {
			http.Error(w, "page rendering is off", http.StatusServiceUnavailable)
			return
		}
		s.newRenderAdminHandlers(s.renderEngine).handleRenderNodes(w, r)
	})
	return d
}

// authorPicker is admin.AuthorStore over people: the store lists them, the
// byline resolver turns a typed name into a person or the organisation.
type authorPicker struct {
	*person.Store
	*person.Byline
}
