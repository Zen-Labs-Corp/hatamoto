// Package starter installs the optional starter content of the setup wizard: a
// home page with sections, About and Contact pages, two blog posts, header and
// footer menus and a contact form. Install is idempotent by slug (a re-run
// fills what is missing and touches nothing else) but not one transaction: the
// content services each own their connections, so an interrupted run is
// finished by running it again.
package starter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/siteconfig"
)

// Entities is the slice of entity.Service the installer uses.
type Entities interface {
	GetSchemaBySlug(ctx context.Context, slug string) (*entity.EntitySchema, error)
	GetEntityBySlug(ctx context.Context, slug string) (*entity.Entity, error)
	CreateEntity(ctx context.Context, userID *uuid.UUID, in *entity.CreateEntityInput) (*entity.Entity, error)
	UpdateEntity(ctx context.Context, id uuid.UUID, userID *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error)
	UpdateEntityStatus(ctx context.Context, id uuid.UUID, userID *uuid.UUID, status entity.EntityStatus) (*entity.Entity, error)
}

// Menus is the slice of menu.Service the installer uses.
type Menus interface {
	GetMenuByLocation(ctx context.Context, location string) (*menu.Menu, error)
	GetMenuWithItems(ctx context.Context, menuID uuid.UUID) (*menu.Menu, error)
	CreateMenu(ctx context.Context, in *menu.CreateMenuInput) (*menu.Menu, error)
	CreateMenuItem(ctx context.Context, menuID uuid.UUID, in *menu.CreateMenuItemInput) (*menu.MenuItem, error)
}

// Forms is the slice of form.Service the installer uses.
type Forms interface {
	GetFormBySlug(ctx context.Context, slug string) (*form.Form, error)
	CreateForm(ctx context.Context, in *form.CreateFormInput) (*form.Form, error)
}

// Site is the slice of siteconfig.Store the installer uses.
type Site interface {
	Update(ctx context.Context, in siteconfig.UpdateInput) (*siteconfig.Config, error)
}

// Installer holds the services. Invalidate clears the rendered-page cache.
type Installer struct {
	Entities   Entities
	Menus      Menus
	Forms      Forms
	Site       Site
	Invalidate func(context.Context)
}

// Options personalise the content.
type Options struct {
	SiteName string
	// NotifyEmail receives contact form submissions ("" = none).
	NotifyEmail string
	// UserID is recorded as the author of the created content.
	UserID *uuid.UUID
}

// Result counts what this run created (a second run reports zeros).
type Result struct {
	Pages, Posts, Menus, MenuItems, Forms int
}

// Total is everything created.
func (r Result) Total() int { return r.Pages + r.Posts + r.Menus + r.MenuItems + r.Forms }

// Install creates the starter content.
func (in *Installer) Install(ctx context.Context, o Options) (Result, error) {
	var res Result
	if o.SiteName == "" {
		o.SiteName = "My site"
	}
	pageSchema, err := in.Entities.GetSchemaBySlug(ctx, "page")
	if err != nil {
		return res, fmt.Errorf("page type: %w", err)
	}
	postSchema, err := in.Entities.GetSchemaBySlug(ctx, "post")
	if err != nil {
		return res, fmt.Errorf("post type: %w", err)
	}

	ids := map[string]uuid.UUID{}
	for _, p := range pages(o) {
		id, created, err := in.ensure(ctx, o, pageSchema.ID, p)
		if err != nil {
			return res, fmt.Errorf("page %s: %w", p.slug, err)
		}
		ids[p.slug] = id
		if created {
			res.Pages++
		}
	}
	for _, p := range posts(o) {
		_, created, err := in.ensure(ctx, o, postSchema.ID, p)
		if err != nil {
			return res, fmt.Errorf("post %s: %w", p.slug, err)
		}
		if created {
			res.Posts++
		}
	}

	if err := in.contactForm(ctx, o, &res); err != nil {
		return res, err
	}
	if err := in.menus(ctx, ids, &res); err != nil {
		return res, err
	}

	// The home page doubles as the site homepage.
	tagline := "Built with Hatamoto"
	if _, err := in.Site.Update(ctx, siteconfig.UpdateInput{HomepageEntityID: ptr(ids["home"]), Tagline: &tagline}); err != nil {
		return res, fmt.Errorf("site settings: %w", err)
	}
	if in.Invalidate != nil {
		in.Invalidate(ctx)
	}
	return res, nil
}

func ptr[T any](v T) *T { return &v }

// entry is one entity to ensure.
type entry struct {
	slug, name string
	attrs      map[string]any
	// replaceSeed lets the entry overwrite an existing page that is still the
	// untouched first-boot seed (the home page).
	replaceSeed bool
}

// ensure creates the entity and publishes it, or leaves an existing one alone.
// The one repair: a draft the starter itself created (it carries "starter":
// true) is published, so a run that died between create and publish is
// finished by running it again instead of leaving the page a 404.
func (in *Installer) ensure(ctx context.Context, o Options, schemaID uuid.UUID, e entry) (uuid.UUID, bool, error) {
	existing, err := in.Entities.GetEntityBySlug(ctx, e.slug)
	switch {
	case err == nil:
		if e.replaceSeed && isSeed(existing) {
			b, _ := json.Marshal(e.attrs)
			raw := json.RawMessage(b)
			if _, err := in.Entities.UpdateEntity(ctx, existing.ID, o.UserID, &entity.UpdateEntityInput{Attributes: &raw}); err != nil {
				return uuid.Nil, false, err
			}
			return existing.ID, true, nil
		}
		if existing.Status == entity.StatusDraft && isStarter(existing) {
			if _, err := in.Entities.UpdateEntityStatus(ctx, existing.ID, o.UserID, entity.StatusPublished); err != nil {
				return uuid.Nil, false, err
			}
			return existing.ID, true, nil
		}
		return existing.ID, false, nil
	case !errors.Is(err, entity.ErrEntityNotFound):
		return uuid.Nil, false, err
	}
	attrs, err := json.Marshal(e.attrs)
	if err != nil {
		return uuid.Nil, false, err
	}
	created, err := in.Entities.CreateEntity(ctx, o.UserID, &entity.CreateEntityInput{SchemaID: schemaID, Name: e.name, Slug: e.slug, Attributes: attrs})
	if err != nil {
		return uuid.Nil, false, err
	}
	if _, err := in.Entities.UpdateEntityStatus(ctx, created.ID, o.UserID, entity.StatusPublished); err != nil {
		return uuid.Nil, false, err
	}
	return created.ID, true, nil
}

// isStarter reports content this installer created.
func isStarter(e *entity.Entity) bool {
	var a struct {
		Starter bool `json:"starter"`
	}
	return json.Unmarshal(e.Attributes, &a) == nil && a.Starter
}

// isSeed reports the first-boot home page: a single "welcome" section, either
// as legacy page_sections or as the only component of a page_doc.
func isSeed(e *entity.Entity) bool {
	var a struct {
		Sections []struct {
			ID string `json:"id"`
		} `json:"page_sections"`
		Doc     json.RawMessage `json:"page_doc"`
		Starter bool            `json:"starter"`
	}
	if json.Unmarshal(e.Attributes, &a) != nil || a.Starter {
		return false
	}
	if len(a.Doc) > 0 {
		d, err := builder.Parse(a.Doc)
		if err != nil {
			return false
		}
		cs := builder.Components(d)
		return len(cs) == 1 && cs[0].ID == "welcome"
	}
	return len(a.Sections) == 1 && a.Sections[0].ID == "welcome"
}

func (in *Installer) contactForm(ctx context.Context, o Options, res *Result) error {
	if _, err := in.Forms.GetFormBySlug(ctx, "contact"); err == nil {
		return nil
	} else if !errors.Is(err, form.ErrFormNotFound) {
		return fmt.Errorf("contact form: %w", err)
	}
	desc := "Messages from the contact page"
	settings := form.FormSettings{
		SubmitButtonText: "Send message", SuccessMessage: "Thanks, we will get back to you soon.", StoreSubmissions: true,
	}
	if o.NotifyEmail != "" {
		settings.NotifyEmail = &o.NotifyEmail
	}
	_, err := in.Forms.CreateForm(ctx, &form.CreateFormInput{
		Name: "Contact", Slug: "contact", Description: &desc, Settings: settings,
		Fields: []form.FormField{
			{ID: "name", Type: "text", Label: "Name", Name: "name", Required: true, Position: 1, Width: "full"},
			{ID: "email", Type: "email", Label: "Email", Name: "email", Required: true, Position: 2, Width: "full"},
			{ID: "message", Type: "textarea", Label: "Message", Name: "message", Required: true, Position: 3, Width: "full"},
		},
	})
	if err != nil {
		return fmt.Errorf("contact form: %w", err)
	}
	res.Forms++
	return nil
}

func (in *Installer) menus(ctx context.Context, ids map[string]uuid.UUID, res *Result) error {
	for _, m := range []struct {
		name, slug, location string
		items                []menuItem
	}{
		{"Header", "header", menu.LocationHeader, []menuItem{{"Home", "home", ""}, {"About", "about", ""}, {"Blog", "", "post"}, {"Contact", "contact", ""}}},
		{"Footer", "footer", menu.LocationFooter, []menuItem{{"About", "about", ""}, {"Contact", "contact", ""}}},
	} {
		existing, err := in.Menus.GetMenuByLocation(ctx, m.location)
		switch {
		case err == nil:
			// A menu the starter began but could not finish (an item insert
			// failed) is completed; one the owner has changed is left alone.
			if existing.Slug == m.slug {
				if err := in.completeMenu(ctx, existing.ID, m.slug, m.items, ids, res); err != nil {
					return err
				}
			}
			continue
		case !errors.Is(err, menu.ErrMenuNotFound):
			return fmt.Errorf("menu %s: %w", m.slug, err)
		}
		loc := m.location
		created, err := in.Menus.CreateMenu(ctx, &menu.CreateMenuInput{Name: m.name, Slug: m.slug, Location: &loc})
		if err != nil {
			return fmt.Errorf("menu %s: %w", m.slug, err)
		}
		res.Menus++
		if err := in.completeMenu(ctx, created.ID, m.slug, m.items, ids, res); err != nil {
			return err
		}
	}
	return nil
}

type menuItem struct{ label, slug, archive string }

// completeMenu adds the starter items a menu is missing, keyed by label. It
// does nothing when the menu holds a label the starter never had (the owner
// has been editing it).
func (in *Installer) completeMenu(ctx context.Context, menuID uuid.UUID, slug string, want []menuItem, ids map[string]uuid.UUID, res *Result) error {
	full, err := in.Menus.GetMenuWithItems(ctx, menuID)
	if err != nil {
		return fmt.Errorf("menu %s: %w", slug, err)
	}
	have := map[string]bool{}
	for _, it := range full.Items {
		have[it.Label] = true
	}
	wanted := map[string]bool{}
	for _, it := range want {
		wanted[it.label] = true
	}
	for l := range have {
		if !wanted[l] {
			return nil
		}
	}
	for i, it := range want {
		if have[it.label] {
			continue
		}
		pos := i
		in2 := &menu.CreateMenuItemInput{Label: it.label, Position: &pos}
		if it.archive != "" {
			in2.LinkType, in2.SchemaSlug = menu.LinkTypeArchive, ptr(it.archive)
		} else {
			id := ids[it.slug]
			in2.LinkType, in2.EntityID = menu.LinkTypeEntity, &id
		}
		if _, err := in.Menus.CreateMenuItem(ctx, menuID, in2); err != nil {
			return fmt.Errorf("menu %s item %s: %w", slug, it.label, err)
		}
		res.MenuItems++
	}
	return nil
}
