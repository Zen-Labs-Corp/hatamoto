package kit

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/siteconfig"
)

// Services are the real services Wire adapts.
type Services struct {
	Manager  *packages.Manager
	Entities *entity.Service
	Menus    *menu.Service
	Site     *siteconfig.Store
	Comments *comment.Service
	// SchemaComments is nil when the site cannot write comment settings.
	SchemaComments SchemaComments
	Pool           *pgxpool.Pool
	// Address returns the site address ("" when none is set).
	Address func(ctx context.Context) string
	Log     *slog.Logger
}

// Wire adapts the services to Deps. The adapters only translate calls.
func Wire(s Services) Deps {
	return Deps{
		Packages:       packagesAdapter{m: s.Manager},
		Schemas:        schemasAdapter{Service: s.Entities, pool: s.Pool},
		Pages:          pagesAdapter{e: s.Entities},
		Menus:          s.Menus,
		Site:           siteAdapter{site: s.Site, entities: s.Entities, address: s.Address},
		Comments:       commentsAdapter{c: s.Comments},
		SchemaComments: s.SchemaComments,
		Applied:        AppliedStore{Pool: s.Pool},
		Log:            s.Log,
	}
}

// Available lists the kits the registry offers.
func Available(ctx context.Context, d Deps) ([]packages.Entry, error) {
	idx, err := d.Packages.Registry(ctx)
	if err != nil {
		return nil, err
	}
	var out []packages.Entry
	for _, e := range idx.Packages {
		if e.Kind == packages.KindKit {
			out = append(out, e)
		}
	}
	return out, nil
}

type packagesAdapter struct{ m *packages.Manager }

func (a packagesAdapter) Registry(ctx context.Context) (*packages.Index, error) {
	if a.m.Registry == nil {
		return nil, packages.ErrRegistryOff
	}
	return a.m.Registry.Fetch(ctx)
}

func (a packagesAdapter) Fetch(ctx context.Context, kind packages.Kind, name string) ([]byte, error) {
	return a.m.FetchFromRegistry(ctx, kind, name)
}

func (a packagesAdapter) Installed(ctx context.Context, kind packages.Kind, name string) (string, bool, error) {
	row, err := a.m.Store().Get(ctx, kind, name)
	if errors.Is(err, packages.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return row.Version, true, nil
}

func (a packagesAdapter) Install(ctx context.Context, raw []byte, grant []string) error {
	if grant == nil {
		grant = []string{}
	}
	_, err := a.m.Install(ctx, raw, packages.InstallRequest{Source: packages.SourceRegistry, Grant: grant})
	return err
}

func (a packagesAdapter) ActiveTheme(ctx context.Context) (string, error) {
	cfg, err := a.m.Store().Config(ctx)
	return cfg.ActiveTheme, err
}

func (a packagesAdapter) ActiveThemeLocations(ctx context.Context) ([]string, error) {
	name, err := a.ActiveTheme(ctx)
	if err != nil || name == "" || name == packages.DefaultTheme {
		return nil, err
	}
	p, _, err := a.m.Store().Load(ctx, packages.KindTheme, name)
	if err != nil {
		return nil, err
	}
	return themeLocations(p), nil
}

func (a packagesAdapter) Activate(ctx context.Context, name string) error {
	return a.m.Activate(ctx, name, false)
}

// schemasAdapter embeds the entity service for the three content type calls and
// adds the origin lookup, which the service does not expose.
type schemasAdapter struct {
	*entity.Service
	pool *pgxpool.Pool
}

func (a schemasAdapter) SchemaOrigin(ctx context.Context, slug string) (string, error) {
	var origin string
	err := a.pool.QueryRow(ctx, `SELECT COALESCE(origin_plugin, '') FROM hatamoto_entity_schemas WHERE slug = $1`, slug).Scan(&origin)
	return origin, err
}

type pagesAdapter struct{ e *entity.Service }

func (a pagesAdapter) GetPage(ctx context.Context, schemaSlug, slug string) (*entity.Entity, error) {
	e, err := a.e.GetEntityByParentSlug(ctx, nil, slug, schemaSlug)
	if errors.Is(err, entity.ErrEntityNotFound) {
		return nil, nil
	}
	return e, err
}

func (a pagesAdapter) CreatePublished(ctx context.Context, in *entity.CreateEntityInput, by *uuid.UUID) (*entity.Entity, error) {
	e, err := a.e.CreateEntity(ctx, by, in)
	if err != nil {
		return nil, err
	}
	return a.e.UpdateEntityStatus(ctx, e.ID, by, entity.StatusPublished)
}

type siteAdapter struct {
	site     *siteconfig.Store
	entities *entity.Service
	address  func(ctx context.Context) string
}

func (a siteAdapter) Homepage(ctx context.Context) (*entity.Entity, error) {
	cfg, err := a.site.Get(ctx)
	if err != nil || cfg.HomepageEntityID == uuid.Nil {
		return nil, err
	}
	e, err := a.entities.GetEntity(ctx, cfg.HomepageEntityID)
	if errors.Is(err, entity.ErrEntityNotFound) {
		return nil, nil
	}
	return e, err
}

func (a siteAdapter) SetHomepage(ctx context.Context, id uuid.UUID) error {
	_, err := a.site.Update(ctx, siteconfig.UpdateInput{HomepageEntityID: &id})
	return err
}

func (a siteAdapter) Address(ctx context.Context) string {
	if a.address == nil {
		return ""
	}
	return a.address(ctx)
}

type commentsAdapter struct{ c *comment.Service }

func (a commentsAdapter) Enabled(ctx context.Context) (bool, error) {
	s, err := a.c.Settings(ctx)
	return s.Enabled, err
}
