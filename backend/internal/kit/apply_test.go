package kit

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/packages"
)

type enabler struct{ calls int }

func (e *enabler) enable(context.Context) error { e.calls++; return nil }

func apply(t *testing.T, f *fake, p *packages.Package, a Accepts, en *enabler) (*Result, error) {
	t.Helper()
	pl, err := BuildPlan(context.Background(), f.deps(), p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return Apply(context.Background(), f.deps(), pl, a, ApplyOptions{Actor: "owner", EnableComments: en.enable})
}

func (f *fake) wrote(prefix string) int {
	n := 0
	for _, w := range f.writes {
		if strings.HasPrefix(w, prefix) {
			n++
		}
	}
	return n
}

func policyEnabled(t *testing.T, f *fake) bool {
	t.Helper()
	var s struct {
		Comments struct {
			Policy struct {
				Enabled bool `json:"enabled"`
			} `json:"policy"`
		} `json:"comments"`
	}
	if err := jsonx.Unmarshal(f.schemas["story"].s.Settings, &s); err != nil {
		t.Fatal(err)
	}
	return s.Comments.Policy.Enabled
}

func TestApplyFreshSiteThenAgainCreatesNothing(t *testing.T) {
	f := newFake(t).withRegistry(t)
	en := &enabler{}
	res, err := apply(t, f, kitPackage(t, nil), Accepts{}, en)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"plugins": 1, "themes": 1, "schemas": 1, "pages": 4, "menus": 1}
	for k, n := range want {
		if res.Created[k] != n {
			t.Errorf("created[%s] = %d, want %d (%v)", k, res.Created[k], n, res.Created)
		}
	}
	if !res.Recorded || f.applied["community"].Version != "1.0.0" || f.applied["community"].AppliedBy != "owner" {
		t.Errorf("recorded=%v row=%+v", res.Recorded, f.applied["community"])
	}
	if !slices.Equal(f.grants["community-plug"], []string{"db", "settings"}) {
		t.Errorf("grant = %v: site.scripts needs the owner's tick", f.grants["community-plug"])
	}
	if len(f.activated) != 0 || f.home.Slug != "home" || en.calls != 0 {
		t.Errorf("no tick given: activated=%v home=%s comments calls=%d", f.activated, f.home.Slug, en.calls)
	}
	if policyEnabled(t, f) {
		t.Error("the community policy is stored off")
	}
	if !hasText(res.Messages, "Comments left off") {
		t.Errorf("messages: %v", res.Messages)
	}

	before := len(f.writes)
	res, err = apply(t, f, kitPackage(t, nil), Accepts{}, en)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total() != 0 || res.Recorded || len(f.writes) != before {
		t.Errorf("second apply: created=%v recorded=%v new writes=%v", res.Created, res.Recorded, f.writes[before:])
	}
	if !slices.Contains(res.Kept, "page 'latest'") || !slices.Contains(res.Kept, "menu 'Header'") || !slices.Contains(res.Kept, "content type 'story'") {
		t.Errorf("kept: %v", res.Kept)
	}
}

func TestApplyTicksSwitchThingsOn(t *testing.T) {
	f := newFake(t).withRegistry(t)
	en := &enabler{}
	res, err := apply(t, f, kitPackage(t, nil), Accepts{Comments: true, ActivateTheme: true, Homepage: true, Extra: []string{"site.scripts", "entities.read"}}, en)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.grants["community-plug"], []string{"db", "settings", "site.scripts"}) {
		t.Errorf("grant = %v (a tick outside the plugin's extra grants nothing)", f.grants["community-plug"])
	}
	if !slices.Equal(f.activated, []string{"community-look"}) || f.home.Slug != "latest" || en.calls != 1 {
		t.Errorf("activated=%v home=%s comments calls=%d", f.activated, f.home.Slug, en.calls)
	}
	if hasText(res.Messages, "Comments left off") {
		t.Errorf("messages: %v", res.Messages)
	}
	// Run again: comments reported on by the site, theme and homepage already set.
	f.comOn = true
	before := len(f.writes)
	if _, err := apply(t, f, kitPackage(t, nil), Accepts{Comments: true, ActivateTheme: true, Homepage: true}, en); err != nil {
		t.Fatal(err)
	}
	if en.calls != 1 || len(f.writes) != before {
		t.Errorf("second run: comments calls=%d new writes=%v", en.calls, f.writes[before:])
	}
}

func TestApplyKeepsWhatTheOwnerHas(t *testing.T) {
	f := newFake(t).withRegistry(t)
	mine := &entity.Entity{ID: uuid.New(), SchemaID: f.schemas["page"].s.ID, Name: "My own latest", Slug: "latest"}
	f.pages["page/latest"] = mine
	held := &menu.Menu{ID: uuid.New(), Name: "Mine"}
	f.menus["header"] = held
	res, err := apply(t, f, kitPackage(t, nil), Accepts{}, &enabler{})
	if err != nil {
		t.Fatal(err)
	}
	if f.pages["page/latest"] != mine || mine.Name != "My own latest" || f.menus["header"] != held {
		t.Error("an existing page or menu was replaced")
	}
	if res.Created["pages"] != 3 || res.Created["menus"] != 0 || !slices.Contains(res.Kept, "page 'latest'") || !slices.Contains(res.Kept, "menu 'Header'") {
		t.Errorf("created=%v kept=%v", res.Created, res.Kept)
	}
}

func TestApplyRefusalsWriteNothing(t *testing.T) {
	f := newFake(t).withRegistry(t)
	_, err := apply(t, f, kitPackage(t, func(k *packages.Kit) { k.Requires = []string{"comment.policy"} }), Accepts{Comments: true}, &enabler{})
	var re *RefusedError
	if !errors.As(err, &re) || !hasText(re.Refusals, "comment.policy") {
		t.Fatalf("err = %v", err)
	}
	f2 := newFake(t).withRegistry(t)
	_, err = apply(t, f2, kitPackage(t, func(k *packages.Kit) { k.Plugins[0].Name = "nobody" }), Accepts{}, &enabler{})
	if !errors.As(err, &re) {
		t.Fatalf("err = %v", err)
	}
	if len(f.writes)+len(f2.writes) != 0 {
		t.Errorf("writes: %v %v", f.writes, f2.writes)
	}
}

func TestApplyCommentsNeedAnAddressBeforeAnyWrite(t *testing.T) {
	f := newFake(t).withRegistry(t)
	f.address = ""
	en := &enabler{}
	_, err := apply(t, f, kitPackage(t, nil), Accepts{Comments: true}, en)
	if !errors.Is(err, ErrNoAddress) || len(f.writes) != 0 || en.calls != 0 {
		t.Fatalf("err=%v writes=%v calls=%d", err, f.writes, en.calls)
	}
	// Without the tick an address is not needed.
	if _, err := apply(t, f, kitPackage(t, nil), Accepts{}, en); err != nil {
		t.Fatal(err)
	}
}

func TestApplyCommentsNeedACallback(t *testing.T) {
	f := newFake(t).withRegistry(t)
	pl, err := BuildPlan(context.Background(), f.deps(), kitPackage(t, nil), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(context.Background(), f.deps(), pl, Accepts{Comments: true}, ApplyOptions{}); err == nil || len(f.writes) != 0 {
		t.Fatalf("err=%v writes=%v", err, f.writes)
	}
}

func TestApplyPolicyOnlyWithTheTick(t *testing.T) {
	old := present
	present = []string{"comment.policy"}
	t.Cleanup(func() { present = old })
	edit := func(k *packages.Kit) { k.Requires = []string{"comment.policy"} }

	f := newFake(t).withRegistry(t)
	f.policy = &fakePolicy{warning: "Read this first."}
	res, err := apply(t, f, kitPackage(t, edit), Accepts{Comments: true}, &enabler{})
	if err != nil || len(f.policy.on) != 0 || policyEnabled(t, f) || !hasText(res.Messages, "Community policy left off") {
		t.Fatalf("err=%v on=%v messages=%v", err, f.policy.on, res.Messages)
	}

	f = newFake(t).withRegistry(t)
	f.policy = &fakePolicy{warning: "Read this first."}
	if _, err = apply(t, f, kitPackage(t, edit), Accepts{Comments: true, Policy: true}, &enabler{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.policy.on, []string{"story"}) {
		t.Errorf("policy on for %v", f.policy.on)
	}
}

func TestApplyContentTypeOwnedByAPlugin(t *testing.T) {
	f := newFake(t)
	f.publish(packages.KindPlugin, "community-plug", "1.2.0", pluginZip(t, "community-plug", "1.2.0", `,"entity_schema":"story"`))
	f.publish(packages.KindTheme, "community-look", "1.0.0", themeZip(t, "community-look", "1.0.0", ""))
	res, err := apply(t, f, kitPackage(t, nil), Accepts{}, &enabler{})
	if err != nil {
		t.Fatal(err)
	}
	if f.wrote("create schema") != 0 || f.wrote("update schema story") != 1 || res.Filled != 1 || res.Created["schemas"] != 0 {
		t.Errorf("writes=%v filled=%d created=%v", f.writes, res.Filled, res.Created)
	}
	if !strings.Contains(string(f.schemas["story"].s.Settings), "/item/{slug}") || policyEnabled(t, f) {
		t.Errorf("settings: %s", f.schemas["story"].s.Settings)
	}
	// Again: the keys are there, nothing to fill.
	before := len(f.writes)
	if res, err = apply(t, f, kitPackage(t, nil), Accepts{}, &enabler{}); err != nil || res.Recorded || len(f.writes) != before {
		t.Errorf("second apply: err=%v recorded=%v writes=%v", err, res.Recorded, f.writes[before:])
	}
}

func TestApplyFillKeepsExistingSettings(t *testing.T) {
	f := newFake(t)
	f.publish(packages.KindPlugin, "community-plug", "1.2.0", pluginZip(t, "community-plug", "1.2.0", `,"entity_schema":"story"`))
	f.publish(packages.KindTheme, "community-look", "1.0.0", themeZip(t, "community-look", "1.0.0", ""))
	f.installed["plugin/community-plug"] = "1.2.0"
	f.schemas["story"] = &schemaRec{origin: "community-plug", s: &entity.EntitySchema{ID: uuid.New(), Slug: "story",
		Settings: []byte(`{"url_pattern":"/s/{slug}","comments":{"mode":"closed"}}`)}}
	if _, err := apply(t, f, kitPackage(t, nil), Accepts{}, &enabler{}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		URLPattern string `json:"url_pattern"`
		Comments   struct {
			Mode  string `json:"mode"`
			Order string `json:"order"`
		} `json:"comments"`
	}
	if err := jsonx.Unmarshal(f.schemas["story"].s.Settings, &got); err != nil {
		t.Fatal(err)
	}
	if got.URLPattern != "/s/{slug}" || got.Comments.Mode != "closed" || got.Comments.Order != "rank" {
		t.Errorf("settings: %s", f.schemas["story"].s.Settings)
	}
}

func TestApplyPluginThatDidNotCreateItsContentType(t *testing.T) {
	f := newFake(t)
	f.pluginCreates = false
	f.publish(packages.KindPlugin, "community-plug", "1.2.0", pluginZip(t, "community-plug", "1.2.0", `,"entity_schema":"story"`))
	f.publish(packages.KindTheme, "community-look", "1.0.0", themeZip(t, "community-look", "1.0.0", ""))
	_, err := apply(t, f, kitPackage(t, nil), Accepts{}, &enabler{})
	if err == nil || !strings.Contains(err.Error(), "plugin community-plug did not create content type story") {
		t.Fatalf("err = %v", err)
	}
	if len(f.applied) != 0 || f.wrote("create schema") != 0 {
		t.Errorf("recorded %v, writes %v", f.applied, f.writes)
	}
}

func TestApplyInterruptedRunIsFinishedByRunningAgain(t *testing.T) {
	f := newFake(t).withRegistry(t)
	f.failPage = "submit"
	if _, err := apply(t, f, kitPackage(t, nil), Accepts{}, &enabler{}); err == nil {
		t.Fatal("the page store failure must surface")
	}
	if len(f.applied) != 0 || f.wrote("create menu") != 0 {
		t.Fatalf("nothing is recorded after a failure: %v %v", f.applied, f.writes)
	}
	res, err := apply(t, f, kitPackage(t, nil), Accepts{}, &enabler{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Recorded || res.Created["pages"] != 2 || f.wrote("create page") != 4 || f.wrote("record") != 1 || f.wrote("install") != 2 {
		t.Errorf("created=%v writes=%v", res.Created, f.writes)
	}
}

func TestApplyInstallFailureStopsBeforeContent(t *testing.T) {
	f := newFake(t).withRegistry(t)
	f.installErr = &packages.ConfirmationRequired{Kind: packages.KindPlugin, Name: "community-plug"}
	_, err := apply(t, f, kitPackage(t, nil), Accepts{}, &enabler{})
	var cr *packages.ConfirmationRequired
	if !errors.As(err, &cr) || len(f.writes) != 0 {
		t.Fatalf("err=%v writes=%v", err, f.writes)
	}
}
