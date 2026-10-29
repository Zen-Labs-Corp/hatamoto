package entity

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

// SchemaSEOPatch changes the archive title and description of a schema. A nil
// field is left alone; an empty string removes the key.
type SchemaSEOPatch struct {
	ArchiveTitle       *string
	ArchiveDescription *string
}

// UpdateSchemaSEOSettings writes the archive title and description into the
// schema's settings without touching any other key, so the SEO settings page
// and the schema editor edit the same stored values. It goes through
// UpdateSchema: the same validators, the built-in guard (a built-in type other
// than person returns ErrCannotModifyBuiltin) and the URL registry invalidation.
func (s *Service) UpdateSchemaSEOSettings(ctx context.Context, id uuid.UUID, p SchemaSEOPatch) error {
	existing, err := s.schemaRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	merged, err := patchSchemaSEOSettings(existing.Settings, p)
	if err != nil {
		return err
	}
	_, err = s.UpdateSchema(ctx, id, &UpdateSchemaInput{Settings: &merged})
	return err
}

// patchSchemaSEOSettings applies p to raw, keeping every key it does not own.
func patchSchemaSEOSettings(raw json.RawMessage, p SchemaSEOPatch) (json.RawMessage, error) {
	obj := map[string]json.RawMessage{}
	if t := strings.TrimSpace(string(raw)); t != "" && t != "null" {
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, settingsErr("", "settings must be a JSON object")
		}
	}
	set := func(key string, v *string) error {
		if v == nil {
			return nil
		}
		val := strings.TrimSpace(*v)
		if val == "" {
			delete(obj, key)
			return nil
		}
		b, err := json.Marshal(val)
		if err != nil {
			return err
		}
		obj[key] = b
		return nil
	}
	if err := set("archive_title", p.ArchiveTitle); err != nil {
		return nil, err
	}
	if err := set("archive_description", p.ArchiveDescription); err != nil {
		return nil, err
	}
	return json.Marshal(obj)
}
