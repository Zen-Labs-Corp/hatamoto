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
