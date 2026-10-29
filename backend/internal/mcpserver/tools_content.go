package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/taxonomy"
)

const (
	readScope  = hmauth.ScopeReadEntities
	writeScope = hmauth.ScopeWriteEntities
	maxListLen = 100
)

type entitySummary struct {
	ID          uuid.UUID  `json:"id"`
	Schema      string     `json:"schema"`
	Name        string     `json:"name"`
	Slug        string     `json:"slug"`
	Locale      string     `json:"locale"`
	Status      string     `json:"status"`
	URL         string     `json:"url,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	// BylinePersonID / BylineOrg are who the entity credits (see entities_update).
	BylinePersonID *uuid.UUID `json:"byline_person_id,omitempty"`
	BylineOrg      bool       `json:"byline_org,omitempty"`
}

func (s *Server) summary(ctx context.Context, e *entity.Entity, withURL bool) entitySummary {
	sum := entitySummary{ID: e.ID, Name: e.Name, Slug: e.Slug, Locale: e.Locale, Status: string(e.Status), UpdatedAt: e.UpdatedAt, PublishedAt: e.PublishedAt,
		BylinePersonID: e.BylinePersonID, BylineOrg: e.BylineOrg}
	if e.Schema != nil {
		sum.Schema = e.Schema.Slug
	}
	if withURL && e.Status == entity.StatusPublished {
		sum.URL = s.d.PageURL(ctx, e)
	}
	return sum
}

func (s *Server) schemaIDBySlug(ctx context.Context, slug string) (uuid.UUID, error) {
	sc, err := s.d.Entities.GetSchemaBySlug(ctx, slug)
	if err != nil {
		return uuid.Nil, err
	}
	return sc.ID, nil
}

// object is a JSON object argument. A map (not RawMessage) so the generated
// input schema says "object".
type object = map[string]any

func rawObject(o object) json.RawMessage {
	if o == nil {
		o = object{}
	}
	b, _ := json.Marshal(o)
	return b
}

func clampLimit(n, def int) int {
	if n < 1 {
		return def
	}
	return min(n, maxListLen)
}

func (s *Server) registerContent() {
	type listIn struct {
		Schema string `json:"schema,omitempty" jsonschema:"schema slug to filter by"`
		Status string `json:"status,omitempty" jsonschema:"draft, scheduled, published or archived"`
		Search string `json:"search,omitempty" jsonschema:"substring of the name"`
		Page   int    `json:"page,omitempty" jsonschema:"1-based page (default 1)"`
		Limit  int    `json:"limit,omitempty" jsonschema:"page size (default 20, max 100)"`
	}
	register(s, toolSpec{name: "entities_list", description: "List entities (pages, posts, ...), newest first, drafts included.", scope: readScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in listIn) (any, error) {
			f := &entity.ListEntitiesFilter{Search: in.Search}
			if in.Schema != "" {
				id, err := s.schemaIDBySlug(ctx, in.Schema)
				if err != nil {
					return nil, err
				}
				f.SchemaID = &id
			}
			if in.Status != "" {
				st, err := entity.ParseStatus(in.Status)
				if err != nil {
					return nil, err
				}
				f.Status = &st
			}
			res, err := s.d.Entities.ListEntitiesWithOffset(ctx, f, &entity.OffsetPage{Page: max(in.Page, 1), Limit: clampLimit(in.Limit, 20)})
			if err != nil {
				return nil, err
			}
			items := make([]entitySummary, len(res.Items))
			for i, e := range res.Items {
				items[i] = s.summary(ctx, e, false)
			}
			return object{"items": items, "total": res.Total, "page": res.Page, "total_pages": res.TotalPages}, nil
		})

	type idIn struct {
		ID string `json:"id" jsonschema:"entity id (UUID)"`
	}
	register(s, toolSpec{name: "entities_get", description: "Get one entity with its attributes.", scope: readScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in idIn) (any, error) {
			id, err := parseID("id", in.ID)
			if err != nil {
				return nil, err
			}
			e, err := s.d.Entities.GetEntity(ctx, id)
			if err != nil {
				return nil, err
			}
			out := object{"entity": e}
			if e.Status == entity.StatusPublished {
				out["url"] = s.d.PageURL(ctx, e)
			}
			return out, nil
		})

	type searchIn struct {
		Query string `json:"query" jsonschema:"websearch-style query over published content"`
		Page  int    `json:"page,omitempty"`
		Limit int    `json:"limit,omitempty" jsonschema:"page size (default 10, max 100)"`
	}
	register(s, toolSpec{name: "search", description: "Full-text search over published content, best match first (what the site's /search shows).", scope: readScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in searchIn) (any, error) {
			q := strings.TrimSpace(in.Query)
			if q == "" {
				return nil, userErr("query is required")
			}
			items, total, err := s.d.PublicEntities.ListPublishedFiltered(ctx, entity.ListFilter{Search: q}, max(in.Page, 1), clampLimit(in.Limit, 10))
			if err != nil {
				return nil, err
			}
			out := make([]entitySummary, len(items))
			for i, e := range items {
				out[i] = s.summary(ctx, e, true)
			}
			return object{"items": out, "total": total}, nil
		})

	type createIn struct {
		Schema     string `json:"schema" jsonschema:"schema slug (see schemas_list)"`
		Name       string `json:"name"`
		Slug       string `json:"slug,omitempty" jsonschema:"derived from the name when empty"`
		Locale     string `json:"locale,omitempty" jsonschema:"BCP 47 language tag such as en or fr; the site language when empty"`
		Attributes object `json:"attributes,omitempty" jsonschema:"content fields as the schema defines them (see schemas_get)"`
	}
	register(s, toolSpec{name: "entities_create", description: "Create a DRAFT entity (optionally with a locale; a slug is unique per schema and locale). Nothing is public until entities_publish. HTML in rich-text fields is sanitised.", scope: writeScope, write: true},
		func(ctx context.Context, key *hmauth.APIKey, in createIn) (any, error) {
			if strings.TrimSpace(in.Name) == "" {
				return nil, userErr("name is required")
			}
			sc, err := s.d.Entities.GetSchemaBySlug(ctx, in.Schema)
			if err != nil {
				return nil, err
			}
			if err := s.checkPageDocRules(ctx, nil, entity.ReuseRole(sc), in.Attributes); err != nil {
				return nil, err
			}
			e, err := s.d.Entities.CreateEntity(ctx, key.CreatedBy, &entity.CreateEntityInput{
				SchemaID: sc.ID, Name: in.Name, Slug: in.Slug, Locale: in.Locale, Attributes: rawObject(in.Attributes)})
			if err != nil {
				return nil, err
			}
			return s.summary(ctx, e, false), nil
		})

	type updateIn struct {
		ID            string `json:"id"`
		Name          string `json:"name,omitempty"`
		Slug          string `json:"slug,omitempty"`
		Locale        string `json:"locale,omitempty" jsonschema:"BCP 47 language tag; omit to keep the stored locale"`
		Attributes    object `json:"attributes,omitempty" jsonschema:"REPLACES all attributes: send the whole object"`
		ChangeSummary string `json:"change_summary,omitempty"`
		// The byline: a person entity (schema "person") or the site organisation.
		BylinePersonID *string `json:"byline_person_id,omitempty" jsonschema:"id of a person entity to credit as the author"`
		BylineOrg      *bool   `json:"byline_org,omitempty" jsonschema:"true credits the site organisation instead of a person; false clears it"`
		ClearByline    bool    `json:"clear_byline,omitempty" jsonschema:"remove the person and organisation byline"`
	}
	register(s, toolSpec{name: "entities_update", description: "Update an entity's name, slug, locale, attributes and/or byline (a new version is stored). Attributes replace the stored ones, so send the whole object. The byline is byline_person_id (a person entity id), byline_org (true: the site organisation) or clear_byline; omit all three to leave it alone. On a credited entity attributes.author and author_name are overwritten with the credited name; change the author only with these fields.", scope: writeScope, write: true, destructive: true},
		func(ctx context.Context, key *hmauth.APIKey, in updateIn) (any, error) {
			id, err := parseID("id", in.ID)
			if err != nil {
				return nil, err
			}
			up := &entity.UpdateEntityInput{}
			if in.Name != "" {
				up.Name = &in.Name
			}
			if in.Slug != "" {
				up.Slug = &in.Slug
			}
			if in.Locale != "" {
				up.Locale = &in.Locale
			}
			if in.Attributes != nil {
				raw := json.RawMessage(rawObject(in.Attributes))
				up.Attributes = &raw
				stored, err := s.store().GetEntity(ctx, id)
				if err != nil {
					return nil, err
				}
				if err := s.checkPageDocRules(ctx, stored, entity.EntityRole(ctx, s.store(), stored), in.Attributes); err != nil {
					return nil, err
				}
			}
			switch {
			case in.ClearByline:
				up.ClearAuthor = true // clears the person, the organisation and the rollback mirror
			default:
				if in.BylinePersonID != nil {
					pid, err := parseID("byline_person_id", *in.BylinePersonID)
					if err != nil {
						return nil, err
					}
					up.BylinePersonID = &pid
				}
				up.BylineOrg = in.BylineOrg
			}
			summary := "[mcp:" + key.Name + "]"
			if in.ChangeSummary != "" {
				summary += " " + in.ChangeSummary
			}
			up.ChangeSummary = &summary
			e, err := s.d.Entities.UpdateEntity(ctx, id, key.CreatedBy, up)
			if err != nil {
				return nil, err
			}
			return s.summary(ctx, e, true), nil
		})

	setStatus := func(name, desc string, to entity.EntityStatus) {
		register(s, toolSpec{name: name, description: desc, scope: writeScope, write: true, idempotent: true, destructive: to != entity.StatusPublished},
			func(ctx context.Context, key *hmauth.APIKey, in idIn) (any, error) {
				id, err := parseID("id", in.ID)
				if err != nil {
					return nil, err
				}
				e, err := s.d.Entities.UpdateEntityStatus(ctx, id, key.CreatedBy, to)
				if err != nil {
					return nil, err
				}
				if e.Schema == nil {
					if full, gerr := s.d.Entities.GetEntity(ctx, id); gerr == nil {
						e = full
					}
				}
				return s.summary(ctx, e, true), nil
			})
	}
	setStatus("entities_publish", "Publish an entity: it becomes public at its URL, in the sitemap and in llms.txt.", entity.StatusPublished)
	setStatus("entities_unpublish", "Take an entity off the site (back to draft).", entity.StatusDraft)

	// Schemas.
	register(s, toolSpec{name: "schemas_list", description: "List the content types (schemas).", scope: readScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, _ struct{}) (any, error) {
			list, err := s.d.Entities.ListSchemas(ctx)
			if err != nil {
				return nil, err
			}
			type row struct {
				ID          uuid.UUID `json:"id"`
				Slug        string    `json:"slug"`
				Name        string    `json:"name"`
				Description *string   `json:"description,omitempty"`
				Builtin     bool      `json:"is_builtin"`
			}
			out := make([]row, len(list))
			for i, sc := range list {
				out[i] = row{sc.ID, sc.Slug, sc.Name, sc.Description, sc.IsBuiltin}
			}
			return object{"schemas": out}, nil
		})
	type schemaIn struct {
		Slug string `json:"slug" jsonschema:"schema slug"`
	}
	register(s, toolSpec{name: "schemas_get", description: "Get a schema: its JSON Schema (the attributes an entity takes) and URL settings.", scope: readScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in schemaIn) (any, error) {
			return s.d.Entities.GetSchemaBySlug(ctx, in.Slug)
		})

	s.registerMedia()
	s.registerTerms()
	s.registerMenus()
	s.registerRedirects()
}

func (s *Server) registerMedia() {
	type uploadIn struct {
		Filename   string `json:"filename" jsonschema:"original file name; the extension must match the content (jpg, png, gif, webp, pdf, mp4, webm)"`
		DataBase64 string `json:"data_base64" jsonschema:"file content, standard base64"`
		AltText    string `json:"alt_text,omitempty"`
		Caption    string `json:"caption,omitempty"`
	}
	register(s, toolSpec{name: "media_upload", description: "Upload a file to the media library. The type is sniffed from the bytes (not the name) and must be allowed; size is capped like the API's upload.", scope: hmauth.ScopeWriteMedia, write: true},
		func(ctx context.Context, key *hmauth.APIKey, in uploadIn) (any, error) {
			maxBytes := int64(s.d.Media.Config().MaxSizeMB) * 1024 * 1024
			// Check the encoded length first so an oversized payload is never decoded.
			if int64(len(in.DataBase64)) > maxBytes/3*4+8 {
				return nil, userErr("file too large (limit %d MB)", s.d.Media.Config().MaxSizeMB)
			}
			data, err := base64.StdEncoding.DecodeString(in.DataBase64)
			if err != nil {
				return nil, userErr("data_base64 is not valid standard base64")
			}
			if int64(len(data)) > maxBytes {
				return nil, userErr("file too large (limit %d MB)", s.d.Media.Config().MaxSizeMB)
			}
			name := filepath.Base(strings.ReplaceAll(in.Filename, `\`, "/"))
			if name == "." || name == "/" || name == "" {
				return nil, userErr("filename is required")
			}
			m, err := s.d.Media.Upload(ctx, key.CreatedBy, bytes.NewReader(data), name, "", int64(len(data)))
			if err != nil {
				return nil, err
			}
			if in.AltText != "" || in.Caption != "" {
				up := &mediaUpdate{}
				if in.AltText != "" {
					up.AltText = &in.AltText
				}
				if in.Caption != "" {
					up.Caption = &in.Caption
				}
				if m2, uerr := s.d.Media.Update(ctx, m.ID, up); uerr == nil {
					m = m2
				}
			}
			return m, nil
		})
}

func (s *Server) registerTerms() {
	type listIn struct {
		Kind    string `json:"kind,omitempty" jsonschema:"tag or category"`
		Search  string `json:"search,omitempty"`
		Page    int    `json:"page,omitempty"`
		PerPage int    `json:"per_page,omitempty"`
	}
	register(s, toolSpec{name: "terms_list", description: "List tags and categories with how many entities use each.", scope: readScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in listIn) (any, error) {
			return s.d.Taxonomy.ListTerms(ctx, in.Kind, in.Search, max(in.Page, 1), clampLimit(in.PerPage, 50))
		})
	type createIn struct {
		Kind string `json:"kind" jsonschema:"tag or category"`
		Name string `json:"name"`
		Slug string `json:"slug,omitempty"`
	}
	register(s, toolSpec{name: "terms_create", description: "Create a tag or category.", scope: hmauth.ScopeWriteTerms, write: true},
		func(ctx context.Context, _ *hmauth.APIKey, in createIn) (any, error) {
			t, err := s.d.Taxonomy.CreateTerm(ctx, taxonomy.CreateTermInput{Kind: in.Kind, Name: in.Name, Slug: in.Slug})
			if err != nil {
				return nil, termError(err)
			}
			s.d.Changed(ctx)
			return t, nil
		})
}

// termError maps the taxonomy service's validation codes to readable text.
func termError(err error) error {
	switch err.Error() {
	case "invalid_kind":
		return userErr("kind must be tag or category")
	case "invalid_name":
		return userErr("name is required (at most %d characters)", taxonomy.MaxNameLen)
	case "invalid_slug":
		return userErr("slug may only hold lowercase letters, digits and hyphens")
	}
	return err
}

type menuItemIn struct {
	Key          string `json:"key,omitempty" jsonschema:"optional id for the item, needed only when another item names it in parent_key"`
	ParentKey    string `json:"parent_key,omitempty" jsonschema:"key of a parent listed EARLIER in items"`
	Label        string `json:"label"`
	LinkType     string `json:"link_type" jsonschema:"entity, external, archive, custom or none (plain text, no link)"`
	EntityID     string `json:"entity_id,omitempty" jsonschema:"for link_type entity; an item without a target renders as plain text"`
	URL          string `json:"url,omitempty" jsonschema:"for external and custom links"`
	SchemaSlug   string `json:"schema_slug,omitempty" jsonschema:"for archive links"`
	OpenInNewTab bool   `json:"open_in_new_tab,omitempty"`
	CSSClasses   string `json:"css_classes,omitempty" jsonschema:"space separated CSS classes for the item, at most 10"`
	// Attrs holds preset ids the theme declares (style, color, badge, icon),
	// show_on (all, desktop or mobile) and the HTML id, title, aria_label and rel.
	Attrs *menu.MenuItemAttrs `json:"attrs,omitempty"`
}

func (s *Server) registerMenus() {
	register(s, toolSpec{name: "menus_list", description: "List the menus (header, footer, ...).", scope: readScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, _ struct{}) (any, error) {
			ms, err := s.d.Menus.ListMenus(ctx)
			return object{"menus": ms}, err
		})
	type getIn struct {
		ID       string `json:"id,omitempty"`
		Slug     string `json:"slug,omitempty"`
		Location string `json:"location,omitempty" jsonschema:"header, footer or sidebar"`
	}
	register(s, toolSpec{name: "menus_get", description: "Get a menu with its nested items; give one of id, slug or location.", scope: readScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in getIn) (any, error) {
			m, err := s.findMenu(ctx, in.ID, in.Slug, in.Location)
			if err != nil {
				return nil, err
			}
			return s.d.Menus.GetMenuWithItems(ctx, m.ID)
		})
	type updateIn struct {
		ID       string       `json:"id"`
		Name     string       `json:"name,omitempty"`
		Location string       `json:"location,omitempty"`
		Items    []menuItemIn `json:"items,omitempty" jsonschema:"when present REPLACES every item; array order is the display order"`
	}
	register(s, toolSpec{name: "menus_update", description: "Rename a menu, move it to a location and/or replace its items. Items are checked before anything is changed.", scope: hmauth.ScopeWriteMenus, write: true, destructive: true, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in updateIn) (any, error) {
			id, err := parseID("id", in.ID)
			if err != nil {
				return nil, err
			}
			plan, err := planMenuItems(in.Items)
			if err != nil {
				return nil, err
			}
			up := &menu.UpdateMenuInput{}
			if in.Name != "" {
				up.Name = &in.Name
			}
			if in.Location != "" {
				up.Location = &in.Location
			}
			m, err := s.d.Menus.ReplaceMenu(ctx, id, up, plan)
			if err != nil {
				return nil, err
			}
			s.d.Changed(ctx)
			return m, nil
		})
}

func (s *Server) findMenu(ctx context.Context, id, slug, location string) (*menu.Menu, error) {
	switch {
	case id != "":
		u, err := parseID("id", id)
		if err != nil {
			return nil, err
		}
		return s.d.Menus.GetMenu(ctx, u)
	case slug != "":
		return s.d.Menus.GetMenuBySlug(ctx, slug)
	case location != "":
		return s.d.Menus.GetMenuByLocation(ctx, location)
	}
	return nil, userErr("give one of id, slug or location")
}

// planMenuItems validates the whole list before the menu is touched. A nil
// list stays nil (items are left alone).
func planMenuItems(items []menuItemIn) ([]menu.ReplaceItem, error) {
	if items == nil {
		return nil, nil
	}
	index := map[string]int{}
	out := make([]menu.ReplaceItem, 0, len(items))
	for i, it := range items {
		p := menu.ReplaceItem{Label: it.Label, LinkType: it.LinkType, OpenInNewTab: it.OpenInNewTab, Parent: -1}
		if strings.TrimSpace(it.Label) == "" {
			return nil, userErr("items[%d]: label is required", i)
		}
		if !menu.ValidLinkType(it.LinkType) {
			return nil, userErr("items[%d]: link_type must be entity, external, archive, custom or none", i)
		}
		if it.EntityID != "" {
			id, err := parseID("items["+itoa(i)+"].entity_id", it.EntityID)
			if err != nil {
				return nil, err
			}
			p.EntityID = &id
		}
		if it.LinkType == menu.LinkTypeExternal && it.URL == "" {
			return nil, userErr("items[%d]: external links need url", i)
		}
		if it.Attrs != nil {
			if err := it.Attrs.Validate(); err != nil {
				return nil, userErr("items[%d]: %v", i, err)
			}
			p.Attrs = it.Attrs.Normalize()
		}
		if it.CSSClasses != "" {
			classes, err := menu.ValidateCSSClasses(it.CSSClasses)
			if err != nil {
				return nil, userErr("items[%d]: %v", i, err)
			}
			p.CSSClasses = &classes
		}
		if it.URL != "" {
			p.URL = &it.URL
		}
		if it.SchemaSlug != "" {
			p.SchemaSlug = &it.SchemaSlug
		}
		if it.ParentKey != "" {
			parent, ok := index[it.ParentKey]
			if !ok {
				return nil, userErr("items[%d]: parent_key %q must be the key of an earlier item", i, it.ParentKey)
			}
			p.Parent = parent
		}
		if it.Key != "" {
			index[it.Key] = i
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Server) registerRedirects() {
	type listIn struct {
		Search  string `json:"search,omitempty"`
		Page    int    `json:"page,omitempty"`
		PerPage int    `json:"per_page,omitempty"`
	}
	register(s, toolSpec{name: "redirects_list", description: "List redirect rules.", scope: readScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in listIn) (any, error) {
			items, total, err := s.d.Redirects.ListRedirects(ctx, redirect.RedirectListParams{Search: in.Search, Page: max(in.Page, 1), PerPage: clampLimit(in.PerPage, 50)})
			return object{"items": items, "total": total}, err
		})
	type createIn struct {
		SourcePath string `json:"source_path" jsonschema:"path on this site, e.g. /old-page; whole-segment * and a trailing ** are wildcards"`
		TargetURL  string `json:"target_url"`
		StatusCode int    `json:"status_code,omitempty" jsonschema:"301 (default), 302, 307 or 308"`
	}
	register(s, toolSpec{name: "redirects_create", description: "Create a redirect.", scope: hmauth.ScopeWriteRedirects, write: true},
		func(ctx context.Context, _ *hmauth.APIKey, in createIn) (any, error) {
			if in.SourcePath == "" || in.TargetURL == "" {
				return nil, userErr("source_path and target_url are required")
			}
			if in.StatusCode == 0 {
				in.StatusCode = 301
			}
			rd, err := s.d.Redirects.CreateRedirect(ctx, &redirect.CreateRedirectInput{SourcePath: in.SourcePath, TargetURL: in.TargetURL, StatusCode: in.StatusCode})
			if err != nil {
				return nil, err
			}
			s.d.Changed(ctx)
			return rd, nil
		})
	type deleteIn struct {
		ID string `json:"id"`
	}
	register(s, toolSpec{name: "redirects_delete", description: "Delete a redirect.", scope: hmauth.ScopeWriteRedirects, write: true, destructive: true, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in deleteIn) (any, error) {
			id, err := parseID("id", in.ID)
			if err != nil {
				return nil, err
			}
			if err := s.d.Redirects.DeleteRedirect(ctx, id); err != nil {
				return nil, err
			}
			s.d.Changed(ctx)
			return object{"deleted": id}, nil
		})
}

type mediaUpdate = media.UpdateMediaInput

func itoa(i int) string { return strconv.Itoa(i) }
