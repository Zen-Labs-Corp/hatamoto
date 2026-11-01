package localeconv

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestPathCheckFixtureHasNoChanges(t *testing.T) {
	_, rep := Analyze(fixtureSnap(), fixturePlan(t))
	if !rep.OK || rep.PathChanges.Count != 0 {
		t.Fatalf("path changes = %+v, errors = %+v", rep.PathChanges, rep.Errors)
	}
	// Region homes are counted as /<segment>, before and after.
	for _, l := range rep.Locales {
		want := "/" + l.Segment
		if l.Home.OldPath != want || l.Home.NewPath != want {
			t.Errorf("%s home = %+v, want %s both ways", l.Tag, l.Home, want)
		}
	}
}

func TestPathCheckSegmentMismatchChangesEveryURL(t *testing.T) {
	_, rep := Analyze(fixtureSnap(), fixturePlan(t, func(p *Plan) { p.Locales[0].Segment = "ex" }))
	if rep.OK || !hasCode(rep.Errors, CodePathChanges) {
		t.Fatalf("errors = %+v", rep.Errors)
	}
	if rep.PathChanges.Count != 4 { // the home and its three published descendants; x4 is archived
		t.Errorf("changes = %+v", rep.PathChanges.Items)
	}
	for _, c := range rep.PathChanges.Items {
		if c.ID == "x0" && (c.Old != "/xx" || c.New != "/ex") {
			t.Errorf("home change = %+v", c)
		}
	}
}

func TestPathCheckPrefixAllFalseChangesDefaultLocale(t *testing.T) {
	f := false
	_, rep := Analyze(fixtureSnap(), fixturePlan(t, func(p *Plan) { p.PrefixAll = &f }))
	if rep.OK || rep.PathChanges.Count == 0 {
		t.Fatalf("prefix_all=false must change the default locale's URLs: %+v", rep.PathChanges)
	}
	for _, c := range rep.PathChanges.Items {
		if c.ID[0] != 'x' {
			t.Errorf("only the default locale loses its prefix, got %+v", c)
		}
	}
}

func TestPathCheckSkipsUnpublished(t *testing.T) {
	snap := fixtureSnap()
	snap.Entities = append(snap.Entities, ent("d1", "page", "draft-page", "", "draft", `{"region":"yy"}`))
	_, rep := Analyze(snap, fixturePlan(t))
	for _, c := range rep.PathChanges.Items {
		if c.ID == "d1" {
			t.Error("a draft's URL is not public, it must not block")
		}
	}
}

func TestPathCheckStripOfMatchedAttribute(t *testing.T) {
	_, rep := Analyze(fixtureSnap(), fixturePlan(t, func(p *Plan) { p.StripAttributes = []string{"region"} }))
	if rep.OK || !hasCode(rep.Errors, CodeStripNeededAttribute) {
		t.Errorf("errors = %+v", rep.Errors)
	}
}

func TestStripSitemapGroupKeepsEverythingElse(t *testing.T) {
	snap := fixtureSnap()
	out, changed, kept, err := StripSitemapGroup(snap.Schemas, "region")
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 3 || changed[0] != (OverrideChange{Schema: "page", Attribute: "region", Equals: "xx", Group: "xx"}) {
		t.Errorf("changed = %+v", changed)
	}
	if !slices.Equal(kept, []string{"url_pattern"}) {
		t.Errorf("kept = %v", kept)
	}
	var st struct {
		URLPattern string `json:"url_pattern"`
		Overrides  []map[string]any
	}
	if err := json.Unmarshal(out[0].Settings, &st); err != nil {
		t.Fatal(err)
	}
	if st.URLPattern != "/{slug}" || len(st.Overrides) != 3 {
		t.Fatalf("settings = %s", out[0].Settings)
	}
	for _, o := range st.Overrides {
		if _, has := o["sitemap_group"]; has || o["url_pattern"] != "/{path}" || o["attribute"] != "region" || o["equals"] == nil {
			t.Errorf("override = %v", o)
		}
	}
	if !bytes.Equal(out[1].Settings, snap.Schemas[1].Settings) {
		t.Error("a schema without overrides must stay as it is")
	}
	if bytes.Contains(snap.Schemas[0].Settings, []byte(`"sitemap_group"`)) == false {
		t.Error("the input must not be modified")
	}
}

func TestStripSitemapGroupOtherAttribute(t *testing.T) {
	out, changed, kept, _ := StripSitemapGroup(fixtureSnap().Schemas, "other")
	if len(changed) != 0 || len(kept) != 0 || !bytes.Equal(out[0].Settings, []byte(pageSettings)) {
		t.Errorf("changed = %+v", changed)
	}
}

func TestAnalyzeRecordsSchemaChangeAndSettingsDelta(t *testing.T) {
	ch, rep := Analyze(fixtureSnap(), fixturePlan(t))
	if len(ch.Schemas) != 1 || ch.Schemas[0].Slug != "page" || bytes.Contains(ch.Schemas[0].After, []byte("sitemap_group")) {
		t.Errorf("schema changes = %+v", ch.Schemas)
	}
	want := SiteSettingsDelta{Language: [2]string{"en", "en-XX"}, LocalesCount: [2]int{0, 3}, PrefixAll: [2]bool{false, true}, XDefault: [2]string{"", "en-XX"}, SitemapNaming: [2]string{"", "legacy"}}
	if rep.SiteSettings != want {
		t.Errorf("settings = %+v", rep.SiteSettings)
	}
	if rep.PlanSHA256 == "" || rep.DataFingerprint == "" {
		t.Error("hashes missing")
	}
}

func TestFingerprintTracksTouchedRows(t *testing.T) {
	_, a := Analyze(fixtureSnap(), fixturePlan(t))
	snap := fixtureSnap()
	snap.Entities[1].UpdatedAt = snap.Entities[1].UpdatedAt.Add(time.Second)
	_, b := Analyze(snap, fixturePlan(t))
	if a.DataFingerprint == b.DataFingerprint {
		t.Error("an editor save of a touched row must change the fingerprint")
	}
	_, c := Analyze(fixtureSnap(), fixturePlan(t))
	if a.DataFingerprint != c.DataFingerprint {
		t.Error("the fingerprint must be stable")
	}
}

func TestRedirectsUnderSegments(t *testing.T) {
	snap := fixtureSnap()
	snap.RedirectSources = []string{"/xx/home", "/xx", "/xxl", "/other", "/yy/old/page", "/xx/home"}
	_, rep := Analyze(snap, fixturePlan(t))
	if rep.Redirects.Count != 3 || !slices.Equal(rep.Redirects.ItemsSample, []string{"/xx", "/xx/home", "/yy/old/page"}) {
		t.Errorf("redirects = %+v", rep.Redirects)
	}
}
