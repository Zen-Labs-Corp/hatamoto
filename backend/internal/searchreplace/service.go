package searchreplace

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/menu"
)

// Limits of one preview.
const (
	// ScanPage is how many entities are read at a time.
	ScanPage = 100
	// CountCap is where counting stops; the preview then says "more than".
	CountCap = 10000
	// ListCap is how many matches the preview lists.
	ListCap = 500
)

// Entities is the part of entity.Service the tool uses. Updates go through
// UpdateEntity only, so sanitising, ownership, versions, events, link sync and
// cache clearing all run.
type Entities interface {
	ListEntitiesWithOffset(ctx context.Context, filter *entity.ListEntitiesFilter, page *entity.OffsetPage) (*entity.OffsetPagedResult[*entity.Entity], error)
	GetEntity(ctx context.Context, id uuid.UUID) (*entity.Entity, error)
	GetSchema(ctx context.Context, id uuid.UUID) (*entity.EntitySchema, error)
	UpdateEntity(ctx context.Context, id uuid.UUID, userID *uuid.UUID, input *entity.UpdateEntityInput) (*entity.Entity, error)
}

// Menus is the part of menu.Service the tool uses.
type Menus interface {
	ListMenus(ctx context.Context) ([]*menu.Menu, error)
	GetMenuWithItems(ctx context.Context, id uuid.UUID) (*menu.Menu, error)
	GetMenuItem(ctx context.Context, id uuid.UUID) (*menu.MenuItem, error)
	UpdateMenuItem(ctx context.Context, id uuid.UUID, input *menu.UpdateMenuItemInput) (*menu.MenuItem, error)
}

// Owners tells which plugin tracks an entity.
type Owners interface {
	Get(ctx context.Context, id uuid.UUID) (*changeset.Ownership, error)
}

// Service finds and replaces text. Menus and Owners may be nil.
type Service struct {
	Ents   Entities
	Menus  Menus
	Owners Owners
}

// kindsFor is the field-kind lookup of an entity: its schema for attributes
// and the active theme for builder components. Preview and apply both use it.
// A schema that cannot be read leaves the key-name rule in force.
func (s *Service) kindsFor(ctx context.Context, cache map[uuid.UUID]*Kinds, e *entity.Entity) *Kinds {
	if k, ok := cache[e.SchemaID]; ok {
		return k
	}
	k := &Kinds{Component: entity.ComponentFieldKind}
	if sc, err := s.Ents.GetSchema(ctx, e.SchemaID); err == nil && sc != nil {
		k.Attr = schemaKinds(sc.Schema)
	}
	cache[e.SchemaID] = k
	return k
}

// Match is one listed occurrence.
type Match struct {
	Token   string // posted back to select it
	Label   string // where in the item
	Context string
}

// Group is the matches of one item: an entity or a menu.
type Group struct {
	IsMenu      bool
	ID          uuid.UUID
	Name        string
	Locale      string // entities only
	PluginOwned bool
	Plugin      string
	Count       int // every match in the item, listed or not
	Matches     []Match
}

// Preview is the dry run.
type Preview struct {
	Total      int  // matches counted, up to CountCap
	TotalItems int  // items with a match
	Capped     bool // counting stopped at CountCap
	Listed     int  // matches in Groups, at most ListCap
	Groups     []Group
}

// Failure is an item that could not be changed.
type Failure struct {
	Name   string
	Reason string
}

// Report is the result of an apply.
type Report struct {
	Replaced  int // matches replaced
	Items     int // entities changed
	MenuItems int
	Skipped   int // items changed since the preview, or whose match is gone
	Failed    []Failure
}

// Preview lists the matches without changing anything.
func (s *Service) Preview(ctx context.Context, o Options, scopes Scope) (*Preview, error) {
	p := &Preview{}
	if o.Find == "" {
		return p, nil
	}
	if scopes&(ScopeContent|ScopeSEO|ScopeOther) != 0 {
		if err := s.previewEntities(ctx, o, scopes, p); err != nil {
			return nil, err
		}
	}
	if scopes&ScopeMenus != 0 && s.Menus != nil && !p.Capped {
		if err := s.previewMenus(ctx, o, p); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (s *Service) previewEntities(ctx context.Context, o Options, scopes Scope, p *Preview) error {
	kinds := map[uuid.UUID]*Kinds{}
	for page := 1; ; page++ {
		res, err := s.Ents.ListEntitiesWithOffset(ctx, &entity.ListEntitiesFilter{}, &entity.OffsetPage{Page: page, Limit: ScanPage})
		if err != nil {
			return err
		}
		for _, e := range res.Items {
			if err := ctx.Err(); err != nil {
				return err
			}
			_, hits, _, err := Walk(e.Attributes, o, scopes, s.kindsFor(ctx, kinds, e), nil)
			if err != nil || len(hits) == 0 {
				continue
			}
			g := Group{ID: e.ID, Name: e.Name, Locale: e.Locale, Count: len(hits)}
			listing := p.Listed < ListCap
			if listing {
				g.PluginOwned, g.Plugin = s.pluginOwner(ctx, e.ID)
			}
			for _, h := range hits {
				if p.Total >= CountCap {
					p.Capped = true
					break
				}
				p.Total++
				if p.Listed < ListCap {
					g.Matches = append(g.Matches, Match{Token: entityToken(e.ID, e.Version, h.Path, h.Index), Label: matchLabel(h.Path), Context: h.Context})
					p.Listed++
				}
			}
			p.TotalItems++
			if listing {
				p.Groups = append(p.Groups, g)
			}
			if p.Capped {
				return nil
			}
		}
		if page >= res.TotalPages || len(res.Items) == 0 {
			return nil
		}
	}
}

func (s *Service) previewMenus(ctx context.Context, o Options, p *Preview) error {
	menus, err := s.Menus.ListMenus(ctx)
	if err != nil {
		return err
	}
	for _, m := range menus {
		full, err := s.Menus.GetMenuWithItems(ctx, m.ID)
		if err != nil {
			return err
		}
		g := Group{IsMenu: true, ID: m.ID, Name: m.Name}
		walkItems(full.Items, func(it *menu.MenuItem) {
			for _, f := range menuFields(it) {
				_, hits, _ := transform(f.value, o, false, nil)
				for _, h := range hits {
					g.Count++
					if p.Total >= CountCap {
						p.Capped = true
						continue
					}
					p.Total++
					if p.Listed < ListCap {
						g.Matches = append(g.Matches, Match{
							Token:   menuToken(m.ID, it.ID, f.name, h.Index, f.value),
							Label:   fmt.Sprintf("%s: %s", f.title, it.Label),
							Context: h.Context,
						})
						p.Listed++
					}
				}
			}
		})
		if g.Count > 0 {
			p.TotalItems++
			if len(g.Matches) > 0 {
				p.Groups = append(p.Groups, g)
			}
		}
		if p.Capped {
			return nil
		}
	}
	return nil
}

type menuField struct{ name, title, value string }

func menuFields(it *menu.MenuItem) []menuField {
	fs := []menuField{{fieldLabel, "Label", it.Label}}
	if it.URL != nil {
		fs = append(fs, menuField{fieldURL, "Link", *it.URL})
	}
	return fs
}

func walkItems(items []*menu.MenuItem, f func(*menu.MenuItem)) {
	for _, it := range items {
		f(it)
		walkItems(it.Children, f)
	}
}

func (s *Service) pluginOwner(ctx context.Context, id uuid.UUID) (bool, string) {
	if s.Owners == nil {
		return false, ""
	}
	o, err := s.Owners.Get(ctx, id)
	if err != nil || o == nil || o.State != changeset.OwnershipTracked {
		return false, ""
	}
	return true, o.Plugin
}

// matchLabel names where in an item a path points, for people.
func matchLabel(path string) string {
	segs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	top := strings.ReplaceAll(segs[0], "~1", "/")
	if top == "page_doc" {
		return "Page builder: " + segs[len(segs)-1]
	}
	name := strings.ReplaceAll(top, "_", " ")
	if name != "" {
		name = strings.ToUpper(name[:1]) + name[1:]
	}
	if len(segs) > 1 {
		name += " > " + strings.Join(segs[1:], " > ")
	}
	return name
}

// Apply replaces the selected matches. Each changed entity gets one version
// through UpdateEntity, written only when it is still at the version the
// preview saw. Menu items are written only when the value is unchanged.
func (s *Service) Apply(ctx context.Context, actor *uuid.UUID, o Options, scopes Scope, tokens []string) (*Report, error) {
	sel, err := ParseSelection(tokens)
	if err != nil {
		return nil, err
	}
	rep := &Report{}
	if o.Find == "" {
		return rep, nil
	}
	summary := fmt.Sprintf("Search and replace: %q -> %q", truncate(o.Find, 60), truncate(o.Replace, 60))

	ids := make([]uuid.UUID, 0, len(sel.Entities))
	for id := range sel.Entities {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	kinds := map[uuid.UUID]*Kinds{}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		s.applyEntity(ctx, actor, o, scopes, summary, id, sel.Entities[id], kinds, rep)
	}
	if s.Menus != nil {
		s.applyMenus(ctx, o, sel, rep)
	}
	return rep, nil
}

func (s *Service) applyEntity(ctx context.Context, actor *uuid.UUID, o Options, scopes Scope, summary string, id uuid.UUID, es *entitySel, kinds map[uuid.UUID]*Kinds, rep *Report) {
	e, err := s.Ents.GetEntity(ctx, id)
	if err != nil {
		rep.Failed = append(rep.Failed, Failure{Name: id.String(), Reason: err.Error()})
		return
	}
	if e.Version != es.Version {
		rep.Skipped++
		return
	}
	out, _, n, err := Walk(e.Attributes, o, scopes, s.kindsFor(ctx, kinds, e), func(path string, i int) bool { return es.pick(path, i) })
	if err != nil {
		rep.Failed = append(rep.Failed, Failure{Name: e.Name, Reason: err.Error()})
		return
	}
	if n == 0 {
		rep.Skipped++
		return
	}
	v := e.Version
	_, err = s.Ents.UpdateEntity(ctx, id, actor, &entity.UpdateEntityInput{Attributes: &out, ChangeSummary: &summary, ExpectedVersion: &v})
	var conflict *entity.VersionConflictError
	switch {
	case errors.As(err, &conflict):
		rep.Skipped++
	case err != nil:
		rep.Failed = append(rep.Failed, Failure{Name: e.Name, Reason: err.Error()})
	default:
		rep.Replaced += n
		rep.Items++
	}
}

func (s *Service) applyMenus(ctx context.Context, o Options, sel Selection, rep *Report) {
	keys := make([]menuKey, 0, len(sel.Menus))
	for k := range sel.Menus {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if a, b := keys[i].Item.String(), keys[j].Item.String(); a != b {
			return a < b
		}
		return keys[i].Field < keys[j].Field
	})
	changed := map[uuid.UUID]bool{}
	for _, k := range keys {
		if ctx.Err() != nil {
			return
		}
		ms := sel.Menus[k]
		it, err := s.Menus.GetMenuItem(ctx, k.Item)
		if err != nil {
			rep.Failed = append(rep.Failed, Failure{Name: "menu item " + k.Item.String(), Reason: err.Error()})
			continue
		}
		var value string
		in := &menu.UpdateMenuItemInput{}
		switch k.Field {
		case fieldLabel:
			value = it.Label
		case fieldURL:
			if it.URL != nil {
				value = *it.URL
			}
		}
		if valueHash(value) != ms.Hash {
			rep.Skipped++
			continue
		}
		out, _, n := transform(value, o, false, func(i int) bool { return ms.picks[i] })
		if n == 0 {
			rep.Skipped++
			continue
		}
		if k.Field == fieldLabel {
			in.Label = &out
		} else {
			in.URL = &out
		}
		if _, err := s.Menus.UpdateMenuItem(ctx, k.Item, in); err != nil {
			rep.Failed = append(rep.Failed, Failure{Name: "menu item " + it.Label, Reason: err.Error()})
			continue
		}
		rep.Replaced += n
		changed[k.Item] = true
	}
	rep.MenuItems += len(changed)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
