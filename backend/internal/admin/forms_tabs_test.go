package admin

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/form"
)

func TestFormTabsPages(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	base := "/admin/forms/design/" + fm.ID.String()
	for _, tab := range []string{"settings", "share"} {
		rec := g.do("GET", base+"/"+tab, "owner-token", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", tab, rec.Code)
		}
		if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %v", tab, issues)
		}
		if n := strings.Count(rec.Body.String(), `class="hm-tabs__tab"`); n != 4 {
			t.Errorf("%s: tab strip has %d items, want 4", tab, n)
		}
	}
	for _, p := range []string{base + "/nope", "/admin/forms/design/" + uuid.NewString() + "/settings", "/admin/forms/design/" + uuid.NewString() + "/share"} {
		if rec := g.do("GET", p, "owner-token", nil, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, rec.Code)
		}
	}
}

func TestFormSettingsSaveKeepsFields(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	before := append([]form.FormField(nil), fm.Fields...)
	page := g.do("GET", "/admin/forms/design/"+fm.ID.String()+"/settings", "owner-token", nil, nil).Body.String()
	if !strings.Contains(page, `name="version" value="`+fm.Version()+`"`) {
		t.Fatalf("settings lacks the version field")
	}
	post := url.Values{"version": {fm.Version()}, "name": {"Renamed"}, "status": {"inactive"}, "store": {"1"}, "notify_email": {"a@example.com"}, "f.0.label": {"ignored"}}
	rec := g.do("POST", "/admin/forms/design/"+fm.ID.String()+"/settings", "owner-token", post, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/forms/design/"+fm.ID.String()+"/settings" {
		t.Fatalf("save: %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	got := ff.forms[fm.ID]
	if got.Name != "Renamed" || got.Status != "inactive" || *got.Settings.NotifyEmail != "a@example.com" {
		t.Errorf("not saved: %+v", got)
	}
	if !reflect.DeepEqual(got.Fields, before) {
		t.Errorf("fields changed: %+v", got.Fields)
	}
}

func TestFormSettingsSaveStaleVersion(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	stale := fm.Version()
	if _, err := ff.SaveDefinition(t.Context(), fm.ID, fm.UpdatedAt, &form.SaveDefinitionInput{Name: "Theirs", Status: form.StatusActive, Fields: fm.Fields}); err != nil {
		t.Fatal(err)
	}
	saves := ff.saves
	rec := g.do("POST", "/admin/forms/design/"+fm.ID.String()+"/settings", "owner-token", url.Values{"version": {stale}, "name": {"Mine"}, "status": {"active"}}, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "This form was changed by someone else. Reload it to see their changes.") {
		t.Fatalf("stale save: %d", rec.Code)
	}
	if ff.saves != saves || ff.forms[fm.ID].Name != "Theirs" {
		t.Errorf("a stale save wrote: %+v", ff.forms[fm.ID])
	}
}

func TestFormSettingsSaveValidates(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	for name, v := range map[string]url.Values{
		"blank name":   {"version": {fm.Version()}, "name": {""}, "status": {"active"}},
		"bad email":    {"version": {fm.Version()}, "name": {"A"}, "status": {"active"}, "notify_email": {"nope"}},
		"bad redirect": {"version": {fm.Version()}, "name": {"A"}, "status": {"active"}, "redirect_url": {"javascript:x"}},
		"bad status":   {"version": {fm.Version()}, "name": {"A"}, "status": {"x"}},
		"long name":    {"version": {fm.Version()}, "name": {strings.Repeat("a", 121)}, "status": {"active"}},
	} {
		if rec := g.do("POST", "/admin/forms/design/"+fm.ID.String()+"/settings", "owner-token", v, nil); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if ff.saves != 0 {
		t.Error("an invalid save wrote")
	}
	if rec := g.do("POST", "/admin/forms/design/"+fm.ID.String()+"/settings", "owner-token", url.Values{"name": {"A"}, "status": {"active"}}, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("no version: %d", rec.Code)
	}
}
