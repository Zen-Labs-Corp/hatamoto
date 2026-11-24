package kit

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/packages"
)

func mustPlan(t *testing.T, f *fake, p *packages.Package) *Plan {
	t.Helper()
	pl, err := BuildPlan(context.Background(), f.deps(), p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return pl
}

func hasText(list []string, sub string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.Contains(s, sub) })
}

func TestPlanFreshSite(t *testing.T) {
	f := newFake(t).withRegistry(t)
	pl := mustPlan(t, f, kitPackage(t, nil))

	if len(pl.Refusals) != 0 {
		t.Fatalf("refusals: %v", pl.Refusals)
	}
	if !pl.Fresh || pl.Tier != "community" || pl.Applied != nil {
		t.Errorf("fresh=%v tier=%q applied=%v", pl.Fresh, pl.Tier, pl.Applied)
	}
	if len(pl.Plugins) != 1 {
		t.Fatalf("plugins: %+v", pl.Plugins)
	}
	pg := pl.Plugins[0]
	if pg.Action != ActionInstall || pg.Version != "1.2.0" || pg.Raw == nil {
		t.Errorf("plugin step: %+v", pg)
	}
	if !slices.Equal(pg.Grant, []string{"db", "settings"}) || !slices.Equal(pg.Extra, []string{"site.scripts"}) {
		t.Errorf("grant=%v extra=%v", pg.Grant, pg.Extra)
	}
	if len(pl.Themes) != 1 || pl.Themes[0].Action != ActionInstall || !pl.Themes[0].Activate {
		t.Errorf("themes: %+v", pl.Themes)
	}
	if len(pl.Schemas) != 1 || pl.Schemas[0].Action != ActionCreate || pl.Schemas[0].Key != "story" {
		t.Errorf("schemas: %+v", pl.Schemas)
	}
	if len(pl.Pages) != 4 || len(pl.Menus) != 1 || pl.Menus[0].Action != ActionCreate {
		t.Errorf("pages=%d menus=%+v", len(pl.Pages), pl.Menus)
	}
	for _, s := range pl.Pages {
		if s.Action != ActionCreate {
			t.Errorf("page %s: %s", s.Key, s.Action)
		}
	}
	if !pl.Comments.Requested || pl.Comments.AlreadyOn || pl.Comments.NoAddress || pl.Comments.Warnings[0] == "" || pl.Comments.Warnings[1] == "" {
		t.Errorf("comments: %+v", pl.Comments)
	}
	if pl.Homepage.Page != "latest" || len(pl.Kept) != 0 {
		t.Errorf("homepage=%q kept=%v", pl.Homepage.Page, pl.Kept)
	}
	if len(f.writes) != 0 {
		t.Errorf("plan wrote: %v", f.writes)
	}
}

func TestPlanExistingSiteListsWhatIsKept(t *testing.T) {
	f := newFake(t).withRegistry(t)
	f.pages["page/latest"] = &entity.Entity{ID: uuid.New(), Slug: "latest"}
	f.menus["header"] = &menu.Menu{ID: uuid.New(), Name: "Header"}
	f.activeTheme = "other"
	f.installed["plugin/community-plug"] = "1.1.0"
	pl := mustPlan(t, f, kitPackage(t, nil))

	if !slices.Contains(pl.Kept, "page 'latest'") || !slices.Contains(pl.Kept, "menu 'Header'") {
		t.Errorf("kept: %v", pl.Kept)
	}
	if pl.Fresh {
		t.Error("a site on another theme is not fresh")
	}
	if pl.Plugins[0].Action != ActionKeep || pl.Plugins[0].Version != "1.1.0" || pl.Plugins[0].Raw != nil {
		t.Errorf("installed plugin in range must be kept: %+v", pl.Plugins[0])
	}
	if len(f.writes) != 0 {
		t.Errorf("plan wrote: %v", f.writes)
	}
}

func TestPlanRefusals(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*fake)
		edit  func(*packages.Kit)
		want  string
	}{
		{"missing feature", nil, func(k *packages.Kit) { k.Requires = []string{"comment.policy", "comment.rank"} }, "comment.policy, comment.rank"},
		{"unknown plugin", nil, func(k *packages.Kit) { k.Plugins[0].Name = "nobody" }, "plugin nobody is not listed"},
		{"unknown theme", nil, func(k *packages.Kit) { k.Themes[0].Name = "nobody" }, "theme nobody is not listed"},
		{"registry version out of range", nil, func(k *packages.Kit) { k.Plugins[0].Range = "^2" }, "outside the range ^2"},
		{"installed out of range", func(f *fake) { f.installed["plugin/community-plug"] = "0.9.0" }, nil, "update community-plug first"},
		{"sdk range", nil, func(k *packages.Kit) { k.SDK = "^0.1" }, "needs SDK ^0.1"},
		{"foreign plugin owns the type", func(f *fake) {
			f.schemas["story"] = &schemaRec{s: &entity.EntitySchema{ID: uuid.New(), Slug: "story"}, origin: "someone-else"}
		}, nil, "belongs to the plugin someone-else"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(t).withRegistry(t)
			if tt.setup != nil {
				tt.setup(f)
			}
			pl := mustPlan(t, f, kitPackage(t, tt.edit))
			if !hasText(pl.Refusals, tt.want) {
				t.Fatalf("refusals %v lack %q", pl.Refusals, tt.want)
			}
			if len(f.writes) != 0 {
				t.Errorf("plan wrote: %v", f.writes)
			}
		})
	}
}

func TestPlanOwnerCreatedTypeIsKept(t *testing.T) {
	f := newFake(t).withRegistry(t)
	f.schemas["story"] = &schemaRec{s: &entity.EntitySchema{ID: uuid.New(), Slug: "story"}}
	pl := mustPlan(t, f, kitPackage(t, nil))
	if len(pl.Refusals) != 0 || pl.Schemas[0].Action != ActionKeep || !slices.Contains(pl.Kept, "content type 'story'") {
		t.Errorf("schemas=%+v refusals=%v kept=%v", pl.Schemas, pl.Refusals, pl.Kept)
	}
}

func TestPlanContentTypeOwnedByAKitPlugin(t *testing.T) {
	f := newFake(t).withRegistry(t)
	f.publish(packages.KindPlugin, "community-plug", "1.2.0", pluginZip(t, "community-plug", "1.2.0", `,"entity_schema":"story"`))
	pl := mustPlan(t, f, kitPackage(t, nil))
	if len(pl.Refusals) != 0 {
		t.Fatalf("refusals: %v", pl.Refusals)
	}
	s := pl.Schemas[0]
	if s.Action != ActionFill || s.Owner != "community-plug" || !strings.Contains(s.Note, "created by plugin community-plug") {
		t.Errorf("schema step: %+v", s)
	}
	if len(f.writes) != 0 {
		t.Errorf("plan wrote: %v", f.writes)
	}

	// Already created by the plugin: still a fill, via the stored origin.
	f2 := newFake(t).withRegistry(t)
	f2.schemas["story"] = &schemaRec{s: &entity.EntitySchema{ID: uuid.New(), Slug: "story"}, origin: "community-plug"}
	pl = mustPlan(t, f2, kitPackage(t, nil))
	if pl.Schemas[0].Action != ActionFill || len(pl.Refusals) != 0 {
		t.Errorf("schema step: %+v refusals %v", pl.Schemas[0], pl.Refusals)
	}
}

func TestPlanMenuLocations(t *testing.T) {
	edit := func(k *packages.Kit) { k.Menus[0].Location = "sidebar-x" }

	f := newFake(t).withRegistry(t)
	pl := mustPlan(t, f, kitPackage(t, edit))
	if pl.Menus[0].Action != ActionSkip || len(pl.Refusals) != 0 ||
		!hasText(pl.Messages, "Menu 'Header' skipped: the active theme has no location 'sidebar-x'.") {
		t.Errorf("menus=%+v refusals=%v messages=%v", pl.Menus, pl.Refusals, pl.Messages)
	}

	f = newFake(t).withRegistry(t)
	f.themeLocs = []string{"sidebar-x"}
	if pl = mustPlan(t, f, kitPackage(t, edit)); pl.Menus[0].Action != ActionCreate {
		t.Errorf("a location of the active theme is offered: %+v", pl.Menus)
	}

	// A location only the kit's own (activated) theme declares is offered too.
	f = newFake(t).withRegistry(t)
	f.publish(packages.KindTheme, "community-look", "1.0.0",
		themeZip(t, "community-look", "1.0.0", `,"menus":{"sidebar-x":{"title":"Side"}}`))
	if pl = mustPlan(t, f, kitPackage(t, edit)); pl.Menus[0].Action != ActionCreate {
		t.Errorf("a location of the kit theme is offered: %+v", pl.Menus)
	}
	// ... unless the kit does not activate that theme.
	f = newFake(t).withRegistry(t)
	f.publish(packages.KindTheme, "community-look", "1.0.0",
		themeZip(t, "community-look", "1.0.0", `,"menus":{"sidebar-x":{"title":"Side"}}`))
	pl = mustPlan(t, f, kitPackage(t, func(k *packages.Kit) { edit(k); k.Themes[0].Activate = false }))
	if pl.Menus[0].Action != ActionSkip {
		t.Errorf("a theme the kit does not activate offers nothing: %+v", pl.Menus)
	}
}

func TestPlanPolicyNeedsRequires(t *testing.T) {
	f := newFake(t).withRegistry(t)
	pl := mustPlan(t, f, kitPackage(t, nil))
	if pl.Policy.Available || !hasText(pl.Messages, "Community policy skipped: the kit does not list comment.policy in requires.") {
		t.Errorf("policy: %+v messages %v", pl.Policy, pl.Messages)
	}
}

func TestPlanPolicyAvailableWithTheSeam(t *testing.T) {
	edit := withPolicyFeature(t)
	f := newFake(t).withRegistry(t)
	pl := mustPlan(t, f, kitPackage(t, edit))
	if !pl.Policy.Available || pl.Policy.Warning != comment.PolicyConfirmWarning || pl.Policy.Tick != comment.PolicyTick ||
		!slices.Equal(pl.Policy.Requested, []string{"story"}) {
		t.Errorf("policy: %+v", pl.Policy)
	}
}

func TestPlanPolicyAlreadyOn(t *testing.T) {
	edit := withPolicyFeature(t)
	f := newFake(t).withRegistry(t)
	f.schemas["story"] = &schemaRec{s: &entity.EntitySchema{ID: uuid.New(), Slug: "story",
		Settings: []byte(`{"comments":{"policy":{"enabled":true,"max_depth":5,"edit_window_minutes":15,"slow_mode_seconds":0}}}`)}}
	pl := mustPlan(t, f, kitPackage(t, edit))
	if !slices.Equal(pl.Policy.AlreadyOn, []string{"story"}) || len(pl.Policy.Requested) != 0 || pl.Policy.Available ||
		!hasText(pl.Kept, "community policy on 'story'") {
		t.Errorf("policy: %+v kept %v", pl.Policy, pl.Kept)
	}
}

func TestPlanPolicyBuiltinSkipped(t *testing.T) {
	edit := withPolicyFeature(t)
	f := newFake(t).withRegistry(t)
	f.schemas["story"] = &schemaRec{s: &entity.EntitySchema{ID: uuid.New(), Slug: "story"}}
	f.builtin = map[string]bool{"story": true}
	pl := mustPlan(t, f, kitPackage(t, edit))
	if pl.Policy.Available || !hasText(pl.Messages, "Community policy skipped for 'story': built-in content types cannot use it.") {
		t.Errorf("policy: %+v messages %v", pl.Policy, pl.Messages)
	}
}

func TestPlanPolicyNoSeam(t *testing.T) {
	edit := withPolicyFeature(t)
	f := newFake(t).withRegistry(t)
	f.noSeam = true
	pl := mustPlan(t, f, kitPackage(t, edit))
	if len(pl.Refusals) != 0 || pl.Policy.Available || !hasText(pl.Messages, "cannot turn it on yet") {
		t.Errorf("policy: %+v messages %v refusals %v", pl.Policy, pl.Messages, pl.Refusals)
	}
}

func TestPlanCommentsNeedAnAddress(t *testing.T) {
	f := newFake(t).withRegistry(t)
	f.address = ""
	f.comOn = true
	pl := mustPlan(t, f, kitPackage(t, nil))
	if !pl.Comments.NoAddress || !pl.Comments.AlreadyOn {
		t.Errorf("comments: %+v", pl.Comments)
	}
}

func TestPlanTierLabel(t *testing.T) {
	f := newFake(t).withRegistry(t)
	for _, tt := range []struct {
		o    Options
		want string
	}{
		{Options{Origin: packages.OriginFirstParty}, "first-party"},
		{Options{Origin: packages.OriginDeveloper}, "developer"},
		{Options{Origin: packages.OriginCommunity}, "community"},
		{Options{Origin: packages.OriginFirstParty, Tier: "verified"}, "verified"},
	} {
		pl, err := BuildPlan(context.Background(), f.deps(), kitPackage(t, nil), tt.o)
		if err != nil || pl.Tier != tt.want {
			t.Errorf("%+v: tier %q err %v, want %q", tt.o, pl.Tier, err, tt.want)
		}
	}
}

func TestGrantFor(t *testing.T) {
	asked := []string{"db", "settings", "site.scripts", "ui.allow:camera"}
	tests := []struct {
		name                     string
		grant                    packages.KitGrant
		asked                    []string
		granted, extra, notAsked []string
	}{
		{"all", packages.KitGrant{All: true}, asked, []string{"db", "settings"}, []string{"site.scripts", "ui.allow:camera"}, nil},
		{"absent", packages.KitGrant{}, asked, nil, asked, nil},
		{"list with site.scripts", packages.KitGrant{Perms: []string{"db", "site.scripts"}}, asked, []string{"db"}, []string{"settings", "site.scripts", "ui.allow:camera"}, nil},
		{"list naming an unasked permission", packages.KitGrant{Perms: []string{"db", "entities.read"}}, []string{"db", "settings"}, []string{"db"}, []string{"settings"}, []string{"entities.read"}},
		{"list naming ui.allow", packages.KitGrant{Perms: []string{"ui.allow:camera"}}, []string{"ui.allow:camera"}, nil, []string{"ui.allow:camera"}, nil},
		{"all leaves every warned permission to the owner", packages.KitGrant{All: true}, []string{"db", "builder.island", "http.fetch.any", "entities.filter", "client.key", "entities.write.own", "comments.moderate"}, []string{"db"}, []string{"builder.island", "http.fetch.any", "entities.filter", "client.key", "entities.write.own", "comments.moderate"}, nil},
		{"a list cannot grant a warned permission", packages.KitGrant{Perms: []string{"builder.island", "http.fetch.any"}}, []string{"builder.island", "http.fetch.any"}, nil, []string{"builder.island", "http.fetch.any"}, nil},
		{"all with nothing asked", packages.KitGrant{All: true}, nil, nil, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, e, n := GrantFor(tt.asked, tt.grant)
			if !slices.Equal(g, tt.granted) || !slices.Equal(e, tt.extra) || !slices.Equal(n, tt.notAsked) {
				t.Errorf("granted=%v extra=%v notAsked=%v", g, e, n)
			}
		})
	}
}

func TestPlanFingerprintFollowsWhatWasResolved(t *testing.T) {
	f := newFake(t).withRegistry(t)
	base := mustPlan(t, f, kitPackage(t, nil))
	if again := mustPlan(t, f, kitPackage(t, nil)); again.Fingerprint() != base.Fingerprint() {
		t.Fatal("the same registry gave two fingerprints")
	}
	for name, edit := range map[string]func(*Plan){
		"kit version":    func(p *Plan) { p.Kit.Version = "9.9.9" },
		"kit digest":     func(p *Plan) { p.Digest[0]++ },
		"plugin version": func(p *Plan) { p.Plugins[0].Version = "9.9.9" },
		"plugin package": func(p *Plan) { p.Plugins[0].Raw = []byte("other") },
		"plugin asks":    func(p *Plan) { p.Plugins[0].Asked = append(slices.Clone(p.Plugins[0].Asked), "http.fetch.any") },
		"theme package":  func(p *Plan) { p.Themes[0].Raw = []byte("other") },
	} {
		p := mustPlan(t, f, kitPackage(t, nil))
		edit(p)
		if p.Fingerprint() == base.Fingerprint() {
			t.Errorf("%s does not change the fingerprint", name)
		}
	}
}
