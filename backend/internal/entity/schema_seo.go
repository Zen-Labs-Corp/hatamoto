package entity

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SchemaSEOPatch changes the SEO-related settings of a schema. A nil field is
// left alone; an empty string removes the key. FAQFromContent true removes the
// key (the default is on); false stores false.
type SchemaSEOPatch struct {
	ArchiveTitle       *string
	ArchiveDescription *string
	SchemaOrgType      *string
	ArticleType        *string
	FAQFromContent     *bool
}

// UpdateSchemaSEOSettings writes the archive title and description into the
// schema's settings without touching any other key, so the SEO settings page
// and the schema editor edit the same stored values. It runs PatchSchemaSEOTx
// in its own transaction, then the URL registry invalidation.
func (s *Service) UpdateSchemaSEOSettings(ctx context.Context, id uuid.UUID, p SchemaSEOPatch) error {
	if err := s.schemaRepo.InTx(ctx, func(tx pgx.Tx) error {
		return s.PatchSchemaSEOTx(ctx, tx, id, p)
	}); err != nil {
		return err
	}
	s.schemaChanged()
	return nil
}

// PatchSchemaSEOTx is UpdateSchemaSEOSettings inside the caller's transaction:
// it locks the schema row, patches the SEO keys and writes with the same
// validators as UpdateSchema. A built-in type takes the schema type, article
// type and FAQ keys (they never change its shape); its archive keys change only
// on person, like UpdateSchema. It runs no hooks: after the commit the caller
// must call SchemaChanged.
func (s *Service) PatchSchemaSEOTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, p SchemaSEOPatch) error {
	existing, err := s.schemaRepo.GetByIDForUpdate(ctx, tx, id)
	if err != nil {
		return err
	}
	if existing.IsBuiltin && existing.Slug != PersonSchemaSlug && (p.ArchiveTitle != nil || p.ArchiveDescription != nil) {
		return ErrCannotModifyBuiltin
	}
	merged, err := patchSchemaSEOSettings(existing.Settings, p)
	if err != nil {
		return err
	}
	_, err = s.writeSchemaTx(ctx, tx, existing, &UpdateSchemaInput{Settings: &merged}, true)
	return err
}

// SchemaChanged invalidates the URL registry after a committed schema write
// made through PatchSchemaSEOTx.
func (s *Service) SchemaChanged() { s.schemaChanged() }

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
	if err := set("schema_org_type", p.SchemaOrgType); err != nil {
		return nil, err
	}
	if err := set("article_type", p.ArticleType); err != nil {
		return nil, err
	}
	if p.FAQFromContent != nil {
		if *p.FAQFromContent {
			delete(obj, "faq_from_content")
		} else {
			obj["faq_from_content"] = json.RawMessage("false")
		}
	}
	return json.Marshal(obj)
}
