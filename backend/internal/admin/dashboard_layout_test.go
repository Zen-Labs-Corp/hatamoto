package admin

import (
	"testing"
)

func TestDashboardLayoutClasses(t *testing.T) {
	t.Parallel()

	sec := func(ids ...string) *dashView {
		v := &dashView{}
		for _, id := range ids {
			v.Sections = append(v.Sections, &dashSection{ID: id})
		}
		return v
	}
	for _, c := range []struct {
		name string
		ids  []string
		want string
	}{
		{"full owner", []string{"dash-health", "dash-pages", "dash-missing", "dash-submissions", "dash-activity"}, "hm-dash--health hm-dash--n3"},
		{"no analytics", []string{"dash-health", "dash-missing", "dash-submissions", "dash-activity"}, "hm-dash--health hm-dash--n2"},
		{"no health", []string{"dash-pages", "dash-missing", "dash-submissions", "dash-activity"}, "hm-dash--n3"},
		{"editor", []string{"dash-submissions"}, "hm-dash--n1"},
		{"activity only", []string{"dash-activity"}, ""},
	} {
		if got := sec(c.ids...).Layout(); got != c.want {
			t.Errorf("%s: Layout() = %q, want %q", c.name, got, c.want)
		}
	}
}
