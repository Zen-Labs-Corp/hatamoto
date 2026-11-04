package packages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/entity"
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
// types of a plugin and removes them again.
type SchemaStore interface {
	CreateSchema(ctx context.Context, input *entity.CreateSchemaInput) (*entity.EntitySchema, error)
	RemovePluginSchemas(ctx context.Context, plugin string) ([]string, error)
	PluginSchemaUses(ctx context.Context, plugin string) ([]entity.PluginSchemaUse, error)
}

// ContentType is an entity type a package installs.
type ContentType struct {
	Slug, Name, Description string
	Schema                  json.RawMessage
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
		if err := json.Unmarshal(raw, &doc); err != nil {
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
// A type that exists is left alone (never overwritten); one taken by something
// else refuses. A failure removes the types this call created.
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
	return nil
}
