package admin

import (
	"slices"
	"testing"
)

// The cards split into two independent columns; a column with no card is left out.
func TestDashboardColumns(t *testing.T) {
	t.Parallel()

	view := func(ids ...string) *dashView {
		v := &dashView{}
		for _, id := range ids {
			v.Sections = append(v.Sections, &dashSection{ID: id})
		}
		return v
	}
	ids := func(secs []*dashSection) []string {
		var out []string
		for _, s := range secs {
			out = append(out, s.ID)
		}
		return out
	}
	for _, c := range []struct {
		name        string
		in          []string
		left, right []string
		one         bool
	}{
		{"full owner", []string{"dash-health", "dash-pages", "dash-missing", "dash-submissions", "dash-activity"},
			[]string{"dash-health", "dash-activity"}, []string{"dash-pages", "dash-submissions", "dash-missing"}, false},
		{"no analytics", []string{"dash-health", "dash-missing", "dash-submissions", "dash-activity"},
			[]string{"dash-health", "dash-activity"}, []string{"dash-submissions", "dash-missing"}, false},
		{"editor", []string{"dash-submissions"}, nil, []string{"dash-submissions"}, true},
		{"activity only", []string{"dash-activity"}, []string{"dash-activity"}, nil, true},
		{"nothing", nil, nil, nil, false},
	} {
		v := view(c.in...)
		if got := ids(v.Left()); !slices.Equal(got, c.left) {
			t.Errorf("%s: Left() = %v, want %v", c.name, got, c.left)
		}
		if got := ids(v.Right()); !slices.Equal(got, c.right) {
			t.Errorf("%s: Right() = %v, want %v", c.name, got, c.right)
		}
		if got := v.OneColumn(); got != c.one {
			t.Errorf("%s: OneColumn() = %v, want %v", c.name, got, c.one)
		}
	}
}
