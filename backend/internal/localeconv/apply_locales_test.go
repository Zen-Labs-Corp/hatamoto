package localeconv

import "testing"

func TestPlannedSiteLocalesKeepsHiddenAndName(t *testing.T) {
	t.Parallel()
	plan := mustParse(t, validPlan)
	stored := []byte(`[{"tag":"en-ZZ","segment":"old","hidden":true,"name":"Zed"},{"tag":"fr","segment":"fr","hidden":true}]`)
	got, err := plannedSiteLocales(stored, plan)
	if err != nil {
		t.Fatal(err)
	}
	want := []SiteLocale{{Tag: "en-XX", Segment: "xx"}, {Tag: "en-ZZ", Segment: "zz", Hidden: true, Name: "Zed"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got, err = plannedSiteLocales(nil, plan); err != nil || got[1].Hidden {
		t.Errorf("no stored list: got %+v err %v", got, err)
	}
}
