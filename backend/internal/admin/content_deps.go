package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/recovery"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/sectiontemplate"
	"hatamoto.dev/core/internal/taxonomy"
)

// EntityStore is entity.Service as the entity and schema screens use it.
type EntityStore interface {
	ListSchemas(ctx context.Context) ([]*entity.EntitySchema, error)
	GetSchema(ctx context.Context, id uuid.UUID) (*entity.EntitySchema, error)
	CreateSchema(ctx context.Context, in *entity.CreateSchemaInput) (*entity.EntitySchema, error)
	UpdateSchema(ctx context.Context, id uuid.UUID, in *entity.UpdateSchemaInput) (*entity.EntitySchema, error)
	DeleteSchema(ctx context.Context, id uuid.UUID) error

	GetEntity(ctx context.Context, id uuid.UUID) (*entity.Entity, error)
	CreateEntity(ctx context.Context, userID *uuid.UUID, in *entity.CreateEntityInput) (*entity.Entity, error)
	UpdateEntity(ctx context.Context, id uuid.UUID, userID *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error)
	UpdateEntityStatus(ctx context.Context, id uuid.UUID, userID *uuid.UUID, status entity.EntityStatus) (*entity.Entity, error)
	ScheduleEntity(ctx context.Context, id uuid.UUID, userID *uuid.UUID, at time.Time) (*entity.Entity, error)
	DeleteEntity(ctx context.Context, id uuid.UUID) error
	ListEntitiesWithOffset(ctx context.Context, f *entity.ListEntitiesFilter, p *entity.OffsetPage) (*entity.OffsetPagedResult[*entity.Entity], error)
	CountEntitiesBySchemaStatus(ctx context.Context) ([]entity.SchemaStatusCount, error)
	CountEntitiesByParent(ctx context.Context, limit int) ([]entity.ParentCount, error)
	CountEntityChildren(ctx context.Context, parentIDs []uuid.UUID) (map[uuid.UUID]int, error)
	// RefUsers lists what embeds a block (a block's usage list).
	RefUsers(ctx context.Context, blockID uuid.UUID, limit int) (users []entity.RefUser, capped bool, err error)

	ListVersions(ctx context.Context, id uuid.UUID, limit int) ([]*entity.EntityVersion, error)
	GetVersion(ctx context.Context, id uuid.UUID, version int) (*entity.EntityVersion, error)
	RestoreVersion(ctx context.Context, id uuid.UUID, userID *uuid.UUID, version int) (*entity.Entity, error)
}

// TermStore is taxonomy.Service as the taxonomy screens and the term picker use it.
type TermStore interface {
	ListTerms(ctx context.Context, kind, search string, page, perPage int) (*taxonomy.TermListResult, error)
	GetTerm(ctx context.Context, id uuid.UUID) (*taxonomy.Term, error)
	CreateTerm(ctx context.Context, in taxonomy.CreateTermInput) (*taxonomy.Term, error)
	UpdateTerm(ctx context.Context, in taxonomy.UpdateTermInput) (*taxonomy.Term, error)
	DeleteTerm(ctx context.Context, id uuid.UUID) error
	MergeTerms(ctx context.Context, from, into uuid.UUID) error
	ListTermsForEntity(ctx context.Context, entityID uuid.UUID) ([]*taxonomy.Term, error)
	ReplaceEntityTerms(ctx context.Context, entityID uuid.UUID, termIDs []uuid.UUID) error
}

// MediaStore is media.Service as the library and the picker use it.
type MediaStore interface {
	Upload(ctx context.Context, userID *uuid.UUID, file io.Reader, filename, mimeType string, size int64) (*media.Media, error)
	Get(ctx context.Context, id uuid.UUID) (*media.Media, error)
	List(ctx context.Context, f *media.ListMediaFilter, limit, offset int) (*media.PagedResult, error)
	Update(ctx context.Context, id uuid.UUID, in *media.UpdateMediaInput) (*media.Media, error)
	Delete(ctx context.Context, id uuid.UUID) error
	Usage(ctx context.Context, m *media.Media) ([]media.Usage, error)
	Config() media.Config
}

// AuthorStore feeds the author picker: the people a byline can credit, and the
// resolver behind "Or add a new author". A name that is the site organisation
// resolves to Org, never to a person.
type AuthorStore interface {
	List(ctx context.Context) ([]*person.Person, error)
	Resolve(ctx context.Context, name string) (entity.Byline, error)
}

// ThemeSection is one section type of the active theme: what the page editor
// offers in "Add section" and builds the section form from.
type ThemeSection struct {
	Type, Title, Description string
	Schema                   json.RawMessage
}

// TemplateStore is sectiontemplate.Service as the page editor uses it: saving a
// section as a template and inserting one.
type TemplateStore interface {
	List(ctx context.Context, f *sectiontemplate.ListFilter, limit, offset int) (*sectiontemplate.ListResult, error)
	Get(ctx context.Context, id uuid.UUID) (*sectiontemplate.SectionTemplate, error)
	Create(ctx context.Context, userID *uuid.UUID, in *sectiontemplate.CreateInput) (*sectiontemplate.SectionTemplate, error)
	RecordUsage(ctx context.Context, id uuid.UUID) error
}

// ContentDeps are the services behind the entity, schema, taxonomy and media
// screens. Any may be nil only in route-table builds.
type ContentDeps struct {
	Entities EntityStore
	Terms    TermStore
	Media    MediaStore
	Authors  AuthorStore
	// PublicPath is the public URL path of an entity ("" when unknown).
	PublicPath func(ctx context.Context, e *entity.Entity) string
	// PublicPaths is PublicPath for many entities of one schema at once (one
	// ancestor query for all of rows, none without nested URLs). routable is
	// false, with nil paths, when the schema has no public URL; rows may be nil
	// to ask only that. Nil falls back to PublicPath per entity.
	PublicPaths func(ctx context.Context, sc *entity.EntitySchema, rows []*entity.Entity) (paths []string, routable bool)
	// Theme lists the active theme's section types; nil (or empty) when page
	// rendering is off, which leaves the page editor without "Add section" and
	// without a preview.
	Theme func() []ThemeSection
	// MenuTheme is what the active theme declares for menu items (styles,
	// colours, badges, icons, class suggestions). Nil, or a zero value, leaves
	// the menu editor with only the class and advanced fields.
	MenuTheme func() render.MenuDecl
	// MenuPreview renders the unsaved menu m, shown at location, inside the
	// site's page as a full HTML document with the Content-Security-Policy to
	// serve it under. It returns ErrMenuPreviewOff when rendering is off. Nil
	// turns the menu preview off.
	MenuPreview func(r *http.Request, location string, m *menu.Menu) (html []byte, csp string, err error)
	// Preview renders unsaved attributes: it answers a POST with the JSON body
	// of /api/v1/render/preview (entity_id, schema, attributes, path, canvas)
	// with the full HTML document. Nil turns the live preview off.
	Preview http.Handler
	// RenderNodes applies builder ops to a draft page doc and renders node
	// fragments for the canvas: it answers a POST with the JSON body of the
	// node render request (the preview body plus ops, render, css, seq).
	// Nil turns the in-place canvas patching off (the editor reloads the frame).
	RenderNodes http.Handler
	// Templates are the section templates; nil hides saving and inserting them.
	Templates TemplateStore
	// Crawlers reads AI-crawler activity for the blog editor's "AI crawlers"
	// card; nil hides the card.
	Crawlers CrawlerStore
	// Recovery keeps the unsaved work of a published post or page per user, so
	// it can be offered back after a closed tab. Nil turns recovery copies
	// off: the routes answer 204 and the editors get no copy.
	Recovery RecoveryStore
}

// RecoveryCopy is one saved recovery copy (see internal/recovery).
type RecoveryCopy = recovery.Copy

// RecoveryStore keeps recovery copies, one per (user, entity).
type RecoveryStore interface {
	Put(ctx context.Context, user, entity uuid.UUID, c RecoveryCopy) error
	// Get returns nil, nil when there is no copy.
	Get(ctx context.Context, user, entity uuid.UUID) (*RecoveryCopy, error)
	Delete(ctx context.Context, user, entity uuid.UUID) error
}
