package kit

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/packages"
)

// ErrNoAddress is returned when the owner accepted turning comments on but the
// site has no address yet. Nothing is written.
var ErrNoAddress = errors.New("kit: the site has no address, set one before turning comments on")

// RefusedError is returned when the plan holds refusals. Nothing is written.
type RefusedError struct{ Refusals []string }

func (e *RefusedError) Error() string { return "kit refused: " + strings.Join(e.Refusals, "; ") }

// Accepts are the owner's ticks: what the kit may switch on beyond adding content.
type Accepts struct {
	Comments, Policy, ActivateTheme, Homepage bool
	// Extra are the permissions ticked beyond what the kit grants itself
	// (site.scripts and ui.allow:* among them).
	Extra []string
}

// ApplyOptions say who applies the kit and how comments are turned on.
type ApplyOptions struct {
	// Actor is recorded as applied_by; ActorID is the author of created pages.
	Actor   string
	ActorID *uuid.UUID
	// EnableComments turns comments on (the caller's audited path). It is
	// called only with Accepts.Comments.
	EnableComments func(context.Context) error
}

// Result is what one Apply did.
type Result struct {
	// Created counts what was added: plugins, themes, schemas, pages, menus.
	Created map[string]int
	// Filled counts content types that only had missing settings added.
	Filled int
	// Kept is what was already on the site; Skipped what does not apply here.
	Kept, Skipped []string
	// Messages are notes for the owner (what was left off and why).
	Messages []string
	// Checked is the number of items looked at.
	Checked int
	// Recorded is true when the applied row was written.
	Recorded bool

	changed bool
}

func (r *Result) created(what string) {
	r.Created[what]++
	r.changed = true
}

// Total is everything created.
func (r *Result) Total() int {
	n := 0
	for _, c := range r.Created {
		n += c
	}
	return n
}

// Apply applies a plan: ensure-by-key in the order of docs/KITS.md. It never
// overwrites what the site has. It is not one transaction; every step can be
// run again, and the applied row is written last, so a run that stopped
// half-way is finished by planning and applying again. Cache invalidation and
// the audit entry are the caller's.
func Apply(ctx context.Context, d Deps, pl *Plan, a Accepts, o ApplyOptions) (*Result, error) {
	if len(pl.Refusals) > 0 {
		return nil, &RefusedError{Refusals: pl.Refusals}
	}
	turnOn := a.Comments && pl.Comments.Requested && !pl.Comments.AlreadyOn
	if turnOn {
		if d.Site.Address(ctx) == "" {
			return nil, ErrNoAddress
		}
		if o.EnableComments == nil {
			return nil, errors.New("kit: no way to turn comments on was given")
		}
	}
	k := pl.Kit
	res := &Result{Created: map[string]int{}}
	res.Checked = len(pl.Plugins) + len(pl.Themes) + len(pl.Schemas) + len(pl.Pages) + len(pl.Menus)

	if err := applyPackages(ctx, d, pl.Plugins, a, res); err != nil {
		return res, err
	}
	if err := applySchemas(ctx, d, pl, a, res); err != nil {
		return res, err
	}
	if err := applyThemes(ctx, d, pl, a, res); err != nil {
		return res, err
	}
	if err := applyPages(ctx, d, pl, o, res); err != nil {
		return res, err
	}
	if err := applyMenus(ctx, d, pl, res); err != nil {
		return res, err
	}
	if err := applySwitches(ctx, d, pl, a, o, turnOn, res); err != nil {
		return res, err
	}

	res.Messages = append(res.Messages, pl.Messages...)
	if res.changed || pl.Applied == nil || pl.Applied.Version != k.Version {
		sum, err := jsonx.Marshal(map[string]any{"created": res.Created, "filled": res.Filled, "kept": len(res.Kept), "skipped": len(res.Skipped)})
		if err != nil {
			return res, err
		}
		if err := d.Applied.Record(ctx, Applied{Name: k.Name, Version: k.Version, AppliedBy: o.Actor, Summary: sum}); err != nil {
			return res, fmt.Errorf("recording the kit: %w", err)
		}
		res.Recorded = true
	}
	return res, nil
}

// applyPackages installs the plugins (and, for themes, see applyThemes) that
// are not installed yet.
func applyPackages(ctx context.Context, d Deps, steps []PackageStep, a Accepts, res *Result) error {
	for _, st := range steps {
		if st.Action != ActionInstall {
			continue
		}
		grant := slices.Clone(st.Grant)
		for _, p := range st.Extra {
			if slices.Contains(a.Extra, p) {
				grant = append(grant, p)
			}
		}
		if err := d.Packages.Install(ctx, st.Raw, grant); err != nil {
			return fmt.Errorf("installing %s %s: %w", st.Kind, st.Name, err)
		}
		res.created(string(st.Kind) + "s")
	}
	return nil
}

func applyThemes(ctx context.Context, d Deps, pl *Plan, a Accepts, res *Result) error {
	if err := applyPackages(ctx, d, pl.Themes, a, res); err != nil {
		return err
	}
	if !a.ActivateTheme {
		return nil
	}
	for _, st := range pl.Themes {
		if !st.Activate {
			continue
		}
		if active, err := d.Packages.ActiveTheme(ctx); err != nil {
			return err
		} else if active != st.Name {
			if err := d.Packages.Activate(ctx, st.Name); err != nil {
				return fmt.Errorf("activating theme %s: %w", st.Name, err)
			}
			res.Messages = append(res.Messages, fmt.Sprintf("Theme %s is now the active theme.", st.Name))
			res.changed = true
		}
		return nil
	}
	return nil
}

func applySchemas(ctx context.Context, d Deps, pl *Plan, a Accepts, res *Result) error {
	for _, step := range pl.Schemas {
		ks := pl.Kit.Schemas[slices.IndexFunc(pl.Kit.Schemas, func(s packages.KitSchema) bool { return s.Slug == step.Key })]
		switch step.Action {
		case ActionSkip:
			res.Skipped = append(res.Skipped, fmt.Sprintf("content type '%s'", ks.Slug))
		case ActionKeep:
			res.Kept = append(res.Kept, fmt.Sprintf("content type '%s'", ks.Slug))
		case ActionCreate:
			settings, err := settingsFor(ks.Settings)
			if err != nil {
				return err
			}
			_, err = d.Schemas.CreateSchema(ctx, &entity.CreateSchemaInput{Name: ks.Name, Slug: ks.Slug, Schema: ks.Schema, Settings: settings})
			switch {
			case errors.Is(err, entity.ErrSlugAlreadyExists):
				res.Kept = append(res.Kept, fmt.Sprintf("content type '%s'", ks.Slug))
			case err != nil:
				return fmt.Errorf("creating content type %s: %w", ks.Slug, err)
			default:
				res.created("schemas")
			}
		case ActionFill:
			if err := fillSchema(ctx, d, step, ks, res); err != nil {
				return err
			}
		}
	}
	return nil
}

// settingsFor is the kit's schema settings with the community policy forced
// off: the policy is turned on only by the policy switch, with the owner's tick.
func settingsFor(raw jsontext.Value) (jsontext.Value, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var m map[string]any
	if err := jsonx.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if c, ok := m["comments"].(map[string]any); ok {
		if p, ok := c["policy"].(map[string]any); ok {
			p["enabled"] = false
		}
	}
	b, err := jsonx.Marshal(m)
	return jsontext.Value(b), err
}

// fillSchema adds the kit's settings keys a plugin-created content type lacks.
func fillSchema(ctx context.Context, d Deps, step ItemStep, ks packages.KitSchema, res *Result) error {
	cur, err := d.Schemas.GetSchemaBySlug(ctx, ks.Slug)
	if errors.Is(err, entity.ErrSchemaNotFound) {
		return fmt.Errorf("plugin %s did not create content type %s", step.Owner, ks.Slug)
	}
	if err != nil {
		return err
	}
	want, err := settingsFor(ks.Settings)
	if err != nil || len(want) == 0 {
		return err
	}
	var have, add map[string]any
	if len(cur.Settings) > 0 {
		if err := jsonx.Unmarshal(cur.Settings, &have); err != nil {
			return err
		}
	}
	if have == nil {
		have = map[string]any{}
	}
	if err := jsonx.Unmarshal(want, &add); err != nil {
		return err
	}
	if !fillMissing(have, add) {
		res.Kept = append(res.Kept, fmt.Sprintf("content type '%s'", ks.Slug))
		return nil
	}
	b, err := jsonx.Marshal(have)
	if err != nil {
		return err
	}
	v := jsontext.Value(b)
	if _, err := d.Schemas.UpdateSchema(ctx, cur.ID, &entity.UpdateSchemaInput{Settings: &v}); err != nil {
		return fmt.Errorf("updating content type %s: %w", ks.Slug, err)
	}
	res.Filled++
	res.changed = true
	return nil
}

// fillMissing copies the keys of add that have none in have, recursing into
// objects present on both sides. It reports whether have changed.
func fillMissing(have, add map[string]any) bool {
	changed := false
	for k, v := range add {
		cur, ok := have[k]
		if !ok {
			have[k], changed = v, true
			continue
		}
		hm, ok1 := cur.(map[string]any)
		am, ok2 := v.(map[string]any)
		if ok1 && ok2 && fillMissing(hm, am) {
			changed = true
		}
	}
	return changed
}

func applyPages(ctx context.Context, d Deps, pl *Plan, o ApplyOptions, res *Result) error {
	for i, step := range pl.Pages {
		kp := pl.Kit.Pages[i]
		schema := pageSchema(kp)
		existing, err := d.Pages.GetPage(ctx, schema, kp.Slug)
		if err != nil {
			return err
		}
		if existing != nil {
			res.Kept = append(res.Kept, fmt.Sprintf("page '%s'", step.Key))
			continue
		}
		sc, err := d.Schemas.GetSchemaBySlug(ctx, schema)
		if err != nil {
			return fmt.Errorf("page %s: content type %s: %w", kp.Slug, schema, err)
		}
		secs := make([]builder.Section, len(kp.Blocks))
		for j, b := range kp.Blocks {
			secs[j] = builder.Section{ID: fmt.Sprintf("%s-%d", b.Type, j+1), Type: b.Type, SortOrder: j + 1, Content: b.Props}
		}
		attrs, err := jsonx.Marshal(map[string]any{"page_doc": builder.FromSections(secs)})
		if err != nil {
			return err
		}
		if _, err := d.Pages.CreatePublished(ctx, &entity.CreateEntityInput{SchemaID: sc.ID, Name: kp.Name, Slug: kp.Slug, Attributes: attrs}, o.ActorID); err != nil {
			return fmt.Errorf("creating page %s: %w", kp.Slug, err)
		}
		res.created("pages")
	}
	return nil
}

func pageSchema(kp packages.KitPage) string {
	if kp.Schema == "" {
		return "page"
	}
	return kp.Schema
}

func applyMenus(ctx context.Context, d Deps, pl *Plan, res *Result) error {
	for i, step := range pl.Menus {
		km := pl.Kit.Menus[i]
		if step.Action == ActionSkip {
			res.Skipped = append(res.Skipped, fmt.Sprintf("menu '%s'", km.Name))
			continue
		}
		items := make([]menu.ReplaceItem, len(km.Items))
		for j, it := range km.Items {
			url := it.URL
			items[j] = menu.ReplaceItem{Label: it.Label, LinkType: menu.LinkTypeCustom, URL: &url, Parent: -1}
		}
		_, created, err := d.Menus.EnsureMenuAtLocation(ctx, menu.EnsureInput{Name: km.Name, Location: km.Location, Items: items})
		if err != nil {
			return fmt.Errorf("creating menu %s: %w", km.Location, err)
		}
		if created {
			res.created("menus")
		} else {
			res.Kept = append(res.Kept, fmt.Sprintf("menu '%s'", km.Name))
		}
	}
	return nil
}

func applySwitches(ctx context.Context, d Deps, pl *Plan, a Accepts, o ApplyOptions, turnOn bool, res *Result) error {
	if a.Homepage && pl.Homepage.Page != "" {
		if err := setHomepage(ctx, d, pl, res); err != nil {
			return err
		}
	}
	if pl.Comments.Requested && !pl.Comments.AlreadyOn {
		if turnOn {
			if err := o.EnableComments(ctx); err != nil {
				return fmt.Errorf("turning comments on: %w", err)
			}
			res.Messages = append(res.Messages, "Comments are on.")
			res.changed = true
		} else {
			res.Messages = append(res.Messages, "Comments left off: you did not accept the comment warnings.")
		}
	}
	if len(pl.Policy.Requested) > 0 {
		switch {
		case a.Policy && pl.Policy.Available:
			for _, slug := range pl.Policy.Requested {
				if err := d.Policy.SetPolicy(ctx, slug, true); err != nil {
					return fmt.Errorf("community policy for %s: %w", slug, err)
				}
			}
			res.Messages = append(res.Messages, "Community policy is on.")
			res.changed = true
		case pl.Policy.Available:
			res.Messages = append(res.Messages, "Community policy left off: you did not accept its warning.")
		}
	}
	return nil
}

func setHomepage(ctx context.Context, d Deps, pl *Plan, res *Result) error {
	i := slices.IndexFunc(pl.Kit.Pages, func(p packages.KitPage) bool { return p.Slug == pl.Homepage.Page })
	if i < 0 {
		return nil
	}
	page, err := d.Pages.GetPage(ctx, pageSchema(pl.Kit.Pages[i]), pl.Homepage.Page)
	if err != nil || page == nil {
		return fmt.Errorf("homepage page %s: %w", pl.Homepage.Page, errors.Join(err, entity.ErrEntityNotFound))
	}
	if cur, err := d.Site.Homepage(ctx); err != nil {
		return err
	} else if cur != nil && cur.ID == page.ID {
		return nil
	}
	if err := d.Site.SetHomepage(ctx, page.ID); err != nil {
		return fmt.Errorf("setting the homepage: %w", err)
	}
	res.Messages = append(res.Messages, fmt.Sprintf("Page '%s' is now the homepage.", pl.Homepage.Page))
	res.changed = true
	return nil
}
