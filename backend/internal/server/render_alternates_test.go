package server

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

func altSet(t *testing.T, xdef string) *entity.LocaleSet {
	t.Helper()
	set, err := entity.NewLocaleSet("en", []entity.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr"}, {Tag: "nl", Segment: "nl"}}, false, xdef)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestHomeAlternates_OrderAndXDefault(t *testing.T) {
	set := altSet(t, "")
	reg := entity.NewURLRegistry(pageSchemaLister{}, nil)
	reg.SetLocales(func(context.Context) (*entity.LocaleSet, error) { return set, nil })
	snap, err := reg.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	alts, xdef := homeAlternates(snap, set, "https://x.io", "fr")
	if len(alts) != 3 || alts[0].Locale != "en" || alts[2].Locale != "nl" {
		t.Fatalf("alts = %+v", alts)
	}
	if alts[1].Href != "https://x.io/fr" || alts[2].Href != "https://x.io/nl" || alts[0].Href != "https://x.io/" || !alts[1].Current {
		t.Errorf("hrefs = %+v", alts)
	}
	if xdef != "https://x.io/" {
		t.Errorf("x-default = %q, want the default home", xdef)
	}
}

func TestXDefaultHref(t *testing.T) {
	alts := []render.Alternate{
		{Locale: "en", Href: "/en", Default: true, Missing: true},
		{Locale: "fr", Href: "/fr"},
		{Locale: "nl", Href: "/nl"},
	}
	live := func(a render.Alternate) bool { return !a.Missing }
	if got := xDefaultHref(altSet(t, ""), alts, live); got != "/fr" {
		t.Errorf("default missing: first live = %q, want /fr", got)
	}
	if got := xDefaultHref(altSet(t, "nl"), alts, live); got != "/nl" {
		t.Errorf("setting live = %q, want /nl", got)
	}
	alts[0].Missing = false
	if got := xDefaultHref(altSet(t, ""), alts, live); got != "/en" {
		t.Errorf("default live = %q, want /en", got)
	}
	alts[2].Missing, alts[1].Missing = true, true
	if got := xDefaultHref(altSet(t, "nl"), alts, live); got != "/en" {
		t.Errorf("setting missing falls back = %q, want /en", got)
	}
}

func hiddenAltSet(t *testing.T) (*entity.LocaleSet, *entity.URLSnapshot) {
	t.Helper()
	set, err := entity.NewLocaleSet("en", []entity.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr", Hidden: true}, {Tag: "nl", Segment: "nl"}}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	reg := entity.NewURLRegistry(pageSchemaLister{}, nil)
	reg.SetLocales(func(context.Context) (*entity.LocaleSet, error) { return set, nil })
	snap, err := reg.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return set, snap
}

func TestHomeAlternatesSkipHiddenLocale(t *testing.T) {
	set, snap := hiddenAltSet(t)
	alts, _ := homeAlternates(snap, set, "https://x.io", "en")
	if len(alts) != 2 || alts[0].Locale != "en" || alts[1].Locale != "nl" {
		t.Fatalf("alts = %+v", alts)
	}
}
