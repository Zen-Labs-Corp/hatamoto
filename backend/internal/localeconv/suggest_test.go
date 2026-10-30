package localeconv

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
)

type fakeLoader struct{ snap *Snapshot }

func (f fakeLoader) LoadSnapshot(context.Context) (*Snapshot, error) { return f.snap, nil }

const (
	gUUID1 = "11111111-1111-4111-8111-111111111111"
	gUUID2 = "22222222-2222-4222-8222-222222222222"
)

func suggestSnap() *Snapshot {
	g := func(seg, key string) string { return fmt.Sprintf(`{"region":%q,"key":%q}`, seg, key) }
	fixtureSeq = 0
	return &Snapshot{
		Site:    SiteState{Language: "en-ZZ"},
		Schemas: []Schema{{ID: "s", Slug: "page", Settings: []byte(pageSettings)}},
		Entities: []Ent{
			ent("a0", "page", "xx", "", "published", `{"region":"xx","lang":"en-XX"}`),
			ent("a1", "page", "about", "a0", "published", g("xx", gUUID1)),
			ent("b0", "page", "yy", "", "published", `{"region":"yy","lang":"en-XA"}`),
			ent("b1", "page", "about", "b0", "published", g("yy", gUUID1)),
			ent("b2", "page", "more", "b0", "published", g("yy", gUUID2)),
			ent("c0", "page", "zz", "", "published", `{"region":"zz","locale":"en-ZZ"}`),
			ent("c1", "page", "about", "c0", "published", g("zz", gUUID1)),
			ent("c2", "page", "more", "c0", "published", g("zz", gUUID2)),
			ent("p1", "page", "contact", "", "published", `{}`), // top-level, no children: not a root
		},
	}
}

func TestSuggestFindsTheRegionTree(t *testing.T) {
	plan, warnings, err := SuggestFrom(context.Background(), fakeLoader{suggestSnap()})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v", warnings)
	}
	if plan.LocaleFrom.Attribute != "region" || plan.GroupFrom == nil || plan.GroupFrom.Attribute != "key" {
		t.Errorf("attributes = %+v group %+v", plan.LocaleFrom, plan.GroupFrom)
	}
	var tags, segs []string
	for _, l := range plan.Locales {
		tags, segs = append(tags, l.Tag), append(segs, l.Segment)
		if l.Segment != l.RootSlug {
			t.Errorf("segment %q must default to the slug %q", l.Segment, l.RootSlug)
		}
	}
	// created_at order: xx, yy, zz. The site language en-ZZ is the default.
	if !slices.Equal(tags, []string{"en-XX", "en-XA", "en-ZZ"}) || !slices.Equal(segs, []string{"xx", "yy", "zz"}) {
		t.Errorf("locales = %+v", plan.Locales)
	}
	if plan.Default != "en-ZZ" || plan.XDefault != "en-ZZ" || !plan.PrefixAllValue() {
		t.Errorf("default = %q x-default = %q", plan.Default, plan.XDefault)
	}
	if plan.LocaleFrom.Map["yy"] != "en-XA" || plan.LocaleFrom.Fallback != FallbackRoot {
		t.Errorf("map = %+v", plan.LocaleFrom)
	}
	if issues := plan.Validate(); len(issues) != 0 {
		t.Errorf("suggested plan does not validate: %+v", issues)
	}
	_, rep := Analyze(suggestSnap(), plan)
	if !rep.OK || rep.PathChanges.Count != 0 {
		t.Errorf("suggested plan does not analyse cleanly: errors %+v changes %+v", rep.Errors, rep.PathChanges)
	}
}

func TestSuggestRootWithoutTag(t *testing.T) {
	snap := suggestSnap()
	snap.Entities[2].Attrs = []byte(`{"region":"yy"}`)
	plan, warnings, err := Suggest(snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || warnings[0].Code != CodeNoTag || warnings[0].EntityID != "b0" {
		t.Errorf("warnings = %+v", warnings)
	}
	if plan.Locales[1].Tag != "" || !hasCode(plan.Validate(), CodeBadTag) {
		t.Errorf("locales = %+v, want an empty tag that validation catches", plan.Locales)
	}
	if _, ok := plan.LocaleFrom.Map["yy"]; ok {
		t.Error("an empty tag must not be mapped")
	}
}

func TestSuggestTieBreaksAlphabetically(t *testing.T) {
	// "area" and "region" both match every root: the alphabetical first wins.
	snap := suggestSnap()
	for _, i := range []int{0, 2, 5} {
		snap.Entities[i].Attrs = fmt.Appendf(nil, `{"region":%q,"area":%q,"lang":%q}`, snap.Entities[i].Slug, snap.Entities[i].Slug, "en-XX")
	}
	plan, _, err := Suggest(snap)
	if err != nil || plan.LocaleFrom.Attribute != "area" {
		t.Errorf("attribute = %q (%v), want area", plan.LocaleFrom.Attribute, err)
	}
}

func TestSuggestPicksAttributeMatchingMostRoots(t *testing.T) {
	snap := suggestSnap()
	snap.Entities[5].Attrs = []byte(`{"region":"other","area":"zz","locale":"en-ZZ"}`)
	snap.Entities[0].Attrs = []byte(`{"region":"xx","area":"xx","lang":"en-XX"}`)
	snap.Entities[2].Attrs = []byte(`{"region":"yy","area":"yy","lang":"en-XA"}`)
	plan, _, _ := Suggest(snap)
	if plan.LocaleFrom.Attribute != "area" {
		t.Errorf("attribute = %q, want area (3 roots against 2)", plan.LocaleFrom.Attribute)
	}
}

func TestSuggestNeedsTwoRoots(t *testing.T) {
	snap := suggestSnap()
	snap.Entities = snap.Entities[:4] // xx and yy only
	if _, _, err := Suggest(snap); err != nil {
		t.Fatalf("two roots are enough: %v", err)
	}
	snap.Entities = snap.Entities[:2]
	if _, _, err := Suggest(snap); !errors.Is(err, ErrNoRegionTree) {
		t.Errorf("err = %v, want ErrNoRegionTree", err)
	}
	if _, _, err := Suggest(&Snapshot{}); !errors.Is(err, ErrNoRegionTree) {
		t.Errorf("empty site: err = %v", err)
	}
}

func TestSuggestDefaultByDescendantsWithoutSiteLanguage(t *testing.T) {
	snap := suggestSnap()
	snap.Site.Language = "en"
	plan, _, _ := Suggest(snap)
	// yy and zz both have two descendants; the earlier one in order wins.
	if plan.Default != "en-XA" {
		t.Errorf("default = %q, want en-XA", plan.Default)
	}
}

func TestSuggestOrdersByStoredAlternates(t *testing.T) {
	snap := suggestSnap()
	snap.Entities[1].Attrs = fmt.Appendf(nil, `{"region":"xx","key":%q,"alternates":[{"lang":"en-ZZ"},{"lang":"en-XX"},{"lang":"en-XA"}]}`, gUUID1)
	plan, _, _ := Suggest(snap)
	var segs []string
	for _, l := range plan.Locales {
		segs = append(segs, l.Segment)
	}
	if !slices.Equal(segs, []string{"zz", "xx", "yy"}) {
		t.Errorf("order = %v, want the alternates order", segs)
	}
}

func TestSuggestWithoutGroups(t *testing.T) {
	snap := suggestSnap()
	for i := range snap.Entities {
		if snap.Entities[i].Slug == "about" || snap.Entities[i].Slug == "more" {
			snap.Entities[i].Attrs = fmt.Appendf(nil, `{"region":%q}`, "xx")
		}
	}
	plan, _, _ := Suggest(snap)
	if plan.GroupFrom != nil {
		t.Errorf("group_from = %+v, want none", plan.GroupFrom)
	}
}

func TestSuggestIsDeterministic(t *testing.T) {
	a, _, _ := Suggest(suggestSnap())
	snap := suggestSnap()
	slices.Reverse(snap.Entities)
	b, _, _ := Suggest(snap)
	if fmt.Sprint(*a.PrefixAll, a.Locales, a.Default, a.LocaleFrom) != fmt.Sprint(*b.PrefixAll, b.Locales, b.Default, b.LocaleFrom) {
		t.Errorf("plans differ with input order:\n%+v\n%+v", a, b)
	}
}
