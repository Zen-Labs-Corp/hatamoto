package entity

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/jsonx"
)

// ErrNotPluginOwned reports that the content type, or the entry, is not the
// calling plugin's. It never says whose it is.
var ErrNotPluginOwned = errors.New("not owned by this plugin")

// ErrFieldNotWritable reports an attribute key outside the plugin's entity_fields.
var ErrFieldNotWritable = fmt.Errorf("%w: attribute is not one the plugin may write", ErrValidationFailed)

// PluginOrigin is the origin recorded on entries a plugin creates.
func PluginOrigin(plugin string) string { return "plugin:" + plugin }

// PluginWrite is what a plugin may set on an entry of the one content type it owns.
type PluginWrite struct {
	Schema             string   // the manifest's entity_schema
	Fields             []string // the manifest's entity_fields
	Name, Slug, Locale string   // Slug and Locale: create only
	// Attributes is a JSON object whose keys are all in Fields. On update it is
	// merged into the stored attributes and a null value removes the key.
	Attributes jsontext.Value
	// Status: on create "" publishes at once, else published or draft; on
	// update "" keeps the stored status.
	Status EntityStatus
}

// PluginCreate creates an entry of plugin's own content type, origin
// plugin:<name>, in one transaction. Ownership is decided here, on the schema
// row, at call time: a type of another plugin, an owner-made type (no
// origin_plugin) and a built-in type all answer ErrNotPluginOwned and write
// nothing. It runs no before_save hook and no authorisation; events and the
// live observer fire after the commit.
func (s *Service) PluginCreate(ctx context.Context, plugin string, w PluginWrite) (*Entity, error) {
	if err := checkFields(w.Attributes, w.Fields); err != nil {
		return nil, err
	}
	status, err := pluginStatus(w.Status, true)
	if err != nil {
		return nil, err
	}
	if len(w.Attributes) == 0 {
		w.Attributes = jsontext.Value(`{}`)
	}
	var id uuid.UUID
	var sc *EntitySchema
	err = s.schemaRepo.InTx(ctx, func(tx pgx.Tx) error {
		if sc, err = ownedSchemaTx(ctx, tx, w.Schema, plugin); err != nil {
			return err
		}
		res, err := s.createTx(ctx, tx, TxOp{
			Schema: w.Schema, Name: w.Name, Slug: w.Slug, Locale: w.Locale,
			Attributes: w.Attributes, Status: status, Origin: PluginOrigin(plugin),
		})
		id = res.ID
		return err
	})
	if err != nil {
		return nil, err
	}
	e, err := s.entityRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	e.Schema = sc
	s.reindexLinks([]uuid.UUID{id})
	s.emit(ctx, EventCreated, e, sc.Slug)
	if e.Status == StatusPublished {
		s.fireLive(ctx, id)
		s.emit(ctx, EventPublished, e, sc.Slug, withPrevStatus(StatusDraft))
	}
	return e, nil
}

// PluginUpdate changes an entry the plugin created: its name, the writable
// attributes (merged) and the status. Origin, slug, locale and parent are kept.
// ErrEntityNotFound for an unknown id, ErrNotPluginOwned for an entry of
// another schema or origin.
func (s *Service) PluginUpdate(ctx context.Context, plugin string, id uuid.UUID, w PluginWrite) (*Entity, error) {
	if err := checkFields(w.Attributes, w.Fields); err != nil {
		return nil, err
	}
	status, err := pluginStatus(w.Status, false)
	if err != nil {
		return nil, err
	}
	var sc *EntitySchema
	var before *Entity
	err = s.schemaRepo.InTx(ctx, func(tx pgx.Tx) error {
		if sc, err = ownedSchemaTx(ctx, tx, w.Schema, plugin); err != nil {
			return err
		}
		cur, err := lockOwnedTx(ctx, tx, plugin, sc.ID, id)
		if err != nil {
			return err
		}
		before = cur.entity(sc)
		op := TxOp{ID: &id, Name: w.Name, Status: status}
		if len(w.Attributes) > 0 {
			if op.Attributes, err = mergeAttrs(cur.Attributes, w.Attributes); err != nil {
				return err
			}
		}
		raw, err := jsonx.Marshal(cur)
		if err != nil {
			return err
		}
		_, err = s.updateTx(ctx, tx, op, cur, raw)
		return err
	})
	if err != nil {
		return nil, err
	}
	e, err := s.entityRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	e.Schema = sc
	s.reindexLinks([]uuid.UUID{id})
	s.emitSaved(ctx, before, e)
	return e, nil
}

// PluginDelete removes an entry the plugin created (hard delete; its comments
// go with it) and returns the pre-image. It refuses an entry that has children.
func (s *Service) PluginDelete(ctx context.Context, plugin, schema string, id uuid.UUID) (*Entity, error) {
	var sc *EntitySchema
	var pre *Entity
	err := s.schemaRepo.InTx(ctx, func(tx pgx.Tx) error {
		var err error
		if sc, err = ownedSchemaTx(ctx, tx, schema, plugin); err != nil {
			return err
		}
		cur, err := lockOwnedTx(ctx, tx, plugin, sc.ID, id)
		if err != nil {
			return err
		}
		pre = cur.entity(sc)
		_, err = deleteTx(ctx, tx, cur)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.emit(ctx, EventDeleted, pre, sc.Slug, func(ev *Event) { ev.Entity = pre })
	return pre, nil
}

// ownedSchemaTx finds the content type slug installed by plugin and locks it
// against a concurrent removal. A missing type, another plugin's, an
// owner-made one (origin_plugin NULL) and a built-in one are all the same
// refusal.
func ownedSchemaTx(ctx context.Context, tx pgx.Tx, slug, plugin string) (*EntitySchema, error) {
	sc := &EntitySchema{Slug: slug}
	err := tx.QueryRow(ctx, `SELECT id, name FROM hatamoto_entity_schemas
		WHERE slug = $1 AND origin_plugin = $2 AND NOT is_builtin FOR SHARE`, slug, plugin).Scan(&sc.ID, &sc.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotPluginOwned
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read entity schema: %w", err)
	}
	return sc, nil
}

// lockOwnedTx locks entry id and checks that it is of the owned schema and was
// created by plugin. Another origin (the owner's edit included) is refused.
func lockOwnedTx(ctx context.Context, tx pgx.Tx, plugin string, schemaID, id uuid.UUID) (*entitySnapshot, error) {
	cur := &entitySnapshot{}
	err := cur.scan(tx.QueryRow(ctx, `SELECT `+snapshotColumns+` FROM hatamoto_entities WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrEntityNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read entity: %w", err)
	}
	if cur.SchemaID != schemaID || cur.Origin != PluginOrigin(plugin) {
		return nil, ErrNotPluginOwned
	}
	return cur, nil
}

// entity is the pre-image as an Entity, for the events a write emits.
func (e *entitySnapshot) entity(sc *EntitySchema) *Entity {
	return &Entity{
		ID: e.ID, SchemaID: e.SchemaID, Name: e.Name, Slug: e.Slug, Locale: e.Locale, Status: EntityStatus(e.Status),
		Attributes: e.Attributes, Metadata: e.Metadata, Version: e.Version, CreatedBy: e.CreatedBy, UpdatedBy: e.UpdatedBy,
		CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt, PublishedAt: e.PublishedAt, ParentID: e.ParentID, Schema: sc,
	}
}

// checkFields refuses a key outside fields, and attributes that are not an object.
func checkFields(attrs jsontext.Value, fields []string) error {
	if len(attrs) == 0 {
		return nil
	}
	entries, err := jsonx.ObjectEntries(attrs)
	if err != nil {
		return fmt.Errorf("%w: attributes must be an object", ErrValidationFailed)
	}
	for _, en := range entries {
		if !slices.Contains(fields, en.Key) {
			return ErrFieldNotWritable
		}
	}
	return nil
}

// pluginStatus is the status a plugin may ask for: published or draft only.
func pluginStatus(st EntityStatus, create bool) (EntityStatus, error) {
	switch st {
	case "":
		if create {
			return StatusPublished, nil
		}
		return "", nil
	case StatusPublished, StatusDraft:
		return st, nil
	}
	return "", ErrInvalidStatus
}

// mergeAttrs lays patch over cur at the top level; a null value removes the key.
func mergeAttrs(cur, patch jsontext.Value) (jsontext.Value, error) {
	m := map[string]jsontext.Value{}
	if len(cur) > 0 {
		if err := jsonx.Unmarshal(cur, &m, jsonx.StoredLoose); err != nil {
			return nil, fmt.Errorf("failed to read stored attributes: %w", err)
		}
	}
	entries, err := jsonx.ObjectEntries(patch)
	if err != nil {
		return nil, fmt.Errorf("%w: attributes must be an object", ErrValidationFailed)
	}
	for _, en := range entries {
		if string(en.Value) == "null" {
			delete(m, en.Key)
			continue
		}
		m[en.Key] = en.Value
	}
	return jsonx.Marshal(m)
}
