package admin

import (
	"context"
	"encoding/json/jsontext"
	"sync"
	"time"

	"hatamoto.dev/core/internal/comment"
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

	plans    int // Plan calls
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

func (f *fakeKits) Plan(context.Context, string) (*kit.Plan, error) {
	f.mu.Lock()
	f.plans++
	f.mu.Unlock()
	return f.plan, f.planErr
}

func (f *fakeKits) Apply(ctx context.Context, pl *kit.Plan, a kit.Accepts, o kit.ApplyOptions) (*kit.Result, error) {
	f.mu.Lock()
	f.applies = append(f.applies, fakeKitApply{pl.Kit.Name, a, o})
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
		plan:      kitPlanFixture(),
	}
}

// kitPlanFixture is a plan with every kind of step, on a site that already has a footer menu.
func kitPlanFixture() *kit.Plan {
	k := &packages.Kit{
		Name: "community", Version: "1.0.0", Title: "Community site", Description: "A ranked community with threaded comments.",
		Schemas: []packages.KitSchema{{Slug: "story", Name: "Stories", Settings: jsontext.Value(`{"url_pattern":"/item/{slug}"}`)}},
		Pages:   []packages.KitPage{{Slug: "latest", Name: "Latest", Homepage: true}, {Slug: "newest", Name: "Newest"}, {Slug: "submit", Name: "Submit"}},
		Menus:   []packages.KitMenu{{Location: "header", Name: "Header", Items: []packages.KitMenuItem{{Label: "latest", URL: "/latest"}, {Label: "newest", URL: "/newest"}}}},
	}
	return &kit.Plan{
		Kit: k, Tier: "first-party",
		Plugins: []kit.PackageStep{{Kind: packages.KindPlugin, Name: "community-plug", Version: "1.0.0", Action: kit.ActionInstall,
			Asked: []string{"db", "client.key", "site.scripts"}, Grant: []string{"db", "client.key"}, Extra: []string{"site.scripts"}}},
		Themes:   []kit.PackageStep{{Kind: packages.KindTheme, Name: "community-look", Version: "1.0.0", Action: kit.ActionInstall, Activate: true}},
		Schemas:  []kit.ItemStep{{Key: "story", Name: "Stories", Action: kit.ActionCreate}},
		Pages:    []kit.ItemStep{{Key: "latest", Name: "Latest", Action: kit.ActionCreate}, {Key: "newest", Name: "Newest", Action: kit.ActionCreate}, {Key: "submit", Name: "Submit", Action: kit.ActionCreate}},
		Menus:    []kit.ItemStep{{Key: "header", Name: "Header", Action: kit.ActionCreate}},
		Comments: kit.CommentsStep{Requested: true, Warnings: [2]string{comment.EnableWarning, comment.EnableConfirmWarning}},
		Homepage: kit.HomepageStep{Page: "latest"},
		Kept:     []string{"menu 'Footer'"},
	}
}
