package kit

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"encoding/json/jsontext"
	"github.com/google/uuid"
	"hatamoto.dev/core/internal/comment"
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
	res, err := apply(t, f, kitPackage(t, nil), Accepts{Comments: true, ActivateTheme: true, Homepage: true, Extra: []string{"community-plug:site.scripts", "community-plug:entities.read", "site.scripts"}}, en)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.grants["community-plug"], []string{"db", "settings", "site.scripts"}) {
		t.Errorf("grant = %v (a tick outside the plugin's extra, or one without its plugin, grants nothing)", f.grants["community-plug"])
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
	_, err := apply(t, f, kitPackage(t, func(k *packages.Kit) { k.Requires = []string{"comment.signin"} }), Accepts{Comments: true}, &enabler{})
	var re *RefusedError
	if !errors.As(err, &re) || !hasText(re.Refusals, "comment.signin") {
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

func withPolicyFeature(*testing.T) func(*packages.Kit) {
	return func(k *packages.Kit) { k.Requires = []string{"comment.policy"} }
}

func TestApplyPolicyOnlyWithTheTick(t *testing.T) {
	edit := withPolicyFeature(t)

	f := newFake(t).withRegistry(t)
	res, err := apply(t, f, kitPackage(t, edit), Accepts{Comments: true}, &enabler{})
	if err != nil || f.wrote("comments story") != 1 || policyEnabled(t, f) || !hasText(res.Messages, "Community policy left off") || len(res.PolicyOn) != 0 {
		t.Fatalf("err=%v writes=%v messages=%v", err, f.writes, res.Messages)
	}

	f = newFake(t).withRegistry(t)
	if res, err = apply(t, f, kitPackage(t, edit), Accepts{Comments: true, Policy: true}, &enabler{}); err != nil {
		t.Fatal(err)
	}
	if f.wrote("comments story") != 2 || !policyEnabled(t, f) { // the create's write, then the switch
		t.Fatalf("writes=%v settings=%s", f.writes, f.schemas["story"].s.Settings)
	}
	st, _ := f.SchemaComments(context.Background(), "story")
	if st.Policy.MaxDepth != 32 || st.Policy.EditWindowMinutes != 120 || st.Policy.SlowModeSeconds != 30 {
		t.Errorf("the kit's numbers must be kept: %+v", st.Policy)
	}
	if want := []PolicyChange{{Slug: "story", SchemaID: st.ID}}; !slices.Equal(res.PolicyOn, want) {
		t.Errorf("PolicyOn = %v, want %v", res.PolicyOn, want)
	}
}

// policyAudit collects what the PolicyOn callback saw.
type policyAudit struct{ got []PolicyChange }

func (a *policyAudit) on(_ context.Context, ch PolicyChange) { a.got = append(a.got, ch) }

func twoPolicyTypes(k *packages.Kit) {
	k.Requires = []string{"comment.policy"}
	second := k.Schemas[0]
	second.Slug, second.Name = "review", "Reviews"
	k.Schemas = append(k.Schemas, second)
	k.Switches.CommentPolicy = []string{"story", "review"}
}

func applyAudited(t *testing.T, f *fake, au *policyAudit) (*Result, error) {
	t.Helper()
	pl, err := BuildPlan(context.Background(), f.deps(), kitPackage(t, twoPolicyTypes), Options{})
	if err != nil {
		t.Fatal(err)
	}
	return Apply(context.Background(), f.deps(), pl, Accepts{Comments: true, Policy: true}, ApplyOptions{Actor: "owner", EnableComments: (&enabler{}).enable, PolicyOn: au.on})
}

func TestApplyAuditsEachPolicyOnce(t *testing.T) {
	f := newFake(t).withRegistry(t)
	au := &policyAudit{}
	res, err := applyAudited(t, f, au)
	if err != nil || len(au.got) != 2 || len(res.PolicyOn) != 2 || au.got[0].Slug != "story" || au.got[1].Slug != "review" {
		t.Fatalf("err=%v audited=%v", err, au.got)
	}
}

func TestApplyAuditsAPolicyWhenALaterOneFails(t *testing.T) {
	f := newFake(t).withRegistry(t)
	f.failPolicy = "review"
	au := &policyAudit{}
	if _, err := applyAudited(t, f, au); err == nil {
		t.Fatal("want the second policy write to fail")
	}
	if len(au.got) != 1 || au.got[0].Slug != "story" {
		t.Errorf("audited = %v, want story once", au.got)
	}
}

func TestApplyPolicyTwiceChangesNothing(t *testing.T) {
	edit := withPolicyFeature(t)
	f := newFake(t).withRegistry(t)
	acc := Accepts{Comments: true, Policy: true}
	if _, err := apply(t, f, kitPackage(t, edit), acc, &enabler{}); err != nil {
		t.Fatal(err)
	}
	f.comOn = true // the enabler fake does not switch comments on itself
	before := len(f.writes)
	res, err := apply(t, f, kitPackage(t, edit), acc, &enabler{})
	if err != nil || len(f.writes) != before || res.Recorded || len(res.PolicyOn) != 0 {
		t.Errorf("second apply: err=%v recorded=%v on=%v writes=%v", err, res.Recorded, res.PolicyOn, f.writes[before:])
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
	if st, _ := f.SchemaComments(context.Background(), "story"); st.Order != "rank" || !st.HasPolicy || st.Policy.MaxDepth != 32 {
		t.Errorf("order and policy not filled: %+v", st)
	}
	if f.wrote("comments story") != 1 || res.Filled != 1 {
		t.Errorf("one seam write expected: %v filled=%d", f.writes, res.Filled)
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

func TestApplyScopesAPermissionTickToItsPlugin(t *testing.T) {
	f := newFake(t)
	steps := []PackageStep{}
	for _, n := range []string{"plug-a", "plug-b"} {
		steps = append(steps, PackageStep{Kind: packages.KindPlugin, Name: n, Version: "1.0.0", Action: ActionInstall,
			Grant: []string{"db"}, Extra: []string{"site.scripts"}, Raw: pluginZip(t, n, "1.0.0", "")})
	}
	err := applyPackages(context.Background(), f.deps(), steps, Accepts{Extra: []string{ExtraKey("plug-a", "site.scripts")}}, &Result{Created: map[string]int{}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.grants["plug-a"], []string{"db", "site.scripts"}) || !slices.Equal(f.grants["plug-b"], []string{"db"}) {
		t.Errorf("grants a=%v b=%v: the tick must reach only plug-a", f.grants["plug-a"], f.grants["plug-b"])
	}
}

func TestApplyCreatesCommentOrderAndPolicyThroughTheSeam(t *testing.T) {
	f := newFake(t).withRegistry(t)
	f.pluginCreates = false
	if _, err := apply(t, f, kitPackage(t, nil), Accepts{}, &enabler{}); err != nil {
		t.Fatal(err)
	}
	if f.wrote("create schema story") != 1 || f.wrote("comments story") != 1 {
		t.Fatalf("writes = %v", f.writes)
	}
	st, _ := f.SchemaComments(context.Background(), "story")
	if st.Order != "rank" || st.Policy != (comment.Policy{MaxDepth: 32, EditWindowMinutes: 120, SlowModeSeconds: 30}) {
		t.Errorf("stored: %+v", st)
	}
	if !strings.Contains(string(f.schemas["story"].s.Settings), "/item/{slug}") || !strings.Contains(string(f.schemas["story"].s.Settings), `"mode"`) {
		t.Errorf("the other keys must survive: %s", f.schemas["story"].s.Settings)
	}
}

func TestApplyCreateKeepsOrderAndPolicyOutOfRawSettings(t *testing.T) {
	f := newFake(t).withRegistry(t)
	f.noSeam = true
	res, err := apply(t, f, kitPackage(t, nil), Accepts{}, &enabler{})
	if err != nil {
		t.Fatal(err)
	}
	if s := string(f.schemas["story"].s.Settings); strings.Contains(s, `"order"`) || strings.Contains(s, `"policy"`) {
		t.Errorf("created settings hold comment order or policy: %s", s)
	}
	if !hasText(res.Messages, "Comment order and policy of 'story' left unset") {
		t.Errorf("messages: %v", res.Messages)
	}
}

func TestPlanRefusesABadCommentPolicyBeforeAnyWrite(t *testing.T) {
	f := newFake(t).withRegistry(t)
	pl, err := BuildPlan(context.Background(), f.deps(), kitPackage(t, func(k *packages.Kit) {
		k.Schemas[0].Settings = jsontext.Value(`{"comments":{"policy":{"max_depth":99}}}`)
	}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasText(pl.Refusals, "max_depth") {
		t.Fatalf("refusals = %v", pl.Refusals)
	}
	var re *RefusedError
	if _, err := Apply(context.Background(), f.deps(), pl, Accepts{}, ApplyOptions{}); !errors.As(err, &re) || len(f.writes) != 0 {
		t.Errorf("err=%v writes=%v", err, f.writes)
	}
}
