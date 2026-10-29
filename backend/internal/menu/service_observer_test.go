package menu

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
)

// stubRepo implements just what the write paths under test call.
type stubRepo struct {
	Repository
	fail error
}

func (r stubRepo) CreateMenu(context.Context, *CreateMenuInput) (*Menu, error) {
	return &Menu{ID: uuid.New()}, r.fail
}
func (r stubRepo) UpdateMenu(context.Context, uuid.UUID, *UpdateMenuInput) (*Menu, error) {
	return &Menu{ID: uuid.New()}, r.fail
}
func (r stubRepo) DeleteMenu(context.Context, uuid.UUID) error { return r.fail }
func (r stubRepo) DeleteMenuItem(context.Context, uuid.UUID) error {
	return r.fail
}
func (r stubRepo) ReplaceMenu(context.Context, uuid.UUID, *UpdateMenuInput, []ReplaceItem) error {
	return r.fail
}
func (r stubRepo) GetMenuWithItems(context.Context, uuid.UUID) (*Menu, error) { return &Menu{}, nil }
func (r stubRepo) GetMenuByID(context.Context, uuid.UUID) (*Menu, error)      { return &Menu{}, nil }
func (r stubRepo) CreateMenuItem(context.Context, uuid.UUID, *CreateMenuItemInput) (*MenuItem, error) {
	return &MenuItem{ID: uuid.New()}, r.fail
}
func (r stubRepo) UpdateMenuItem(context.Context, uuid.UUID, *UpdateMenuItemInput) (*MenuItem, error) {
	return &MenuItem{ID: uuid.New()}, r.fail
}
func (r stubRepo) ReorderItems(context.Context, uuid.UUID, []ReorderInput) error { return r.fail }

func TestServiceWritesNotifyChangeObserver(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	id := uuid.New()
	writes := map[string]func(*Service) error{
		"CreateMenu": func(s *Service) error { _, err := s.CreateMenu(ctx, &CreateMenuInput{Name: "Main"}); return err },
		"UpdateMenu": func(s *Service) error { _, err := s.UpdateMenu(ctx, id, &UpdateMenuInput{}); return err },
		"ReplaceMenu": func(s *Service) error {
			_, err := s.ReplaceMenu(ctx, id, &UpdateMenuInput{}, nil)
			return err
		},
		"DeleteMenu": func(s *Service) error { return s.DeleteMenu(ctx, id) },
		"CreateMenuItem": func(s *Service) error {
			_, err := s.CreateMenuItem(ctx, id, &CreateMenuItemInput{Label: "x", LinkType: LinkTypeCustom})
			return err
		},
		"UpdateMenuItem": func(s *Service) error { _, err := s.UpdateMenuItem(ctx, id, &UpdateMenuItemInput{}); return err },
		"DeleteMenuItem": func(s *Service) error { return s.DeleteMenuItem(ctx, id) },
		"ReorderItems":   func(s *Service) error { return s.ReorderItems(ctx, id, nil) },
	}
	for name, write := range writes {
		calls := 0
		s := NewService(stubRepo{}, log)
		s.SetChangeObserver(func(context.Context) { calls++ })
		if err := write(s); err != nil || calls != 1 {
			t.Errorf("%s: err=%v calls=%d, want one notification", name, err, calls)
		}
		// A refused write must not purge.
		calls = 0
		s = NewService(stubRepo{fail: errors.New("boom")}, log)
		s.SetChangeObserver(func(context.Context) { calls++ })
		if err := write(s); err == nil || calls != 0 {
			t.Errorf("%s (failing): err=%v calls=%d, want an error and no notification", name, err, calls)
		}
	}
}

// captureRepo records what the service hands to the repository.
type captureRepo struct {
	stubRepo
	replaced []ReplaceItem
	created  *CreateMenuItemInput
	updated  *UpdateMenuItemInput
}

func (r *captureRepo) ReplaceMenu(_ context.Context, _ uuid.UUID, _ *UpdateMenuInput, items []ReplaceItem) error {
	r.replaced = items
	return nil
}
func (r *captureRepo) CreateMenuItem(_ context.Context, _ uuid.UUID, in *CreateMenuItemInput) (*MenuItem, error) {
	r.created = in
	return &MenuItem{ID: uuid.New()}, nil
}
func (r *captureRepo) UpdateMenuItem(_ context.Context, _ uuid.UUID, in *UpdateMenuItemInput) (*MenuItem, error) {
	r.updated = in
	return &MenuItem{ID: uuid.New()}, nil
}

func TestServiceCleansItemSettings(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	id := uuid.New()
	classes := "  a   b "
	url, ent := "#", uuid.New()

	repo := &captureRepo{}
	s := NewService(repo, log)
	_, err := s.ReplaceMenu(ctx, id, nil, []ReplaceItem{
		{Label: "Product", LinkType: LinkTypeNone, URL: &url, EntityID: &ent, CSSClasses: &classes, Attrs: MenuItemAttrs{ShowOn: "all"}, Parent: -1},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := repo.replaced[0]
	if got.URL != nil || got.EntityID != nil || got.SchemaSlug != nil {
		t.Errorf("none item kept a target: %+v", got)
	}
	if got.CSSClasses == nil || *got.CSSClasses != "a b" || got.Attrs.ShowOn != "" {
		t.Errorf("settings not normalised: classes=%v attrs=%+v", got.CSSClasses, got.Attrs)
	}
	if classes != "  a   b " || url != "#" {
		t.Error("the caller's values were modified")
	}

	// A nil list still means "leave the items alone".
	if _, err := s.ReplaceMenu(ctx, id, &UpdateMenuInput{}, nil); err != nil || repo.replaced != nil {
		t.Errorf("nil items: err=%v replaced=%v", err, repo.replaced)
	}

	bad := "a<b"
	for name, call := range map[string]func() error{
		"replace classes": func() error {
			_, err := s.ReplaceMenu(ctx, id, nil, []ReplaceItem{{Label: "x", LinkType: LinkTypeCustom, CSSClasses: &bad}})
			return err
		},
		"replace attrs": func() error {
			_, err := s.ReplaceMenu(ctx, id, nil, []ReplaceItem{{Label: "x", LinkType: LinkTypeCustom, Attrs: MenuItemAttrs{Rel: []string{"x"}}}})
			return err
		},
		"create": func() error {
			_, err := s.CreateMenuItem(ctx, id, &CreateMenuItemInput{Label: "x", LinkType: LinkTypeNone, CSSClasses: &bad})
			return err
		},
		"update": func() error {
			_, err := s.UpdateMenuItem(ctx, id, &UpdateMenuItemInput{Attrs: &MenuItemAttrs{Color: "#fff"}})
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrInvalidAttrs) {
			t.Errorf("%s: err = %v, want ErrInvalidAttrs", name, err)
		}
	}

	if _, err := s.CreateMenuItem(ctx, id, &CreateMenuItemInput{Label: "x", LinkType: LinkTypeNone, URL: &url}); err != nil {
		t.Fatal(err)
	}
	if repo.created.URL != nil || repo.created.Attrs == nil {
		t.Errorf("create input not cleaned: %+v", repo.created)
	}
}
