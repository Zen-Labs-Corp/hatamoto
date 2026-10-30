package localeconv

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

var fixtureEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fixtureSeq makes created_at strictly increasing so tie-breaks are visible.
var fixtureSeq int

func ent(id, schema, slug, parent, status, attrs string) Ent {
	fixtureSeq++
	if attrs == "" {
		attrs = "{}"
	}
	pub := fixtureEpoch
	return Ent{
		ID: id, Schema: schema, Slug: slug, ParentID: parent, Locale: "en", Status: status,
		Nested: schema == "page", Attrs: json.RawMessage(attrs),
		CreatedAt: fixtureEpoch.Add(time.Duration(fixtureSeq) * time.Minute), UpdatedAt: fixtureEpoch, PublishedAt: &pub,
	}
}

const pageSettings = `{"url_pattern":"/{slug}","overrides":[
 {"attribute":"region","equals":"xx","url_pattern":"/{path}","sitemap_group":"xx"},
 {"attribute":"region","equals":"yy","url_pattern":"/{path}","sitemap_group":"yy"},
 {"attribute":"region","equals":"zz","url_pattern":"/{path}","sitemap_group":"zz"}]}`

func region(tag, seg string) string {
	return fmt.Sprintf(`{"region":%q,"lang":%q}`, seg, tag)
}

// fixtureSnap is a three-region tree: each region root has children three
// levels deep, one archived "home" child, translation groups (one with a
// missing copy, one singleton) and a shared post.
func fixtureSnap() *Snapshot {
	rx, ry, rz := region("en-XX", "xx"), region("en-XA", "yy"), region("en-ZZ", "zz")
	child := func(seg string) string { return fmt.Sprintf(`{"region":%q}`, seg) }
	withGroup := func(seg, g string) string { return fmt.Sprintf(`{"region":%q,"key":%q}`, seg, g) }
	fixtureSeq = 0
	return &Snapshot{
		Schemas: []Schema{
			{ID: "s-page", Slug: "page", Name: "Page", Settings: json.RawMessage(pageSettings)},
			{ID: "s-post", Slug: "post", Name: "Post", Settings: json.RawMessage(`{}`)},
		},
		Site: SiteState{Language: "en"},
		Entities: []Ent{
			ent("x0", "page", "xx", "", "published", rx),
			ent("x1", "page", "about", "x0", "published", withGroup("xx", "g-about")),
			ent("x2", "page", "team", "x1", "published", withGroup("xx", "g-team")),
			ent("x3", "page", "bio", "x2", "published", child("xx")),
			ent("x4", "page", "home", "x0", "archived", child("xx")),
			ent("y0", "page", "yy", "", "published", ry),
			ent("y1", "page", "about", "y0", "published", withGroup("yy", "g-about")),
			ent("y2", "page", "solo", "y0", "published", withGroup("yy", "g-solo")),
			ent("z0", "page", "zz", "", "published", rz),
			ent("z1", "page", "about", "z0", "published", withGroup("zz", "g-about")),
			ent("z2", "page", "team", "z0", "published", withGroup("zz", "g-team")),
			ent("p1", "post", "news", "", "published", `{}`),
		},
	}
}

func fixturePlan(t *testing.T, mutate ...func(*Plan)) *Plan {
	t.Helper()
	p := mustParse(t, `{
  "locales": [
    {"tag":"en-XX","segment":"xx","root_slug":"xx"},
    {"tag":"en-XA","segment":"yy","root_slug":"yy"},
    {"tag":"en-ZZ","segment":"zz","root_slug":"zz"}
  ],
  "default": "en-XX", "x_default": "en-XX",
  "locale_from": {"attribute":"region","map":{"xx":"en-XX","yy":"en-XA","zz":"en-ZZ"}},
  "group_from": {"attribute":"key"}
}`)
	for _, m := range mutate {
		m(p)
	}
	return p
}

func planned(t *testing.T, ch *Changes, id string) *Planned {
	t.Helper()
	for i := range ch.Planned {
		if ch.Planned[i].Ent.ID == id {
			return &ch.Planned[i]
		}
	}
	t.Fatalf("no planned row %s", id)
	return nil
}

func hasCode(issues []Issue, code string) bool { return slices.Contains(codes(issues), code) }

func TestAnalyzeLocalesHomesAndParents(t *testing.T) {
	ch, rep := Analyze(fixtureSnap(), fixturePlan(t))
	if !rep.OK {
		t.Fatalf("errors: %+v", rep.Errors)
	}
	for id, want := range map[string]State{
		"x0": {Locale: "en-XX", Slug: "home"},                 // home keeps top level, takes the home slug
		"x1": {Locale: "en-XX", Slug: "about"},                // child of a home goes top level
		"x2": {Locale: "en-XX", Slug: "team", ParentID: "x1"}, // deeper rows keep their chain
		"x3": {Locale: "en-XX", Slug: "bio", ParentID: "x2"},
		"y0": {Locale: "en-XA", Slug: "home"},
		"z1": {Locale: "en-ZZ", Slug: "about"},
		"p1": {Locale: "und", Slug: "news"}, // no attribute, no home above: shared
	} {
		got := planned(t, ch, id).After
		if got.Locale != want.Locale || got.Slug != want.Slug || got.ParentID != want.ParentID {
			t.Errorf("%s after = %+v, want %+v", id, got, want)
		}
	}
	if !planned(t, ch, "x0").Home || planned(t, ch, "x1").Home {
		t.Error("only the region roots are homes")
	}
	if planned(t, ch, "p1").Changed() != true { // en -> und
		t.Error("shared row retag not counted as a change")
	}
}

func TestAnalyzeArchivedHomeRenamed(t *testing.T) {
	ch, rep := Analyze(fixtureSnap(), fixturePlan(t))
	if len(rep.RenamedCollisions) != 1 {
		t.Fatalf("renamed = %+v", rep.RenamedCollisions)
	}
	r := rep.RenamedCollisions[0]
	if r.ID != "x4" || r.OldSlug != "home" || r.NewSlug != "home-old-1" || r.Schema != "page" || r.Reason == "" {
		t.Errorf("renamed row = %+v", r)
	}
	if got := planned(t, ch, "x4").After; got.Slug != "home-old-1" || got.ParentID != "" {
		t.Errorf("x4 after = %+v", got)
	}
	if planned(t, ch, "x0").After.Slug != "home" {
		t.Error("the home must keep the slug")
	}
}

func TestAnalyzeRenameFindsFirstFreeNumber(t *testing.T) {
	snap := fixtureSnap()
	snap.Entities = append(snap.Entities, ent("x5", "page", "home-old-1", "x0", "draft", `{"region":"xx"}`))
	_, rep := Analyze(snap, fixturePlan(t))
	if len(rep.RenamedCollisions) != 1 || rep.RenamedCollisions[0].NewSlug != "home-old-2" {
		t.Errorf("renamed = %+v, want home-old-2", rep.RenamedCollisions)
	}
}

func TestAnalyzePublishedCollisionBlocks(t *testing.T) {
	snap := fixtureSnap()
	snap.Entities[4].Status = "published" // x4, the "home" child of the xx home
	_, rep := Analyze(snap, fixturePlan(t))
	if rep.OK || !hasCode(rep.Errors, CodePublishedCollision) {
		t.Fatalf("report = %+v", rep.Errors)
	}
	if rep.Errors[0].EntityID != "x4" {
		t.Errorf("collision blames %q, want x4", rep.Errors[0].EntityID)
	}
}

func TestAnalyzeOnConflictAbort(t *testing.T) {
	_, rep := Analyze(fixtureSnap(), fixturePlan(t, func(p *Plan) { p.Home.OnConflict = OnConflictAbort }))
	if rep.OK || !hasCode(rep.Errors, CodeSlugCollision) || len(rep.RenamedCollisions) != 0 {
		t.Errorf("errors = %+v renamed = %+v", rep.Errors, rep.RenamedCollisions)
	}
}

func TestAnalyzeGroups(t *testing.T) {
	ch, rep := Analyze(fixtureSnap(), fixturePlan(t))
	// g-about (3 locales) and g-team (xx and zz, a missing copy) form groups; g-solo stays alone.
	if rep.Groups.Formed != 2 || rep.Groups.Singletons != 1 || len(rep.Groups.Conflicts) != 0 {
		t.Errorf("groups = %+v", rep.Groups)
	}
	for id, key := range map[string]string{"x1": "g-about", "y1": "g-about", "z1": "g-about", "x2": "g-team", "z2": "g-team", "y2": ""} {
		if got := planned(t, ch, id).GroupKey; got != key {
			t.Errorf("%s group key = %q, want %q", id, got, key)
		}
	}
}

func TestAnalyzeGroupTwoInLocale(t *testing.T) {
	snap := fixtureSnap()
	snap.Entities = append(snap.Entities, ent("x6", "page", "about-2", "x0", "published", `{"region":"xx","key":"g-about"}`))
	_, rep := Analyze(snap, fixturePlan(t))
	if rep.OK || !hasCode(rep.Errors, CodeGroupTwoInLocale) {
		t.Fatalf("errors = %+v", rep.Errors)
	}
	c := rep.Groups.Conflicts
	if len(c) != 1 || c[0].Value != "g-about" || c[0].Locale != "en-XX" || !slices.Equal(c[0].IDs, []string{"x1", "x6"}) {
		t.Errorf("conflicts = %+v", c)
	}
}

func TestAnalyzeCounts(t *testing.T) {
	_, rep := Analyze(fixtureSnap(), fixturePlan(t))
	want := map[string]int{"en-XX": 5, "en-XA": 3, "en-ZZ": 3}
	for _, l := range rep.Locales {
		if l.Total != want[l.Tag] || l.CountsBySchema["page"] != l.Total {
			t.Errorf("%s = %+v, want total %d", l.Tag, l, want[l.Tag])
		}
	}
	if rep.Shared.Total != 1 || rep.Shared.CountsBySchema["post"] != 1 {
		t.Errorf("shared = %+v", rep.Shared)
	}
	if rep.Locales[0].Home.ID != "x0" || rep.Locales[1].Segment != "yy" {
		t.Errorf("locales = %+v", rep.Locales)
	}
}

func TestAnalyzeUnmatchedAndNoHome(t *testing.T) {
	snap := fixtureSnap()
	snap.Entities = append(snap.Entities,
		ent("q1", "page", "stray", "", "published", `{"region":"qq"}`),
		ent("q2", "page", "stray-2", "", "published", `{"region":"qq"}`),
		ent("q3", "page", "mapped", "", "published", `{"region":"yy"}`))
	ch, rep := Analyze(snap, fixturePlan(t))
	if len(rep.UnmatchedValues) != 1 || rep.UnmatchedValues[0].Value != "qq" || rep.UnmatchedValues[0].Count != 2 {
		t.Errorf("unmatched = %+v", rep.UnmatchedValues)
	}
	if !hasCode(rep.Warnings, CodeUnmatchedValue) || !hasCode(rep.Warnings, CodeAttributeWithoutHome) || !rep.OK {
		t.Errorf("warnings = %+v errors = %+v", rep.Warnings, rep.Errors)
	}
	if got := planned(t, ch, "q1").After.Locale; got != "und" {
		t.Errorf("unmatched without a home = %q, want und", got)
	}
	if got := planned(t, ch, "q3").After; got.Locale != "en-XA" || got.ParentID != "" {
		t.Errorf("mapped top-level row = %+v", got)
	}
}

func TestAnalyzeFallbackShared(t *testing.T) {
	snap := fixtureSnap()
	snap.Entities = append(snap.Entities, ent("x7", "page", "plain", "x0", "published", `{}`))
	ch, _ := Analyze(snap, fixturePlan(t))
	if got := planned(t, ch, "x7").After.Locale; got != "en-XX" {
		t.Errorf("fallback root: locale = %q, want en-XX", got)
	}
	ch, _ = Analyze(snap, fixturePlan(t, func(p *Plan) { p.LocaleFrom.Fallback = FallbackShared }))
	if got := planned(t, ch, "x7").After.Locale; got != "und" {
		t.Errorf("fallback shared: locale = %q, want und", got)
	}
}

func TestAnalyzeRootProblems(t *testing.T) {
	snap := fixtureSnap()
	snap.Entities[0].Status = "draft"
	_, rep := Analyze(snap, fixturePlan(t))
	if rep.OK || !hasCode(rep.Errors, CodeRootNotFound) {
		t.Errorf("errors = %+v", rep.Errors)
	}
	snap = fixtureSnap()
	snap.Entities = append(snap.Entities, ent("x9", "page", "xx", "", "published", `{}`))
	if _, rep = Analyze(snap, fixturePlan(t)); !hasCode(rep.Errors, CodeRootAmbiguous) {
		t.Errorf("errors = %+v", rep.Errors)
	}
	if _, rep = Analyze(fixtureSnap(), fixturePlan(t, func(p *Plan) { p.RootSchema = "nope" })); !hasCode(rep.Errors, CodeUnknownSchema) {
		t.Errorf("errors = %+v", rep.Errors)
	}
}

func TestAnalyzeValidationErrorsStopEarly(t *testing.T) {
	ch, rep := Analyze(fixtureSnap(), fixturePlan(t, func(p *Plan) { p.Default = "en-QQ" }))
	if rep.OK || !hasCode(rep.Errors, CodeDefaultNotListed) || len(ch.Planned) != 0 {
		t.Errorf("errors = %+v planned = %d", rep.Errors, len(ch.Planned))
	}
}

func TestAnalyzeStripAttributes(t *testing.T) {
	ch, _ := Analyze(fixtureSnap(), fixturePlan(t, func(p *Plan) { p.StripAttributes = []string{"lang", "absent"} }))
	if got := planned(t, ch, "x0").Strip; !slices.Equal(got, []string{"lang"}) {
		t.Errorf("x0 strip = %v", got)
	}
	if got := planned(t, ch, "x1").Strip; len(got) != 0 {
		t.Errorf("x1 strip = %v, want none", got)
	}
}

func TestAnalyzeIsDeterministic(t *testing.T) {
	snap := fixtureSnap()
	slices.Reverse(snap.Entities)
	a, ra := Analyze(snap, fixturePlan(t))
	b, rb := Analyze(fixtureSnap(), fixturePlan(t))
	ja, _ := json.Marshal(ra)
	jb, _ := json.Marshal(rb)
	if string(ja) != string(jb) {
		t.Errorf("reports differ with input order:\n%s\n%s", ja, jb)
	}
	if len(a.Planned) != len(b.Planned) || a.Planned[0].Ent.ID != b.Planned[0].Ent.ID {
		t.Error("planned rows are not ordered by id")
	}
	if strings.Contains(string(ja), "null") {
		t.Errorf("report has null lists: %s", ja)
	}
}
