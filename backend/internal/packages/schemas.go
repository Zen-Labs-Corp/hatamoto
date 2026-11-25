package packages

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

// A plugin package may ship entity types: schemas/<slug>.json, a JSON Schema
// document (its "title" names the type, its "description" describes it) with
// no matching sections/<slug>.html. They are created when the plugin is
// enabled, kept when it is disabled, and removed on uninstall only when the
// owner asks for it. A schema with a section template is a page-builder
// section, not a content type.
const (
	schemasDir      = "schemas"
	maxPackSchemas  = 16
	schemaOriginCol = "origin_plugin"
)

var schemaSlugRe = regexp.MustCompile(`^[a-z]([a-z0-9]|-[a-z0-9]){0,63}$`)

// SchemaStore is the entity service as the manager uses it: it creates the
// types of a plugin, extends them and removes them again.
type SchemaStore interface {
	UpdateSchema(ctx context.Context, id uuid.UUID, input *entity.UpdateSchemaInput) (*entity.EntitySchema, error)
	CreateSchema(ctx context.Context, input *entity.CreateSchemaInput) (*entity.EntitySchema, error)
	RemovePluginSchemas(ctx context.Context, plugin string) ([]string, error)
	PluginSchemaUses(ctx context.Context, plugin string) ([]entity.PluginSchemaUse, error)
}

// ContentType is an entity type a package installs.
type ContentType struct {
	Slug, Name, Description string
	Schema                  jsontext.Value
}

// EntitySchemas returns the content types of p: the schemas/<slug>.json files
// that have no sections/<slug>.html, sorted by slug.
func (p *Package) EntitySchemas() ([]ContentType, error) {
	if p.Kind != KindPlugin {
		return nil, nil
	}
	files, err := fs.Glob(p.FS(), schemasDir+"/*.json")
	if err != nil {
		return nil, err
	}
	var out []ContentType
	for _, f := range files {
		slug := strings.TrimSuffix(path.Base(f), ".json")
		if p.Has("sections/" + slug + ".html") {
			continue
		}
		if len(slug) > 64 || !schemaSlugRe.MatchString(slug) {
			return nil, refuse("%s: the content type name must be lowercase letters, digits and dashes, starting with a letter", f)
		}
		raw, err := p.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var doc struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		if err := jsonx.UnmarshalPinned(raw, &doc); err != nil {
			return nil, refuse("%s: %v", f, err)
		}
		ct := ContentType{Slug: slug, Name: doc.Title, Description: doc.Description, Schema: raw}
		if ct.Name == "" {
			ct.Name = strings.ToUpper(slug[:1]) + strings.ReplaceAll(slug[1:], "-", " ")
		}
		out = append(out, ct)
	}
	if len(out) > maxPackSchemas {
		return nil, refuse("%d content types, at most %d per package", len(out), maxPackSchemas)
	}
	return out, nil
}

// ContentTypeNames lists the names of the types p installs, for the prompt and
// the plugin page. A package that cannot be read lists none.
func ContentTypeNames(p *Package) []string {
	cts, _ := p.EntitySchemas()
	names := make([]string, 0, len(cts))
	for _, ct := range cts {
		names = append(names, ct.Name)
	}
	return names
}

// ErrContentTypeTaken is returned when a plugin's content type slug is already
// used by something that is not this plugin's.
var ErrContentTypeTaken = errors.New("content type already exists")

// schemaOwners reads who owns each slug in slugs: the plugin name, "" for an
// owner-created or built-in type. Slugs not in the map are free.
func (m *Manager) schemaOwners(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, slugs []string) (map[string]string, error) {
	rows, err := q.Query(ctx, `SELECT slug, COALESCE(`+schemaOriginCol+`, '') FROM hatamoto_entity_schemas WHERE slug = ANY($1)`, slugs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var slug, owner string
		if err := rows.Scan(&slug, &owner); err != nil {
			return nil, err
		}
		out[slug] = owner
	}
	return out, rows.Err()
}

// checkSchemas refuses a package whose content type names are taken by
// something else. It writes nothing.
func (m *Manager) checkSchemas(ctx context.Context, p *Package) error {
	cts, err := p.EntitySchemas()
	if err != nil || len(cts) == 0 || m.Schemas == nil {
		return err
	}
	slugs := make([]string, len(cts))
	for i, ct := range cts {
		slugs[i] = ct.Slug
	}
	owners, err := m.schemaOwners(ctx, m.Pool, slugs)
	if err != nil {
		return err
	}
	for _, ct := range cts {
		if owner, taken := owners[ct.Slug]; taken && owner != p.Name {
			return fmt.Errorf("content type %s already exists: %w", ct.Slug, ErrContentTypeTaken)
		}
	}
	return nil
}

// installSchemas creates the content types of the plugin that do not exist yet.
// A type this plugin already owns gains the properties it lacks and nothing
// else changes; one taken by something else refuses. A failure while creating
// removes the types this call created. Extending is best effort: a failure is
// logged and the stored schema is kept.
func (m *Manager) installSchemas(ctx context.Context, p *Package) error {
	if m.Schemas == nil {
		return nil
	}
	if err := m.checkSchemas(ctx, p); err != nil {
		return err
	}
	cts, err := p.EntitySchemas()
	if err != nil || len(cts) == 0 {
		return err
	}
	slugs := make([]string, len(cts))
	for i, ct := range cts {
		slugs[i] = ct.Slug
	}
	owners, err := m.schemaOwners(ctx, m.Pool, slugs)
	if err != nil {
		return err
	}
	var created []string
	for _, ct := range cts {
		if _, ok := owners[ct.Slug]; ok {
			continue
		}
		in := &entity.CreateSchemaInput{Name: ct.Name, Slug: ct.Slug, Schema: ct.Schema, OriginPlugin: p.Name}
		if ct.Description != "" {
			in.Description = &ct.Description
		}
		if _, err := m.Schemas.CreateSchema(ctx, in); err != nil {
			if len(created) > 0 {
				_, _ = m.Pool.Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE slug = ANY($1) AND `+schemaOriginCol+` = $2`, created, p.Name)
			}
			return fmt.Errorf("content type %s: %w", ct.Slug, err)
		}
		created = append(created, ct.Slug)
	}
	if len(created) > 0 {
		m.Audit(ctx, "package.schemas.install", "plugin:"+p.Name, map[string]any{"content_types": created})
	}
	m.extendSchemas(ctx, p, cts)
	return nil
}

// TypeChange is what an install would do to one content type the plugin already owns.
type TypeChange struct {
	Slug, Name string // Name: the stored row's name (the owner may have renamed it)
	ID         uuid.UUID
	SchemaMerge
}

// schemaChanges merges each shipped schema into the stored schema of the same
// slug owned by p (origin_plugin = p.Name). Types owned by the site or another
// plugin are never read. A type whose JSON cannot be merged is logged and skipped.
func (m *Manager) schemaChanges(ctx context.Context, p *Package, cts []ContentType) ([]TypeChange, error) {
	if len(cts) == 0 {
		return nil, nil
	}
	slugs := make([]string, len(cts))
	for i, ct := range cts {
		slugs[i] = ct.Slug
	}
	rows, err := m.Pool.Query(ctx, `SELECT id, slug, name, schema FROM hatamoto_entity_schemas WHERE slug = ANY($1) AND `+schemaOriginCol+` = $2`, slugs, p.Name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type row struct {
		id     uuid.UUID
		name   string
		schema []byte
	}
	stored := map[string]row{}
	for rows.Next() {
		var r row
		var slug string
		if err := rows.Scan(&r.id, &slug, &r.name, &r.schema); err != nil {
			return nil, err
		}
		stored[slug] = r
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []TypeChange
	for _, ct := range cts {
		r, ok := stored[ct.Slug]
		if !ok {
			continue
		}
		merge, err := MergeSchema(r.schema, ct.Schema)
		if err != nil {
			m.Logger.Error("packages: content type cannot be merged; the stored schema is kept", "plugin", p.Name, "content_type", ct.Slug, "err", err)
			continue
		}
		out = append(out, TypeChange{Slug: ct.Slug, Name: r.name, ID: r.id, SchemaMerge: merge})
	}
	return out, nil
}

// extendSchemas adds the new shipped properties to the types p already owns.
// The read in schemaChanges and the UpdateSchema write are not one lock: an
// admin saving the same type in that window is overwritten (accepted race).
func (m *Manager) extendSchemas(ctx context.Context, p *Package, cts []ContentType) {
	changes, err := m.schemaChanges(ctx, p, cts)
	if err != nil {
		m.Logger.Error("packages: content types not extended", "plugin", p.Name, "err", err)
		return
	}
	added := map[string][]string{}
	kept := map[string][]string{}
	for _, tc := range changes {
		others := append(slices.Clone(tc.Retyped), tc.Missing...)
		if len(others) > 0 {
			m.Logger.Warn("packages: content type fields kept as stored", "plugin", p.Name, "content_type", tc.Slug, "retyped", tc.Retyped, "not_shipped", tc.Missing)
		}
		if len(tc.Added) == 0 {
			continue
		}
		merged := tc.Merged
		if _, err := m.Schemas.UpdateSchema(ctx, tc.ID, &entity.UpdateSchemaInput{Schema: &merged}); err != nil {
			m.Logger.Error("packages: content type not extended; the stored schema is kept", "plugin", p.Name, "content_type", tc.Slug, "err", err)
			continue
		}
		added[tc.Slug] = tc.Added
		if len(others) > 0 {
			kept[tc.Slug] = others
		}
	}
	if len(added) > 0 {
		m.Audit(ctx, "package.schemas.extend", "plugin:"+p.Name, map[string]any{"content_types": added, "kept": kept})
	}
}

// SchemaPreview says, without writing, what installing p would do to content
// types: the names of the types it would create (slug not taken by anyone) and
// the owned types it would extend (Added non-empty and the merged schema valid).
func (m *Manager) SchemaPreview(ctx context.Context, p *Package) (create []string, extend []TypeChange, err error) {
	if m.Schemas == nil || m.Pool == nil || p.Kind != KindPlugin {
		return nil, nil, nil
	}
	cts, err := p.EntitySchemas()
	if err != nil || len(cts) == 0 {
		return nil, nil, err
	}
	slugs := make([]string, len(cts))
	for i, ct := range cts {
		slugs[i] = ct.Slug
	}
	owners, err := m.schemaOwners(ctx, m.Pool, slugs)
	if err != nil {
		return nil, nil, err
	}
	for _, ct := range cts {
		if _, taken := owners[ct.Slug]; !taken {
			create = append(create, ct.Name)
		}
	}
	changes, err := m.schemaChanges(ctx, p, cts)
	if err != nil {
		return nil, nil, err
	}
	v := entity.NewValidator()
	for _, tc := range changes {
		if len(tc.Added) > 0 && v.ValidateSchema(tc.Merged) == nil {
			extend = append(extend, tc)
		}
	}
	return create, extend, nil
}
