package kit

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/packages"
)

// The interfaces below are the slices of the core services Plan and Apply
// use. Wire adapts the real services to them; the tests use in-memory fakes.

// Packages is the package manager as a kit sees it.
type Packages interface {
	Registry(ctx context.Context) (*packages.Index, error)
	Fetch(ctx context.Context, kind packages.Kind, name string) ([]byte, error)
	Installed(ctx context.Context, kind packages.Kind, name string) (version string, ok bool, err error)
	// Install stores a package; grant is the permissions approved for a plugin.
	Install(ctx context.Context, raw []byte, grant []string) error
	ActiveTheme(ctx context.Context) (string, error)
	// ActiveThemeLocations lists the menu locations the active theme declares
	// beyond the core ones.
	ActiveThemeLocations(ctx context.Context) ([]string, error)
	Activate(ctx context.Context, name string) error
}

// Schemas is the content type store.
type Schemas interface {
	GetSchemaBySlug(ctx context.Context, slug string) (*entity.EntitySchema, error)
	CreateSchema(ctx context.Context, in *entity.CreateSchemaInput) (*entity.EntitySchema, error)
	UpdateSchema(ctx context.Context, id uuid.UUID, in *entity.UpdateSchemaInput) (*entity.EntitySchema, error)
}

// Pages is the page store. GetPage returns (nil, nil) when the slug is free.
type Pages interface {
	GetPage(ctx context.Context, slug string) (*entity.Entity, error)
	CreatePublished(ctx context.Context, in *entity.CreateEntityInput, by *uuid.UUID) (*entity.Entity, error)
}

// Menus is the menu store.
type Menus interface {
	GetMenuByLocation(ctx context.Context, location string) (*menu.Menu, error)
	EnsureMenuAtLocation(ctx context.Context, in menu.EnsureInput) (*menu.Menu, bool, error)
}

// Site is the site configuration. Homepage returns nil when none is set.
type Site interface {
	Homepage(ctx context.Context) (*entity.Entity, error)
	SetHomepage(ctx context.Context, id uuid.UUID) error
	Address(ctx context.Context) string
}

// Comments only reads: turning comments on goes through the caller's
// callback (Options.EnableComments), never through this package.
type Comments interface {
	Enabled(ctx context.Context) (bool, error)
}

// Policy turns the community comment policy on for one content type. It is nil
// until the part that ships comment.policy supplies it. Warning is the text
// the owner must see first (ADR 0005 9.5); this package never writes it.
type Policy interface {
	SetPolicy(ctx context.Context, schemaSlug string, on bool) error
	Warning() string
}

// AppliedLog reads and records applied kits (AppliedStore implements it).
type AppliedLog interface {
	Get(ctx context.Context, name string) (*Applied, error)
	Record(ctx context.Context, a Applied) error
}

// Deps are the services Plan and Apply work on.
type Deps struct {
	Packages Packages
	Schemas  Schemas
	Pages    Pages
	Menus    Menus
	Site     Site
	Comments Comments
	Policy   Policy
	Applied  AppliedLog
	Log      *slog.Logger
}
