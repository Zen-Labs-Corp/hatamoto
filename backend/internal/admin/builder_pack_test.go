package admin

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

const packTestSchema = `{
	"type":"object","additionalProperties":false,
	"x-hm":{"label":"Promo banner","hint":"A promo strip","group":"Layout"},
	"properties":{
		"title":{"type":"string","maxLength":80,"x-hm-bind":"text"},
		"link":{"type":"string","x-hm-bind":"url"},
		"tone":{"enum":["info","warn"]}}}`

const packNodeDoc = `{"v":2,"nodes":[{"id":"pk","t":"promo_banner","p":{"title":"Sale","tone":"warn"}},{"id":"h1","t":"heading","p":{"text":"Hi","level":2}}]}`

func registerPack(t *testing.T) {
	t.Helper()
	w, err := builder.ParsePackWidget("promo", "banner", []byte(packTestSchema), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.SetPackKinds([]builder.PackWidgetSchema{w}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { builder.SetPackKinds(nil); builder.SetExtSources(nil) })
}

func registerSources(t *testing.T) {
	t.Helper()
	builder.SetExtSources([]builder.ExtSource{
		{Plugin: "weather", Name: "temp", Label: "Temperature", Kind: builder.ExtValue, Class: builder.BindText},
		{Plugin: "weather", Name: "forecast", Label: "Forecast", Kind: builder.ExtItems},
	})
}

type bootDoc struct {
	Kinds []struct {
		Kind  string `json:"kind"`
		Group string `json:"group"`
		Label string `json:"label"`
		Props []struct {
			Key  string `json:"key"`
			Bind string `json:"bind"`
		} `json:"props"`
	} `json:"kinds"`
	BindSources []struct{ Source, Label, Group, Class string } `json:"bindSources"`
	Options     map[string][]string                            `json:"options"`
}

func bootOf(t *testing.T, body string) bootDoc {
	t.Helper()
	const open = `id="hm-builder-boot">`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatalf("no boot data in %.200s", body)
	}
	rest := body[i+len(open):]
	var b bootDoc
	if err := json.Unmarshal([]byte(rest[:strings.Index(rest, "</script>")]), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBuilderBootCarriesPackKindsAndSources(t *testing.T) {
	registerPack(t)
	registerSources(t)
	r := newEditorRig(t)
	r.ent.Attributes = json.RawMessage(`{"page_doc":` + packNodeDoc + `}`)
	rec := r.do("GET", r.path(), "owner-token", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("builder screen: %d", rec.Code)
	}
	boot := bootOf(t, rec.Body.String())
	var found bool
	for _, k := range boot.Kinds {
		if k.Kind == "promo_banner" {
			found = true
			if k.Group != "Plugin widgets" || k.Label != "Promo banner" {
				t.Errorf("pack kind = %+v, want group Plugin widgets", k)
			}
			if len(k.Props) != 3 {
				t.Errorf("props = %+v", k.Props)
			}
		}
	}
	if !found {
		t.Fatal("pack kind missing from the boot kinds")
	}
	var temp bool
	for _, s := range boot.BindSources {
		if s.Source == "ext.weather.temp" {
			temp = s.Group == "weather" && s.Label == "Temperature" && s.Class == "text"
		}
		if s.Source == "ext.weather.forecast" {
			t.Error("an items source is offered as a prop binding")
		}
	}
	if !temp {
		t.Errorf("value source missing from bindSources: %+v", boot.BindSources)
	}
	if got := boot.Options["sources"]; !slices.Equal(got, []string{"ext.weather.forecast"}) {
		t.Errorf("options.sources = %v", got)
	}
}

func TestOutlineRendersPackWidget(t *testing.T) {
	registerPack(t)
	registerSources(t)
	r := newOutlineRig(t)
	r.setDoc(packNodeDoc)
	_, body := r.get(t, "?outline=1")
	has(t, body, `<legend class="hm-bo__legend">Promo banner</legend>`, `name="n.pk.p.title"`, `name="n.pk.p.link"`, `name="n.pk.p.tone"`,
		`name="n.pk.b.title"`, `name="n.pk.b.link"`, `<option value="ext.weather.temp">weather: Temperature</option>`, `<option value="promo_banner">Promo banner</option>`)
	if i := strings.Index(body, `name="n.pk.b.link"`); strings.Contains(body[i:i+strings.Index(body[i:], "</select>")], "ext.weather.temp") {
		t.Error("a text source is offered to a url prop")
	}
	// saving a prop edit goes through the pack schema
	f := outlineForm(t, body)
	f.Set("n.pk.p.title", "Big sale")
	f.Set("n.pk.b.title", "ext.weather.temp")
	if rec := r.post(f, "save"); rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	n, _ := r.doc(t).Find("pk")
	if n.B["title"] != "ext.weather.temp" || n.P["tone"] != "warn" {
		t.Errorf("node = %+v %+v", n.P, n.B)
	}
}

func TestOutlineOrphanNotice(t *testing.T) {
	registerPack(t)
	r := newOutlineRig(t)
	r.setDoc(packNodeDoc)
	builder.SetPackKinds(nil) // uninstalled
	code, body := r.get(t, "?outline=1")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	has(t, body, `Widget “promo_banner” is not installed`, `name="_op" value="del.pk"`, `name="_op" value="down.pk"`)
	if strings.Contains(body, `name="n.pk.p.title"`) {
		t.Error("an orphan offers prop fields")
	}
	// the page still saves and the orphan survives
	if rec := r.post(outlineForm(t, body), "save"); rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	n, _ := r.doc(t).Find("pk")
	if n == nil || n.P["title"] != "Sale" {
		t.Errorf("orphan lost on save: %+v", n)
	}
}
