package admin

import (
	"context"
	"sync"
	"time"

	"hatamoto.dev/core/internal/kit"
	"hatamoto.dev/core/internal/packages"
)

// fakeKits is a KitService with canned answers; it records every Apply.
type fakeKits struct {
	mu        sync.Mutex
	applied   []KitApplied
	available []KitEntry
	listErr   error
	plan      *kit.Plan
	planErr   error
	result    *kit.Result
	applyErr  error

	applies  []fakeKitApply
	enabling error // what the EnableComments callback returned
}

type fakeKitApply struct {
	Name string
	A    kit.Accepts
	O    kit.ApplyOptions
}

func (f *fakeKits) List(context.Context) ([]KitApplied, []KitEntry, error) {
	return f.applied, f.available, f.listErr
}

func (f *fakeKits) Plan(context.Context, string) (*kit.Plan, error) { return f.plan, f.planErr }

func (f *fakeKits) Apply(ctx context.Context, name string, a kit.Accepts, o kit.ApplyOptions) (*kit.Result, error) {
	f.mu.Lock()
	f.applies = append(f.applies, fakeKitApply{name, a, o})
	f.mu.Unlock()
	if a.Comments && o.EnableComments != nil {
		if err := o.EnableComments(ctx); err != nil {
			f.enabling = err
			return nil, err
		}
	}
	if f.applyErr != nil {
		return nil, f.applyErr
	}
	if f.result != nil {
		return f.result, nil
	}
	return &kit.Result{Created: map[string]int{}}, nil
}

// kitsFixture is a neutral fake: one applied kit and one the registry offers.
func kitsFixture() *fakeKits {
	return &fakeKits{
		applied:   []KitApplied{{Name: "community", Title: "Community site", Version: "1.0.0", AppliedBy: "owner@example.org", AppliedAt: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)}},
		available: []KitEntry{{Name: "community", Title: "Community site", Version: "1.0.0", Tier: "first-party", Applied: true}, {Name: "journal", Title: "Journal", Version: "0.3.0", Tier: "community"}},
		plan:      &kit.Plan{Kit: &packages.Kit{Name: "community", Version: "1.0.0", Title: "Community site"}, Tier: "first-party"},
	}
}
