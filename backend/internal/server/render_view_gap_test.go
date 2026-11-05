package server

import (
	"encoding/json/jsontext"
	"reflect"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/siteconfig"
)

const gapSiteProfile = `{"catalog_name":"Offers","service_provider":true,
  "services":[{"name":"Repair","url":"/services/repair","price_min":"10","price_max":"20","unit_text":"HOUR"}],
  "additional_properties":[{"name":"Recommendation score","value":"9.6","min_value":"0","max_value":"10"},{"value":"nameless"}]}`

func gapConfig(cards string) *siteconfig.Config {
	return &siteconfig.Config{
		BusinessProfile: jsontext.Value(gapSiteProfile),
		LocaleProfiles:  jsontext.Value(cards),
	}
}

func TestOrgViewGapFields(t *testing.T) {
	org, err := orgView(gapConfig(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if org.CatalogName != "Offers" || !org.ServiceProvider {
		t.Errorf("catalogue = %q provider = %v", org.CatalogName, org.ServiceProvider)
	}
	wantSvc := []render.ServiceView{{Name: "Repair", URL: "/services/repair", PriceMin: "10", PriceMax: "20", UnitText: "HOUR"}}
	if !reflect.DeepEqual(org.Services, wantSvc) {
		t.Errorf("services = %+v", org.Services)
	}
	wantProps := []render.PropertyView{{Name: "Recommendation score", Value: "9.6", MinValue: "0", MaxValue: "10"}}
	if !reflect.DeepEqual(org.AdditionalProperties, wantProps) {
		t.Errorf("properties = %+v", org.AdditionalProperties)
	}
	empty, err := orgView(&siteconfig.Config{BusinessProfile: jsontext.Value(`{"service_provider":false}`)})
	if err != nil || empty.ServiceProvider || empty.CatalogName != "" || empty.AdditionalProperties != nil {
		t.Errorf("empty profile = %+v, %v", empty, err)
	}
}

func TestLocaleBusinessResolvesGapFields(t *testing.T) {
	set := businessSet(t, false, entity.LocaleEntry{Tag: "en"}, entity.LocaleEntry{Tag: "fr", Segment: "fr"}, entity.LocaleEntry{Tag: "nl", Segment: "nl"})
	cfg := gapConfig(`{
	  "fr": {"address":{"street":"Example Street 1","city":"Sample City"}},
	  "nl": {"address":{"street":"Example Street 2","city":"Sample City"},"catalog_name":"Aanbod","service_provider":false,
	         "additional_properties":[{"name":"Tier","value":"Gold"}]}}`)

	inherit, err := localeBusiness(cfg, set, "fr")
	if err != nil || inherit == nil {
		t.Fatalf("fr: %+v, %v", inherit, err)
	}
	if inherit.CatalogName != "Offers" || !inherit.ServiceProvider || len(inherit.AdditionalProperties) != 1 ||
		inherit.AdditionalProperties[0].Name != "Recommendation score" || len(inherit.Services) != 1 {
		t.Errorf("empty card did not inherit: %+v", inherit)
	}

	own, err := localeBusiness(cfg, set, "nl")
	if err != nil || own == nil {
		t.Fatalf("nl: %+v, %v", own, err)
	}
	if own.CatalogName != "Aanbod" || own.ServiceProvider ||
		!reflect.DeepEqual(own.AdditionalProperties, []render.PropertyView{{Name: "Tier", Value: "Gold"}}) {
		t.Errorf("card override lost: %+v", own)
	}
}

func TestLocaleSummariesMarkDefault(t *testing.T) {
	set := businessSet(t, false, entity.LocaleEntry{Tag: "en"}, entity.LocaleEntry{Tag: "fr", Segment: "fr"})
	got, err := localeSummaries(gapConfig(`{}`), set)
	if err != nil {
		t.Fatal(err)
	}
	def := map[string]bool{}
	for _, l := range got {
		def[l.Tag] = l.Default
	}
	if !reflect.DeepEqual(def, map[string]bool{"en": true, "fr": false}) {
		t.Errorf("default marks = %v", def)
	}
}

func TestLocaleSummariesResolveCardWithoutOffice(t *testing.T) {
	set := businessSet(t, false, entity.LocaleEntry{Tag: "en"}, entity.LocaleEntry{Tag: "fr", Segment: "fr"})
	cfg := gapConfig(`{"fr": {"catalog_name":"Offres","service_provider":false,
	  "additional_properties":[{"name":"Tier","value":"Gold"}]}}`)
	got, err := localeSummaries(cfg, set)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range got {
		switch l.Tag {
		case "fr":
			if l.HasOffice || l.CatalogName != "Offres" || l.ServiceProvider ||
				!reflect.DeepEqual(l.AdditionalProperties, []render.PropertyView{{Name: "Tier", Value: "Gold"}}) {
				t.Errorf("fr = %+v", l)
			}
		case "en":
			if l.CatalogName != "Offers" || !l.ServiceProvider || len(l.AdditionalProperties) != 1 {
				t.Errorf("en did not inherit: %+v", l)
			}
		}
	}
}
