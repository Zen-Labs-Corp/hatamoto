package migrations

import (
	"reflect"
	"slices"
	"sort"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/jsonx/keynorm"
	"hatamoto.dev/core/internal/siteconfig"
)

// shapeDrift lists where the frozen shape and the shape of the current type
// disagree on key names. A key the current type has and the frozen shape lacks
// is always drift (a variant of it would stay unnormalised). A frozen key the
// type lacks is allowed unless strict (theme_extra also holds extras that are
// not part of the typed theme).
func shapeDrift(path string, frozen, current *keynorm.Shape, strict bool) []string {
	if current == nil {
		return nil // opaque or free-form now: nothing to compare
	}
	if frozen == nil {
		return []string{path + ": the type now has a nested shape the migration treats as opaque"}
	}
	var out []string
	names := func(s *keynorm.Shape) []string {
		var n []string
		for k := range s.Fields {
			n = append(n, k)
		}
		sort.Strings(n)
		return n
	}
	for _, k := range names(current) {
		child, ok := frozen.Fields[k]
		if !ok {
			out = append(out, path+"/"+k+": missing from the frozen shape")
			continue
		}
		out = append(out, shapeDrift(path+"/"+k, child, current.Fields[k], strict)...)
	}
	if strict {
		for _, k := range names(frozen) {
			if _, ok := current.Fields[k]; !ok {
				out = append(out, path+"/"+k+": frozen key the type no longer has")
			}
		}
	}
	if current.Elem != nil || frozen.Elem != nil {
		out = append(out, shapeDrift(path+"[]", frozen.Elem, current.Elem, strict)...)
	}
	return out
}

func TestNormaliseStoredJSONKeysShapesMatchTypes(t *testing.T) {
	cases := []struct {
		table, column string
		typ           reflect.Type
		strict        bool
	}{
		{"hatamoto_site_config", "theme_extra", reflect.TypeFor[siteconfig.ThemeConfig](), false},
		{"hatamoto_entity_schemas", "settings", reflect.TypeFor[entity.SchemaURLSettings](), true},
		{"hatamoto_forms", "fields", reflect.TypeFor[[]form.FormField](), true},
		{"hatamoto_forms", "settings", reflect.TypeFor[form.FormSettings](), true},
	}
	frozen := map[string]*keynorm.Shape{}
	for _, c := range storedKeyColumns() {
		frozen[c.table+"."+c.column] = c.shape
	}
	if len(frozen) != len(cases) {
		t.Fatalf("migration covers %d columns, test knows %d", len(frozen), len(cases))
	}
	for _, c := range cases {
		key := c.table + "." + c.column
		f, ok := frozen[key]
		if !ok {
			t.Errorf("%s is not a migration column", key)
			continue
		}
		if drift := shapeDrift(key, f, keynorm.ShapeOf(c.typ), c.strict); len(drift) > 0 {
			t.Errorf("frozen shape of %s drifted from its type (add a new migration for new keys, do not edit this one):\n%v", key, drift)
		}
	}
}

// The migration touches the four typed columns and nothing else.
func TestNormaliseStoredJSONKeysColumns(t *testing.T) {
	var got []string
	for _, c := range storedKeyColumns() {
		got = append(got, c.table+"."+c.column)
	}
	slices.Sort(got)
	want := []string{"hatamoto_entity_schemas.settings", "hatamoto_forms.fields", "hatamoto_forms.settings", "hatamoto_site_config.theme_extra"}
	if !slices.Equal(got, want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}
}
