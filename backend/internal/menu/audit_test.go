package menu

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
)

func TestServiceWritesCallAuditHookOnce(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	id := uuid.New()
	loc := "header"
	name := "Main"
	cases := []struct {
		name   string
		action string
		keys   []string
		write  func(*Service) error
	}{
		{"CreateMenu", "menu.created", []string{"name"}, func(s *Service) error {
			_, err := s.CreateMenu(ctx, &CreateMenuInput{Name: "Main"})
			return err
		}},
		{"UpdateMenu", "menu.updated", []string{"name"}, func(s *Service) error {
			_, err := s.UpdateMenu(ctx, id, &UpdateMenuInput{})
			return err
		}},
		{"ReplaceMenu", "menu.updated", []string{"name", "location", "items"}, func(s *Service) error {
			_, err := s.ReplaceMenu(ctx, id, &UpdateMenuInput{Name: &name, Location: &loc}, []ReplaceItem{{Label: "a", LinkType: LinkTypeCustom}})
			return err
		}},
		{"DeleteMenu", "menu.deleted", nil, func(s *Service) error { return s.DeleteMenu(ctx, id) }},
		{"CreateMenuItem", "menu.item_created", []string{"name"}, func(s *Service) error {
			_, err := s.CreateMenuItem(ctx, id, &CreateMenuItemInput{Label: "x", LinkType: LinkTypeCustom})
			return err
		}},
		{"UpdateMenuItem", "menu.item_updated", []string{"name"}, func(s *Service) error {
			_, err := s.UpdateMenuItem(ctx, id, &UpdateMenuItemInput{})
			return err
		}},
		{"DeleteMenuItem", "menu.item_deleted", nil, func(s *Service) error { return s.DeleteMenuItem(ctx, id) }},
		{"ReorderItems", "menu.items_reordered", []string{"items"}, func(s *Service) error { return s.ReorderItems(ctx, id, nil) }},
	}
	for _, c := range cases {
		var got []auditCall
		s := NewService(stubRepo{}, log)
		s.SetAudit(func(_ context.Context, action, target string, detail map[string]any) {
			got = append(got, auditCall{action, target, detail})
		})
		if err := c.write(s); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(got) != 1 || got[0].action != c.action || got[0].target == "" || len(got[0].detail) != len(c.keys) {
			t.Fatalf("%s: calls = %+v, want one %s with keys %v", c.name, got, c.action, c.keys)
		}
		for _, k := range c.keys {
			if _, ok := got[0].detail[k]; !ok {
				t.Errorf("%s: detail %v lacks %q", c.name, got[0].detail, k)
			}
		}

		// A failing write records nothing; a nil hook is safe.
		got = nil
		s = NewService(stubRepo{fail: errors.New("boom")}, log)
		s.SetAudit(func(context.Context, string, string, map[string]any) { t.Errorf("%s: hook called on failure", c.name) })
		_ = c.write(s)
		_ = c.write(NewService(stubRepo{}, log))
	}
}

type auditCall struct {
	action, target string
	detail         map[string]any
}
