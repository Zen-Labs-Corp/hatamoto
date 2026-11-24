package pluginhost

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"slices"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
)

// EntityWriter is the write side of the entity service a plugin reaches through
// entities.create, entities.update and entities.delete. *entity.Service
// implements it; every method decides ownership itself.
type EntityWriter interface {
	PluginCreate(ctx context.Context, plugin string, w entity.PluginWrite) (*entity.Entity, error)
	PluginUpdate(ctx context.Context, plugin string, id uuid.UUID, w entity.PluginWrite) (*entity.Entity, error)
	PluginDelete(ctx context.Context, plugin, schema string, id uuid.UUID) (*entity.Entity, error)
}

// entityWriteAudit actions, one audit row per successful write.
const (
	auditEntityCreate = "plugin.entity.create"
	auditEntityUpdate = "plugin.entity.update"
	auditEntityDelete = "plugin.entity.delete"
)

// The write functions come in this order: unavailable, argument checks (they do
// not spend a rate token), the per-plugin limiter, the service call without the
// visitor's subject (a plugin write is not a person's action), the audit row.

func fnEntitiesCreate(ctx context.Context, a *API, e *callEnv, raw jsontext.Value) (any, error) {
	if a.svc.EntityWrites == nil {
		return nil, unavailable(sdk.FnEntitiesCreate)
	}
	in, err := decodeArgs[sdk.EntityWrite](raw)
	if err != nil {
		return nil, err
	}
	if in.ID != "" {
		return nil, invalid("id is for entities.update")
	}
	w, err := entityWriteArgs(e, in, true)
	if err != nil {
		return nil, err
	}
	if err := a.allowWrite(e.plugin, sdk.FnEntitiesCreate); err != nil {
		return nil, err
	}
	ent, err := a.svc.EntityWrites.PluginCreate(authzctx.Without(ctx), e.plugin, w)
	if err != nil {
		return nil, entityWriteErr(sdk.FnEntitiesCreate, err)
	}
	a.audit(ctx, e.plugin, auditEntityCreate, ent.ID.String(), map[string]any{"plugin": e.plugin, "schema": w.Schema, "name": ent.Name})
	return a.entityDTO(ctx, ent), nil
}

func fnEntitiesUpdate(ctx context.Context, a *API, e *callEnv, raw jsontext.Value) (any, error) {
	if a.svc.EntityWrites == nil {
		return nil, unavailable(sdk.FnEntitiesUpdate)
	}
	in, err := decodeArgs[sdk.EntityWrite](raw)
	if err != nil {
		return nil, err
	}
	id, err := parseID(in.ID)
	if err != nil {
		return nil, err
	}
	if in.Slug != "" || in.Locale != "" {
		return nil, invalid("slug and locale are set at create and cannot change")
	}
	w, err := entityWriteArgs(e, in, false)
	if err != nil {
		return nil, err
	}
	if err := a.allowWrite(e.plugin, sdk.FnEntitiesUpdate); err != nil {
		return nil, err
	}
	ent, err := a.svc.EntityWrites.PluginUpdate(authzctx.Without(ctx), e.plugin, id, w)
	if err != nil {
		return nil, entityWriteErr(sdk.FnEntitiesUpdate, err)
	}
	fields := []string{}
	if entries, err := jsonx.ObjectEntries(w.Attributes); err == nil {
		for _, en := range entries {
			fields = append(fields, en.Key)
		}
	}
	if w.Name != "" {
		fields = append(fields, "name")
	}
	if w.Status != "" {
		fields = append(fields, "status")
	}
	slices.Sort(fields)
	a.audit(ctx, e.plugin, auditEntityUpdate, ent.ID.String(), map[string]any{"plugin": e.plugin, "schema": w.Schema, "fields": fields})
	return a.entityDTO(ctx, ent), nil
}

func fnEntitiesDelete(ctx context.Context, a *API, e *callEnv, raw jsontext.Value) (any, error) {
	if a.svc.EntityWrites == nil {
		return nil, unavailable(sdk.FnEntitiesDelete)
	}
	in, err := decodeArgs[sdk.EntityDeleteArgs](raw)
	if err != nil {
		return nil, err
	}
	id, err := parseID(in.ID)
	if err != nil {
		return nil, err
	}
	if e.manifest.EntitySchema == "" {
		return nil, invalid("the plugin declares no entity_schema")
	}
	if err := a.allowWrite(e.plugin, sdk.FnEntitiesDelete); err != nil {
		return nil, err
	}
	pre, err := a.svc.EntityWrites.PluginDelete(authzctx.Without(ctx), e.plugin, e.manifest.EntitySchema, id)
	if err != nil {
		return nil, entityWriteErr(sdk.FnEntitiesDelete, err)
	}
	a.audit(ctx, e.plugin, auditEntityDelete, id.String(), map[string]any{"plugin": e.plugin, "schema": e.manifest.EntitySchema, "name": pre.Name})
	return struct{}{}, nil
}

// entityWriteArgs checks a create or update call against the manifest and the
// size caps and returns what the service takes.
func entityWriteArgs(e *callEnv, in sdk.EntityWrite, create bool) (entity.PluginWrite, error) {
	if e.manifest.EntitySchema == "" {
		return entity.PluginWrite{}, invalid("the plugin declares no entity_schema")
	}
	if err := text("name", in.Name, maxNameLen, create); err != nil {
		return entity.PluginWrite{}, err
	}
	if err := text("slug", in.Slug, maxSlugLen, false); err != nil {
		return entity.PluginWrite{}, err
	}
	if err := text("locale", in.Locale, maxLocaleLen, false); err != nil {
		return entity.PluginWrite{}, err
	}
	switch in.Status {
	case "", string(entity.StatusPublished), string(entity.StatusDraft):
	default:
		return entity.PluginWrite{}, invalid("status must be %q or %q", entity.StatusPublished, entity.StatusDraft)
	}
	strs := []string{in.Name, in.Slug, in.Locale}
	if len(in.Attributes) > 0 {
		entries, err := jsonx.ObjectEntries(in.Attributes)
		if err != nil {
			return entity.PluginWrite{}, invalid("attributes must be a JSON object")
		}
		for _, en := range entries {
			if !slices.Contains(e.manifest.EntityFields, en.Key) {
				return entity.PluginWrite{}, invalid("attribute %q is not in the plugin's entity_fields", en.Key)
			}
		}
	}
	if changeset.WriteTooLarge(strs, in.Attributes) {
		return entity.PluginWrite{}, invalid("the entry is too large (a string over %d bytes, over %d strings, or attributes over %d bytes)",
			changeset.MaxCreateString, changeset.MaxCreateStrings, changeset.MaxCreateAttributes)
	}
	return entity.PluginWrite{
		Schema: e.manifest.EntitySchema, Fields: e.manifest.EntityFields,
		Name: in.Name, Slug: in.Slug, Locale: in.Locale,
		Attributes: in.Attributes, Status: entity.EntityStatus(in.Status),
	}, nil
}

// allowWrite takes a token from the plugin's write limiter (60 per minute,
// burst 20, shared by the three functions).
func (a *API) allowWrite(plugin, fn string) error {
	if !a.limits(plugin).writes.Allow() {
		return herr(sdk.ErrRateLimited, "%s: 60 per minute", fn)
	}
	return nil
}

// entityWriteErr maps the service's ownership refusal; the message names
// neither the content type nor the entry's origin.
func entityWriteErr(fn string, err error) error {
	if errors.Is(err, entity.ErrNotPluginOwned) {
		return herr(sdk.ErrNotOwned, "%s: not this plugin's content type or entry", fn)
	}
	return err
}

// audit records a successful plugin write; it never fails the call.
func (a *API) audit(ctx context.Context, plugin, action, target string, detail map[string]any) {
	if a.svc.Audit != nil {
		a.svc.Audit(ctx, plugin, action, target, detail)
	}
}
