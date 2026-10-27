package wpimport

import (
	"context"
	"log/slog"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/taxonomy"
)

// Deps are the Hatamoto services the importer writes through, built the same
// way the server builds them so an import and an editor save behave alike.
type Deps struct {
	DB         *database.DB
	Entities   *entity.Service
	SchemaRepo entity.SchemaRepository
	Media      *media.Service
	Taxonomy   *taxonomy.Service
	TermRepo   taxonomy.Repository
	Menus      *menu.Service
	People     *person.Store
	Redirects  *redirect.Service
	Site       *siteconfig.Store
	Logger     *slog.Logger
}

// DepsConfig is the media storage configuration.
type DepsConfig struct {
	MediaDir     string
	MediaBaseURL string
	MediaMaxMB   int
}

// NewDeps wires the services over db. Call Close when done.
func NewDeps(db *database.DB, cfg DepsConfig, logger *slog.Logger) *Deps {
	schemaRepo := entity.NewPostgresSchemaRepository(db)
	ents := entity.NewService(schemaRepo, entity.NewPostgresEntityRepository(db), logger)
	ents.SetBylines(person.NewByline(db.Pool()))
	site := siteconfig.NewStore(db, logger)
	// {year}/{month}/{day} tokens are read in the site time zone, as the server does.
	ents.URLRegistry().SetTimezone(func(ctx context.Context) string {
		if c, err := site.Get(ctx); err == nil {
			return c.Timezone
		}
		return ""
	})
	mc := media.DefaultConfig()
	if cfg.MediaDir != "" {
		mc.UploadDir = cfg.MediaDir
	}
	if cfg.MediaBaseURL != "" {
		mc.BaseURL = cfg.MediaBaseURL
	}
	if cfg.MediaMaxMB > 0 {
		mc.MaxSizeMB = cfg.MediaMaxMB
	}
	termRepo := taxonomy.NewPostgresRepository(db)
	return &Deps{
		DB: db, Entities: ents, SchemaRepo: schemaRepo,
		Media:    media.NewService(media.NewPostgresRepository(db), mc, logger),
		Taxonomy: taxonomy.NewService(termRepo, logger), TermRepo: termRepo,
		Menus:  menu.NewService(menu.NewPostgresRepository(db), logger),
		People: person.NewPoolStore(db.Pool()), Redirects: redirect.NewService(db, logger), Site: site, Logger: logger,
	}
}

// Close stops the background work of the redirect service.
func (d *Deps) Close() { d.Redirects.Close() }
