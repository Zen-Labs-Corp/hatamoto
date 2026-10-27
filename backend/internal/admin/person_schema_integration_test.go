//go:build integration

package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

// The built-in person type opens in the Types editor: a site can add a field
// and change the URL pattern, the core fields are locked, delete is not offered.
func TestPersonTypeEditor_Integration(t *testing.T) {
	p := newPGRig(t)
	ctx := context.Background()
	person, err := p.ents.GetSchemaBySlug(ctx, entity.PersonSchemaSlug)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = p.ents.UpdateSchema(ctx, person.ID, &entity.UpdateSchemaInput{Schema: &person.Schema, Settings: &person.Settings})
	})
	path := "/admin/settings/types/" + person.ID.String()

	body := p.get(path)
	for _, want := range []string{"You can add fields", "Add field", `name="f.r0.name"`, "readonly"} {
		if !strings.Contains(body, want) {
			t.Errorf("person editor lacks %q", want)
		}
	}
	if strings.Contains(body, `value="remove:`) || strings.Contains(body, "/delete\"") {
		t.Error("a core field can be removed or the type deleted from the editor")
	}

	form := func(extra func(url.Values)) url.Values {
		v := url.Values{"name": {"x"}, "url_pattern": {"/team/{slug}"}, "archive_title": {"Our team"}, "archive_crumb_label": {"Team"}}
		for _, r := range fieldRows(person.Schema) {
			v.Add("field", r.Key)
			v.Set("f."+r.Key+".raw", r.Raw)
			v.Set("f."+r.Key+".name", r.Name)
			v.Set("f."+r.Key+".kind", r.Kind)
			v.Set("f."+r.Key+".title", r.Title)
		}
		extra(v)
		return v
	}
	rec := p.post(path, form(func(v url.Values) {
		v.Add("field", "r90")
		v.Set("f.r90.name", "pronouns")
		v.Set("f.r90.kind", "text")
		v.Set("f.r90.title", "Pronouns")
	}))
	if rec.Code != 303 {
		t.Fatalf("add a field: %d\n%s", rec.Code, rec.Body.String())
	}
	saved, err := p.ents.GetSchema(ctx, person.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := saved.URLSettings(); st.URLPattern != "/team/{slug}" || st.ArchiveCrumbLabel != "Team" || saved.Name != person.Name {
		t.Errorf("settings %s name %q", saved.Settings, saved.Name)
	}
	names := map[string]bool{}
	for _, r := range fieldRows(saved.Schema) {
		names[r.Name] = true
	}
	if !names["pronouns"] || !names["job_title"] || !names["on_team"] {
		t.Errorf("fields after save: %v", names)
	}

	// Dropping a core field is refused on the form, and nothing is stored.
	rec = p.post(path, form(func(v url.Values) {
		rows := v["field"][:0:0]
		for _, k := range v["field"] {
			if v.Get("f."+k+".name") != "job_title" {
				rows = append(rows, k)
			}
		}
		v["field"] = rows
	}))
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "core person field") {
		t.Errorf("drop job_title: %d", rec.Code)
	}
	if rec := p.post("/admin/settings/types/"+person.ID.String()+"/delete", nil); rec.Code == 303 && !strings.Contains(rec.Header().Get("Location"), "/admin/settings/types") {
		t.Errorf("delete: %d", rec.Code)
	}
	if _, err := p.ents.GetSchema(ctx, person.ID); err != nil {
		t.Errorf("the person type was deleted: %v", err)
	}
}
