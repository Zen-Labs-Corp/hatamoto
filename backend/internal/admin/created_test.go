package admin

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestCreatedID(t *testing.T) {
	t.Parallel()

	id := uuid.New().String()
	for name, tc := range map[string]struct{ query, want string }{
		"valid":   {"?created=" + id, id},
		"missing": {"", ""},
		"junk":    {"?created=<script>", ""},
		"empty":   {"?created=", ""},
	} {
		if got := createdID(httptest.NewRequest("GET", "/x"+tc.query, nil)); got != tc.want {
			t.Errorf("%s: createdID = %q, want %q", name, got, tc.want)
		}
	}
}

func TestCreatedPinFirst(t *testing.T) {
	t.Parallel()

	rows := []ui.Row{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	ids := func(rs []ui.Row) string {
		var out []string
		for _, r := range rs {
			out = append(out, r.ID)
		}
		return strings.Join(out, "")
	}
	for id, want := range map[string]string{"c": "cab", "b": "bac", "a": "abc", "zzz": "abc", "": "abc"} {
		if got := ids(pinFirst(rows, id)); got != want {
			t.Errorf("pinFirst(%q) = %s, want %s", id, got, want)
		}
	}
	if ids(rows) != "abc" {
		t.Error("pinFirst mutated its input")
	}
}

func TestCreatedRedirect(t *testing.T) {
	t.Parallel()

	for base, want := range map[string]string{
		"/admin/theme/menus":                  "/admin/theme/menus?created=x",
		"/admin/settings/taxonomies?kind=tag": "/admin/settings/taxonomies?kind=tag&created=x",
	} {
		if got := createdRedirect(base, "x"); got != want {
			t.Errorf("createdRedirect(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestCreatedRowMarkup(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	tbl := fixtureTable()
	out, err := a.RenderPartial("hm/table", tbl)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "hm-row--new") || strings.Contains(string(out), "(new)") {
		t.Error("a table without CreatedID marks a row as new")
	}
	tbl.CreatedID = "u2"
	out, err = a.RenderPartial("hm/table", tbl)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `<tr id="row-u2" class="hm-row--new">`) || strings.Count(s, "hm-row--new") != 1 {
		t.Error("only the created row carries hm-row--new")
	}
	if !strings.Contains(s, `<span class="hm-visually-hidden"> (new)</span>`) {
		t.Error("created row lacks the visually hidden marker")
	}
}
