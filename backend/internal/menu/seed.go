package menu

import (
	"context"
	"log/slog"
)

// EnsureMenuAtLocation creates the menu unless one already holds the location
// (see Repository.EnsureMenuAtLocation). The bool reports whether it was created.
func (s *Service) EnsureMenuAtLocation(ctx context.Context, in EnsureInput) (*Menu, bool, error) {
	if in.Slug == "" {
		in.Slug = generateSlug(in.Name)
	}
	m, created, err := s.ensure(ctx, in)
	if err != nil {
		return nil, false, err
	}
	if created {
		s.logger.Info("seeded menu", slog.String("slug", m.Slug), slog.String("location", in.Location))
		s.notifyChanged(ctx)
	}
	return m, created, nil
}

// SeedDecl creates, for every declared location that has default items, a menu
// titled like the location with slug = location name and those items, unless a
// menu already holds the location. It never updates or deletes a menu. The
// result lists the locations created.
func (s *Service) SeedDecl(ctx context.Context, locs []DeclLocation) ([]string, error) {
	var created []string
	for _, loc := range locs {
		if len(loc.Default) == 0 {
			continue
		}
		items := make([]ReplaceItem, len(loc.Default))
		for i, d := range loc.Default {
			label := d.Label
			it := ReplaceItem{Label: label, Parent: -1}
			if d.Archive != "" {
				schema := d.Archive
				it.LinkType, it.SchemaSlug = LinkTypeArchive, &schema
			} else {
				u := d.URL
				it.LinkType, it.URL = LinkTypeCustom, &u
			}
			items[i] = it
		}
		_, made, err := s.ensure(ctx, EnsureInput{Name: loc.Title, Slug: loc.Name, Location: loc.Name, Items: items})
		if err != nil {
			return created, err
		}
		if made {
			created = append(created, loc.Name)
		}
	}
	if len(created) > 0 {
		s.notifyChanged(ctx)
	}
	return created, nil
}

// ensure is EnsureMenuAtLocation without the change notification, so SeedDecl
// can notify once for the whole batch.
func (s *Service) ensure(ctx context.Context, in EnsureInput) (*Menu, bool, error) {
	if !ValidLocation(in.Location) || in.Location == "" {
		return nil, false, ErrInvalidLocation
	}
	return s.repo.EnsureMenuAtLocation(ctx, in)
}
