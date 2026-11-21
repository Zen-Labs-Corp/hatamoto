package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/menu"
)

// menuStub serves menus by location; batchErr fails the batched lookup and
// failAt the per-location lookup of one location.
type menuStub struct {
	menu.Repository
	at       map[string]*menu.Menu
	batchErr error
	failAt   string
}

func (r menuStub) GetMenusWithItemsByLocations(_ context.Context, locs []string) (map[string]*menu.Menu, error) {
	if r.batchErr != nil {
		return nil, r.batchErr
	}
	out := map[string]*menu.Menu{}
	for _, l := range locs {
		if m := r.at[l]; m != nil {
			out[l] = m
		}
	}
	return out, nil
}

func (r menuStub) GetMenuByLocation(_ context.Context, loc string) (*menu.Menu, error) {
	if loc == r.failAt {
		return nil, errors.New("boom")
	}
	return r.at[loc], nil
}

func (r menuStub) GetMenuWithItems(_ context.Context, id uuid.UUID) (*menu.Menu, error) {
	for _, m := range r.at {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, nil
}

func TestMenusAt(t *testing.T) {
	header := &menu.Menu{ID: uuid.New(), Name: "h"}
	footer := &menu.Menu{ID: uuid.New(), Name: "f"}
	both := map[string]*menu.Menu{"header": header, "footer": footer}
	boom := errors.New("batch failed")
	for _, c := range []struct {
		name string
		repo menuStub
		want map[string]*menu.Menu
	}{
		{"batch", menuStub{at: both}, both},
		{"header only", menuStub{at: map[string]*menu.Menu{"header": header}}, map[string]*menu.Menu{"header": header}},
		{"footer only", menuStub{at: map[string]*menu.Menu{"footer": footer}}, map[string]*menu.Menu{"footer": footer}},
		{"batch error loads each location", menuStub{at: both, batchErr: boom}, both},
		{"batch error, header fails", menuStub{at: both, batchErr: boom, failAt: "header"}, map[string]*menu.Menu{"footer": footer}},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := &Server{menuService: menu.NewService(c.repo, slog.New(slog.NewTextHandler(io.Discard, nil)))}
			got := srv.menusAt(context.Background(), "header", "footer")
			if len(got) != len(c.want) {
				t.Fatalf("got %d menus, want %d", len(got), len(c.want))
			}
			for loc, m := range c.want {
				if got[loc] != m {
					t.Errorf("%s: got %v, want %v", loc, got[loc], m)
				}
			}
		})
	}
}
